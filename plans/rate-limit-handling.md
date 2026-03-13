# Rate Limit (429) ハンドリング

## 概要

APIが429 (Rate Limit) を返した場合に、期限切れキャッシュへのフォールバックと適切なバックオフを行う機能を追加する。

## 背景

- 現状: APIが非200ステータスを返すと一律エラーとして扱い、使用率 0% のデフォルト値を表示する
- 問題: Rate Limit 時に 0% が表示されるのは誤解を招く。実際には使用率が高いからこそ Rate Limit がかかっている可能性が高い
- 解決: 429 を受けた場合、期限切れキャッシュにフォールバックし、`Retry-After` ヘッダーを尊重してバックオフする

## 現状のコード

### `fetchFromAPI()` (main.go:528-583)

```go
if resp.StatusCode != http.StatusOK {
    return nil, fmt.Errorf("API request failed: status %d", resp.StatusCode)
}
```

429 を含む全てのエラーが同一のエラーパスに入る。

### `getCachedOrFetch()` (main.go:495-509)

```go
cache, err := readCache(cacheFile)
if err == nil && sl.isCacheValid(cache) {
    return cache, nil
}

cache, err = sl.fetchFromAPI(cacheFile, endpoint)
if err != nil {
    return nil, fmt.Errorf("failed to fetch from API: %w", err)
}
```

API失敗時にキャッシュへのフォールバックがない。

### `runWithConfig()` (main.go:325-329)

```go
cache, err := sl.getCachedOrFetch(cacheFile, apiEndpoint)
if err != nil {
    cache = &CacheData{Utilization: 0.0}
}
```

最終的に 0% がフォールバック値として使われる。

## 実装計画

### 1. `RateLimitError` 型の追加

Rate Limit を他のエラーと区別するための専用エラー型を定義する。

```go
// RateLimitError は API の Rate Limit レスポンスを表すエラー型
type RateLimitError struct {
    RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
    return fmt.Sprintf("rate limited: retry after %v", e.RetryAfter)
}
```

### 2. `fetchFromAPI()` の修正

429 レスポンスを検出し、`Retry-After` ヘッダーをパースして `RateLimitError` を返す。

```go
if resp.StatusCode == http.StatusTooManyRequests {
    retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
    return nil, &RateLimitError{RetryAfter: retryAfter}
}

if resp.StatusCode != http.StatusOK {
    return nil, fmt.Errorf("API request failed: status %d", resp.StatusCode)
}
```

### 3. `parseRetryAfter()` 関数の追加

`Retry-After` ヘッダーを秒数としてパースする。ヘッダーが無い場合やパース失敗時はデフォルト値（60秒）を返す。

```go
const defaultRetryAfter = 60 * time.Second

// parseRetryAfter は Retry-After ヘッダーの値をパースする
// 秒数形式のみサポート。パース失敗時はデフォルト値を返す
func parseRetryAfter(value string) time.Duration {
    if value == "" {
        return defaultRetryAfter
    }
    seconds, err := strconv.Atoi(value)
    if err != nil {
        return defaultRetryAfter
    }
    return time.Duration(seconds) * time.Second
}
```

### 4. `getCachedOrFetch()` の修正

Rate Limit エラー時に期限切れキャッシュにフォールバックし、キャッシュの `CachedAt` を更新して連続リクエストを防ぐ。

```go
func (sl *StatusLine) getCachedOrFetch(cacheFile string, endpoint string) (*CacheData, error) {
    cache, err := readCache(cacheFile)
    if err == nil && sl.isCacheValid(cache) {
        return cache, nil
    }

    // 期限切れキャッシュを保持（フォールバック用）
    staleCache := cache

    newCache, fetchErr := sl.fetchFromAPI(cacheFile, endpoint)
    if fetchErr == nil {
        return newCache, nil
    }

    // Rate Limit エラー時: 期限切れキャッシュにフォールバック
    var rateLimitErr *RateLimitError
    if errors.As(fetchErr, &rateLimitErr) && staleCache != nil && staleCache.ResetsAt != "" {
        // CachedAt を更新してバックオフ期間中の再リクエストを防ぐ
        staleCache.CachedAt = time.Now().Unix()
        if saveErr := saveCache(cacheFile, staleCache); saveErr != nil {
            fmt.Fprintf(sl.stderr, "warning: failed to save cache: %v\n", saveErr)
        }
        return staleCache, nil
    }

    return nil, fmt.Errorf("failed to fetch from API: %w", fetchErr)
}
```

