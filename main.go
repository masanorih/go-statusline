package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
// Deprecated: StatusLine構造体の依存性注入を使用してください
var getHistoryModTimeFunc = getHistoryModTime

// StatusLine はステータスライン生成の依存性を管理する構造体
type StatusLine struct {
	httpClient        *http.Client
	getHistoryModTime func() (time.Time, error)
	getAccessToken    func() (string, error)
	execCommand       func(name string, arg ...string) *exec.Cmd
	stderr            io.Writer
}

// StatusLineOption は StatusLine のオプション設定用関数型
type StatusLineOption func(*StatusLine)

// NewStatusLine は新しい StatusLine インスタンスを作成
func NewStatusLine(opts ...StatusLineOption) *StatusLine {
	sl := &StatusLine{
		httpClient:        &http.Client{Timeout: 10 * time.Second},
		getHistoryModTime: getHistoryModTime,
		getAccessToken:    getAccessToken,
		execCommand:       exec.Command,
		stderr:            os.Stderr,
	}

	for _, opt := range opts {
		opt(sl)
	}

	return sl
}

// WithHTTPClient はカスタムHTTPクライアントを設定
func WithHTTPClient(client *http.Client) StatusLineOption {
	return func(sl *StatusLine) {
		sl.httpClient = client
	}
}

// WithHistoryModTimeFunc はカスタムのhistory更新時刻取得関数を設定
func WithHistoryModTimeFunc(fn func() (time.Time, error)) StatusLineOption {
	return func(sl *StatusLine) {
		sl.getHistoryModTime = fn
	}
}

// WithAccessTokenFunc はカスタムのアクセストークン取得関数を設定
func WithAccessTokenFunc(fn func() (string, error)) StatusLineOption {
	return func(sl *StatusLine) {
		sl.getAccessToken = fn
	}
}

// WithExecCommand はカスタムのexec.Command関数を設定（テスト用）
func WithExecCommand(fn func(name string, arg ...string) *exec.Cmd) StatusLineOption {
	return func(sl *StatusLine) {
		sl.execCommand = fn
	}
}

// WithStderr はカスタムのstderr出力先を設定（テスト用）
func WithStderr(w io.Writer) StatusLineOption {
	return func(sl *StatusLine) {
		sl.stderr = w
	}
}

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
	ResetsAt          string  `json:"resets_at"`          // リセット時刻（ISO8601形式）
	Utilization       float64 `json:"utilization"`        // 5時間使用率（0-100）
	WeeklyUtilization float64 `json:"weekly_utilization"` // 週間使用率（0-100）
	CachedAt          int64   `json:"cached_at"`          // キャッシュ作成時刻（Unix時刻）
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
	SevenDay struct {
		ResetsAt    string  `json:"resets_at"`
		Utilization float64 `json:"utilization"`
	} `json:"seven_day"`
}

func main() {
	sl := NewStatusLine()
	if err := sl.run(os.Stdin, os.Stdout, ""); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

// run はメインロジックを実行（テスト可能）
// cacheFileが空の場合はデフォルトパスを使用
func (sl *StatusLine) run(stdin io.Reader, stdout io.Writer, cacheFile string) error {
	// 標準入力からJSONを読み込む
	var input InputData
	if err := json.NewDecoder(stdin).Decode(&input); err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}

	// 累積トークン数を計算
	totalTokens := input.ContextWindow.TotalInputTokens + input.ContextWindow.TotalOutputTokens
	totalTokensStr := formatTokens(totalTokens)

	// キャッシュファイルのパスを取得
	if cacheFile == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		cacheFile = filepath.Join(homeDir, ".claude", ".usage_cache.json")
	}

	// キャッシュの有効性をチェックし、必要に応じて取得
	cache, err := sl.getCachedOrFetch(cacheFile, apiEndpoint)
	if err != nil {
		// デフォルト値で継続
		cache = &CacheData{Utilization: 0.0}
	}

	// リセット時刻をフォーマット
	resetTime := formatResetTime(cache.ResetsAt)

	// 使用率をフォーマット（色付き）
	fiveHourUsage := sl.colorizeUsage(cache.Utilization)
	weeklyUsage := sl.colorizeUsage(cache.WeeklyUtilization)

	// ステータスラインを出力
	if resetTime != "" {
		fmt.Fprintf(stdout, "go-statusline | Model: %s | Total Tokens: %s | 5h: %s | resets: %s | week: %s\n",
			input.Model.DisplayName, totalTokensStr, fiveHourUsage, resetTime, weeklyUsage)
	} else {
		fmt.Fprintf(stdout, "go-statusline | Model: %s | Total Tokens: %s | 5h: %s | resets: N/A | week: %s\n",
			input.Model.DisplayName, totalTokensStr, fiveHourUsage, weeklyUsage)
	}

	return nil
}

