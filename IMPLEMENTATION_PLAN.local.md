# codex-worker-orchestrator 実装計画

固定pathはrepository rootの`IMPLEMENTATION_PLAN.local.md`。このファイルを未完了作業と進行状態の唯一の正とし、Git管理する。内部TODOは補助扱い。

作業開始・再開時はこのファイルを必ず再読する。対象は新しいCodex session、context compaction後、rate limit/provider-unavailable後、長時間停止後、継続指示受領時、内部TODOとの不一致時。conversation memory・圧縮context・内部TODOより本ファイルを優先する。本ファイルとGit/working treeが矛盾する場合は現物を確認し、本ファイルを修正してから続行する。

完了証跡とescaped bug/review原因分析は`IMPLEMENTATION_HISTORY.md`へ分離する。同historyは通常の作業開始・再開時には全文を読まず、false-complete棚卸し、原因分析、過去対策とのinvariant照合で必要な見出しだけを検索して読む。現在状態・優先順・次の操作をhistoryへ重複記載しない。

## 最上位目的

Sol High相当の品質をできるだけ維持しながら、Codex / Sol側の実消費量を大幅に削減する。

GLM委譲率、PACKETサイズ、GLM token、Solへ戻した回数等は代理指標であり最終目的ではない。Direct Codex比でCodex削減が小さく、GLM消費が大きく、品質も低下する場合は、現architectureの維持を前提にせず、委譲範囲の縮小・簡素化・一部撤去を判断対象にする。

最上位EvalはDirect Codex対Codex + glm-workerの`Codex Reduction`と`Quality Delta`。

## 現在作業中

- Task: structured output移行を本配置し、Codex desktop親tool orchestrationのterminal payload二面表示を同じproduction caller境界で解消する
- 前段完了: role別typed schema・authoritative `structured_output`・意味検証1回修正・v2→v3 checkpoint変換・telemetry分離・installer preflightを独立reviewとSol品質ゲート後に個別commit済み
- 現在境界: `glm-worker` processのaccepted terminal resultは1件だけとし、長時間background cellではraw stdoutを描画せず内部保存し、cell終端後の短い同期callで一度だけ親へ渡す。JSON object全体の二面表示を防ぐcaller contractであり、repo側blind dedupeは行わない
- 進行状態: structured output production移行の初回commit完了。plan/historyを完了証跡と次taskへ同期して同一commitへamend中
- 次の操作: amend後に`install.sh`本配置・binary/instruction一致を確認し、追加AI callなしのdelayed markerと、次の実`glm-worker` terminal resultでraw内部保存→同期1回取得のユーザー可視postconditionを検証する

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

- [x] Codex↔GLM structured outputをproduction workflowへ単一protocolとして移行
  - [x] status/risk/decision/targets/artifacts/test obligations/options/state transition/evidence/findings等をtyped schemaへ移し、未知の意味問題だけ短いfree-textへ残す。日本語を英語化すること自体を目的にしない
  - [x] Claude runnerへ`--json-schema`とauthoritative `structured_output`抽出を統合し、schema vocabularyをGo側でobject root・properties/required/enum/array/items/boolean/string/numberへ事前制限する。CLI/result contract欠落とstructured retry exhaustionはfail closedにする
  - [x] worker/reviewer/report-only/fix/recompressionのproduction dispatchをtyped resultへ統一し、field間workflow semantics・artifact存在・risk floor・snapshot/state invariantはGo validator/state machineへ保持する
  - [x] 独自`PACKET_BEGIN/END`・KEY parser・duplicate/stray/reemit・構造欠陥用recompressionの不要部分を削除し、旧PACKET+JSONの恒久二重protocolを作らない。provider error/rate-limitのplain/result signal分類は維持する
  - [x] unit/scenario/production integration、session resume、telemetry exact-once、report-only、provider recovery、installer preflightを固定。自然発生した最初の429で分類channelを再確認し、retry-exhaustion頻度・costが旧recompressionを上回る場合は撤退する
- [ ] Codex desktopのbackground `functions.exec`完了outputと後続`functions.wait` result cardで同一terminal payloadが二面表示される親orchestration境界を回避する。単一postconditionを「1 accepted terminal resultにつき親tool orchestration全体でユーザー可視payloadは1回」とし、長時間cellではraw stdoutを描画せず内部保存、完了後の短い同期callで一度だけ親へ渡す。`EVAL.md`のcaller側echo除外を撤回し、追加AI callなしのdelayed markerと実`glm-worker` terminal resultで同じproduction caller境界を検証するまで完了扱いにしない。structured JSONの同一object全体が二度描画され得るため、JSON移行だけを解決根拠にしない
- [ ] reviewer snapshotと親専有plan/history更新を両立させる。rate-limit待機中の親Codex必須更新だけでreview-resumeの全worktree同一性が失敗した実例を基準に、worker/reviewer実装surfaceの外部変更はfail closedのまま、親管理2fileの承認済み親変更を識別してreview再開できる単一contractへ修正する。親更新禁止・snapshot全面緩和・worker/reviewer checklist追加では代替しない
- [ ] telemetry測定基盤を改善し、TaskStats model call数とraw JSONL record数のcoverageを`complete/incomplete`・欠損call数・usage unknownとして表示する。既知の`ccc205d1`はwrapper孤児化時の1 callがstatsだけに残るため、推測補完せずhistorical gapとして分離する
- [ ] worker call長大化を品質を落とさず制御可能にする。v3 worker-new 41 callのturn中央値55・p95 137に対し現task resumeは320 turn／約20.08であり、まずoutlierをtask/phase/session別に可視化し、複数責務の事前分割または意味milestone checkpointへ返す。hard turn cap・session rotationは中断時の品質と追加call costを検証するまで導入しない
- [ ] compaction閾値、worker model routing、test impact selectionを品質を落とさず評価可能にする。現event metadataはBash回数・durationだけでtest/search/build等を区別できないため、raw commandを保存せずallowlist分類したoperation categoryを追加する。reviewerはvalid終端66件中8件が`FIX_REQUIRED`、GLM-4.7は6 call treeのみのため、review縮小・4.7拡大・test省略はDirect/orchestrated品質証拠なしに実施しない
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
