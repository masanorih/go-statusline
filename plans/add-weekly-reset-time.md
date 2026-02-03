# week リセット日時の表示追加

## 概要

week の使用率表示の後にリセット日時を追加する。

## 出力フォーマット

変更前:
```
go-statusline | Model: Sonnet 4.5 | Total Tokens: 200.0k | 5h: 34.0% [...] | resets: 14:00 | week: 22.0% [...]
```

変更後:
```
go-statusline | Model: Sonnet 4.5 | Total Tokens: 200.0k | 5h: 34.0% [...] | resets: 14:00 | week: 22.0% [...] | resets: 02/05 14:00
```

## 日付フォーマット

- 形式: `MM/DD HH:MM`
- 例: `02/05 14:00`
- 月・日は二桁でゼロ埋め

## 変更内容

### 1. CacheData 構造体
- `WeeklyResetsAt` フィールドを追加

### 2. fetchFromAPI 関数
- `apiResp.SevenDay.ResetsAt` を `cache.WeeklyResetsAt` に保存

### 3. formatResetTimeWithDate 関数（新規）
- 日付付きのリセット時刻フォーマット関数を追加
- 形式: `MM/DD HH:MM`

### 4. run 関数
- `weeklyResetTime` を取得してフォーマット
- 出力に追加

## 作業手順（TDD）

1. [x] 計画ファイル作成
2. [x] テスト作成（formatResetTimeWithDate）
3. [x] テスト実行、失敗確認
4. [x] 実装
5. [x] 全テスト実行、成功確認
