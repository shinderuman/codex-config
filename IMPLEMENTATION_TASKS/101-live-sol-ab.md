# Task: 実Sol High Direct baseline対orchestrated本番A/B

## Status

blocked-user-permission

## Original instruction

ユーザー明示許可後だけ同一条件で実行し、Codex ReductionとQuality Deltaを最上位判定する。

## Amendments

none

## Purpose

orchestrator全体の最終価値を実測する。

## Contract

同一条件、actual usage、quality artifact、時間、GLM tree usageを比較する。

## Must not

明示許可なしに実行しない。

## Acceptance criteria

許可後の再現可能A/Bと採否。

## Historical invariants

fixed eval-ab基盤。

## Dependencies

- 008-machine-protocol-measurement.md
- 020-repo-search-telemetry-eval.md
- 022-final-verification.md

## Review findings

none

## Current boundary

ユーザー許可待ち。
