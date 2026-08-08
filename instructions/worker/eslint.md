# ESLint規則
- リポジトリルートの`eslint.config.mjs`とflat configを使用。
- `eslint` / `globals`はルートpackage.jsonのdevDependenciesで管理。
- browser/node globalsは`globals`パッケージ。
- ユーザーレベルESLint設定を作らない。
- `npx eslint ...`を使用しJS編集後は`--fix`を実行。
- 警告を勝手に許容せずエラーは解消。
- テストが存在する場合は変更後に実行。
