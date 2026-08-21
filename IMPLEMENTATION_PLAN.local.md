# codex-worker-orchestrator 実装計画

固定pathはrepository rootの`IMPLEMENTATION_PLAN.local.md`。このファイルを未完了作業と進行状態の唯一の正とし、Git管理する。内部TODOは補助扱い。

作業開始・再開時はこのファイルを必ず再読する。対象は新しいCodex session、context compaction後、rate limit/provider-unavailable後、長時間停止後、継続指示受領時、内部TODOとの不一致時。conversation memory・圧縮context・内部TODOより本ファイルを優先する。本ファイルとGit/working treeが矛盾する場合は現物を確認し、本ファイルを修正してから続行する。

完了証跡とescaped bug/review原因分析は`IMPLEMENTATION_HISTORY.md`へ分離する。同historyは通常の作業開始・再開時には全文を読まず、false-complete棚卸し、原因分析、過去対策とのinvariant照合で必要な見出しだけを検索して読む。現在状態・優先順・次の操作をhistoryへ重複記載しない。

## 最上位目的

Sol High相当の品質をできるだけ維持しながら、Codex / Sol側の実消費量を大幅に削減する。

GLM委譲率、PACKETサイズ、GLM token、Solへ戻した回数等は代理指標であり最終目的ではない。Direct Codex比でCodex削減が小さく、GLM消費が大きく、品質も低下する場合は、現architectureの維持を前提にせず、委譲範囲の縮小・簡素化・一部撤去を判断対象にする。

最上位EvalはDirect Codex対Codex + glm-workerの`Codex Reduction`と`Quality Delta`。

## 現在作業中

- Task: `install.sh` preflightが途中のtest/build failure後も配置を続行するfail-openを修正し、watch本配置を再検証する
- 発見経路: watch本配置時、tracked `IMPLEMENTATION_HISTORY.md`未分類でworkflow testが失敗した一方、preflight subshellは`if ! (...)`文脈で後続commandへ進み、最後の成功をsubshell成功としてbinary/instruction配置まで続行した
- 前段完了: plan/history両surfaceの親Codex専有・self-protection/model-call前後guardと、after-read failureを含む実行済みTask Work Call telemetry exact-onceを独立review・Sol品質ゲート後にcommit済み
- 次の操作: installer preflightの最初の失敗を確実に伝播し、managed file/binaryへ触れず停止するcontractを独立taskで実装・review・commitする。全test通過後に再install・binary/instruction一致・実運用watch終了を確認する

更新タイミング:

- 各コミット直後
- 優先順位・依存関係の変更時
- GLM rate limitまたはprovider障害による停止時
- 新しい作業指示の受領・棚卸し時
- 新しいGLM task開始時、reviewer移行時、Sol判断待ち、resume時、task完了時
- 外部レビューでescaped bug/reviewが見つかった時
- Eval結果で採用・撤回・保留が決まった時
- taskを分割・統合した時

このplanの本文、`[x]`、優先順、現在状態を更新できるのは親Codexだけとする。GLM worker/reviewerはこのfileを編集せず、必要な更新候補と根拠をPACKETへ記載する。

各commit直後に完了項目、残課題、優先順、現在作業中、Eval待ちを照合する。未完了を`[x]`にせず、完了済みTODOを残さない。

未完了項目を`[x]`へ移せるのは、必要な実装・unit/scenario test・独立reviewer・HIGH時のSol品質ゲート・指摘後の再review・個別commitまで完了した時だけ。GLM workerの`IMPLEMENTED`だけでは完了にしない。調査/Eval判断taskは固有の完了条件を満たした時点とする。

GLMにはcommitさせない。独立review・必要なSol確認・品質ゲートを通過してCodexが承認した項目は、追加確認を待たずCodexが適切な粒度で個別commitする。pushは禁止。

実行基盤へ影響する変更は、review・承認・個別commit完了後の適切な区切りで`install.sh`により本配置し、配置済み現物を確認する。改善を長期間未配置のまま溜めず、後続taskで実運用結果を得られる状態にする。

## 未完了（優先順）

