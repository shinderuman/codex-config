# Task: compaction thresholdを評価可能にする

## Status

planned

## Original instruction

Task 011と既存telemetryを使いcompaction threshold変更を評価可能にし、task file再読方式でcompaction後の要求保持が改善したかを別軸で観測する。threshold変更そのものはユーザー許可と品質証拠まで保留する。

## Amendments

none

## Purpose

要求保持とtoken削減を混同せず、threshold変更の判断材料を作る。

## Contract

- operation category、turn/token、requirement preservation signalを分離
- current thresholdは変更しない

## Must not

- 無許可threshold変更、追加benchmark callを行わない

## Acceptance criteria

- 変更判断に必要なmetricとbaseline
- blocked taskへ採否根拠を渡す
- review、必要なSol gate、commit

## Historical invariants

- structured output compaction履歴、Task 001 lifecycle

## Dependencies

- 001-requirement-task-lifecycle.md
- 011-operation-category-telemetry.md

## Review findings

none

## Current boundary

依存task待ち。
