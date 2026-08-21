# Task: TARGETS element semanticを修正

## Status

planned

## Original instruction

現HEAD `ce86313`の`packet.requireTargets()`は配列長だけを検査するため`targets:[""]`と`["   "]`を許し、`NEEDS_SOL_REVIEW`のnone拒否もall-noneだけなので`["none","foo.go:10"]`を許す。required statusは1要素以上、全elementはTrimSpace後非空、NEEDS_SOL_REVIEWはnoneを1要素でも含めたら拒否する。PASS/FIX_REQUIRED/NEEDS_SOL_DECISIONでnone sentinelを残すなら`["none"]`を正規形とし、noneと具体targetの混在を全statusで拒否することを検討する。PACKET report-only契約を壊さず、正規形を1箇所で定義してworker/reviewerで共有する。

## Amendments

none

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
