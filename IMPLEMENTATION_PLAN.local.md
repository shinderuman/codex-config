# codex-worker-orchestrator 実装計画

固定pathはrepository rootの`IMPLEMENTATION_PLAN.local.md`。このファイルを未完了作業と進行状態の唯一の正とし、Git管理する。内部TODOは補助扱い。

作業開始・再開時はこのファイルを必ず再読する。対象は新しいCodex session、context compaction後、rate limit/provider-unavailable後、長時間停止後、継続指示受領時、内部TODOとの不一致時。conversation memory・圧縮context・内部TODOより本ファイルを優先する。本ファイルとGit/working treeが矛盾する場合は現物を確認し、本ファイルを修正してから続行する。

完了証跡とescaped bug/review原因分析は`IMPLEMENTATION_HISTORY.md`へ分離する。同historyは通常の作業開始・再開時には全文を読まず、false-complete棚卸し、原因分析、過去対策とのinvariant照合で必要な見出しだけを検索して読む。現在状態・優先順・次の操作をhistoryへ重複記載しない。

## 最上位目的

Sol High相当の品質をできるだけ維持しながら、Codex / Sol側の実消費量を大幅に削減する。

GLM委譲率、PACKETサイズ、GLM token、Solへ戻した回数等は代理指標であり最終目的ではない。Direct Codex比でCodex削減が小さく、GLM消費が大きく、品質も低下する場合は、現architectureの維持を前提にせず、委譲範囲の縮小・簡素化・一部撤去を判断対象にする。

最上位EvalはDirect Codex対Codex + glm-workerの`Codex Reduction`と`Quality Delta`。

## 現在作業中

- Task: external reviewで判明したstructured output protocol regression修正
- 前段完了: telemetry coverage表示を実装し、必要test・race・lint・vet・build・独立reviewer・HIGH変更のSol品質gate・指摘後再review・個別commitを完了
- 現在境界: `FIX_REQUIRED`と`PASS`の`TARGETS`必須契約をtyped validatorへ復元し、producer JSON SchemaとGo parserの未知field受理集合を一致させる。旧status別required fieldsとの対応と修正retry分類を機械testで固定する
- 進行状態: 前taskのplan/historyを同一commitへamendする直前
- 次の操作: amend後に`install.sh`で本配置と一致を確認し、escaped review原因層規則を読んで新規GLM taskを開始する

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

- [ ] external reviewで判明したstructured output protocol regressionを修正する。`FIX_REQUIRED`と`PASS`の`TARGETS`必須契約を旧protocolからtyped validatorへ復元し、producer JSON Schemaが許容する未知fieldとGo parserの受理集合不一致を解消する。status別旧required fieldsから新schema/validatorへの対応表とproducer/consumer acceptance集合を機械testで固定する
- [ ] tracked canonical planのstale-by-one再発を修正する。final HEAD上のplanが完了済みcommitを「amend直前」「install前」と記述していないことを機械postconditionで保証し、運用instructionだけで解消扱いしない
- [ ] worker call長大化を品質を落とさず制御可能にする。v3 worker-new 41 callのturn中央値55・p95 137に対し現task resumeは320 turn／約20.08であり、まずoutlierをtask/phase/session別に可視化し、複数責務の事前分割または意味milestone checkpointへ返す。hard turn cap・session rotationは中断時の品質と追加call costを検証するまで導入しない
- [ ] compaction閾値、worker model routing、test impact selectionを品質を落とさず評価可能にする。現event metadataはBash回数・durationだけでtest/search/build等を区別できないため、raw commandを保存せずallowlist分類したoperation categoryを追加する。reviewerはvalid終端66件中8件が`FIX_REQUIRED`、GLM-4.7は6 call treeのみのため、review縮小・4.7拡大・test省略はDirect/orchestrated品質証拠なしに実施しない
- [ ] fixed Eval harnessとescaped bug/review corpusの残項目を統合
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
