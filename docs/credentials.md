# Claude Code 認証情報の取得方法

## 概要

このドキュメントは、Claude Code の認証情報（OAuth トークン）の取得方法について、プラットフォーム別の実装知見をまとめたものです。

## プラットフォーム別の認証情報保存場所

### macOS

Claude Code は macOS において、認証情報を **macOS Keychain** に暗号化して保存します。

- **保存場所**: macOS Keychain
- **アイテム名**: `Claude Code-credentials`
- **形式**: JSON形式の文字列として保存

#### Keychainからの取得方法

```bash
security find-generic-password -s "Claude Code-credentials" -w
```

このコマンドは以下のようなJSON文字列を返します：

```json
{
  "claudeAiOauth": {
    "accessToken": "sk-ant-oat01-...",
    "refreshToken": "sk-ant-ort01-...",
    "expiresAt": 1750934742168,
    "scopes": ["user:inference", "user:profile"],
    "subscriptionType": "pro"
  }
}
```

### Linux / Unix

Linux や Unix 環境では、認証情報はファイルシステムに保存されます。

- **保存場所**: `~/.claude/.credentials.json`
- **形式**: JSON ファイル

#### ファイルからの取得方法

```bash
cat ~/.claude/.credentials.json
```

ファイルの内容は macOS の Keychain と同じ JSON 構造です。

## 実装の詳細

### Go言語での実装

本プログラムでは、以下の戦略で認証情報を取得しています：

1. **macOS の Keychain から取得を試みる**（優先）
2. **失敗した場合、ファイルから取得する**（フォールバック）

これにより、両方のプラットフォームに対応しています。

#### コード例

```go
// getAccessToken は認証情報を取得する
// macOSの場合はKeychainから、それ以外はファイルから取得
func getAccessToken() (string, error) {
    // macOSの場合、Keychainから取得を試みる
    token, err := getAccessTokenFromKeychain()
    if err == nil && token != "" {
        return token, nil
    }

    // Keychainからの取得に失敗した場合、ファイルから取得を試みる
    return getAccessTokenFromFile()
}

// getAccessTokenFromKeychain はmacOSのKeychainから認証情報を取得
func getAccessTokenFromKeychain() (string, error) {
    cmd := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w")
    output, err := cmd.Output()
    if err != nil {
        return "", err
    }

    var creds Credentials
    if err := json.Unmarshal(output, &creds); err != nil {
        return "", err
    }

    if creds.ClaudeAiOauth.AccessToken == "" {
        return "", fmt.Errorf("アクセストークンが空です")
    }

    return creds.ClaudeAiOauth.AccessToken, nil
}
```

### Bash での実装

Bash版の場合、以下のように実装できます：

```bash
# macOSの場合
TOKEN=$(security find-generic-password -s "Claude Code-credentials" -w 2>/dev/null | jq -r '.claudeAiOauth.accessToken')

# Linux/Unixの場合（フォールバック）
if [ -z "$TOKEN" ] || [ "$TOKEN" = "null" ]; then
    TOKEN=$(jq -r '.claudeAiOauth.accessToken' "$HOME/.claude/.credentials.json" 2>/dev/null)
fi
```

## 認証情報の構造

```json
{
  "claudeAiOauth": {
    "accessToken": "sk-ant-oat01-...",      // アクセストークン
    "refreshToken": "sk-ant-ort01-...",     // リフレッシュトークン
    "expiresAt": 1750934742168,             // 有効期限（Unix時刻ミリ秒）
    "scopes": [                             // OAuth スコープ
      "user:inference",
      "user:profile"
    ],
    "subscriptionType": "pro"               // サブスクリプション種別
  }
}
```

### フィールドの説明

| フィールド | 説明 |
|-----------|------|
| `accessToken` | API リクエストに使用するアクセストークン |
| `refreshToken` | アクセストークンを更新するためのリフレッシュトークン |
| `expiresAt` | トークンの有効期限（Unix時刻、ミリ秒単位） |
| `scopes` | OAuth スコープのリスト |
| `subscriptionType` | ユーザーのサブスクリプション種別 |

## トラブルシューティング

### macOS で Keychain からの取得に失敗する

**症状**: `security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.`

**原因**:
- Claude Code の認証が完了していない
- 古いバージョンの Claude Code を使用している（ファイルベースの認証）

**解決方法**:
1. Claude Code で再ログイン: `claude auth login` または `/login`
2. プログラムは自動的にファイルからの取得にフォールバックします

### Linux でファイルが見つからない

**症状**: `~/.claude/.credentials.json: No such file or directory`

**原因**:
- Claude Code の認証が完了していない

**解決方法**:
1. Claude Code で認証を実行: `claude auth login`

### API リクエストが 401 エラーになる

**症状**: `APIリクエストが失敗: ステータス 401`

**原因**:
- アクセストークンの有効期限切れ
- 無効なトークン

**解決方法**:
1. Claude Code で再ログイン: `claude auth login` または `/login`
2. トークンは自動的に更新されます

## 変更履歴

### macOS での Keychain 移行について

Claude Code は比較的最近のバージョンから、macOS において認証情報を Keychain に保存するようになりました。

**移行の動作**:
- `/login` コマンド実行後、`~/.claude/.credentials.json` ファイルが削除される
- 認証情報が Keychain に移行される

**後方互換性**:
- 本プログラムは Keychain とファイルの両方に対応
- 古いバージョンの Claude Code でも動作します

## セキュリティに関する注意

### 認証情報の取り扱い

1. **認証情報をログに出力しない**
   - トークンは機密情報です
   - デバッグ時も出力を避けてください

2. **認証情報ファイルを Git に含めない**
   - `~/.claude/.credentials.json` は個人の認証情報です
   - `.gitignore` に含める必要はありません（ホームディレクトリ配下のため）

3. **エラーメッセージに注意**
   - 認証エラーは標準エラー出力に出力しない
   - ステータスライン表示の場合、エラーはサイレントに処理する

### 推奨事項

- 認証情報の読み取りは最小限に
- キャッシュを活用してAPI呼び出しを削減
- エラー時は適切なデフォルト値で継続

## 参考リンク

- [Claude Code 公式ドキュメント](https://code.claude.com/docs/)
- [Claude Code IAM ドキュメント](https://code.claude.com/docs/en/iam.md)
- [macOS Keychain Services](https://developer.apple.com/documentation/security/keychain_services)
