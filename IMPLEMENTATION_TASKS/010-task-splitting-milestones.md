# Task: task事前分割とsemantic milestoneを運用評価

## Status

planned

## Original instruction

````text
## Task 010: task事前分割 / semantic milestoneの運用評価

Task 009のデータを使い、

-巨大taskの事前分割
-意味milestone checkpoint
- resume boundary

が品質を落とさずcall長大化を抑えるか評価する。

このTask management再設計自体の導入後データも使う。

hard capは証拠なしに導入しない。
````

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
- 独立reviewer、risk/contractに応じて必要なSol品質gate、commit

## Historical invariants

- worker outlier履歴、session aging観測

## Dependencies

- `IMPLEMENTATION_TASKS/009-worker-call-outliers.md`

## Review findings

none

## Current boundary

Task 009待ち。
