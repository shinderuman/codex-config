# Task: tracked canonical plan stale-by-oneを機械postconditionで解消

## Status

planned

## Original instruction

````text
## Task 002: tracked canonical plan stale-by-oneを機械postconditionで解消

現在PlanでACTIVEになっている既存未完了task。

### Contract

commit-ready初回commit
→ 親metadata同期
→ amend
という手順を「守るはず」ではなく、final HEADのpostconditionで保証する。

### Acceptance

final HEAD上で少なくとも、

- PlanのACTIVE/NEXTがHEAD実態と一致
- 完了済みcommitを「amend直前」と書いていない
- 完了済みcommitを「install前」と誤記していない
- 削除済みtask fileをACTIVE参照していない
- ACTIVE task fileが実在する
- PlanのGit境界とHEADが矛盾しない

ことを機械確認する。

新しい4層構造に合わせてpostconditionを作り直す。

文書instructionだけを対策にしない。

---
````

## Amendments

- 2026-08-22: 旧2file構造向けinstall preflight案はTask 001より先に実装しない。退避patchは`/private/tmp/codex-config-stale-task-a0e6878f.patch`、SHA-256 `2b33f6b0e097238651e1b46a8847189681b74c8df09c73b3b914b792a7ed7c6f`

## Purpose

canonical Planが次sessionの誤った制御情報になる再発を親orchestration層で止める。

## Contract

- final HEADのPlan/ACTIVE/NEXT/task-file存在/Git境界をproduction pathで検証する
- commit前完了禁止、同一commit metadata同期、push禁止を維持する
- Task 001後の4層構造を対象にする

## Must not

- instruction・checklist・今回だけの手修正で完了扱いしない
- worker-startへ不必要なglobal fail-closed面を増やさない

## Acceptance criteria

- 4cedc91型stale、削除済みACTIVE、欠損ACTIVE file、HEAD境界不一致を実Git scenarioで拒否
- 同期済みfinal HEADと正当なworking tree作業状態を許容
- failure後の同一commit復旧経路を固定
- test/shell syntax/vet/build/gofmt、独立reviewer、Sol gate、commit

## Historical invariants

- Historyの「tracked canonical plan stale-by-one再発」および`c6a0bb0`

## Dependencies

- 001-requirement-task-lifecycle.md

## Review findings

- `4cedc91`のPlanは完了済みcommitをamend/install前として記述し、旧対策直後にfalse-completeが再発した

## Current boundary

Task 001後に新構造で再設計する。旧構造向け未review差分はpatch退避済みでworking treeから除去。