// formatTokens はトークン数をフォーマット（1000以上は"k"単位）
func formatTokens(tokens int64) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000.0)
	}
	return fmt.Sprintf("%d", tokens)
}

// colorizeUsage は使用率に応じて色付けした文字列とプログレスバーを返す
// 下方向部分ブロック文字(▁▂▃▅▆▇)で6段階の小数部を表現
func colorizeUsage(usage float64) string {
	sl := &StatusLine{stderr: io.Discard}
	return sl.colorizeUsage(usage)
}

// colorizeUsage は使用率に応じて色付けした文字列とプログレスバーを返す（StatusLineメソッド版）
func (sl *StatusLine) colorizeUsage(usage float64) string {
	// 異常値の警告
	if usage < 0 || usage > 100 {
		fmt.Fprintf(sl.stderr, "warning: unexpected usage value: %.1f\n", usage)
	}

	return colorizeUsageInternal(usage)
}

// colorizeUsageInternal は使用率に応じて色付けした文字列とプログレスバーを返す（内部実装）
func colorizeUsageInternal(usage float64) string {
	const barWidth = 20

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

	// バーの塗りつぶし文字数を計算
	totalBlocks := usage / 100.0 * float64(barWidth)

	// 負の値は0にクリップ
	if totalBlocks < 0 {
		totalBlocks = 0
	}

	filled := int(totalBlocks)
	if filled > barWidth {
		filled = barWidth
	}

	// 小数部分から下方向部分ブロック文字を選択
	var shade string
	shadeWidth := 0
	if filled < barWidth {
		fraction := totalBlocks - float64(filled)
		switch {
		case fraction >= 5.0/6.0:
			shade = "▇"
			shadeWidth = 1
		case fraction >= 4.0/6.0:
			shade = "▆"
			shadeWidth = 1
		case fraction >= 3.0/6.0:
			shade = "▅"
			shadeWidth = 1
		case fraction >= 2.0/6.0:
			shade = "▃"
			shadeWidth = 1
		case fraction >= 1.0/6.0:
			shade = "▂"
			shadeWidth = 1
		case fraction > 0:
			shade = "▁"
			shadeWidth = 1
		}
	}

	// バーを構築: 完全ブロック + シェード + 空白
	empty := barWidth - filled - shadeWidth
	bar := strings.Repeat("█", filled) + shade + strings.Repeat(" ", empty)
	return fmt.Sprintf("%s%.1f%% [%s]%s", color, usage, bar, colorReset)
}

// isCacheValid はキャッシュが有効かどうかをチェック
func isCacheValid(cache *CacheData) bool {
	sl := &StatusLine{getHistoryModTime: getHistoryModTimeFunc}
	return sl.isCacheValid(cache)
}

// isCacheValid はキャッシュが有効かどうかをチェック（StatusLineメソッド版）
func (sl *StatusLine) isCacheValid(cache *CacheData) bool {
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
	historyModTime, err := sl.getHistoryModTime()
	if err == nil && historyModTime.After(cacheTime) {
		return false
	}

	return true
}

