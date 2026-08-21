# Task: review/fix model call縮小

## Status

blocked-user-permission

## Original instruction

````text
# 14. BLOCKED / USER PERMISSION WAITとして保持するtask

以下はtask fileを作ってよいが、`blocked-user-permission`とし自動開始しない。

---

## Blocked F: review/fix model call縮小

convergence実測後。

同一snapshot / verification-only / non-semantic round等が安全に縮小できる証拠が出た場合のみ。
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

review品質を維持してmodel callを削減する。

## Contract

convergence/quality evidenceと明示許可に基づく。

## Must not

reviewer省略を先行導入しない。

## Acceptance criteria

許可原文をAmendmentsへ保存し、Task 008 / 009 artifactを読んでconcrete Contract / Must not / Acceptance criteria / rollbackを確定してからACTIVE候補にする。

## Historical invariants

reviewer FIX_REQUIRED率、risk floor。

## Dependencies

- `IMPLEMENTATION_TASKS/008-machine-protocol-measurement.md`
- `IMPLEMENTATION_TASKS/009-worker-call-outliers.md`

## Review findings

none

## Current boundary

evidence/permission待ち。
