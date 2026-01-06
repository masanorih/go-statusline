package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	pollInterval = 10 * time.Minute                               // キャッシュの有効期限（10分）
	apiEndpoint  = "https://api.anthropic.com/api/oauth/usage"   // Anthropic API エンドポイント
	apiBeta      = "oauth-2025-04-20"                             // API ベータ版指定
)

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
		fmt.Fprintf(os.Stderr, "使用状況データの取得エラー: %v\n", err)
		// デフォルト値で継続
		cache = &CacheData{Utilization: 0.0}
	}

	// リセット時刻をフォーマット
	resetTime := formatResetTime(cache.ResetsAt)

	// 5時間使用率をフォーマット
	fiveHourUsage := fmt.Sprintf("%.2f", cache.Utilization)

	// ステータスラインを出力
	if resetTime != "" {
		fmt.Printf("Model: %s | Total Tokens: %s | 5h Usage: %s%% | 5h Resets: %s\n",
			input.Model.DisplayName, totalTokensStr, fiveHourUsage, resetTime)
	} else {
		fmt.Printf("Model: %s | Total Tokens: %s | 5h Usage: %s%% | 5h Resets: N/A\n",
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

// isCacheValid はキャッシュが有効かどうかをチェック
func isCacheValid(cache *CacheData) bool {
	if cache.CachedAt == 0 {
		return false
	}
	cacheAge := time.Since(time.Unix(cache.CachedAt, 0))
	return cacheAge < pollInterval
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

	// キャッシュデータを作成
	cache := &CacheData{
		ResetsAt:    apiResp.FiveHour.ResetsAt,
		Utilization: apiResp.FiveHour.Utilization,
		CachedAt:    time.Now().Unix(),
	}

	// キャッシュファイルに保存
	if err := saveCache(cacheFile, cache); err != nil {
		// エラーをログに出力するが、失敗させない
		fmt.Fprintf(os.Stderr, "警告: キャッシュの保存に失敗: %v\n", err)
	}

	return cache, nil
}

// getAccessToken は認証情報ファイルからアクセストークンを読み込む
func getAccessToken() (string, error) {
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
