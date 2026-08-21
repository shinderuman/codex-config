# Task: session rotation

## Status

blocked-user-permission

## Original instruction

session aging実測後まで保留し、compactionとは別論点として扱う。

## Amendments

none

## Purpose

長寿命sessionの品質/cost劣化がある場合だけ対策する。

## Contract

Task 009/010のevidenceに基づく。

## Must not

無条件rotationやhard capを導入しない。

## Acceptance criteria

許可後のrotation条件とquality comparison。

## Historical invariants

session aging telemetry。

## Dependencies

- 009-worker-call-outliers.md
- 010-task-splitting-milestones.md

## Review findings

none

## Current boundary

evidence/permission待ち。
