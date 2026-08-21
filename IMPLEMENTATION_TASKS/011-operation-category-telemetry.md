# Task: allowlist operation category telemetryを追加

## Status

planned

## Original instruction

raw command本文を保存せず、search、test、build、format、install、git-read、git-write、file-read、file-write、other等のcoarse allowlist categoryをevent metadataへ追加する。分類のためのAI callを追加せず、privacyと肥大化を避ける。

## Amendments

none

## Purpose

compaction、test impact、tool usageを生commandなしで評価可能にする。

## Contract

- deterministic allowlist分類、unknownはother
- raw command、秘密、path本文を新規保存しない

## Must not

- AI分類、full command logging、高cardinality labelを追加しない

## Acceptance criteria

- 各categoryと曖昧case、privacy、旧eventの扱いをtest固定
- stats/timeline接続と加法整合
- test/race/vet/build/gofmt、独立reviewer、Sol gate、commit

## Historical invariants

- Historyのstream-json event metadata、telemetry exact-once

## Dependencies

- 001-requirement-task-lifecycle.md

## Review findings

none

## Current boundary

未着手。
