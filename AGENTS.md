# このリポジトリの作業bootstrap規則

- 作業開始時と再開時(Sol判断後の継続・review修正・provider障害やrate limit上限後のresume)に、repository rootへ`IMPLEMENTATION_PLAN.local.md`が存在するか確認する。
- 存在する場合だけ必ず読み、未完了作業と進行状態の唯一の正として扱う。過去sessionの記憶や推測をこれより優先しない。計画本文を他fileへ複製しない。
- `IMPLEMENTATION_PLAN.local.md`はGit管理するtracked canonical sourceとし、公開`.gitignore`へ追加しない。
- このplanの本文・`[x]`・優先順・現在状態を更新できるのは親Codexだけである。GLM worker/reviewerはこのfileを読み取り専用で参照し、編集・生成・復元・削除を行わない。必要な更新は更新候補と根拠をPACKETへ記載して親Codexへ報告する。
- 存在しない場合は推測・復元・自動生成をせず、通常のrepository状態から作業する。Git indexで追跡されているのにworking treeへ存在しない場合と、Git repository内で追跡判定自体ができない場合は親Codexが置いた正が失われた・確認できない状態であるため、GLM worker/reviewerはmodel呼出前にfail closedで親Codexへ返す。未追跡で最初から存在しない他repositoryおよびGit管理外directoryでは通常作業を許可し、GLMは同fileを生成しない。
