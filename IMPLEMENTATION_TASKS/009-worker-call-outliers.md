# Task: worker call outlierを可視化

## Status

planned

## Original instruction

v3 worker-new 41 callのturn中央値55・p95 137に対しstructured移行task resumeは320 turn・約20.08。まずtask/phase/session/modelごとのoutlierを追加AI callなしで可視化する。hard turn capやsession rotationはまだ導入しない。

## Amendments

none

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
- test/race/vet/build/gofmt、独立reviewer、必要なSol gate、commit

## Historical invariants

- History 2026-08-21 canonical telemetry分析

## Dependencies

- 001-requirement-task-lifecycle.md
- 008-machine-protocol-measurement.md

## Review findings

none

## Current boundary

未着手。
