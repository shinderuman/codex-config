# Task: machine-only backward compatibilityとlegacy migrationを棚卸し・削減

## Status

planned

## Original instruction

glm-worker/Codex/GLMだけが読むmachine dataを公開APIのように扱わず、old parser/legacy field/migration/fallback/deprecated suffix・phase inference/複数schema同時維持を「一応読める」だけで恒久保持しない。対象はstructured result、Codex-facing output、resume/checkpoint、telemetry/passive event JSONL、timeline/convergence/status、repo-search cache、report-only metadata、Result text parser、stats、protocol state。

実在候補として`packet.FromDisplayLines()`、v2→v3 checkpoint、old report-only phase/suffix推定、old stats、old telemetry skip、`PacketCompactions`、`--decision`/`--fix` argv compatibility等をproduction用途から分類する。old versionはreject/skip/reset/rebuild/delete/resume不能へ単純化し、active checkpointはtask完了後変更・旧binary完了・Sol判断で保護する。cacheはdiscard/rebuild、logsはcurrent versionから蓄積し一回限り分析をproduction migrationにしない。

## Amendments

none

## Purpose

parser/state分岐とescaped surfaceを削減しcurrent contractへ収束する。

## Contract

- legacy候補を現在必要/active task一時保護/削除/skip-reset-rebuildへ根拠付き分類
- schema意味変更はversionを上げ、同version内の意味driftを作らない
- current validationはstrict fail closedを維持

## Must not

- 既存実装や後方互換だけを残存理由にしない
- machine schema互換とhuman CLI featureを混同しない
- active checkpointを無断破壊しない

## Acceptance criteria

- 全対象inventory artifact
- 不要parser/migration/fallback/推定削除
- mismatch方針とactive task保護をproduction/testで固定
- code/branch量変化を測定可能にする
- test/race/vet/build/gofmt、独立reviewer、Sol gate、commit

## Historical invariants

- v2/v3 checkpoint、structured output、telemetry version、repo-search cacheの履歴見出し

## Dependencies

- 006-codex-facing-compact-result.md

## Review findings

none

## Current boundary

未着手。実在legacy候補の網羅確認前。
