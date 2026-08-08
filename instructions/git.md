# Git詳細規則
- `git diff` / `git show` / `git log`を`head` / `tail`等へパイプしない。
- 明示的な依頼がない限り`git commit`しない。
- `cherry-pick` / `merge` / `rebase` / `revert`を明示的に依頼された場合、その操作に必要なコミット作成は対象。
- コミット前に`git diff --cached`を確認する。
- コミットメッセージはstaged diffに存在する事実だけから作る。会話履歴・推測・diffにない効果を含めない。
- 新しいセッションで最初のコミット時は`git log -5`を確認し既存スタイルへ合わせる。

形式:
```text
<prefix>: <description>

- <change 1>
- <change 2>
```

prefix: `feat` `fix` `refactor` `improve` `docs` `style` `test` `chore` `perf` `ci` `build` `revert`

- `git push`等Gitリモートへの書き込みは禁止。
- 「ユーザーレベルのPush禁止ルールを今回だけ解除する」と明示された場合だけ例外。
