# Task: 本配置一致確認

## Status

planned

## Original instruction

実行基盤へ影響する個別commitを適切な区切りで`install.sh`本配置し、installed binary、managed instructions、source HEADの一致を確認する。改善を長期間未配置のまま後続実運用へ進めない。

## Amendments

none

## Purpose

repository上の改善と実運用binary/promptの乖離を防ぐ。

## Contract

- install preflight成功とbyte/hash一致
- source-only metadata変更とruntime変更を区別

## Must not

- review/commit前install、dirty implementation installを行わない

## Acceptance criteria

- 各runtime区切りのinstall smokeと現物一致
- final全体verification後の一致確認

## Historical invariants

- installer preflight fail-closed、managed file一致

## Dependencies

- runtime影響taskのcommit
- 022-final-verification.md

## Review findings

none

## Current boundary

各commit区切りで参照するが、最終完了はTask 022後。
