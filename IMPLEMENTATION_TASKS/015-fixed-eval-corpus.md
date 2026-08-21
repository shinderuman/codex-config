# Task: fixed Eval harness/corpusの未実装部分を統合

## Status

planned

## Original instruction

````text
## Task 015: fixed Eval harness/corpusの未実装部分統合

wrapperで固定できるoffline/fake-provider scenarioだけを対象。

以下の既存contractを重複実装しない。

- HIGH semantic defectをreviewer/Solが逃すcase
- external feasibility未検証なのにproductionへ進むcase
- safe-stopだけで親USER_REQUEST完了扱いするcase
- diagnosisに本文が必要なのにstatus/sizeだけ残すcase

既にwiring済みのものはfalse-complete確認だけし、追加checklistを増殖させない。

### live behavior

実Sol/Codexを消費するpositive/negative Evalは別途ユーザー明示許可待ちとし、Task 015完了条件へ混ぜない。
````

## Amendments

none

## Purpose

既知escaped behaviorを追加AI callなしのproduction-path corpusへ固定する。

## Contract

- existing wrapper gate/wiringを再利用し未実装だけ追加
- scripted期待packetとproduction prompt/dispatch因果を分離固定

## Must not

- live Eval、重複prompt checklist、新reviewer層を追加しない

## Acceptance criteria

- 4 caseのoffline contractとwiring現物照合
- false-completeなら該当taskをreopen
- test/race/vet/build/gofmt、独立reviewer、risk/contractに応じて必要なSol品質gate、commit

## Historical invariants

- `e79e1ab`、`6d8d278`、`fc5f740`、`6257133`

## Dependencies

- `IMPLEMENTATION_TASKS/001-requirement-task-lifecycle.md`

## Review findings

none

## Current boundary

既存wiringあり。live behaviorはpermission待ち。
