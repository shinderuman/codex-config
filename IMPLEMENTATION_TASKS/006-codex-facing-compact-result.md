# Task: result field auditとCodex-facing compact structured output

## Status

planned

## Original instruction

````text
# 7. machine protocol改善を3 taskへ分割する

現在Planの「machine-oriented Codex出力とmachine-only schema単純化」を1巨大taskで実装しない。

---

## Task 006: result field auditとCodex-facing compact structured output

### Purpose

内部structured outputを最後に旧PACKET風textへ戻し、Codexが再度自然言語から構造を復元する無駄を削減する。

### 現状

`GLM`
→ Claude `structured_output`
→ Go `Result`
→ semantic validation
→ `Result.Display()`
→ `STATUS: ...`等の旧PACKET風text
→ Codex

### 目標

`GLM`
→ typed structured output
→ Go semantic validation
→ compact machine-oriented structured result
→ Codex

### field audit

少なくとも以下を実データで棚卸し。

- summary
- requirement_coverage
- tests
- unverified
- decision
- evidence
- options
- recommendation
- test_obligations
- invariants
- test_evidence
- issues
- residual_risk
- sol_question
- targets
- artifacts

各fieldを、

- current structureが自然な最小表現なので維持
- typed object/array/enum/bool/ID化
- short free text維持
- 削除
- artifact/reference分離

へ分類し、根拠をtask artifactへ残す。

typed化のためのtyped化は禁止。

特にCodexがfree textから、

- 状態
- category
- severity
- target
- option
- recommended/rejected
- evidence path/range
- test result
- verification state

を再構築しているものを優先。

### Codex-facing output

通常machine経路はcompact JSONを第一候補。

pretty printしない。

長文をそのままJSON stringへ詰めただけで完了にしない。

同じ情報の重複を除く。

human-readable表示が本当に必要なら明示modeへ分離し、machine protocolを正とする。

### IMPORTANT

現在`Result.Display()`はstdoutだけでなくprompt/state/checkpoint等にも利用されている可能性があるため、単純にMarshalへ置換せず全call siteを分類する。

machine protocolとdiagnostic human projectionを混同しない。

### schema/validator責務

- JSON Schema: type/enum/basic required
- Go: status間semantic/workflow invariant
- free text: schema化しにくい新規意味

複雑なschema compositionを増やさない。

### semantic contract

Task 003のTARGETS正規形を含め、status別contractをtableとして固定する。

---
````

## Amendments

none

## Purpose

Codexの再解釈tokenとprotocol correctionを削減し、最上位Codex Reductionへ接続する。

## Contract

- `Result.Display()`全call siteをmachine output、prompt/state/checkpoint、human diagnosticへ分類
- JSON Schemaはtype/enum/basic required、Goはworkflow semantic、free textは新規意味へ限定
- Task 003のTARGETS正規形を含むstatus別contractをtable化

## Must not

- JSON化自体を目的にしない
- complex schema composition、MCP、daemon、socket、persistent processを導入しない
- semantic情報を削ってbytesだけ減らさない

## Acceptance criteria

- field audit artifactと根拠
- compact machine output実装、人間向けprojection分離、全consumer配線
- 重複削減、semantic保持、schema/validator acceptance一致
- output bytes/token proxyの基礎比較
- test/race/vet/build/gofmt、独立reviewer、Sol gate、commit

## Historical invariants

- structured output移行`22c1d0b`、status契約修正`ce86313`

## Dependencies

- 003-targets-element-semantics.md
- 005-multi-repository-isolation.md

## Review findings

- internal JSON化をCodex-facing machine protocol完了と局所化したfalse-complete

## Current boundary

未着手。通常stdoutは旧PACKET風Displayのまま。
