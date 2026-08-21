# Task: test impact selectionを評価可能にする

## Status

planned

## Original instruction

operation categoryで実際に何testを実行しているかを可視化してから、省略可能性を評価する。品質証拠なしにtestを削らない。

## Amendments

- 2026-08-22 parent maintenance:

````text
## 7. Task 014はcoarse `test` categoryだけで「test coverage」を主張しない

Task 011のcategoryが単に、

`test`

だけなら、

> 実際に何testを実行しているか

や、

> どのtestを省略可能か

までは分かりません。

現在Task 014には、

* change category
* test category
* current execution coverage

という表現がありますが、それらのsourceがまだ明確ではありません。

### 修正

Task 014実装前に、利用可能な一次証拠を明確にしてください。

raw commandを保存しない原則を維持したまま、既存stream-json/eventからdeterministically取得できるなら、

例えば必要最小限のtest subtypeとして、

* unit
* race
* vet
* build
* integration
* installer smoke
* other

等を検討して構いません。

ただしtaxonomyを先に増やさないでください。

既存情報で安全に分類できない場合は、

> test call数 / duration / failure outcomeまでは測れるが、suite-level coverageはunknown

と正直に扱ってください。

不十分なtelemetryから「省略可能」という結論を作らないでください。

`change category`も既存のdeterministic sourceがなければ新しいAI classificationを導入しないでください。
````

## Purpose

検証品質を維持しながら無駄なtest costの有無を判断する。

## Contract

- 利用可能な一次証拠を先に明示し、deterministically得られる範囲だけchange/test category、call数、duration、failure/escaped outcomeを対応付ける
- suite-level coverageを取得できない場合はunknownとし、taxonomyやAI classificationを先に増やさない
- selection導入は別blocked判断へ渡す

## Must not

- このtaskでtest省略をproduction有効化しない
- 不十分なtelemetryから省略可能性やcoverageを主張しない

## Acceptance criteria

- deterministic sourceのinventoryと、取得可能なtest call数/duration/failure outcome。suite-level coverageを取得不能ならunknown
- 省略候補は十分な品質証拠がある場合だけ提示
- unknown/insufficient dataを明示
- 独立reviewer、risk/contractに応じて必要なSol品質gate、commit

## Historical invariants

- installer preflight、full test gate、escaped review履歴

## Dependencies

- 011-operation-category-telemetry.md

## Review findings

none

## Current boundary

依存task待ち。
