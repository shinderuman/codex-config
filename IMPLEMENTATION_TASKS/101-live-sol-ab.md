# Task: 実Sol High Direct baseline対orchestrated本番A/B

## Status

blocked-user-permission

## Original instruction

````text
# 14. BLOCKED / USER PERMISSION WAITとして保持するtask

以下はtask fileを作ってよいが、`blocked-user-permission`とし自動開始しない。

---

## Blocked A: 実Sol High Direct baseline vs orchestrated本番A/B

ユーザー明示許可後のみ。

最上位判定:

- Codex Reduction
- Quality Delta
````

## Amendments

- 2026-08-22 parent maintenance:

````text
## 12. `tree usage`等の未定義metricを勝手に増やさない

Task 013やblocked A/B等に、既存schema上の意味が明確でないmetric名がある場合は確認してください。

特に`tree usage`が現在のtelemetry/Evalで具体的な定義を持つか確認すること。

既存metricなら正しい名称・定義をHistorical invariantへ紐付ける。

存在しない/曖昧なら、このtaskのためだけに新telemetryを増やさず削除または既存usage metricへ置換してください。

名前だけから新しい観測機能を実装しないでください。
````

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

orchestrator全体の最終価値を実測する。

## Contract

同一条件、actual usage、quality artifact、時間と、A/B schemaの`glm_usage`を比較する。`glm_usage.source=glm-worker-task-stats`はTask Work Callのalias別tree token集計とmodel call数を使い、曖昧な別metricのためにtelemetryを追加しない。

## Must not

明示許可なしに実行しない。

## Acceptance criteria

許可受領時は原文をAmendmentsへ保存し、prerequisite artifactを読んでconcrete Contract / Must not / Acceptance criteriaを確定してからACTIVE候補にする。その後、再現可能A/Bと採否。

## Historical invariants

fixed eval-ab基盤。`glm-worker/internal/abeval/usage.go`の`GLMUsageFromTaskStats()`。

## Dependencies

- `IMPLEMENTATION_TASKS/008-machine-protocol-measurement.md`
- `IMPLEMENTATION_TASKS/020-repo-search-telemetry-eval.md`
- `IMPLEMENTATION_TASKS/022-final-verification.md`

## Review findings

none

## Current boundary

ユーザー許可待ち。
