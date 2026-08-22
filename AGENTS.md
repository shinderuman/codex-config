# このリポジトリの作業bootstrap規則

- 作業開始時と再開時(Sol判断後の継続・review修正・provider障害やrate limit上限後のresume)に、repository rootへ`IMPLEMENTATION_PLAN.local.md`が存在するか確認する。
- 存在する場合だけ必ず読み、未完了作業と進行状態の唯一の正として扱う。過去sessionの記憶や推測をこれより優先しない。計画本文を他fileへ複製しない。
- `IMPLEMENTATION_PLAN.local.md`はGit管理するtracked canonical sourceとし、公開`.gitignore`へ追加しない。
- このplanの本文・`[x]`・優先順・現在状態を更新できるのは親Codexだけである。GLM worker/reviewerはこのfileを読み取り専用で参照し、編集・生成・復元・削除を行わない。必要な更新は更新候補と根拠をPACKETへ記載して親Codexへ報告する。
- 存在しない場合は推測・復元・自動生成をせず、通常のrepository状態から作業する。Git indexで追跡されているのにworking treeへ存在しない場合と、Git repository内で追跡判定自体ができない場合は親Codexが置いた正が失われた・確認できない状態であるため、GLM worker/reviewerはmodel呼出前にfail closedで親Codexへ返す。未追跡で最初から存在しない他repositoryおよびGit管理外directoryでは通常作業を許可し、GLMは同fileを生成しない。
- 完了証跡とescaped bug/review原因分析は`IMPLEMENTATION_HISTORY.md`へ分離する。同historyは親Codex専有のtracked archiveであり、GLM worker/reviewerは編集・生成・削除を行わず、通常の作業開始・再開時に全文を読まず必要な見出しだけを検索して読む。planが存在するrepositoryでは、Git indexで追跡されているのにworking treeへ存在しない場合と呼出開始前後で内容・存在が変化した場合をplanと同じmodel呼出前後guardでfail closed検出する。planの無い旧repositoryとhistory未作成状態の通常作業は許可する。
- planの`## ACTIVE`節は実行中taskの要求正本を`IMPLEMENTATION_TASKS/`配下へ1件だけ指す。`IMPLEMENTATION_RULES.md`・`IMPLEMENTATION_PLAN.local.md`・`IMPLEMENTATION_TASKS/`配下全file・`IMPLEMENTATION_HISTORY.md`をparent-managed implementation metadataの単一集合として親Codexだけが編集し、GLM worker/reviewerは4面とも読み取り専用とする。pathごとの分岐を増やさず、集合の追跡中欠損・呼出前後変化・停止期間外変化はすべて同じfail closed guardで検出する。
- ACTIVE task fileのOriginal instruction・Amendments・Resolved references・Contract・Must not・Acceptance criteriaはtask開始・Sol判断後継続・review修正・resumeの全呼出で、workerとreviewerがそれぞれ独立にtask file本文から読む。USER_REQUEST・会話要約・過去session記憶を要求定義の代わりにしない。planが存在するのにACTIVE欄からtask fileを一意に解決できない場合(未記載・複数記載・配置契約外・参照file欠損)はmodel呼出前にfail closedとする。task完了時のfile削除・history移行・plan昇格はTask 002の完了flowで行う。
