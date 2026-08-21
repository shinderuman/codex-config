# Task: reviewer diff-first impact expansionと独立search

## Status

planned

## Original instruction

````text
## Task 017: reviewer diff-first impact expansion + independent search

reviewerはまずdiff起点。

impact expansion後、必要な時だけ独立search。

worker search結果をそのまま信頼しない。
````

## Amendments

none

## Purpose

review tokenを抑えつつ影響範囲漏れと自己充足reviewを防ぐ。

## Contract

- diff-first、impact expansion、conditional independent searchをproduction prompt/dispatchへ配線
- worker query/resultとreviewer検証を分離記録

## Must not

- reviewer常時full search、worker結果の無検証採用をしない

## Acceptance criteria

- diff充分/不足、independent query、impact漏れscenario
- test、独立reviewer、risk/contractに応じて必要なSol品質gate、commit

## Historical invariants

- reviewer独立性、BM25 core

## Dependencies

- `IMPLEMENTATION_TASKS/016-worker-repo-search-integration.md`

## Review findings

none

## Current boundary

未着手。
