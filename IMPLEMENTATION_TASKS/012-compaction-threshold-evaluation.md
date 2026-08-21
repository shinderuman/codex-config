# Task: compaction thresholdを評価可能にする

## Status

planned

## Original instruction

Task 011と既存telemetryを使いcompaction threshold変更を評価可能にし、task file再読方式でcompaction後の要求保持が改善したかを別軸で観測する。threshold変更そのものはユーザー許可と品質証拠まで保留する。

## Amendments

- 2026-08-22 parent maintenance:

````text
## 6. Task 012の「requirement preservation signal」を曖昧なproxyにしない

Task 012には、

> requirement preservation signal

という表現があります。

このままだとCodexが独自scoreやheuristicを作り、

> fileを読んだから要求保持できた

等を品質metricとして扱う危険があります。

### 修正

「要求保持」を単一の推測scoreにしないでください。

少なくとも以下を分離してください。

#### deterministic workflow evidence

compaction/resume scenarioで、

* RULES再読
* Plan再読
* ACTIVE task再読
* Original instruction / Amendments再読
* reviewerがContract vs Original instructionを確認

というproduction contractが成立したか。

#### semantic regression evidence

fixed scenarioでcompaction前後に、

* requirement
* Must not
* Acceptance criteria

が脱落せず実装/review判断へ反映されたか。

#### real operation observation

実運用で観測可能な事実だけ記録する。

file-read event等を観測できても、

> 読んだ = 理解した = 品質保持した

とは推定しない。

Task 012の目的は、

> threshold変更を評価可能にする

ことであり、

「要求保持score」を新規発明することではありません。
````

## Purpose

要求保持とtoken削減を混同せず、threshold変更の判断材料を作る。

## Contract

- operation category、turn/token、deterministic workflow evidence、semantic regression evidence、real operation observationを分離
- file-read eventを理解・品質保持のproxyにせず、単一のrequirement preservation scoreを作らない
- current thresholdは変更しない

## Must not

- 無許可threshold変更、追加benchmark callを行わない

## Acceptance criteria

- 変更判断に必要なmetricとbaseline
- compaction/resumeの再読contract成立とfixed semantic regressionを別々に検証し、実運用観測は観測可能な事実だけを記録
- blocked taskへ採否根拠を渡す
- 独立reviewer、risk/contractに応じて必要なSol品質gate、commit

## Historical invariants

- structured output compaction履歴、Task 001 lifecycle

## Dependencies

- 001-requirement-task-lifecycle.md
- 011-operation-category-telemetry.md

## Review findings

none

## Current boundary

依存task待ち。
