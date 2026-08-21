# Task: conditional review/tool output改善

## Status

planned

## Original instruction

Task 008、009、011等の結果で効果が確認された改善だけを採用し、事前に便利そうという理由で追加しない。

## Amendments

- 2026-08-22 parent maintenance:

````text
## 4. Task 021は通常のGLM implementation taskではなくparent decision gateとして扱う

`021-conditional-improvements.md`は自身ですでに、

* umbrellaをGLMへ直接dispatchしない
* measurement結果でcandidateを採否
* 採用candidateは別task fileを作る

としています。

これは通常implementation taskではありません。

親Codexのevaluation / decision checkpointです。

### 修正

Task 021をGLM workerへそのままdispatchしないことを維持してください。

実行時は親CodexがTask 008 / 009 / 011等のartifactを読み、

* 採用
* 棄却
* data不足で保留

を判定するだけにしてください。

採用する実装は必ずsemantic filenameの独立taskを作成する。

棄却結果はHistoryへ記録する。

Task 021自身のためだけに通常implementation task同等の、

* GLM implementation
* 独立reviewer
* Sol gate

を機械的に回さないでください。

必要なanalysisが大きい場合だけworkerへread-only調査を委譲してください。
````

## Purpose

親Codexのdecision checkpointとして、品質証拠なしの複雑性増殖を防ぐ。

## Contract

- candidateごとにevidence、expected reduction、quality risk、rollbackを示す
- 親CodexがTask 008 / 009 / 011等のartifactから採用・棄却・data不足を判定する
- 採用変更はsemantic filenameの個別taskへ分割し、棄却はHistoryへ記録する

## Must not

- このumbrellaをそのままGLM実装taskへ渡さない
- 通常implementation同等のGLM implementation、独立reviewer、Sol gateを機械的に回さない。大規模analysisだけread-only workerへ委譲できる

## Acceptance criteria

- candidateの採用/棄却/保留を測定結果で決定
- 採用分は新task file、棄却はHistoryへ記録

## Historical invariants

- complexity responsibility、conditional convergence方針

## Dependencies

- `IMPLEMENTATION_TASKS/008-machine-protocol-measurement.md`
- `IMPLEMENTATION_TASKS/009-worker-call-outliers.md`
- `IMPLEMENTATION_TASKS/011-operation-category-telemetry.md`

## Review findings

none

## Current boundary

測定task待ち。parent decision gateのため直接dispatch禁止。
