# Statusline のカスタマイズ設定

## 概要

設定ファイルで statusline の表示をカスタマイズできるようにする。

## 設定ファイル

- 場所: `~/.config/go-statusline/config.json`
- フォーマット: JSON

## 設定項目

### 1. 各項目の on/off

| 項目 | 設定キー | デフォルト | 説明 |
|------|----------|-----------|------|
| App Name | `show_app_name` | true | 「go-statusline」の表示 |
| Model | `show_model` | true | モデル名の表示 |
| Total Tokens | `show_tokens` | true | トークン数の表示 |
| 5h Usage | `show_5h_usage` | true | 5時間使用率の表示 |
| 5h Resets | `show_5h_resets` | true | 5時間リセット時刻の表示 |
| Week Usage | `show_week_usage` | true | 週間使用率の表示 |
| Week Resets | `show_week_resets` | true | 週間リセット時刻の表示 |

### 2. グラフの横幅

| 設定キー | デフォルト | 説明 |
|----------|-----------|------|
| `bar_width` | 20 | プログレスバーの幅（文字数） |

## 設定ファイル例

```json
{
  "show_app_name": true,
  "show_model": true,
  "show_tokens": true,
  "show_5h_usage": true,
  "show_5h_resets": true,
  "show_week_usage": true,
  "show_week_resets": true,
  "bar_width": 20
}
```

### コンパクト設定例

```json
{
  "show_app_name": false,
  "show_model": false,
  "show_tokens": false,
  "show_5h_usage": true,
  "show_5h_resets": true,
  "show_week_usage": true,
  "show_week_resets": false,
  "bar_width": 10
}
```

出力例:
```
5h: 34.0% [███▃      ] | resets: 14:00 | week: 22.0% [██▂       ]
```

## 実装計画

### 1. Config 構造体の追加

```go
type Config struct {
    ShowAppName    bool `json:"show_app_name"`
    ShowModel      bool `json:"show_model"`
    ShowTokens     bool `json:"show_tokens"`
    Show5hUsage    bool `json:"show_5h_usage"`
    Show5hResets   bool `json:"show_5h_resets"`
    ShowWeekUsage  bool `json:"show_week_usage"`
    ShowWeekResets bool `json:"show_week_resets"`
    BarWidth       int  `json:"bar_width"`
}
```

### 2. デフォルト設定の関数

```go
func defaultConfig() *Config {
    return &Config{
        ShowAppName:    true,
        ShowModel:      true,
        ShowTokens:     true,
        Show5hUsage:    true,
        Show5hResets:   true,
        ShowWeekUsage:  true,
        ShowWeekResets: true,
        BarWidth:       20,
    }
}
```

### 3. 設定ファイルの読み込み

```go
func loadConfig() (*Config, error) {
    configPath := filepath.Join(getConfigDir(), "config.json")

    // ファイルが存在しない場合はデフォルト設定を返す
    if _, err := os.Stat(configPath); os.IsNotExist(err) {
        return defaultConfig(), nil
    }

    // ファイルを読み込んでパース
    // ...
}
```

### 4. colorizeUsage の修正

- `barWidth` を引数またはConfig経由で受け取るように変更

### 5. run 関数の修正

- 設定ファイルを読み込み
- 各項目の表示/非表示を制御
- 出力フォーマットを動的に構築

## 変更ファイル

| ファイル | 変更内容 |
|---------|---------|
| main.go | Config構造体追加、loadConfig関数追加、run関数修正、colorizeUsage修正 |
| main_test.go | 設定関連のテスト追加 |

## 作業手順（TDD）

1. [x] テスト作成（Config, loadConfig, defaultConfig）
2. [x] テスト実行、失敗確認
3. [x] Config構造体とloadConfig実装
4. [x] colorizeUsageのbarWidth対応
5. [x] run関数の出力制御実装
6. [x] 全テスト実行、成功確認

## 将来的な拡張（今回は対象外）

- 色のカスタマイズ
- 出力フォーマットのテンプレート化
- 複数プロファイルのサポート
