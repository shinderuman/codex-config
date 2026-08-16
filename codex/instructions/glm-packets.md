# GLM結果処理

`glm-worker`からpacketまたは`STATUS: WORKER_ERROR`を含む結果を受け取った場合に適用する。

## 共通

- `ARTIFACTS`が`none`以外なら、要求・判断・報告に必要な成果物だけを記載パスから確認し、packetへ全内容を転載しない。
- 原因不明runtime failureの診断に必要なevidenceを求めた依頼では、`ARTIFACTS`参照先を`~/.codex/instructions/failure-evidence.md`の受理条件で必要範囲だけ確認する。

## `STATUS: NEEDS_SOL_DECISION`

- `DECISION`・`EVIDENCE`・`OPTIONS`・`RECOMMENDATION`・`TEST_OBLIGATIONS`を評価する。
- packetで足りるならリポジトリを再探索しない。判断不能な場合だけ`TARGETS`に限定して現物を確認する。
- 判断後は元依頼を再記述せず`glm-worker --decision "<判断>"`で同じworker sessionを継続する。

## `STATUS: PASS`

- 圧縮packetについて、要求との意味的一致・要求漏れ・矛盾・残余リスクを評価する。
- `RISK: LOW`かつ不整合・不確実性がなければ、GLMの調査をやり直さず全diffも読まない。
- PASSを機械的に信用せず、圧縮された意味情報への最終判断はSol Highが行う。

## `STATUS: NEEDS_SOL_REVIEW`

- `TARGETS`と`SOL_QUESTION`に限定して実コードまたはdiffを確認する。
- 修正が必要ならCodex自身で編集せず`glm-worker --fix "<修正方針>"`で同じworker sessionへ差し戻す。修正後は独立reviewerまで自動再実行される。

## `STATUS: WORKER_ERROR`

- エラー要約を確認し、無関係なリポジトリ調査をSol Highが代行しない。
- session破損が明示されている場合だけ`glm-worker --reset`後に再実行する。
