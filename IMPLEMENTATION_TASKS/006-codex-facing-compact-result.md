# Task: result field auditとCodex-facing compact structured output

## Status

planned

## Original instruction

現在の`GLM → structured_output(JSON) → Go Result → semantic validation → Result.Display() → 旧PACKET風text → Codex`を見直し、`GLM → typed structured output → Go semantic validation → compact machine-oriented structured result → Codex`を第一候補とする。長文fieldをJSON stringへ詰めるだけ、pretty print、同じ情報の複数field/文章重複で完了にしない。human-readable表示が必要なら明示modeへ分離しmachine protocolを正とする。

summary、requirement_coverage、tests、unverified、decision、evidence、options、recommendation、test_obligations、invariants、test_evidence、issues、residual_risk、sol_question、targets、artifactsを実データで、維持/typed化/short free text/削除/artifact-referenceへ分類する。Codexがfree textから状態/category/severity/target/option/recommendation/evidence range/test result/verification stateを再構築している箇所を優先するがtyped化自体を目的にしない。

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
