# statusline

Claude Code ステータスライン表示ツール

## 概要

このプログラムは、Claude Code のステータスラインにモデル名、トークン使用量、5時間使用率、リセット時刻を表示します。

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
go-statusline | Model: Sonnet 4.5 | Total Tokens: 200.0k | 5h Usage: 34.0% [██████              ] | 5h Resets: 14:00
```

5h Usage はプログレスバー（20文字幅）付きで表示され、使用率に応じて色が変化します：
- 0-24%: 緑
- 25-49%: 黄
- 50-74%: オレンジ
- 75-100%: 赤

## 出力フィールド

| フィールド | 説明 |
|-----------|------|
| Model | 現在のモデル名 |
| Total Tokens | 累積トークン数（入力 + 出力） |
| 5h Usage | 5時間使用率（パーセンテージ + プログレスバー） |
| 5h Resets | 次のリセット時刻（HH:MM形式） |

## キャッシュ

使用データは `~/.claude/.usage_cache.json` にキャッシュされます。キャッシュの有効期限は **2分間** で、期限が切れると自動的にAPIから最新のデータを取得します。また、`history.jsonl` が更新された場合もキャッシュを無効化してAPIから再取得します（ただし最小30秒間隔）。

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

## ライセンス

MIT License

## 関連リンク

- [Claude Code](https://claude.com/claude-code)
