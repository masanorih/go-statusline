# コード品質改善計画

## 概要

批判的分析に基づく改善点のまとめ。優先度順に整理。

---

## 高優先度

### 1. テストカバレッジの向上（現状42.9%）

**問題点**
- `main()`, `fetchFromAPI()`, `getAccessToken*()` がテストされていない
- HTTPリクエストやKeychain連携のモックがない

**改善案**
- インターフェースによる依存性注入を導入
- `httptest` パッケージでAPIレスポンスをモック
- Keychain連携を抽象化してテスト可能にする

**対象ファイル**: main.go, main_test.go

### 2. グローバル変数によるモックの排除

**問題点**
```go
var getHistoryModTimeFunc = getHistoryModTime
```
- テストの並行実行で競合状態を起こす可能性

**改善案**
- 構造体にメソッドとして実装
- 依存性注入パターンを採用

```go
type StatusLine struct {
    historyModTimeFunc func() (time.Time, error)
    httpClient         *http.Client
}
```

### 3. エラーハンドリングの改善

**問題点**
- キャッシュ保存失敗がサイレント（300行目）
- エラーメッセージが日本語と英語で混在

**改善案**
- `log` パッケージまたはstderrへのログ出力を追加
- エラーメッセージを英語に統一（国際化対応）

---

## 中優先度

### 4. formatResetTime の切り上げロジック修正

**問題点**
```go
t = t.Add(59 * time.Second)
```
- 厳密な切り上げではない

**改善案**
```go
t = t.Truncate(time.Minute).Add(time.Minute)
```

### 5. HTTPクライアントの再利用

**問題点**
```go
client := &http.Client{Timeout: 10 * time.Second}
```
- 毎回新規作成で接続プールが活用されない

**改善案**
- パッケージレベルでクライアントを定義
- または構造体のフィールドとして保持

```go
var httpClient = &http.Client{
    Timeout: 10 * time.Second,
    Transport: &http.Transport{
        MaxIdleConns:       10,
        IdleConnTimeout:    30 * time.Second,
    },
}
```

### 6. マジックナンバーの定数化

**問題点**
- `barWidth = 20` が関数内で定義
- 部分ブロックの閾値 `5.0/6.0` 等がハードコード

**改善案**
```go
const (
    barWidth        = 20
    shadeSteps      = 6
    shadeThreshold1 = 1.0 / shadeSteps
    shadeThreshold2 = 2.0 / shadeSteps
    // ...
)
```

### 7. 異常値のログ出力

**問題点**
- 負の使用率や100%超過を検出してもログに残らない

**改善案**
```go
if usage < 0 || usage > 100 {
    fmt.Fprintf(os.Stderr, "warning: unexpected usage value: %.1f\n", usage)
}
```

---

## 低優先度

### 8. パッケージ分割

**問題点**
- 430行のmain.goに全ロジックが集約

**改善案**（将来的に検討）
```
statusline/
  cmd/statusline/main.go
  internal/
    cache/cache.go
    api/client.go
    format/tokens.go
    format/progress.go
```

### 9. タイムゾーン依存テストの修正

**問題点**
```go
expected: "10:30", // Will vary based on timezone
```

**改善案**
- `time.FixedZone` を使用してUTC固定でテスト
- または期待値をタイムゾーンに応じて動的に計算

### 10. テスト日付のハードコード

**問題点**
- `"2026-01-06T10:00:00Z"` など固定日付

**改善案**
- `time.Now()` ベースで相対的に生成

### 11. GoDocコメントの充実

**対象関数**
- `APIResponse` 構造体のフィールド説明
- エラーケースの説明
- 各関数の戻り値の説明

---

## 実装順序の提案

1. テストカバレッジ向上（依存性注入の導入）
2. エラーハンドリング改善
3. マジックナンバー定数化
4. HTTPクライアント再利用
5. formatResetTime修正
6. テストの修正（タイムゾーン、日付）
7. GoDocコメント追加
8. パッケージ分割（必要に応じて）

---

## 備考

- 現状でも実用上は問題なく動作している
- 改善は段階的に進め、各段階でテストを通すこと
- パッケージ分割は機能追加の必要性が生じた時点で検討
