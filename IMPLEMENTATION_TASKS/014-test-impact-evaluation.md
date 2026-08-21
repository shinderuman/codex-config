# Task: test impact selectionを評価可能にする

## Status

planned

## Original instruction

operation categoryで実際に何testを実行しているかを可視化してから、省略可能性を評価する。品質証拠なしにtestを削らない。

## Amendments

none

## Purpose

検証品質を維持しながら無駄なtest costの有無を判断する。

## Contract

- change category、test category、failure/escaped outcomeを対応付ける
- selection導入は別blocked判断へ渡す

## Must not

- このtaskでtest省略をproduction有効化しない

## Acceptance criteria

- current execution coverageと省略候補の品質証拠
- unknown/insufficient dataを明示
- review、必要なSol gate、commit

## Historical invariants

- installer preflight、full test gate、escaped review履歴

## Dependencies

- 011-operation-category-telemetry.md

## Review findings

none

## Current boundary

依存task待ち。
