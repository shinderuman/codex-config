# Task: GLM-5-Turbo等model routing再設計

## Status

blocked-user-permission

## Original instruction

実測品質証拠と許可条件が揃うまで保留する。

## Amendments

- 2026-08-22 parent maintenance:

````text
## 11. blocked taskはplaceholder contractのままACTIVE化しない

blocked taskには、

> 許可後の個別contract

だけが書かれているものがあります。

現在blockedである間はそれで構いません。

ただしユーザー許可が出た時に、そのままACTIVEへ昇格して実装開始しないでください。

まず、

1. ユーザー許可をAmendmentへlossless保存
2. prerequisite evaluation artifactを読む
3. concrete Contract
4. Must not
5. Acceptance criteria

をtask fileへ確定する。

その後でACTIVE候補にしてください。

「permission received」だけで設計未確定taskをGLMへ投げないでください。
````

## Purpose

品質を維持してprovider costを最適化する。

## Contract

Task 013のevidenceに基づく。

## Must not

sample不足downgradeをしない。

## Acceptance criteria

許可原文をAmendmentsへ保存し、Task 013 artifactを読んでconcrete Contract / Must not / Acceptance criteriaを確定してからACTIVE候補にする。

## Historical invariants

GLM-4.7 sample不足。

## Dependencies

- 013-worker-model-routing-evaluation.md

## Review findings

none

## Current boundary

evidence/permission待ち。
