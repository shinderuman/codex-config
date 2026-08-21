# Task: machine protocol変更前後のmeasurement

## Status

planned

## Original instruction

同じsemantic payloadで旧PACKET風textとcompact structured outputを比較し、glm-worker→Codex stdout bytes、token proxy、free text bytes/ratio、structured field bytes/ratio、field重複、legacy/migration code量、protocol branch数、format correction call、semantic correction call、information loss、Codex判断に必要なsemantic保持を測る。JSON punctuation/key名で逆に増える場合はJSON形式を目的化しない。実Sol High Direct A/Bはユーザー明示許可なしで実行しない。

## Amendments

none

## Purpose

protocol簡素化が見た目ではなくCodex Reductionとmaintenance costへ効くか判断する。

## Contract

- 追加AI callなしのfixed input比較を正とする
- semantic lossをbytes削減と相殺しない
- correction callは保存telemetryから比較する

## Must not

- 実Sol/Codex本番A/Bを無許可実行しない
- JSON採用を成功条件にしない

## Acceptance criteria

- 列挙metricのbefore/afterと再現可能artifact
- semantic保持判定と採用/撤退基準
- Direct/orchestrated本番A/Bをpermission待ちのまま分離
- test、独立reviewer、必要なSol gate、commit

## Historical invariants

- 2026-08-21 telemetry分析、fixed Eval基盤

## Dependencies

- 006-codex-facing-compact-result.md
- 007-machine-only-legacy-cleanup.md

## Review findings

none

## Current boundary

未着手。
