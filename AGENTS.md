# このリポジトリの作業bootstrap規則

- 作業開始時と再開時(Sol判断後の継続・review修正・provider障害やrate limit上限後のresume)に、repository rootへ`IMPLEMENTATION_PLAN.local.md`が存在するか確認する。
- 存在する場合だけ必ず読み、未完了作業と進行状態の唯一の正として扱う。過去sessionの記憶や推測をこれより優先しない。計画本文を他fileへ複製しない。
- 存在しない場合は推測・復元・自動生成をせず、通常のrepository状態から作業する。
- `IMPLEMENTATION_PLAN.local.md`はGit管理外(repository-local exclude)で運用し、公開`.gitignore`へ追加しない。
