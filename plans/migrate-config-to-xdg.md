# 設定ファイルの ~/.config/go-statusline/ への移行

## 概要

go-statusline 独自のファイルを XDG Base Directory Specification に準拠した場所に移動する。

## 現在のファイル構成

| ファイル | 場所 | 管理者 | 移動可否 |
|---------|------|--------|---------|
| キャッシュ | `~/.claude/.usage_cache.json` | go-statusline | 可 |
| 認証情報 | `~/.claude/.credentials.json` | Claude Code | 不可（読み取りのみ） |
| 履歴 | `~/.claude/history.jsonl` | Claude Code | 不可（読み取りのみ） |

## 移行後のファイル構成

| ファイル | 新しい場所 | 備考 |
|---------|-----------|------|
| キャッシュ | `~/.config/go-statusline/cache.json` | 移動 |
| 認証情報 | `~/.claude/.credentials.json` | 変更なし |
| 履歴 | `~/.claude/history.jsonl` | 変更なし |

## 検討事項

### 1. XDG 準拠について

XDG Base Directory Specification では:
- **設定ファイル**: `$XDG_CONFIG_HOME` (デフォルト: `~/.config/`)
- **キャッシュファイル**: `$XDG_CACHE_HOME` (デフォルト: `~/.cache/`)

キャッシュファイルは厳密には `~/.cache/go-statusline/cache.json` が適切かもしれない。

### 2. 環境変数のサポート

```go
func getConfigDir() string {
    if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
        return filepath.Join(xdgConfig, "go-statusline")
    }
    homeDir, _ := os.UserHomeDir()
    return filepath.Join(homeDir, ".config", "go-statusline")
}
```

### 3. 後方互換性

旧パス (`~/.claude/.usage_cache.json`) が存在する場合の移行処理:
- 旧ファイルが存在し、新ファイルが存在しない場合は移行
- 移行後は旧ファイルを削除

## 実装計画

### 1. 定数・関数の追加

```go
const (
    appName = "go-statusline"
)

func getConfigDir() string {
    if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
        return filepath.Join(xdgConfig, appName)
    }
    homeDir, _ := os.UserHomeDir()
    return filepath.Join(homeDir, ".config", appName)
}

func getCacheFilePath() string {
    return filepath.Join(getConfigDir(), "cache.json")
}

func getLegacyCacheFilePath() string {
    homeDir, _ := os.UserHomeDir()
    return filepath.Join(homeDir, ".claude", ".usage_cache.json")
}
```

### 2. 移行処理の追加

```go
func migrateLegacyCache() error {
    legacyPath := getLegacyCacheFilePath()
    newPath := getCacheFilePath()

    // 旧ファイルが存在しない場合は何もしない
    if _, err := os.Stat(legacyPath); os.IsNotExist(err) {
        return nil
    }

    // 新ファイルが既に存在する場合は何もしない
    if _, err := os.Stat(newPath); err == nil {
        return nil
    }

    // ディレクトリを作成
    if err := os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
        return err
    }

    // ファイルを移動
    return os.Rename(legacyPath, newPath)
}
```

### 3. run 関数の修正

```go
func (sl *StatusLine) run(...) error {
    // 移行処理
    if err := migrateLegacyCache(); err != nil {
        fmt.Fprintf(sl.stderr, "warning: failed to migrate cache: %v\n", err)
    }

    // キャッシュファイルパスを取得
    if cacheFile == "" {
        cacheFile = getCacheFilePath()
    }
    // ...
}
```

## 変更ファイル

| ファイル | 変更内容 |
|---------|---------|
| main.go | `getConfigDir()`, `getCacheFilePath()`, `getLegacyCacheFilePath()`, `migrateLegacyCache()` 追加、`run()` 修正 |
| main_test.go | 新関数のテスト追加 |

## 作業手順（TDD）

1. [x] テスト作成（getConfigDir, migrateLegacyCache）
2. [x] テスト実行、失敗確認
3. [x] 実装
4. [x] 全テスト実行、成功確認

## 決定事項

1. キャッシュファイルの配置先: `~/.config/go-statusline/cache.json`
2. 旧キャッシュファイル: 移行後に削除する
