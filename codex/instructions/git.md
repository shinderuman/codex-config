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

### final HEAD postconditionの機械強制

手順の実施を同期保証と同一視してamendを飛ばし、final HEADのplanだけが完了済みcommitを「amend直前」「install前」等の未実施操作として記述するstale-by-oneが再発したため、install.shがmanaged配置の前段階としてfinal HEAD postconditionを機械検証する。文書手順だけを同期保証にしない。

- 順序は「実装・commit-ready planの初回commit → 親CodexによるPlan・IMPLEMENTATION_TASKS・Historyの完了同期 → 同一commitへのamend → install.shのgate通過と本配置 → 次task・handoff」であり、installをamendより先に行わない。
- gateは`git show HEAD:IMPLEMENTATION_PLAN.local.md`の内容だけを判定し、dirty working treeのplanを判定に使わない。次taskの作業中planはcommit前のworking treeだけに置ける。
- gateが検証するfinal HEADのpostconditionは、PlanのACTIVE欄が`IMPLEMENTATION_TASKS/`配下の`.md` task fileへ一意に解決できること、ACTIVE/NEXT/BLOCKEDの各欄はbulletが存在するならすべてがbullet構文およびtask path契約へ解決されること(NEXT/BLOCKEDの空欄は許容する)、ACTIVE/NEXT/BLOCKEDが参照するtask fileがすべてHEAD treeへregular fileとして存在すること、ACTIVE task fileがNEXT/BLOCKEDへ重複記載されていないこと、Git境界のbranchがHEADの実際のbranchと一致すること、そして現在のGit境界・停止理由・次の親Codex操作が完了済みcommitの操作をamend直前・install前・amendの前等の未実施として記述していないことである。
- task path契約はruntime配置契約(`validateActiveTaskPath`)と同じである。`IMPLEMENTATION_TASKS/` prefix・`.md` suffixを要求し、空segment・`.`・`..`・二重slash・backslashを拒否する。番号prefixは要求せずsubdirectoryを許容する。install.shの`validate_plan_task_path`とruntimeの受理集合の一致は`TestPlanFinalHeadTaskPathValidatorMatchesRuntime`が固定する。
- bullet構文はruntime ACTIVE解決(`activeSectionEntries`/`activeEntryPath`)と同じである。逆引用符は項目全体を1組で囲む場合だけpath区切りとして扱い、逆引用符なしの直書きは項目全体をpath候補とする。閉じbacktick欠損・前後の余分なtext・複数backtick組はmalformedとしてACTIVE/NEXT/BLOCKEDすべてでfail closedに拒否する。install.shの`plan_bullet_paths`とruntimeの抽出規則の一致は`TestPlanFinalHeadBulletExtractionMatchesRuntime`が固定する。
- 過渡表現の判定は英数字identifier境界で行い`uninstall前`等の別語へ誤一致しない。完了済み操作に続く正当な現在task記述(「amend後のpostconditionを実装する」等)で使う「amend後」は対象外とする。判定はbyte志向の`LC_ALL=C`で行い、BSD grepのUTF-8 localeがnegated classをmultibyte前置文字へ一致させない欠陥へ依存しない。
- gate失敗時はinstall・次task・handoffへ進まず、Plan・IMPLEMENTATION_TASKS・Historyを完了同期して同一commitへamendする。追加commitの連鎖やworking treeだけの修正で通過扱いにしない。amend失敗でobsolete HEADが残っている間もgateは拒否し続ける。
- gateの適用外は非Git directory・commitが存在しないrepository・planがGit indexで未追跡のrepository・planがHEADへ未収録のrepositoryだけとし、planを置かない他repositoryの通常installを妨げない。
- worker-start guard・worker/reviewer個別checklist・全repository共通hookは追加しない。
