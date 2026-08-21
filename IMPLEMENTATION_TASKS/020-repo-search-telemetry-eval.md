# Task: repo-search telemetryとEval hooks

## Status

planned

## Original instruction

repo-searchをDirect/orchestrated A/Bへ接続できるtelemetryとfixed Eval hookを実装する。本番benchmark自体はユーザー許可待ち。

## Amendments

none

## Purpose

search導入のCodex ReductionとQuality Deltaを測定可能にする。

## Contract

- query category、hit/miss、result count、fallback、durationを秘密なしで記録
- A/B schemaへ接続し実runは分離

## Must not

- raw query/result本文、無許可本番A/Bを保存・実行しない

## Acceptance criteria

- telemetry加法整合とfixed Eval hook
- test/race/vet/build/gofmt、独立reviewer、risk/contractに応じて必要なSol品質gate、commit

## Historical invariants

- eval-ab read-only、telemetry exact-once

## Dependencies

- 019-repo-search-product-wiring.md

## Review findings

none

## Current boundary

未着手。
