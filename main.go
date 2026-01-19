package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	pollInterval     = 2 * time.Minute                             // 最大キャッシュ有効期限（2分）
	minFetchInterval = 30 * time.Second                            // 最小APIアクセス間隔（30秒）
	apiEndpoint      = "https://api.anthropic.com/api/oauth/usage" // Anthropic API エンドポイント
	apiBeta          = "oauth-2025-04-20"                          // API ベータ版指定

	// ANSI カラーコード
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorOrange = "\033[38;5;208m"
	colorRed    = "\033[31m"
)

// getHistoryModTimeFunc は history.jsonl の更新時刻を取得する関数（テスト用に差し替え可能）
var getHistoryModTimeFunc = getHistoryModTime

// InputData は Claude Code から渡される標準入力のJSON構造
type InputData struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	ContextWindow struct {
		TotalInputTokens  int64 `json:"total_input_tokens"`
		TotalOutputTokens int64 `json:"total_output_tokens"`
	} `json:"context_window"`
}

// CacheData はキャッシュされる使用状況データ
type CacheData struct {
	ResetsAt    string  `json:"resets_at"`    // リセット時刻（ISO8601形式）
	Utilization float64 `json:"utilization"`  // 5時間使用率（0-100）
	CachedAt    int64   `json:"cached_at"`    // キャッシュ作成時刻（Unix時刻）
}

// Credentials は OAuth 認証情報
type Credentials struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
	} `json:"claudeAiOauth"`
}

// APIResponse は Anthropic API のレスポンス構造
type APIResponse struct {
	FiveHour struct {
		ResetsAt    string  `json:"resets_at"`
		Utilization float64 `json:"utilization"`
	} `json:"five_hour"`
}

func main() {
	// 標準入力からJSONを読み込む
	var input InputData
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		fmt.Fprintf(os.Stderr, "入力の読み込みエラー: %v\n", err)
		os.Exit(1)
	}

	// 累積トークン数を計算
	totalTokens := input.ContextWindow.TotalInputTokens + input.ContextWindow.TotalOutputTokens
	totalTokensStr := formatTokens(totalTokens)

	// キャッシュファイルのパスを取得
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ホームディレクトリの取得エラー: %v\n", err)
		os.Exit(1)
	}
	cacheFile := filepath.Join(homeDir, ".claude", ".usage_cache.json")

	// キャッシュの有効性をチェックし、必要に応じて取得
	cache, err := getCachedOrFetch(cacheFile)
	if err != nil {
		// デフォルト値で継続
		cache = &CacheData{Utilization: 0.0}
	}

	// リセット時刻をフォーマット
	resetTime := formatResetTime(cache.ResetsAt)

	// 5時間使用率をフォーマット（色付き）
	fiveHourUsage := colorizeUsage(cache.Utilization)

	// ステータスラインを出力
	if resetTime != "" {
		fmt.Printf("go-statusline | Model: %s | Total Tokens: %s | 5h Usage: %s | 5h Resets: %s\n",
			input.Model.DisplayName, totalTokensStr, fiveHourUsage, resetTime)
	} else {
		fmt.Printf("go-statusline | Model: %s | Total Tokens: %s | 5h Usage: %s | 5h Resets: N/A\n",
			input.Model.DisplayName, totalTokensStr, fiveHourUsage)
	}
}

// formatTokens はトークン数をフォーマット（1000以上は"k"単位）
func formatTokens(tokens int64) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000.0)
	}
	return fmt.Sprintf("%d", tokens)
}

// colorizeUsage は使用率に応じて色付けした文字列を返す
func colorizeUsage(usage float64) string {
	var color string
	switch {
	case usage < 25:
		color = colorGreen
	case usage < 50:
		color = colorYellow
	case usage < 75:
		color = colorOrange
	default:
		color = colorRed
	}
	return fmt.Sprintf("%s%.1f%%%s", color, usage, colorReset)
}

// isCacheValid はキャッシュが有効かどうかをチェック
func isCacheValid(cache *CacheData) bool {
	if cache.CachedAt == 0 {
		return false
	}
	// キャッシュに有効なデータが含まれているか検証
	if cache.ResetsAt == "" {
		return false
	}

	cacheTime := time.Unix(cache.CachedAt, 0)
	cacheAge := time.Since(cacheTime)

	// 最小インターバル以内なら常に有効（API保護）
	if cacheAge < minFetchInterval {
		return true
	}

	// 最大キャッシュ有効期限を超えていたら無効
	if cacheAge >= pollInterval {
		return false
	}

	// history.jsonl がキャッシュより新しければ無効
	historyModTime, err := getHistoryModTimeFunc()
	if err == nil && historyModTime.After(cacheTime) {
		return false
	}

	return true
}

