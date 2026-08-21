# Task: multi-repository process concurrencyとshared resource isolationを固定

## Status

planned

## Original instruction

異なるrepositoryのglm-worker同時利用を通常contractとしglobal serializationしない。canonical repo root hash単位state、repo別lock/session/checkpoint/cwd/cache/task IDを維持する。2つの独立temp Git repoを追加AI callなしで並列実行し、state dir/lock/task/worker/reviewer/checkpoint/telemetry/event/cache/reset/resume/status/PTY payload/modeが相互混入しないこと、repo A lock中にrepo Bが動き同一repo 2本目だけ拒否されることをprocess-levelに固定する。

`GLM_WORKER_HOME`、prompt dir、Claude config/settings、Codex automation TOML/SQLite、provider quota、temp dir、installed binaryをread-only shared、namespace済み、upstream管理、concrete collision candidateへ分類する。quota共有はstate isolation bugではなく、evidenceなしにglobal lockを追加しない。

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
