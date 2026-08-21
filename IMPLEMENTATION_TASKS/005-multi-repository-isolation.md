# Task: multi-repository process concurrencyとshared resource isolationを固定

## Status

planned

## Original instruction

````text
## Task 005: multi-repository process concurrency / shared resource isolationを固定

Task 004と別責務として扱う。

### Contract

異なるrepositoryで`glm-worker`を同時に利用できることを通常contractとする。

例:

repo A:
`Codex A → PTY A → glm-worker A`

repo B:
`Codex B → PTY B → glm-worker B`

これらをglobal serializationしない。

### 現状維持すべき設計

- canonical repo root hash単位state
- repo別lock
- repo別session/checkpoint
- subprocess cwd repo別
- repo-search cache repo別
- task ID/session ID非混入

### process-level test

2つの独立temp Git repoで並列実行し、追加AI callなしで確認。

- state dir別
- lock path別
- repo A lock中にrepo B起動可能
- 同一repoの2本目だけlock拒否
- task.id非混入
- worker/reviewer session非混入
- checkpoint非混入
- telemetry非混入
- event log非混入
- repo-search cache非混入
- reset非干渉
- resume非干渉
- status非干渉
- PTY stdin payload非混入
- PTY Aのmode変更がPTY Bへ影響しない

### shared resource audit

最低限:

- `GLM_WORKER_HOME`
- prompt dir
- Claude config dir
- Claude settings override
- Codex automation TOML/SQLite
- provider/Z.ai quota
- temp dir
- installed glm-worker binary

を、

- read-only shared
- repo/task namespace済み
- upstream管理
- concrete collision candidate

へ分類する。

concrete evidenceなしにglobal lockを追加しない。

provider quota共有はrepository state競合とは分離する。

同一provider quotaを2 repoが消費すること自体をbug扱いしない。

---
````

## Amendments

none

## Purpose

複数repositoryの通常並列利用でrepository-local stateを混同せず、不要な直列化によるthroughput低下を防ぐ。

## Contract

- repository hash namespaceとrepo-local lockを維持
- provider quota共有とcheckpoint/status/session isolationを分離
- stdinをlock前に読む現順序は実害が小さければ維持

## Must not

- global lock/daemon/socket/scheduler/queue/coordinatorを追加しない
- shared config directoryだけを理由にClaude processを直列化しない

## Acceptance criteria

- 2 repo process-level parallel testで列挙対象すべて非混入
- same repo second processだけlock拒否
- PTY A/B modeとpayload非干渉
- shared resource auditをartifactまたはtracked contractへ記録
- rate-limit/provider recoveryが他repo stateを変更しない
- test/race/vet/build/gofmt、独立reviewer、Sol gate、commit

## Historical invariants

- repository単位生存判定とrepo-search cache namespaceの完了証跡

## Dependencies

- 001-requirement-task-lifecycle.md
- 004-self-contained-stdin-pty.md

## Review findings

none

## Current boundary

未着手。現設計のrepo hash分離は存在するがprocess-level統合保証がない。