// getCachedOrFetch はキャッシュデータを取得、またはAPIから取得
func getCachedOrFetch(cacheFile string) (*CacheData, error) {
	// キャッシュの読み込みを試行
	cache, err := readCache(cacheFile)
	if err == nil && isCacheValid(cache) {
		return cache, nil
	}

	// キャッシュが無効または存在しない場合、APIから取得
	cache, err = fetchFromAPI(cacheFile)
	if err != nil {
		return nil, fmt.Errorf("APIからの取得に失敗: %w", err)
	}

	return cache, nil
}

// readCache はファイルからキャッシュを読み込む
func readCache(cacheFile string) (*CacheData, error) {
	file, err := os.Open(cacheFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var cache CacheData
	if err := json.NewDecoder(file).Decode(&cache); err != nil {
		return nil, err
	}

	return &cache, nil
}

// fetchFromAPI はAPIから使用状況データを取得してキャッシュを更新
func fetchFromAPI(cacheFile string) (*CacheData, error) {
	// アクセストークンを取得
	token, err := getAccessToken()
	if err != nil {
		return nil, fmt.Errorf("アクセストークンの取得に失敗: %w", err)
	}

	// HTTPリクエストを作成
	req, err := http.NewRequest("GET", apiEndpoint, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", apiBeta)

	// リクエストを送信
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("APIリクエストが失敗: ステータス %d", resp.StatusCode)
	}

	// レスポンスをパース
	var apiResp APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}

	// APIレスポンスに有効なデータが含まれているか検証
	if apiResp.FiveHour.ResetsAt == "" {
		return nil, fmt.Errorf("APIレスポンスに有効なデータが含まれていません")
	}

	// キャッシュデータを作成
	cache := &CacheData{
		ResetsAt:    apiResp.FiveHour.ResetsAt,
		Utilization: apiResp.FiveHour.Utilization,
		CachedAt:    time.Now().Unix(),
	}

	// キャッシュファイルに保存
	// エラーが発生してもサイレントに処理し、プログラムは継続する
	_ = saveCache(cacheFile, cache)

	return cache, nil
}

// getAccessToken は認証情報を取得する
// macOSの場合はKeychainから、それ以外はファイルから取得
func getAccessToken() (string, error) {
	// macOSの場合、Keychainから取得を試みる
	token, err := getAccessTokenFromKeychain()
	if err == nil && token != "" {
		return token, nil
	}

	// Keychainからの取得に失敗した場合、ファイルから取得を試みる
	return getAccessTokenFromFile()
}

// getAccessTokenFromKeychain はmacOSのKeychainから認証情報を取得
func getAccessTokenFromKeychain() (string, error) {
	cmd := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	var creds Credentials
	if err := json.Unmarshal(output, &creds); err != nil {
		return "", err
	}

	if creds.ClaudeAiOauth.AccessToken == "" {
		return "", fmt.Errorf("アクセストークンが空です")
	}

	return creds.ClaudeAiOauth.AccessToken, nil
}

// getAccessTokenFromFile はファイルから認証情報を取得
func getAccessTokenFromFile() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	credFile := filepath.Join(homeDir, ".claude", ".credentials.json")
	file, err := os.Open(credFile)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var creds Credentials
	if err := json.NewDecoder(file).Decode(&creds); err != nil {
		return "", err
	}

	if creds.ClaudeAiOauth.AccessToken == "" {
		return "", fmt.Errorf("アクセストークンが空です")
	}

	return creds.ClaudeAiOauth.AccessToken, nil
}

// saveCache はキャッシュデータをファイルに保存
func saveCache(cacheFile string, cache *CacheData) error {
	// ディレクトリが存在することを確認
	dir := filepath.Dir(cacheFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// 最初に一時ファイルに書き込む
	tmpFile := cacheFile + ".tmp"
	file, err := os.Create(tmpFile)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(cache); err != nil {
		file.Close()
		os.Remove(tmpFile)
		return err
	}

	if err := file.Close(); err != nil {
		os.Remove(tmpFile)
		return err
	}

	// アトミックなリネーム
	return os.Rename(tmpFile, cacheFile)
}

// getHistoryModTime は history.jsonl の更新時刻を取得
func getHistoryModTime() (time.Time, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return time.Time{}, err
	}

	historyFile := filepath.Join(homeDir, ".claude", "history.jsonl")
	info, err := os.Stat(historyFile)
	if err != nil {
		return time.Time{}, err
	}

	return info.ModTime(), nil
}

// formatResetTime はリセット時刻をHH:MM形式にフォーマット
func formatResetTime(resetsAt string) string {
	if resetsAt == "" {
		return ""
	}

	// ISO8601時刻をパース
	t, err := time.Parse(time.RFC3339, resetsAt)
	if err != nil {
		return ""
	}

	// 59秒を加算（切り上げ）
	t = t.Add(59 * time.Second)

	// ローカル時刻に変換してフォーマット
	localTime := t.Local()
	return localTime.Format("15:04")
}
