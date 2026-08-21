# Task: allowlist operation category telemetryを追加

## Status

planned

## Original instruction

````text
## Task 011: operation category telemetry

raw command本文を保存せず、

- search
- test
- build
- format
- install
- git-read
- git-write
- file-read
- file-write
- other

等のallowlist coarse categoryをevent metadataへ追加する。

分類のために新しいAI callを追加しない。

command文字列のprivacy/肥大化を避ける。
````

## Amendments

- 2026-08-22 parent maintenance:

````text
## 5. operation category telemetryを不要に多surfaceへ展開しない

Task 011の本来目的は、

> raw commandを保存せず、粗いoperation categoryを取得し、compaction / test impact / tool usage評価へ使う

ことです。

現在Acceptanceに、

> stats/timeline接続

まで入っています。

これは目的達成に必要とは限りません。

### 修正

まず最小実装として、

* event metadataへdeterministic category
* 保存telemetryから集計可能
* Task 012 / 014が読み取れる

ところまでを正としてください。

status / timeline / watch等の複数presentation surfaceへ表示することを必須完了条件にしないでください。

既存の単一read-only aggregation経路で十分ならそれを再利用してください。

実際に人間/親Codexがtimeline表示を必要とする証拠が出た場合だけ追加してください。

観測機能のためにpresentation codeを増殖させないでください。
````

## Purpose

compaction、test impact、tool usageを生commandなしで評価可能にする。

## Contract

- deterministic allowlist分類、unknownはother
- raw command、秘密、path本文を新規保存しない
- event metadataから保存telemetryの単一read-only aggregation経路で集計し、Task 012 / 014が利用できるようにする

## Must not

- AI分類、full command logging、高cardinality labelを追加しない
- status / timeline / watch等のpresentation surfaceを必要性の証拠なしに増やさない

## Acceptance criteria

- 各categoryと曖昧case、privacy、旧eventの扱いをtest固定
- 保存telemetry上の集計と加法整合、Task 012 / 014からの読取可能性
- test/race/vet/build/gofmt、独立reviewer、risk/contractに応じて必要なSol品質gate、commit

## Historical invariants

- Historyのstream-json event metadata、telemetry exact-once

## Dependencies

none

## Review findings

none

## Current boundary

未着手。
