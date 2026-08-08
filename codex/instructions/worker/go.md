# Go規則
- private識別子はcamelCase、public識別子はPascalCase。
- エラーには必要なコンテキストを含める。
- サイレント失敗より明示的なエラーハンドリングを優先する。
- ビルド確認は`go build -o /dev/null .`を使用する。
