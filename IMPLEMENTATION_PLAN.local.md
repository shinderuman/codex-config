# codex-worker-orchestrator 実装index

恒久workflowは `IMPLEMENTATION_RULES.md`、個別要求は `IMPLEMENTATION_TASKS/*.md`、完了証跡とescaped原因は `IMPLEMENTATION_HISTORY.md`を正とする。このfileへtask詳細を複製しない。

## 最上位目的

Sol High相当の品質をできるだけ維持しながらCodex / Sol側の実消費量を大幅に削減する。最上位EvalはDirect Codex対Codex + glm-workerのCodex ReductionとQuality Delta。

## ACTIVE

- `IMPLEMENTATION_TASKS/post-integration-contract-consistency.md`

## NEXT（優先順）

- `IMPLEMENTATION_TASKS/004-self-contained-stdin-pty.md`
- `IMPLEMENTATION_TASKS/instruction-fixed-context-audit.md`
- `IMPLEMENTATION_TASKS/005-multi-repository-isolation.md`
- `IMPLEMENTATION_TASKS/006-codex-facing-compact-result.md`
- `IMPLEMENTATION_TASKS/007-machine-only-legacy-cleanup.md`
- `IMPLEMENTATION_TASKS/008-machine-protocol-measurement.md`
- `IMPLEMENTATION_TASKS/009-worker-call-outliers.md`
- `IMPLEMENTATION_TASKS/010-task-splitting-milestones.md`
- `IMPLEMENTATION_TASKS/011-operation-category-telemetry.md`
- `IMPLEMENTATION_TASKS/012-compaction-threshold-evaluation.md`
- `IMPLEMENTATION_TASKS/013-worker-model-routing-evaluation.md`
- `IMPLEMENTATION_TASKS/014-test-impact-evaluation.md`
- `IMPLEMENTATION_TASKS/015-fixed-eval-corpus.md`
- `IMPLEMENTATION_TASKS/016-worker-repo-search-integration.md`
- `IMPLEMENTATION_TASKS/017-reviewer-diff-first-search.md`
- `IMPLEMENTATION_TASKS/018-exhaustive-search-gate.md`
- `IMPLEMENTATION_TASKS/019-repo-search-product-wiring.md`
- `IMPLEMENTATION_TASKS/020-repo-search-telemetry-eval.md`
- `IMPLEMENTATION_TASKS/021-conditional-improvements.md`
- `IMPLEMENTATION_TASKS/022-final-verification.md`

## BLOCKED / USER_PERMISSION_WAIT

- `IMPLEMENTATION_TASKS/101-live-sol-ab.md`
- `IMPLEMENTATION_TASKS/102-model-routing-redesign.md`
- `IMPLEMENTATION_TASKS/103-compaction-threshold-change.md`
- `IMPLEMENTATION_TASKS/104-test-impact-selection.md`
- `IMPLEMENTATION_TASKS/105-session-rotation.md`
- `IMPLEMENTATION_TASKS/106-review-call-reduction.md`

## 現在のGit境界

- branch: `main`
- implementation baseline: Task 003 completion commit（current HEAD）
- metadata boundary: Task 003をHistoryへ移行してtask fileを削除。割り込み要求を`post-integration-contract-consistency.md`へlosslessに固定してACTIVEとし、Task 004はNEXT先頭を維持。TARGETS element正規形・status横断受理集合・dispatch前補正をcurrent HEADへ収録し、final HEAD gate・本配置・installed/source一致・status smoke確認済み
- push: 禁止

## 現在の停止理由

Task 003は完了。Task 001/002 production integration後の不整合修正taskを割り込みACTIVEとして固定済みで未着手。

## 次の親Codex操作

`post-integration-contract-consistency.md`のlossless sourceとderived contractを再読し、production/state/task lifecycle contractの収束作業を開始する。
