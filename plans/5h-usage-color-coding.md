# 5h Usage 色付け機能

## 概要

statusline の 5h Usage 表示を使用率に応じて4段階で色付けする。

## 仕様

### 色付け範囲

- 5h Usage の値（パーセンテージ部分）のみを色付け
- 他の項目（Model、Total Tokens、5h Resets）は色付けしない

### 4段階の閾値と配色

| 範囲 | 色 | ANSI コード | 意味 |
|------|-----|-------------|------|
| 0-25% | 緑 | `\033[32m` | 余裕あり |
| 25-50% | 黄色 | `\033[33m` | 通常 |
| 50-75% | オレンジ | `\033[38;5;208m` | 注意 |
| 75-100% | 赤 | `\033[31m` | 警告 |

### 出力形式

```
go-statusline | Model: Opus 4.5 | Total Tokens: 123.4k | 5h Usage: [色]25.0%[リセット] | 5h Resets: 10:30
```

## 実装計画

### 1. テスト作成（TDD）

`main_test.go` に以下のテストを追加：

```go
func TestColorizeUsage(t *testing.T) {
    tests := []struct {
        name     string
        usage    float64
        expected string
    }{
        {"0%は緑", 0.0, "\033[32m0.0%\033[0m"},
        {"24.9%は緑", 24.9, "\033[32m24.9%\033[0m"},
        {"25%は黄色", 25.0, "\033[33m25.0%\033[0m"},
        {"49.9%は黄色", 49.9, "\033[33m49.9%\033[0m"},
        {"50%はオレンジ", 50.0, "\033[38;5;208m50.0%\033[0m"},
        {"74.9%はオレンジ", 74.9, "\033[38;5;208m74.9%\033[0m"},
        {"75%は赤", 75.0, "\033[31m75.0%\033[0m"},
        {"100%は赤", 100.0, "\033[31m100.0%\033[0m"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := colorizeUsage(tt.usage)
            if result != tt.expected {
                t.Errorf("colorizeUsage(%v) = %q, want %q", tt.usage, result, tt.expected)
            }
        })
    }
}
```

### 2. 実装

`main.go` に以下を追加：

```go
// ANSI カラーコード
const (
    colorReset  = "\033[0m"
    colorGreen  = "\033[32m"
    colorYellow = "\033[33m"
    colorOrange = "\033[38;5;208m"
    colorRed    = "\033[31m"
)

// colorizeUsage は使用率に応じて色付けした文字列を返す
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
    return fmt.Sprintf("%s%.1f%%%s", color, usage, colorReset)
}
```

### 3. main 関数の修正

```go
// 変更前
fiveHourUsage := fmt.Sprintf("%.1f", cache.Utilization)

// 変更後
fiveHourUsage := colorizeUsage(cache.Utilization)
```

出力部分も修正：

```go
// 変更前
fmt.Printf("go-statusline | Model: %s | Total Tokens: %s | 5h Usage: %s%% | 5h Resets: %s\n",
    input.Model.DisplayName, totalTokensStr, fiveHourUsage, resetTime)

// 変更後（%%を削除、colorizeUsageが%を含むため）
fmt.Printf("go-statusline | Model: %s | Total Tokens: %s | 5h Usage: %s | 5h Resets: %s\n",
    input.Model.DisplayName, totalTokensStr, fiveHourUsage, resetTime)
```

## 作業手順

1. [ ] テストコード作成（`TestColorizeUsage`）
2. [ ] テスト実行、失敗確認
3. [ ] テストコミット
4. [ ] `colorizeUsage` 関数実装
5. [ ] `main` 関数修正
6. [ ] 全テスト実行、成功確認
7. [ ] 実装コミット

## 備考

- オレンジ色は 256 色モード（`\033[38;5;208m`）を使用
- 一部の古いターミナルでは 256 色がサポートされていない可能性があるが、現代のターミナルではほぼ対応済み
