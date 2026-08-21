# Task: session rotation

## Status

blocked-user-permission

## Original instruction

````text
# 14. BLOCKED / USER PERMISSION WAITとして保持するtask

以下はtask fileを作ってよいが、`blocked-user-permission`とし自動開始しない。

---

## Blocked E: session rotation

session aging実測後。
compactionとは別論点。
````

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

長寿命sessionの品質/cost劣化がある場合だけ対策する。

## Contract

Task 009/010のevidenceに基づく。

## Must not

無条件rotationやhard capを導入しない。

## Acceptance criteria

許可原文をAmendmentsへ保存し、Task 009 / 010 artifactを読んでconcrete rotation Contract / Must not / Acceptance criteria / quality comparisonを確定してからACTIVE候補にする。

## Historical invariants

session aging telemetry。

## Dependencies

- `IMPLEMENTATION_TASKS/009-worker-call-outliers.md`
- `IMPLEMENTATION_TASKS/010-task-splitting-milestones.md`

## Review findings

none

## Current boundary

evidence/permission待ち。
