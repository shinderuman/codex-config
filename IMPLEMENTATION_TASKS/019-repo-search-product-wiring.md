# Task: repo-search feature flag、CLI、install integration

## Status

planned

## Original instruction

````text
## Task 019: repo-search feature flag / CLI / install integration

- feature flag
- CLI
- install.sh distribution
- managed instruction
- installer smoke

をproduction wiring。

既存BM25 pure coreを壊さない。
````

## Amendments

none

## Purpose

repo-searchを管理可能なproduction featureとして配布する。

## Contract

- default/flag/CLI/help/config/installの一貫性
- disabled時に既存挙動不変

## Must not

- core再実装、外部依存追加を行わない

## Acceptance criteria

- feature on/off、CLI、installer preflight/smoke、managed現物一致
- test/race/vet/build/gofmt、独立reviewer、risk/contractに応じて必要なSol品質gate、commit

## Historical invariants

- installer fail-closed、BM25 core

## Dependencies

- `IMPLEMENTATION_TASKS/016-worker-repo-search-integration.md`
- `IMPLEMENTATION_TASKS/018-exhaustive-search-gate.md`

## Review findings

none

## Current boundary

未着手。
