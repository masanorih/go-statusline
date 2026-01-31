# グローバル変数 getHistoryModTimeFunc の削除

## 概要

テスト用のグローバル変数 `getHistoryModTimeFunc` を削除し、StatusLine 構造体の依存性注入パターンに完全移行する。

## 現状

```go
// Deprecated: StatusLine構造体の依存性注入を使用してください
var getHistoryModTimeFunc = getHistoryModTime
```

### 使用箇所

| ファイル | 行 | 用途 |
|---------|-----|------|
| main.go:45-47 | 定義 | グローバル変数の宣言 |
| main.go:294 | 参照 | `isCacheValid` 関数内で使用 |
| main_test.go:43-45 | テスト | TestIsCacheValid_WithHistory でモック |
| main_test.go:93-162 | テスト | TestIsCacheValid_HistoryScenarios でモック |

## 問題点

- グローバル変数によるモックはテストの並行実行で競合状態を起こす可能性
- StatusLine 構造体に `WithHistoryModTimeFunc` オプションが既にあるため、グローバル変数は不要

## 実装計画

### Step 1: テストの修正

テストを `WithHistoryModTimeFunc` を使用するように修正する。

**修正対象:**
- `TestIsCacheValid_WithHistory`
- `TestIsCacheValid_HistoryScenarios`

**修正内容:**
```go
// 変更前
originalFunc := getHistoryModTimeFunc
defer func() { getHistoryModTimeFunc = originalFunc }()
getHistoryModTimeFunc = func() (time.Time, error) { ... }

// 変更後
sl := NewStatusLine(
    WithHistoryModTimeFunc(func() (time.Time, error) { ... }),
)
```

### Step 2: main.go の修正

1. グローバル変数 `getHistoryModTimeFunc` を削除
2. パッケージレベル関数 `isCacheValid` を修正（グローバル変数を参照しないように）

### Step 3: テスト実行

全テストが通過することを確認。

## 作業手順（TDD）

1. [x] 計画ファイル作成
2. [x] テストコードを修正（WithHistoryModTimeFunc 使用）
3. [x] テスト実行、成功確認
4. [x] テストコミット (76de255)
5. [x] main.go からグローバル変数を削除
6. [x] 全テスト実行、成功確認
7. [x] 実装コミット (5f50751)

## 備考

- この変更は純粋なリファクタリングで、機能の変更はない
- テストの並行実行安全性が向上する
