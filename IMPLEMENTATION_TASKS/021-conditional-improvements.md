# Task: conditional review/tool output改善

## Status

planned

## Original instruction

Task 008、009、011等の結果で効果が確認された改善だけを採用し、事前に便利そうという理由で追加しない。

## Amendments

none

## Purpose

品質証拠なしの複雑性増殖を防ぐ。

## Contract

- candidateごとにevidence、expected reduction、quality risk、rollbackを示す
- 採用変更は個別taskへ分割する

## Must not

- このumbrellaをそのままGLM実装taskへ渡さない

## Acceptance criteria

- candidateの採用/棄却/保留を測定結果で決定
- 採用分は新task file、棄却はHistoryへ記録

## Historical invariants

- complexity responsibility、conditional convergence方針

## Dependencies

- 008-machine-protocol-measurement.md
- 009-worker-call-outliers.md
- 011-operation-category-telemetry.md

## Review findings

none

## Current boundary

測定task待ち。umbrellaのため直接dispatch禁止。
