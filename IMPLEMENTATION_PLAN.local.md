# codex-worker-orchestrator 実装index

恒久workflowは `IMPLEMENTATION_RULES.md`、個別要求は `IMPLEMENTATION_TASKS/*.md`、完了証跡とescaped原因は `IMPLEMENTATION_HISTORY.md`を正とする。このfileへtask詳細を複製しない。

## 最上位目的

Sol High相当の品質をできるだけ維持しながらCodex / Sol側の実消費量を大幅に削減する。最上位EvalはDirect Codex対Codex + glm-workerのCodex ReductionとQuality Delta。

## ACTIVE

- `IMPLEMENTATION_TASKS/001-requirement-task-lifecycle.md`

## NEXT（優先順）

- `IMPLEMENTATION_TASKS/002-plan-final-head-postcondition.md`
- `IMPLEMENTATION_TASKS/003-targets-element-semantics.md`
- `IMPLEMENTATION_TASKS/004-self-contained-stdin-pty.md`
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
- implementation baseline: `ce86313`
- metadata boundary: 全未完了taskの新RULES migration後、task固有の補正・evaluation detailをRULESから除去して恒久invariantへ収束。Task 001 checkpointへのproduction code変更なし
- push: 禁止

## 現在の停止理由

Task 001 production統合のGLM worker-newが5時間rate limitで停止中。resetは2026-08-22 05:12:27 JST、再開予定は05:14:27 JST。checkpoint・sessionは保持。

## 次の親Codex操作

rate limit reset後、保存済みTask 001 worker checkpointを同じsessionから再開し、production統合へ進む。
