# stdin rate_limits 対応

## 概要

Claude Code の statusline 機能が更新され、stdin JSON に `rate_limits` フィールドが直接含まれるようになった。現状の go-statusline は OAuth トークン取得 -> API リクエスト -> キャッシュ管理という独自パイプラインで使用率を取得しているが、stdin から直接取得できれば大幅に簡素化できる。

## 背景

- 現状: 毎回 OAuth トークンを取得し、`https://api.anthropic.com/api/oauth/usage` に API リクエストを送り、レスポンスをキャッシュしている
- 問題: Claude Code 自体が `rate_limits` を提供するようになったため、独自の API 呼び出しは冗長
- 注意: `rate_limits` は Pro/Max 加入者のみ。最初の API 呼び出しまで `null` になる可能性がある
- 解決: stdin の `rate_limits` を優先し、取得できない場合のみ既存の API 取得にフォールバックする

## 新規 stdin フィールド

Claude Code が提供する新しいフィールド:

```json
{
  "rate_limits": {
    "five_hour": {
      "used_percentage": 25.0,
      "resets_at": 1743580800
    },
    "seven_day": {
      "used_percentage": 10.0,
      "resets_at": 1744185600
    }
  },
  "cost": {
    "total_cost_usd": 0.1234
  }
}
```

注意: `resets_at` は Unix エポック秒 (int64)。既存の API レスポンスは ISO8601 文字列 (`"resets_at": "2025-04-02T12:00:00Z"`) だが、stdin は数値。

## 現状のコード

### InputData (main.go:242-252)

```go
type InputData struct {
    Model struct {
        DisplayName string `json:"display_name"`
    } `json:"model"`
    ContextWindow struct {
        TotalInputTokens  int64    `json:"total_input_tokens"`
        TotalOutputTokens int64    `json:"total_output_tokens"`
        UsedPercentage    *float64 `json:"used_percentage"`
    } `json:"context_window"`
}
```

`rate_limits` フィールドが存在しない。

### runWithConfig (main.go:328-417)

API 呼び出しは常に実行される:

```go
cache, err := sl.getCachedOrFetch(cacheFile, apiEndpoint)
if err != nil {
    cache = &CacheData{Utilization: 0.0}
}
```

stdin のデータに関わらず、毎回 API を呼びに行く。

## 実装計画

### 1. InputData 構造体の拡張 (main.go:242-252)

```go
type InputData struct {
    Model struct {
        DisplayName string `json:"display_name"`
    } `json:"model"`
    ContextWindow struct {
        TotalInputTokens  int64    `json:"total_input_tokens"`
        TotalOutputTokens int64    `json:"total_output_tokens"`
        UsedPercentage    *float64 `json:"used_percentage"`
    } `json:"context_window"`
    RateLimits *struct {
        FiveHour *struct {
            UsedPercentage float64 `json:"used_percentage"`
            ResetsAt       int64   `json:"resets_at"`
        } `json:"five_hour"`
        SevenDay *struct {
            UsedPercentage float64 `json:"used_percentage"`
            ResetsAt       int64   `json:"resets_at"`
        } `json:"seven_day"`
    } `json:"rate_limits"`
    Cost *struct {
        TotalCostUSD float64 `json:"total_cost_usd"`
    } `json:"cost"`
}
```

- `rate_limits` は null の可能性があるため pointer 型
- `resets_at` は Unix エポック秒 (int64)

### 2. Unix エポック秒 -> ISO8601 変換ヘルパー

既存の `formatResetTime` / `formatResetTimeWithDate` は ISO8601 文字列を期待するため、Unix エポック秒を ISO8601 に変換するヘルパーを追加する。

```go
func unixToISO8601(epoch int64) string {
    if epoch == 0 {
        return ""
    }
    return time.Unix(epoch, 0).UTC().Format(time.RFC3339)
}
```

### 3. runWithConfig のロジック変更 (main.go:328-417)

stdin の `rate_limits` が存在する場合は API 呼び出しをスキップし、stdin のデータを直接 CacheData に変換する。

