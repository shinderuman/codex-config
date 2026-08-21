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

## tracked canonical planのcommit同期

repository rootに親Codex管理のtracked canonical plan(`IMPLEMENTATION_PLAN.local.md`)が存在するrepositoryのcommitだけに適用する親Codex orchestration contractである。plan本文・`[x]`・優先順・現在状態の更新権限が親Codex専有であること、commit実行の承認条件、Gitリモートへの書込禁止、wrapperのplan file不変guardは本契約で変更しない。worker/reviewerへの個別checklist追加で代替しない。

planをtask commitへ含め、`[x]`を個別commit後だけに限定し、各commit直後にplanを更新する運用を同時に適用すると、commit前のplanに完了を書けない一方でcommit後の更新が別commitを待ち、HEADのplanが現在作業より一世代古いstale-by-oneになる。これを初回commitと同一commitへのamendからなる二段階で解消する。

1. 実装・test・独立review・必要なSol品質gate完了後も未完了項目を`[x]`にせず、planを作業実態と次task内容へ同期したcommit-ready状態へ更新する。
2. 実装とcommit-ready planを初回commitへ含める。
3. 親Codexが直ちにplanと`IMPLEMENTATION_HISTORY.md`を完了証跡(`[x]`)・次task・実working tree状態へ同期する。
4. 同期済みplan/historyだけを初回commitと同じcommitへamendする。
5. final HEADとclean working treeを確認してからinstall・次task・handoffへ進む。

- 初回commitとamendの間に停止・ユーザー報告でのturn終了・別task開始・GLM起動・install・handoffを行わず、amendまでを同じturnの連続操作とする。
- amend失敗時はobsolete HEADのままinstall・次task・handoffへ進まず、同じcommitへのplan/history同期を復旧して再度amendする。追加commitの連鎖でplan同期を先送りしない。
- 大規模ledger・別status DB・追加commitの連鎖・worker/reviewer個別checklistは追加しない。
