# stdin effort / thinking / output_style 表示対応

## 概要

Claude Code の statusline 機能が更新され、stdin JSON に `effort`、`thinking`、`output_style` フィールドが含まれるようになった。これらを取り込み、statusline に表示できるようにする。

## 背景

- 現状の `InputData` は `model.display_name` / `context_window` / `rate_limits` / `cost` のみを認識している
- 本体の更新により、以下のセッション状態が stdin から取得できるようになった
  - `effort.level` ... reasoning effort レベル（`low` / `medium` / `high` / `xhigh` / `max`）
  - `thinking.enabled` ... extended thinking の有効フラグ
  - `output_style.name` ... 現在の出力スタイル名（`default` ほか）
- いずれも「今どのモード・設定で動いているか」を一目で示せるため、statusline 表示の実用性が高い

## 新規 stdin フィールド

```json
{
  "effort": {
    "level": "high"
  },
  "thinking": {
    "enabled": true
  },
  "output_style": {
    "name": "default"
  }
}
```

注意:

- `effort` は現在のモデルが effort パラメータをサポートする場合のみ存在する（非対応モデルでは absent）
- `thinking` / `output_style` も古いバージョンの Claude Code では存在しない可能性がある
- いずれも absent の場合は表示しない（pointer 型で nil 判定する）

## 現状のコード

### InputData (main.go:243-266)

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
    RateLimits *struct { ... } `json:"rate_limits"`
    Cost *struct {
        TotalCostUSD float64 `json:"total_cost_usd"`
    } `json:"cost"`
}
```

`effort` / `thinking` / `output_style` フィールドが存在しない。

### Config (main.go:99-110)

```go
type Config struct {
    ShowAppName      bool `json:"show_app_name"`
    ShowModel        bool `json:"show_model"`
    ShowTokens       bool `json:"show_tokens"`
    ShowContextUsage bool `json:"show_context_usage"`
    Show5hUsage      bool `json:"show_5h_usage"`
    Show5hResets     bool `json:"show_5h_resets"`
    ShowWeekUsage    bool `json:"show_week_usage"`
    ShowWeekResets   bool `json:"show_week_resets"`
    ShowCost         bool `json:"show_cost"`
    BarWidth         int  `json:"bar_width"`
}
```

### 表示組み立て (main.go:404-448)

`parts = append(parts, ...)` で各要素を組み立て、` | ` で結合して出力する。

## 実装計画

### 1. InputData 構造体の拡張 (main.go:243-266)

末尾に以下を追加する。

```go
    Effort *struct {
        Level string `json:"level"`
    } `json:"effort"`
    Thinking *struct {
        Enabled bool `json:"enabled"`
    } `json:"thinking"`
    OutputStyle *struct {
        Name string `json:"name"`
    } `json:"output_style"`
```

- いずれも absent の可能性があるため pointer 型

### 2. Config に表示フラグを追加 (main.go:99-110)

```go
    ShowEffort      bool `json:"show_effort"`
    ShowThinking    bool `json:"show_thinking"`
    ShowOutputStyle bool `json:"show_output_style"`
```

デフォルト: いずれも `false`（既存ユーザーの表示を変えないため、`defaultConfig` には追加しない）。利用者は自身の `config.json` に `show_effort` / `show_thinking` / `show_output_style` を追記して有効化する。

注意: `show_effort` は `show_model` が true の場合のみ意味を持つ（effort は Model セグメントに追記されるため）。`show_model` が false なら effort も表示されない。

### 3. 表示ロジックの追加

3 項目で差し込み方が異なる。

#### effort ... Model セグメントに追記 (main.go:410-412)

独立セグメントにはせず、`Model:` の値の末尾に `(level)` を付ける。

```go
if cfg.ShowModel {
    modelStr := input.Model.DisplayName
    if cfg.ShowEffort && input.Effort != nil && input.Effort.Level != "" {
        modelStr = fmt.Sprintf("%s - %s", modelStr, input.Effort.Level)
    }
    parts = append(parts, fmt.Sprintf("Model: %s", modelStr))
}
```

結果: `Model: Opus 4.8 (1M context) - high`

#### thinking ... Model の直後に独立セグメント

`thinking.enabled` が true のときだけ `thinking` の語のみを出す。off のときは何も出さない。

```go
if cfg.ShowThinking && input.Thinking != nil && input.Thinking.Enabled {
    parts = append(parts, "thinking")
}
```

#### output_style ... thinking の直後に独立セグメント

```go
if cfg.ShowOutputStyle && input.OutputStyle != nil && input.OutputStyle.Name != "" {
    parts = append(parts, fmt.Sprintf("style: %s", input.OutputStyle.Name))
}
```

表示フォーマット:

- effort: `Model:` 末尾に ` - high`（ハイフン区切り、値のみ）
- thinking: `thinking`（enabled 時のみ。off は非表示）
- output_style: `style: default`

差し込み位置（Model 直後）の最終的な並び:

```
go-statusline | Model: Opus 4.8 (1M context) - high | thinking | style: default | ctx: ... | 5h: ... | resets: ... | week: ... | resets: ...
```

### 4. テストケース

#### InputData パーステスト

- effort あり: `effort.level` が正しくパースされる
- effort なし: `Effort == nil`
- thinking あり (enabled true/false): 正しくパースされる
- thinking なし: `Thinking == nil`
- output_style あり: `output_style.name` が正しくパースされる
- output_style なし: `OutputStyle == nil`

#### 表示テスト

effort:

- ShowEffort ON + level あり: `Model:` の値が `... - high` で終わる
- ShowEffort ON + Effort nil: `(...)` が追記されない（DisplayName そのまま）
- ShowEffort ON + level 空文字: 追記されない
- ShowEffort OFF: 追記されない
- ShowModel OFF + ShowEffort ON: Model セグメント自体が出ない

thinking:

- ShowThinking ON + enabled true: `thinking` セグメントが含まれる
- ShowThinking ON + enabled false: `thinking` が含まれない
- ShowThinking ON + Thinking nil: `thinking` が含まれない
- ShowThinking OFF: `thinking` が含まれない

output_style:

- ShowOutputStyle ON + name あり: `style: default` が含まれる
- ShowOutputStyle ON + OutputStyle nil: `style:` が含まれない
- ShowOutputStyle OFF: `style:` が含まれない

## ファイル変更一覧

| ファイル     | 変更内容                                                            |
| ------------ | ------------------------------------------------------------------- |
| main.go      | InputData 拡張、Config に 3 フラグ追加、表示ロジック追加            |
| main_test.go | InputData パーステスト、3 項目の表示テスト                          |
| README.md    | config.json の新フラグ説明、表示項目の追記                          |

## 考慮事項

### デフォルト OFF とする理由

- 既存ユーザーの statusline 表示を勝手に変えない
- effort / thinking は非対応モデルや旧バージョンで absent になりうるため、明示的に有効化したユーザーにのみ表示する

### vim.mode を含めない理由

- vim.mode はユーザーの入力欄の状態であり、vim キーバインド利用者以外には不要
- 今回のスコープからは除外する

## 実装順序

1. テストを作成（TDD）
   - InputData パーステスト（effort / thinking / output_style）
   - 表示テスト（ON/OFF/absent の各ケース）
2. テスト実行、失敗を確認
3. テストが正しいことを確認した段階でコミット
4. `InputData` 構造体を拡張
5. `Config` に 3 フラグを追加
6. 表示ロジックを追加
7. テスト実行、成功を確認
8. README 更新
