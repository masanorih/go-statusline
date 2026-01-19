# キャッシュ無効化戦略: history.jsonl 更新時刻による判定

## 概要

現状の時間ベースのポーリングに加え、`~/.claude/history.jsonl` の更新時刻を監視することで、Claude Code の使用があった場合に即座にAPIアクセスを行う機能を追加する。

## 背景

- 現状: キャッシュは10分間有効。10分経過するまでAPIアクセスしない
- 問題: プロンプト送信直後でも古いキャッシュが表示される可能性がある
- 解決: `history.jsonl` の更新時刻がキャッシュ作成時刻より新しければ、APIアクセスを行う

## 対象ファイル

- `~/.claude/history.jsonl`: Claude Code の会話履歴。プロンプト送信のたびに更新される

## 設定値

```go
const (
    pollInterval     = 2 * time.Minute   // 最大キャッシュ有効期限
    minFetchInterval = 30 * time.Second  // 最小APIアクセス間隔
)
```

### 設定値の根拠

| 設定 | 値 | 理由 |
|-----|-----|------|
| pollInterval | 2分 | 放置時でも比較的新しいデータを維持 |
| minFetchInterval | 30秒 | 連続プロンプト時のAPI保護。体験とのバランス |

### 負荷見積もり

| シナリオ | 1時間あたりのAPIアクセス |
|---------|----------------------|
| 通常使用（5分に1回プロンプト） | 約12回 |
| 頻繁使用（30秒に1回プロンプト） | 最大120回 |
| 放置状態 | 0回（statusline が呼ばれないため） |

## 実装計画

### 1. 定数の変更

```go
const (
    pollInterval     = 2 * time.Minute   // 最大キャッシュ有効期限（10分から変更）
    minFetchInterval = 30 * time.Second  // 最小APIアクセス間隔（新規追加）
    apiEndpoint      = "https://api.anthropic.com/api/oauth/usage"
    apiBeta          = "oauth-2025-04-20"
)
```

### 2. 新規関数の追加

#### `getHistoryModTime()` 関数

```go
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
```

### 3. `isCacheValid()` 関数の修正

現在の実装:
```go
func isCacheValid(cache *CacheData) bool {
    if cache.CachedAt == 0 {
        return false
    }
    cacheAge := time.Since(time.Unix(cache.CachedAt, 0))
    return cacheAge < pollInterval
}
```

修正後:
```go
func isCacheValid(cache *CacheData) bool {
    if cache.CachedAt == 0 {
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
    historyModTime, err := getHistoryModTime()
    if err == nil && historyModTime.After(cacheTime) {
        return false
    }

    return true
}
```

### 4. テストの追加

#### テストケース

1. キャッシュ作成から10秒経過 -> 有効（minFetchInterval 以内）
2. キャッシュ作成から1分経過、history.jsonl 更新あり -> 無効
3. キャッシュ作成から1分経過、history.jsonl 更新なし -> 有効
4. キャッシュ作成から3分経過 -> 無効（pollInterval 超過）
5. history.jsonl が存在しない場合 -> 時間ベース判定にフォールバック

#### テスト実装方針

- テスト用に history.jsonl のパスを注入可能にする
- または `getHistoryModTime()` を変数化してモック可能にする

### 5. ファイル変更一覧

| ファイル | 変更内容 |
|---------|---------|
| `main.go` | 定数変更、`getHistoryModTime()` 追加、`isCacheValid()` 修正 |
| `main_test.go` | 新規テストケース追加、既存テストの時間値修正 |

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
    +-- CachedAt == 0 ? -----------------> 無効 --> API アクセス
    |
    +-- 30秒以内 ? ----------------------> 有効 --> キャッシュ使用
    |
    +-- 2分以上経過 ? -------------------> 無効 --> API アクセス
    |
    +-- history.jsonl > CachedAt ? ------> 無効 --> API アクセス
    |
    v
有効 --> キャッシュデータを使用
```

## 考慮事項

### パフォーマンス

- `os.Stat()` のオーバーヘッドは最小限（ファイル内容を読まない）
- 最小インターバル判定を先に行うことで、不要な Stat 呼び出しを回避

### エッジケース

1. history.jsonl が存在しない（新規インストール等）
   - エラーを無視し、時間ベース判定にフォールバック

2. ファイルシステムの時刻精度
   - Unix 時刻（秒単位）で比較
   - 1秒以内の差は検出できない可能性があるが実用上問題なし

3. 複数の Claude Code セッションが同時に動作
   - 他のセッションの使用も検知される（意図した動作）

4. 30秒以内の連続プロンプト
   - 最初のプロンプト後のAPIアクセスのみ実行
   - 2回目以降は30秒経過まで待機

## 実装順序

1. テストを作成（TDD）
   - 既存テストの時間値を修正（10分 -> 2分）
   - 新規テストケースを追加
2. テスト実行、失敗を確認
3. 定数を変更
4. `getHistoryModTime()` 関数を実装
5. `isCacheValid()` を修正
6. テスト実行、成功を確認
7. 動作確認
