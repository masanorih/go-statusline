# statusline (golang version)

Claude Code ステータスライン表示ツール - golang実装版

## 概要

このプログラムは、Claude Code のステータスラインにモデル名、トークン使用量、5時間使用率、リセット時刻を表示します。

Bash版からの主な改善点：
- **50-100倍の高速化** (起動時間: 80-230ms → 1-3ms)
- **安定したメモリ使用量** (5-20MB変動 → 3-6MB固定)
- **プロセス起動回数の削減** (10-15回 → 1回)
- **外部依存なし** (標準ライブラリのみ使用)

## 必要環境

- golang 1.21以上（ビルド時のみ）
- Claude Code の認証情報が `~/.claude/.credentials.json` に保存されていること

## インストール

### ビルド済みバイナリをインストール

```bash
make install
```

### 手動インストール

```bash
# ビルド
make build

# バイナリをコピー
cp statusline ~/.claude/statusline
chmod +x ~/.claude/statusline
```

### settings.json を更新

`~/.claude/settings.json` を編集：

```json
{
  "statusLine": {
    "type": "command",
    "command": "~/.claude/statusline"
  }
}
```

## 開発

### ビルド

```bash
# 現在のプラットフォーム向けビルド
make build

# 全プラットフォーム向けビルド
make build-all
```

### テスト

```bash
# テスト実行
make test

# カバレッジ付きテスト
go test -cover

# ベンチマーク
go test -bench=. -benchmem
```

### クロスコンパイル

```bash
# Linux (amd64)
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o statusline-linux-amd64

# macOS (Intel)
GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o statusline-darwin-amd64

# macOS (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o statusline-darwin-arm64
```

## 使い方

Claude Code を起動すると、ステータスラインに以下のような情報が表示されます：

```
go-statusline | Model: Sonnet 4.5 | Total Tokens: 200.0k | 5h Usage: 34.00% | 5h Resets: 14:00
```

## 出力フィールド

| フィールド | 説明 |
|-----------|------|
| Model | 現在のモデル名 |
| Total Tokens | 累積トークン数（入力 + 出力） |
| 5h Usage | 5時間使用率（パーセンテージ） |
| 5h Resets | 次のリセット時刻（HH:MM形式） |

## キャッシュ

使用データは `~/.claude/.usage_cache.json` にキャッシュされます。キャッシュの有効期限は **10分間** で、期限が切れると自動的にAPIから最新のデータを取得します。

### キャッシュ構造

```json
{
  "resets_at": "2026-01-05T14:00:00Z",
  "utilization": 34.0,
  "cached_at": 1736072345
}
```

## サポートプラットフォーム

- Linux (amd64, arm64)
- macOS (Intel, Apple Silicon)

## パフォーマンス

### ベンチマーク結果

```
BenchmarkFormatTokens-16       4960544    237.1 ns/op    24 B/op    2 allocs/op
BenchmarkIsCacheValid-16      26394518     45.86 ns/op    0 B/op    0 allocs/op
BenchmarkFormatResetTime-16   11438455    103.8 ns/op     5 B/op    1 allocs/op
```

### Bash版との比較

| 項目 | Bash版 | golang版 | 改善率 |
|------|--------|------|--------|
| 起動時間 | 80-230ms | 1-3ms | **95-98%削減** |
| プロセス起動 | 10-15回 | 1回 | **90%削減** |
| メモリ使用量 | 5-20MB変動 | 3-6MB固定 | **安定化** |
| バイナリサイズ | N/A | 4.9MB | - |

## ライセンス

MIT License

## 関連リンク

- [Bash版](https://github.com/masanorih/statusline.sh)
- [Claude Code](https://claude.com/claude-code)
