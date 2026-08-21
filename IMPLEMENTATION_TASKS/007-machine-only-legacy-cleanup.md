# Task: machine-only backward compatibilityとlegacy migrationを棚卸し・削減

## Status

planned

## Original instruction

````text
## Task 007: machine-only backward compatibility / legacy migrationを棚卸しして削減

### User requirement

glm-worker自身・Codex/GLMだけが読むmachine dataについて、公開APIのような後方互換性を原則維持しない。

人間が過去versionを読むための互換layerを積み増す意味はない。

### 対象

少なくとも:

- GLM→glm-worker structured result
- glm-worker→Codex machine output
- resume/checkpoint JSON
- telemetry JSONL
- passive event JSONL
- timeline/convergence/status観測schema
- repo-search cache
- report-only checkpoint metadata
- Result display/text parser
- protocol state
- internal stats schema

### 現在実在するlegacy候補を必ず確認

例:

- `packet.FromDisplayLines()`による旧text PACKET→Result変換
- v2→v3 checkpoint upgrade
- old report-only checkpointをphase/suffixから推定する処理
- old stats archive compatibility
- old telemetry version skip
- `PacketCompactions`等の旧protocol専用field
- `--decision` / `--fix` argv modeを「後方互換の短文用」として残す記述
- legacy field / fallback parser / deprecated suffix inference

上記は「削除しろ」と先に決めつけず、実際のproduction用途を分類する。

ただし、

「既に実装済みだから」
「一応古いものも読めるから」

は残す理由にならない。

### 基本方針

machine-only old versionは用途に応じて:

- reject
- skip
- reset
- rebuild
- delete
- resume不能として明示終了

へ単純化。

active checkpointだけは変更時点の進行taskを壊さないよう、

- task完了後にschema変更
- old binaryでそのtaskだけ完了
- 必要ならSol判断

で保護する。

恒久migrationとは分離。

### cache

再生成可能cache:

`version mismatch → discard → rebuild`

旧cache migration禁止。

### telemetry/event log

新schemaへ変えたら新runからcurrent schemaを正にする。

過去logが必要な一回限りの分析はofflineで行い、production migration frameworkへしない。

version意味変更を同version番号内で行わない。

### current validation

後方互換を削ることとfail-open化を混同しない。

current schemaについてはstrict validationを維持。

### CLI argv compatibility

`--decision` / `--fix`を残すかは、「人間向け公開CLIとして現在も有用か」で判断する。

Codex transportがstdin modeへ統一され、実利用がなく、後方互換だけが理由なら削除候補。

ただしユーザーがterminalから短いdecision/fixを手入力する実用途があるなら、machine schema互換とは別にhuman CLI featureとして評価する。

---
````

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
- test/race/vet/build/gofmt、独立reviewer、risk/contractに応じて必要なSol品質gate、commit

## Historical invariants

- v2/v3 checkpoint、structured output、telemetry version、repo-search cacheの履歴見出し

## Dependencies

- 006-codex-facing-compact-result.md

## Review findings

none

## Current boundary

未着手。実在legacy候補の網羅確認前。
