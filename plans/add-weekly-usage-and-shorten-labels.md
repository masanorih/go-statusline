# 週間使用率の追加とラベル短縮

## 概要
- 「5h Usage」-> 「5h」に短縮
- 「5h Resets」-> 「resets」に短縮
- 「week」を追加してAPIの`seven_day`使用率をプログレスバー付きで表示
- resetsは5hのresets_atのみ表示

## 出力フォーマットの変更

現在:
```
go-statusline | Model: Sonnet 4.5 | Total Tokens: 200.0k | 5h Usage: 34.0% [...] | 5h Resets: 14:00
```

変更後:
```
go-statusline | Model: Sonnet 4.5 | Total Tokens: 200.0k | 5h: 34.0% [...] | week: 22.0% [...] | resets: 14:00
```

## APIレスポンス構造（確認済み）
```json
{
  "five_hour": { "utilization": 9.0, "resets_at": "..." },
  "seven_day": { "utilization": 22.0, "resets_at": "..." }
}
```

## 変更ファイル
1. `main.go` - 構造体変更、fetchFromAPI変更、出力フォーマット変更
2. `main_test.go` - テストケース追加

## 実装手順（TDD）

### Step 1: テスト追加

**main_test.go - TestAPIResponseParsing に追加:**
- `seven_day`付きレスポンスのパーステスト
- `seven_day`なしレスポンスの後方互換テスト

**main_test.go - TestCacheOperations に追加:**
- weekly付きキャッシュの保存/読み込みテスト
- 旧フォーマットキャッシュの後方互換テスト

### Step 2: テスト失敗確認

### Step 3: 実装

**APIResponse構造体 (main.go:56-62):**
```go
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
```

**CacheData構造体 (main.go:42-47):**
```go
type CacheData struct {
    ResetsAt          string  `json:"resets_at"`
    Utilization       float64 `json:"utilization"`
    WeeklyUtilization float64 `json:"weekly_utilization"`
    CachedAt          int64   `json:"cached_at"`
}
```

**fetchFromAPI (main.go:284-):**
- `cache.WeeklyUtilization = apiResp.SevenDay.Utilization` を追加

**出力フォーマット (main.go:99-104):**
- `weeklyUsage := colorizeUsage(cache.WeeklyUtilization)` を追加
- フォーマット文字列を変更: `5h: %s | week: %s | resets: %s`

### Step 4: テスト成功確認

## 検証方法
```bash
go test -v
go build -o statusline && echo '{"model":{"display_name":"test"},"context_window":{"total_input_tokens":50000,"total_output_tokens":50000}}' | ./statusline
```
