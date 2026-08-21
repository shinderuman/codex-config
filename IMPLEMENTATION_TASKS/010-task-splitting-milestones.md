# Task: task事前分割とsemantic milestoneを運用評価

## Status

planned

## Original instruction

Task 009と新task management導入後のデータを使い、巨大taskの事前分割、意味milestone checkpoint、resume boundaryが品質を落とさずcall長大化を抑えるか評価する。hard turn capは証拠なしに導入しない。

## Amendments

none

## Purpose

worker call長大化を機械的切断ではなく責務境界で抑える。

## Contract

- task file粒度とobserved call dataを対応付ける
- quality/call cost/追加call数を併記する

## Must not

- hard cap、無条件rotation、品質証拠なしの分割強制を行わない

## Acceptance criteria

- 分割/milestone/resumeの比較と採否条件
- session rotationとは別論点で結論
- review、必要なSol gate、commit

## Historical invariants

- worker outlier履歴、session aging観測

## Dependencies

- 009-worker-call-outliers.md

## Review findings

none

## Current boundary

Task 009待ち。
