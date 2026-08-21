# Task: worker call outlierを可視化

## Status

planned

## Original instruction

v3 worker-new 41 callのturn中央値55・p95 137に対しstructured移行task resumeは320 turn・約20.08。まずtask/phase/session/modelごとのoutlierを追加AI callなしで可視化する。hard turn capやsession rotationはまだ導入しない。

## Amendments

- 2026-08-22 parent maintenance:

````text
#### worker call outlier

Task 009は保存済みtelemetryから分析可能であり、machine protocol measurement Task 008が完了しないと成立しないtaskではありません。

Task 008をhard dependencyから外してください。

Task 008を先にやるpriorityを維持すること自体は構いません。
````

## Purpose

品質を落とさず長大callの発生条件を測定可能にする。

## Contract

- 保存telemetryからtask/phase/session/model別分布とoutlierを表示
- raw prompt/responseを保存・表示しない

## Must not

- hard cap、session rotation、benchmark追加callを導入しない

## Acceptance criteria

- median/p95/outlierと対象taskを再現可能に表示
- current/resumeを区別し既知例と整合
- test/race/vet/build/gofmt、独立reviewer、risk/contractに応じて必要なSol品質gate、commit

## Historical invariants

- History 2026-08-21 canonical telemetry分析

## Dependencies

- `IMPLEMENTATION_TASKS/001-requirement-task-lifecycle.md`

## Review findings

none

## Current boundary

未着手。
