# codex-worker-orchestrator 実装計画

固定pathはrepository rootの`IMPLEMENTATION_PLAN.local.md`。このファイルを未完了作業と進行状態の唯一の正とし、Git管理する。内部TODOは補助扱い。

作業開始・再開時はこのファイルを必ず再読する。対象は新しいCodex session、context compaction後、rate limit/provider-unavailable後、長時間停止後、継続指示受領時、内部TODOとの不一致時。conversation memory・圧縮context・内部TODOより本ファイルを優先する。本ファイルとGit/working treeが矛盾する場合は現物を確認し、本ファイルを修正してから続行する。

完了証跡とescaped bug/review原因分析は`IMPLEMENTATION_HISTORY.md`へ分離する。同historyは通常の作業開始・再開時には全文を読まず、false-complete棚卸し、原因分析、過去対策とのinvariant照合で必要な見出しだけを検索して読む。現在状態・優先順・次の操作をhistoryへ重複記載しない。

## 最上位目的

Sol High相当の品質をできるだけ維持しながら、Codex / Sol側の実消費量を大幅に削減する。

GLM委譲率、PACKETサイズ、GLM token、Solへ戻した回数等は代理指標であり最終目的ではない。Direct Codex比でCodex削減が小さく、GLM消費が大きく、品質も低下する場合は、現architectureの維持を前提にせず、委譲範囲の縮小・簡素化・一部撤去を判断対象にする。

最上位EvalはDirect Codex対Codex + glm-workerの`Codex Reduction`と`Quality Delta`。

## 現在作業中

- Task: PoCで成立したCodex↔GLM structured outputをproduction workflowへ単一protocolとして移行する
- 前段完了: Claude Code 2.1.226・Z.ai Coding Plan・GLM-5.3・stream-json・`--json-schema`の実経路でschema適合output 2/2、同一session resume、deterministic extraction、metadata保持、invalid/unsupported fail closed、provider分類channel維持を確認し、現行PACKET比較後にGo判断を個別commit済み
- 移行境界: `1 model call = 1 subprocess`とsession continuityを維持する。schema vocabularyをGo側で事前制限し、typed resultを唯一のworkflow protocolへ移す。field間semantic validation・disk/state invariantはGo側に残し、旧PACKET+JSONの恒久二重protocol、MCP/custom tool、persistent process、daemon/socketは導入しない
- 次の操作: schema/typed result、Claude runner transport、worker/reviewer/report-only/recompression経路、semantic validator、telemetry、migration testのproduction責務境界をGLMへ設計・実装させ、独立reviewとHIGH時のSol品質ゲートへ進める

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

- [ ] Codex↔GLM structured outputをproduction workflowへ単一protocolとして移行
  - [ ] status/risk/decision/targets/artifacts/test obligations/options/state transition/evidence/findings等をtyped schemaへ移し、未知の意味問題だけ短いfree-textへ残す。日本語を英語化すること自体を目的にしない
  - [ ] Claude runnerへ`--json-schema`とauthoritative `structured_output`抽出を統合し、schema vocabularyをGo側でobject root・properties/required/enum/array/items/boolean/string/numberへ事前制限する。CLI/result contract欠落とstructured retry exhaustionはfail closedにする
  - [ ] worker/reviewer/report-only/fix/recompressionのproduction dispatchをtyped resultへ統一し、field間workflow semantics・artifact存在・risk floor・snapshot/state invariantはGo validator/state machineへ保持する
  - [ ] 独自`PACKET_BEGIN/END`・KEY parser・duplicate/stray/reemit・構造欠陥用recompressionの不要部分を削除し、旧PACKET+JSONの恒久二重protocolを作らない。provider error/rate-limitのplain/result signal分類は維持する
  - [ ] unit/scenario/production integration、session resume、telemetry exact-once、report-only、provider recovery、installer preflightを固定し、自然発生した最初の429で分類channelを再確認する。retry-exhaustion頻度・costが旧recompressionを上回る場合は撤退する
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
