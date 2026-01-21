# 5h Usage プログレスバー追加

## 概要

`5h Usage: x%` の右側にグラフィカルなプログレスバーを追加する。

## 仕様

### 表示形式

```
5h Usage: 25.0% [█████               ]
5h Usage: 50.0% [██████████          ]
5h Usage: 75.0% [███████████████     ]
5h Usage: 100.0% [████████████████████]
```

### 詳細

- バー幅: 20文字（括弧 `[]` の内側）
- 塗りつぶし文字: `█`（U+2588 FULL BLOCK）
- 空白部分: スペース
- 色付け範囲: `x% [████...    ]` 全体（パーセンテージとバー）
- 色分け基準: 既存の閾値を使用
  - 0-24%: 緑
  - 25-49%: 黄
  - 50-74%: オレンジ
  - 75-100%: 赤

### 出力例

変更前:
```
go-statusline | Model: Claude Opus 4 | Total Tokens: 15.2k | 5h Usage: 25.0% | 5h Resets: 21:00
```

変更後:
```
go-statusline | Model: Claude Opus 4 | Total Tokens: 15.2k | 5h Usage: 25.0% [█████               ] | 5h Resets: 21:00
```

## 実装計画

### 1. テスト作成

`main_test.go` に `colorizeUsage` 関数のテストを追加:

- 0% の場合: バーが空
- 25% の場合: バーが5文字埋まる（緑）
- 50% の場合: バーが10文字埋まる（黄）
- 75% の場合: バーが15文字埋まる（オレンジ）
- 100% の場合: バーが20文字埋まる（赤）
- 境界値テスト: 24.9%, 25.0%, 49.9%, 50.0%, 74.9%, 75.0%, 99.9%, 100.0%

### 2. 実装

`colorizeUsage` 関数を修正:

```go
const barWidth = 20

func colorizeUsage(usage float64) string {
    var color string
    switch {
    case usage < 25:
        color = colorGreen
    case usage < 50:
        color = colorYellow
    case usage < 75:
        color = colorOrange
    default:
        color = colorRed
    }

    // バーの塗りつぶし文字数を計算
    filled := int(usage / 100.0 * float64(barWidth))
    if filled > barWidth {
        filled = barWidth
    }

    bar := strings.Repeat("█", filled) + strings.Repeat(" ", barWidth-filled)
    return fmt.Sprintf("%s%.1f%% [%s]%s", color, usage, bar, colorReset)
}
```

### 3. 必要な変更

- `import` に `"strings"` を追加
- `colorizeUsage` 関数の修正
- 既存のテストの期待値を更新

## 備考

- ターミナル幅は取得できないため、固定幅（20文字）を使用
- Unicode の `█` は一般的なターミナルでサポートされている