// getCachedOrFetch はキャッシュデータを取得、またはAPIから取得
func getCachedOrFetch(cacheFile string) (*CacheData, error) {
	sl := NewStatusLine()
	return sl.getCachedOrFetch(cacheFile, apiEndpoint)
}

// getCachedOrFetch はキャッシュデータを取得、またはAPIから取得（StatusLineメソッド版）
func (sl *StatusLine) getCachedOrFetch(cacheFile string, endpoint string) (*CacheData, error) {
	// キャッシュの読み込みを試行
	cache, err := readCache(cacheFile)
	if err == nil && sl.isCacheValid(cache) {
		return cache, nil
	}

	// キャッシュが無効または存在しない場合、APIから取得
	cache, err = sl.fetchFromAPI(cacheFile, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from API: %w", err)
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
	sl := NewStatusLine()
	return sl.fetchFromAPI(cacheFile, apiEndpoint)
}

// fetchFromAPI はAPIから使用状況データを取得してキャッシュを更新（StatusLineメソッド版）
func (sl *StatusLine) fetchFromAPI(cacheFile string, endpoint string) (*CacheData, error) {
	// アクセストークンを取得
	token, err := sl.getAccessToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	// HTTPリクエストを作成
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", apiBeta)

	// リクエストを送信
	resp, err := sl.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed: status %d", resp.StatusCode)
	}

	// レスポンスをパース
	var apiResp APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}

	// APIレスポンスに有効なデータが含まれているか検証
	if apiResp.FiveHour.ResetsAt == "" {
		return nil, fmt.Errorf("API response contains no valid data")
	}

	// キャッシュデータを作成
	cache := &CacheData{
		ResetsAt:          apiResp.FiveHour.ResetsAt,
		Utilization:       apiResp.FiveHour.Utilization,
		WeeklyUtilization: apiResp.SevenDay.Utilization,
		CachedAt:          time.Now().Unix(),
	}

	// キャッシュファイルに保存
	// エラーが発生しても警告を出力してプログラムは継続する
	if err := saveCache(cacheFile, cache); err != nil {
		fmt.Fprintf(sl.stderr, "warning: failed to save cache: %v\n", err)
	}

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
	sl := NewStatusLine()
	return sl.getAccessTokenFromKeychain()
}

// getAccessTokenFromKeychain はmacOSのKeychainから認証情報を取得（StatusLineメソッド版）
func (sl *StatusLine) getAccessTokenFromKeychain() (string, error) {
	cmd := sl.execCommand("security", "find-generic-password", "-s", "Claude Code-credentials", "-w")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	var creds Credentials
	if err := json.Unmarshal(output, &creds); err != nil {
		return "", err
	}

	if creds.ClaudeAiOauth.AccessToken == "" {
		return "", fmt.Errorf("access token is empty")
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
	return getAccessTokenFromFileWithPath(credFile)
}

// getAccessTokenFromFileWithPath は指定されたパスから認証情報を取得（テスト用）
func getAccessTokenFromFileWithPath(credFile string) (string, error) {
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
		return "", fmt.Errorf("access token is empty")
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
	return getHistoryModTimeWithPath(historyFile)
}

// getHistoryModTimeWithPath は指定されたパスのファイル更新時刻を取得（テスト用）
func getHistoryModTimeWithPath(historyFile string) (time.Time, error) {
	info, err := os.Stat(historyFile)
	if err != nil {
		return time.Time{}, err
	}

	return info.ModTime(), nil
}

// roundUpToMinute は時刻を分単位で切り上げる
// 秒数やナノ秒がある場合は次の分に切り上げ、ちょうどの分ならそのまま
func roundUpToMinute(t time.Time) time.Time {
	truncated := t.Truncate(time.Minute)
	if t.After(truncated) {
		return truncated.Add(time.Minute)
	}
	return truncated
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

	// 分単位で切り上げ
	t = roundUpToMinute(t)

	// ローカル時刻に変換してフォーマット
	localTime := t.Local()
	return localTime.Format("15:04")
}
