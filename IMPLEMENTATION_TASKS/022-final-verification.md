# Task: 全体verification

## Status

planned

## Original instruction

全未完了implementation完了後だけ、go test ./...、race、vet、build、gofmt、install smoke、self-protection、provider accounting、packet/result semantic、parent metadata guards、task lifecycle、PTY、multi-repo、repo-search、fixed Eval offline corpus、clean worktree、Plan/Task/History整合を確認する。このtask自身で新機能を追加せず、failureは該当taskをreopenする。

## Amendments

none

## Purpose

局所PASSを全体contract完了と誤認せずrelease可能性を確認する。

## Contract

- 列挙gateをfreshに実行し証跡化
- failureを原因taskへ戻す

## Must not

- 新機能追加、failureのその場scope拡張、無許可live Evalを行わない

## Acceptance criteria

- 全gate成功、clean worktree、metadata整合
- 独立reviewer、必要なSol gate、commit

## Historical invariants

- 全完了証跡の必要見出し

## Dependencies

- 002〜021の非blocked implementation完了

## Review findings

none

## Current boundary

最終段階まで開始禁止。