### 5. テストケース

#### `parseRetryAfter` のテスト

| ケース     | 入力      | 期待値             |
| ---------- | --------- | ------------------ |
| 正常な秒数 | "30"      | 30秒               |
| 空文字列   | ""        | 60秒（デフォルト） |
| 不正な値   | "invalid" | 60秒（デフォルト） |
| ゼロ       | "0"       | 0秒                |

#### `fetchFromAPI` のテスト

| ケース            | レスポンス | 期待結果                        |
| ----------------- | ---------- | ------------------------------- |
| 429 + Retry-After | 429, "30"  | RateLimitError{RetryAfter: 30s} |
| 429 ヘッダーなし  | 429, ""    | RateLimitError{RetryAfter: 60s} |
| 500 エラー        | 500        | 通常のエラー（変更なし）        |

#### `getCachedOrFetch` のテスト

| ケース                       | 状態            | 期待結果                                |
| ---------------------------- | --------------- | --------------------------------------- |
| 429 + 期限切れキャッシュあり | stale cache存在 | 期限切れキャッシュを返す、CachedAt 更新 |
| 429 + キャッシュなし         | cache不在       | エラーを返す                            |
| 429 + 空キャッシュ           | ResetsAt=""     | エラーを返す                            |

## 動作フロー

```
go-statusline 実行
    |
    v
キャッシュ読み込み
    |
    v
isCacheValid() 判定
    |
    +-- 有効 --> キャッシュ使用
    |
    +-- 無効 --> API アクセス
                    |
                    +-- 200 OK --> 新データでキャッシュ更新
                    |
                    +-- 429 Rate Limit
                    |       |
                    |       +-- 期限切れキャッシュあり --> CachedAt 更新、キャッシュ使用
                    |       |
                    |       +-- キャッシュなし --> エラー
                    |
                    +-- その他のエラー --> エラー（0% フォールバック）
```

## ファイル変更一覧

| ファイル     | 変更内容                                                                                                                               |
| ------------ | -------------------------------------------------------------------------------------------------------------------------------------- |
| main.go      | `RateLimitError` 型追加、`parseRetryAfter()` 追加、`fetchFromAPI()` 修正、`getCachedOrFetch()` 修正、`errors` パッケージの import 追加 |
| main_test.go | `parseRetryAfter` テスト、429 時の `fetchFromAPI` テスト、フォールバック動作の `getCachedOrFetch` テスト                               |

## 考慮事項

### `Retry-After` ヘッダーの扱い

- 秒数形式（例: "30"）のみサポートする
- HTTP-date 形式（例: "Thu, 01 Dec 2025 16:00:00 GMT"）は現時点ではサポートしない
- Anthropic API が秒数形式を使用していることを前提とする

### `CachedAt` 更新の意味

- Rate Limit 時に `CachedAt` を現在時刻に更新することで、`isCacheValid()` の `minFetchInterval`（30秒）が効く
- これにより最低30秒間は再リクエストを行わない
- `Retry-After` の値を `CachedAt` に直接反映するより単純で、既存のキャッシュ機構と整合性がある

### フォールバックキャッシュの鮮度

- 期限切れキャッシュの `Utilization` 値は古い可能性があるが、0% を表示するよりは有用
- ユーザーには stale であることを明示しない（statusline のスペースが限られているため）

## 実装順序

1. テストを作成（TDD）
   - `parseRetryAfter` のテスト
   - `fetchFromAPI` の 429 ハンドリングテスト
   - `getCachedOrFetch` のフォールバックテスト
2. テスト実行、失敗を確認
3. `RateLimitError` 型を追加
4. `parseRetryAfter()` を実装
5. `fetchFromAPI()` を修正
6. `getCachedOrFetch()` を修正
7. テスト実行、成功を確認
8. 動作確認
