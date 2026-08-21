# Task: TARGETS element semanticを修正

## Status

planned

## Original instruction

````text
# 1. 最新pushの外部review結果

## 1.1 review範囲

`4cedc91..ce86313`

新規commitは以下の2件。

- `0ae3e46 improve: telemetry coverageを明示する`
- `ce86313 fix: structured outputの意味契約を復元する`

`0ae3e46`について、今回確認したsource範囲では新しい具体的なproduction defectは見つけていない。
TaskStats model call数とcurrent-version raw Task Work Call record数をtask単位で比較し、missing/excess/orphan/unreadableを分離し、usage completenessを推測しない方向は現在の目的と整合している。

`ce86313`では前回外部reviewで指摘した以下2件は概ね修正されている。

- reviewer `PASS` / `FIX_REQUIRED` / `NEEDS_SOL_REVIEW`およびworker `NEEDS_SOL_DECISION`の`TARGETS`契約脱落
- producer JSON Schemaが未知propertyを許す一方、consumer Go parserが未知fieldを拒否していた受理集合不一致

ただし、以下の新しいfalse-completeが残っている。

## 1.2 HIGH: TARGETS「非空」契約が要素内容まで強制されていない

現HEADの`packet.requireTargets()`は`len(result.Targets) == 0`だけを拒否している。

そのため以下が通る。

- `targets: [""]`
- `targets: ["   "]`

これは`FIX_REQUIRED`でauto-fix対象が実質空のままdispatchされる、または`NEEDS_SOL_DECISION`でSolが読む対象を実質失うことを許す。

さらに`NEEDS_SOL_REVIEW`ではEVAL上、

> `none`要素拒否

と明示しているが、実装は`listOnlyNone()`で「全要素がnoneの時」だけ拒否している。

したがって、

`targets: ["none", "glm-worker/internal/foo.go:10"]`

は通る。

契約文と実装の受理集合が一致していない。

### 修正要求

TARGETS element contractを明示し、少なくとも以下を機械検証すること。

- requiredなstatusでは配列長1以上
- 各elementは`TrimSpace`後に空でない
- `NEEDS_SOL_REVIEW`では`none`を**1要素でも含めたら拒否**
- `PASS` / `FIX_REQUIRED` / `NEEDS_SOL_DECISION`で概念的対象なしを表す`none` sentinelを残すなら、`["none"]`を正規形とする
- `none`と具体targetの混在を許す意味がないなら全statusで混在拒否することを検討する
- `PACKET`予約値はreport-only用途だけで成立する既存contractを壊さない
- whitespace-only、empty element、mixed none、duplicate target、case variantをtestで固定する
- producer schemaで表現できないsemanticはGo validatorで強制する

単にtestを追加して現在の`listOnlyNone()`を温存するのではなく、TARGETSの正規形を1箇所で定義してworker/reviewer両方が共有すること。

### なぜCodex+GLMが見落としたか

前回reviewの「TARGETSがfieldとして脱落した」という問題を修正することに集中し、

- fieldが存在する
- arrayが空でない

までを復元した時点で旧contractを戻したと判断した。

しかし「非空field」という旧text protocolの意味をtyped arrayへ移す際、

`TARGETS: foo`

の「値自体が非空」という条件を、

`len([]string) > 0`

だけへ誤って写像した。

またEVAL文言は「NEEDS_SOL_REVIEWのnone要素拒否」となっているのに、unit implementationは「all-none拒否」になっており、contract文とpredicateの集合比較をしていない。

前回と同じく、schema/validatorの各安全策を局所的にreviewし、producer/consumer/semantic predicateの**受理集合そのもの**をtable-drivenに比較しなかったことが原因。

この原因を`IMPLEMENTATION_HISTORY.md`へ追加すること。

---

## Task 003: latest review false-complete — TARGETS element semanticを修正

本指示「1.2」のreview findingをそのままtask contractへ保存する。

### 必須test

- empty array
- `[""]`
- `["   "]`
- `["none"]`
- `["NONE"]`
- `["none", "foo.go:10"]`
- concrete single target
- multiple concrete target
- PACKET report-only
- duplicate target
- worker NEEDS_SOL_DECISION
- reviewer PASS
- reviewer FIX_REQUIRED
- reviewer NEEDS_SOL_REVIEW

old protocolからの意味対応では「field存在」だけでなくelement正規形を比較する。

---
````

## Amendments

none

## Resolved references

- 「本指示『1.2』」= Original instruction内の`## 1.2 HIGH: TARGETS「非空」契約が要素内容まで強制されていない`

## Purpose

typed arrayへの移行で「非空field」を配列長だけへ誤写像したacceptance-set regressionを修正する。

## Contract

- TARGETS element正規形を単一predicateで定義
- schemaで表現できないsemanticはGo validatorで強制
- PACKET reserved valueとstatus別none規則を明示

## Must not

- `listOnlyNone()`を温存してtestだけ増やさない
- field存在だけで旧意味契約復元としない

## Acceptance criteria

- empty array、empty element、whitespace、none、NONE、mixed none、concrete single/multiple、PACKET、duplicateを固定
- worker NEEDS_SOL_DECISION、reviewer PASS/FIX_REQUIRED/NEEDS_SOL_REVIEWをtable-drivenに比較
- auto-fix/親dispatch前にinvalidを止めるproduction-path test
- test/race/vet/build/gofmt、独立reviewer、Sol gate、commit

## Historical invariants

- Historyの「structured output status契約と受理集合の脱落」

## Dependencies

- 001-requirement-task-lifecycle.md
- 002-plan-final-head-postcondition.md

## Review findings

- why missed: field存在とarray非空までで復元完了とし、element集合とEVAL文言/predicate集合を比較しなかった

## Current boundary

未着手。外部review `4cedc91..ce86313`で再open。