```go
var cache *CacheData

if input.RateLimits != nil && input.RateLimits.FiveHour != nil {
    // stdin から直接取得
    cache = &CacheData{
        Utilization: input.RateLimits.FiveHour.UsedPercentage,
        ResetsAt:    unixToISO8601(input.RateLimits.FiveHour.ResetsAt),
    }
    if input.RateLimits.SevenDay != nil {
        cache.WeeklyUtilization = input.RateLimits.SevenDay.UsedPercentage
        cache.WeeklyResetsAt = unixToISO8601(input.RateLimits.SevenDay.ResetsAt)
    }
} else {
    // 既存の API 取得 + キャッシュロジック（フォールバック）
    var err error
    cache, err = sl.getCachedOrFetch(cacheFile, apiEndpoint)
    if err != nil {
        cache = &CacheData{Utilization: 0.0}
    }
}
```

### 4. Config に ShowCost を追加 (main.go:98-109)

```go
ShowCost bool `json:"show_cost"`
```

デフォルト: `false`（既存ユーザーの表示を変えないため）

### 5. コスト表示の追加 (main.go:374-411)

```go
if cfg.ShowCost && input.Cost != nil {
    parts = append(parts, fmt.Sprintf("cost: $%.4f", input.Cost.TotalCostUSD))
}
```

### 6. テストケース

#### InputData パーステスト

| ケース              | 入力                       | 期待結果           |
| ------------------- | -------------------------- | ------------------ |
| rate_limits あり    | 完全な rate_limits JSON    | 正しくパースされる |
| rate_limits が null | rate_limits フィールドなし | RateLimits == nil  |
| five_hour のみ      | seven_day なし             | SevenDay == nil    |
| cost あり           | cost フィールドあり        | 正しくパースされる |

#### stdin rate_limits 優先テスト

| ケース              | stdin            | 期待結果                         |
| ------------------- | ---------------- | -------------------------------- |
| rate_limits あり    | rate_limits 含む | API 呼び出しなし、stdin 値を使用 |
| rate_limits が null | rate_limits なし | API フォールバック実行           |

#### unixToISO8601 テスト

| ケース | 入力       | 期待結果               |
| ------ | ---------- | ---------------------- |
| 正常値 | 1743580800 | "2025-04-02T12:00:00Z" |
| ゼロ   | 0          | ""                     |

#### コスト表示テスト

| ケース         | ShowCost | Cost   | 期待結果                   |
| -------------- | -------- | ------ | -------------------------- |
| 表示 ON + あり | true     | 0.1234 | "cost: $0.1234" が含まれる |
| 表示 OFF       | false    | 0.1234 | cost が含まれない          |
| 表示 ON + null | true     | null   | cost が含まれない          |

## 動作フロー

```
go-statusline 実行
    |
    v
stdin JSON 読み込み
    |
    v
rate_limits フィールドあり?
    |
    +-- あり --> stdin から直接 CacheData を生成（API 呼び出しなし）
    |
    +-- なし --> 既存フロー
                    |
                    v
                キャッシュ読み込み
                    |
                    +-- 有効 --> キャッシュ使用
                    |
                    +-- 無効 --> API アクセス
                                    |
                                    +-- 200 OK --> 新データ
                                    +-- 429 --> 期限切れキャッシュ
                                    +-- エラー --> 0% フォールバック
```

## ファイル変更一覧

| ファイル     | 変更内容                                                                                      |
| ------------ | --------------------------------------------------------------------------------------------- |
| main.go      | InputData 拡張、unixToISO8601 追加、runWithConfig 分岐追加、Config に ShowCost 追加、表示追加 |
| main_test.go | InputData パーステスト、stdin 優先テスト、unixToISO8601 テスト、コスト表示テスト              |

## 考慮事項

### 既存の API フォールバックを残す理由

- `rate_limits` は Pro/Max 加入者のみ提供される
- 最初の API 呼び出しまで `null` になる可能性がある
- 古いバージョンの Claude Code では `rate_limits` フィールド自体が存在しない

### 削除しないコード

今回は API 取得関連のコードは削除しない。将来的に `rate_limits` が安定して提供されることが確認できたら、API 取得コードの削除を検討する。

## 実装順序

1. テストを作成（TDD）
   - `unixToISO8601` のテスト
   - `InputData` パーステスト（rate_limits, cost）
   - stdin rate_limits 優先テスト
   - コスト表示テスト
2. テスト実行、失敗を確認
3. `InputData` 構造体を拡張
4. `unixToISO8601` を実装
5. `runWithConfig` に分岐ロジックを追加
6. `Config` に `ShowCost` を追加、表示ロジックを追加
7. テスト実行、成功を確認
8. 動作確認
