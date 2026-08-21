# Task: test impactによるtest省略

## Status

blocked-user-permission

## Original instruction

品質証拠後までtest省略を保留する。

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

verification cost削減可能性を安全に採否する。

## Contract

Task 014のevidenceと許可に基づく。

## Must not

品質証拠なしにtestを削らない。

## Acceptance criteria

許可原文をAmendmentsへ保存し、Task 014 artifactを読んでconcrete selection Contract / Must not / Acceptance criteria / rollbackを確定してからACTIVE候補にする。

## Historical invariants

full test gate。

## Dependencies

- 014-test-impact-evaluation.md

## Review findings

none

## Current boundary

evidence/permission待ち。
