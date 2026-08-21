# Task: exhaustive search gate

## Status

planned

## Original instruction

exhaustive確認が要求されたcaseではBM25 top-Nだけで完了扱いせず、worker query/resultをreviewerが独立検証する。

## Amendments

none

## Purpose

ranking上位だけで網羅性を誤認するfalse-completeを防ぐ。

## Contract

- exhaustive requirementを通常searchから区別
- corpus policyと独立検証証跡を固定

## Must not

- top-N取得だけでexhaustive表示しない

## Acceptance criteria

- positive/negative exhaustive scenarioとproduction wiring
- test、独立reviewer、Sol gate、commit

## Historical invariants

- BM25 corpus境界とfingerprint統一

## Dependencies

- 016-worker-repo-search-integration.md
- 017-reviewer-diff-first-search.md

## Review findings

none

## Current boundary

未着手。
