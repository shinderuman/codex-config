# Task: review/fix model call縮小

## Status

blocked-user-permission

## Original instruction

同一snapshot、verification-only、non-semantic round等が安全に縮小できるconvergence証拠が出た場合だけ検討する。

## Amendments

none

## Purpose

review品質を維持してmodel callを削減する。

## Contract

convergence/quality evidenceと明示許可に基づく。

## Must not

reviewer省略を先行導入しない。

## Acceptance criteria

許可後の安全条件とrollback。

## Historical invariants

reviewer FIX_REQUIRED率、risk floor。

## Dependencies

- 008-machine-protocol-measurement.md
- 009-worker-call-outliers.md

## Review findings

none

## Current boundary

evidence/permission待ち。
