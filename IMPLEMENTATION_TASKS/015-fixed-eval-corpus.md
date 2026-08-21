# Task: fixed Eval harness/corpusの未実装部分を統合

## Status

planned

## Original instruction

wrapperで固定できるoffline/fake-provider scenarioだけを対象に、HIGH semantic defectをreviewer/Solが逃すcase、external feasibility未検証なのにproductionへ進むcase、safe-stopだけで親USER_REQUEST完了扱いするcase、診断に本文が必要なのにstatus/sizeだけ残すcaseを統合する。既にwiring済みcontractはfalse-complete確認だけ行い、重複checklistを増殖させない。実Sol/Codexを使うlive positive/negative Evalはユーザー明示許可待ちとして分離する。

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
- test/race/vet/build/gofmt、独立reviewer、Sol gate、commit

## Historical invariants

- `e79e1ab`、`6d8d278`、`fc5f740`、`6257133`

## Dependencies

- 001-requirement-task-lifecycle.md

## Review findings

none

## Current boundary

既存wiringあり。live behaviorはpermission待ち。
