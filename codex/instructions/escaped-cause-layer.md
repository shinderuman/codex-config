# escaped bug/reviewの原因層分類

外部review・実運用・後続taskで、検証・review・commit済みの変更に不具合(escaped bug)や見逃し(escaped review)が見つかり、その原因分析を開始する場合だけ適用する。親Codexのorchestration contractであり、worker/reviewerへの個別checklist追加で代替しない。

## 適用条件

- escaped bug・escaped reviewの原因分析を開始する場合だけ適用する。
- 通常の実装・調査task、review通過時の通常確認、新規依頼の受け付けへこの分類を要求しない。

## 分析開始時の原因層分類

- 対策・prompt変更・gate追加の検討より先に、production code・prompt・PACKET契約・raw telemetry/log・Git履歴等の一次証拠から、失敗が発生した層を分類する。
- `glm-worker`内部のworker/reviewer pipeline失敗: production promptとdispatch分岐の不一致、scenario/test契約と実装の不一致、matcherや状態遷移の契約漏れ等。
- 親Codex orchestration失敗: critical assumptionの確定、親USER_REQUEST lifecycle、runtime evidence管理、semantic deltaに基づくreview invocation等、親側の委譲・受理・遷移契約の不足。
- 一次証拠で層が確定できない場合は推測で確定させず、原因判定に必要な一次証拠の取得を先にする。

## 対策の層整合

- 親Codex orchestration失敗が原因の場合、worker/reviewer promptへの個別checklist追加や個別gate追加だけで解決扱いにしない。親側contractで対策する。
- worker/reviewer prompt・個別gate・新しい対策を追加する前に、原因が本当にその層で発生したかを分類結果と照合する。
- 既存対策が直接対応している原因へ、重複する新しい対策を追加しない。

## Eval・scenarioの因果要求

- scripted runnerが期待packetを直接返すscenarioは、期待終端の再現だけでは採用根拠としない。productionのprompt・dispatch分岐と実際に渡す内容・期待判断の因果を別testで固定する。

## orchestration

- 原因分析の調査はGLMへ委譲してよい。原因層の分類と対策方向の最終判断は親Codexが行い、GLMだけで確定させない。
- 分類結果と対策方向は、既存対策の維持・撤回・追加を判断する根拠として次の対策判断へ引き継ぐ。