- [ ] `install.sh` preflightで途中のtest/build failureが後続commandの成功に上書きされ配置が続行するfail-openを修正し、最初の失敗でmanaged file/binaryへ触れず停止することをtestで固定する
- [ ] task terminal status後も`glm-worker --watch`が終了せず親Codexが無出力待機を続ける実運用問題を、watch process lifecycleと親completion wait contractの境界で特定・修正する
- [ ] `ba2414b` BM25 repo-search coreのfalse-completeを修正する。fingerprintとenumeration/rebuildでrepository/corpus policyを共有し、nested repo・submodule・default/追加exclude・large file・binary・symlinkの検索対象外状態とcache freshness影響をproduction `Search()` testで固定する
- [ ] tracked canonical planのstale-by-oneを解消する。commit-ready→commit→親Codex plan最終同期→同一commit amend等の単純contractを確定し、commit前の虚偽完了とcommit後のobsolete HEADを両方防ぐ。大規模ledgerは追加しない
- [ ] Codex↔GLM structured output PoCを実施する
  - [ ] 現行Claude Code・Z.ai Coding Plan・GLM-5.3 mapping・`-p`・`--output-format stream-json`・session resumeの実経路で公式`--json-schema`成立性を最小PoC確認する。schema適合最終output、stream-json抽出、resume維持、invalid/unsupported fail closed、provider classification、token/cost/session metadataを検証し、不成立ならproductionへ進まず`NEEDS_SOL_DECISION`へ戻す
  - [ ] 現行PACKETとstructured outputについてbyte/token proxy、format/recompression call、意味情報欠落、parser/validator複雑性、GLM call数、Sol判断情報保持を比較する。実Sol High Direct baseline・本番A/Bはユーザー明示許可なしに実行しない
  - [ ] 成立後の目標protocolはstatus/risk/decision/targets/artifacts/test obligations/options/state transition/evidence/findings等をtyped JSONへ移し、未知の意味問題だけ短いfree-textへ残す。日本語を英語化すること自体を目的にしない
  - [ ] PoC成立前にproduction PACKET/parserを置換しない。成立後は独自`PACKET_BEGIN/END`・KEY parser・duplicate/stray/reemitの不要部分を削除し、旧PACKET+JSONの恒久二重protocolを避ける。field間workflow semanticsは必要に応じGo validator/state machineへ残す
  - [ ] MCP/hosted MCP quota、custom Tool Use、`--input-format stream-json`長寿命process、daemon/socketは今回scope外。`1 model call = 1 subprocess`とsession ID/`--resume` continuityを維持する
- [ ] fixed Eval harnessとescaped bug/review corpusの残項目を統合
  - [x] workflow clock abstraction逸脱を固定（`946a49e`、install preflight・配置一致完了）
  - [ ] reviewer/SolがHIGH変更の意味上の欠陥を逃すbehavioral scenarioを固定（wrapper production gateは`e79e1ab`で固定。live reviewer/Sol positive/negative Evalは明示許可待ち）
  - [ ] 未検証の外部成立性を越えてPoCからproduction/IaCへ進むcaseを拒否するbehavioral scenarioを固定（parent Eval contract/wiringは`6d8d278`で固定。live parent behaviorは明示許可待ち）
  - [ ] 安全停止・状態保全だけで未解決の親USER_REQUESTを完了扱いするcaseを拒否するbehavioral scenarioを固定（parent Eval contract/wiringは`fc5f740`で固定。live parent behaviorは明示許可待ち）
  - [ ] 原因判定に本文等が必要なのにstatus/sizeだけを残して修正を重ねるcaseを拒否するbehavioral scenarioを固定（parent Eval contract/wiringは`6257133`で固定。live parent behaviorは明示許可待ち）
- [ ] workerは対象不明時repo-search、reviewerはdiff起点impact expansion後に必要時だけ独立検索する方針を実装
- [ ] exhaustive確認ではBM25だけで終了せず、workerのquery/resultをreviewerが独立検証するgateを追加
- [ ] repo-search feature flag、CLI、install.sh配布連携、worker/reviewer利用、installer smokeを実装
- [ ] repo-search telemetryとDirect/orchestratedを含むEval A/B比較を実装
- [ ] compaction閾値、worker model routing、test impact selectionを品質を落とさず評価可能にする
- [ ] Eval結果で必要性が確認できたconditional review/tool output改善だけを追加
- [ ] 全test/race/vet/build/install smoke、fixed Eval、self-protection、provider accounting、packet contract、clean worktreeを確認
- [ ] 実行基盤へ影響する個別commit後の適切な区切りで`install.sh`本配置と配置済み現物一致を確認

## 意図的にEval結果待ち

- [ ] 実Sol High Direct baselineとorchestrated本番A/Bを同一条件で実行し、Codex Reduction / Quality Deltaを判定する（ユーザー明示許可後のみ実行）
- [ ] GLM-5-Turbo等のmodel routing再設計
- [ ] compaction閾値の変更
- [ ] test impact selectionによるテスト省略
- [ ] conditional rule/tool outputの追加採用
- [ ] session agingの実測結果から、同一task内session rotationが必要か判断する（compactionとは別論点）
- [ ] review/fix convergenceの実測結果から、同一snapshot・verification-only・非意味変更roundのreviewer model callを縮小できるか判断する

## 実装しない

- AST/LSP/code graph
- vector DB/embedding/reranker
- MCP/外部search API/外部有料observability
- 新reviewer層、reviewer subagent、自動multi-agent探索
- provider abstraction拡大
- ユーザー許可のないbenchmark目的の実Sol/Codex消費、無意味な大量repeat、観測用追加AI prompt
