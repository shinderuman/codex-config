# Task: worker model routingを評価可能にする

## Status

planned

## Original instruction

GLM-4.7等はsample不足のため、実運用データが揃うまで品質証拠なしのdowngradeをしない。ユーザー許可のないbenchmark目的追加callは禁止する。

## Amendments

none

## Purpose

Codex/GLM costとQuality Deltaを実データで比較できるようにする。

## Contract

- alias、resolved model、role、phase、quality outcome、tree usageを比較
- routing変更は別blocked判断へ渡す

## Must not

- sample不足でdowngrade、無許可benchmarkを行わない

## Acceptance criteria

- sample sufficiencyと評価metricを定義
- 現dataをunknownとして正しく表示
- review、必要なSol gate、commit

## Historical invariants

- 2026-08-21 GLM-4.7 sample 6 call tree

## Dependencies

- 009-worker-call-outliers.md
- 011-operation-category-telemetry.md

## Review findings

none

## Current boundary

依存data待ち。
