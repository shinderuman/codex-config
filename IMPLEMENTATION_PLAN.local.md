# codex-worker-orchestrator 実装計画

固定pathはrepository rootの`IMPLEMENTATION_PLAN.local.md`。このファイルを未完了作業と進行状態の唯一の正とし、Git管理する。内部TODOは補助扱い。

作業開始・再開時はこのファイルを必ず再読する。対象は新しいCodex session、context compaction後、rate limit/provider-unavailable後、長時間停止後、継続指示受領時、内部TODOとの不一致時。conversation memory・圧縮context・内部TODOより本ファイルを優先する。本ファイルとGit/working treeが矛盾する場合は現物を確認し、本ファイルを修正してから続行する。

完了証跡とescaped bug/review原因分析は`IMPLEMENTATION_HISTORY.md`へ分離する。同historyは通常の作業開始・再開時には全文を読まず、false-complete棚卸し、原因分析、過去対策とのinvariant照合で必要な見出しだけを検索して読む。現在状態・優先順・次の操作をhistoryへ重複記載しない。

## 最上位目的

Sol High相当の品質をできるだけ維持しながら、Codex / Sol側の実消費量を大幅に削減する。

GLM委譲率、PACKETサイズ、GLM token、Solへ戻した回数等は代理指標であり最終目的ではない。Direct Codex比でCodex削減が小さく、GLM消費が大きく、品質も低下する場合は、現architectureの維持を前提にせず、委譲範囲の縮小・簡素化・一部撤去を判断対象にする。

最上位EvalはDirect Codex対Codex + glm-workerの`Codex Reduction`と`Quality Delta`。

## machine protocol単純化方針

- Codex向け通常出力は旧PACKET風textを正とせず、`GLM typed structured output → Go semantic validation → compact structured JSON → Codex`を第一候補とする。長文fieldのJSON string詰め替えでは完了にせず、machine-oriented表現、重複排除、typed化、最小free text、人間向け表示の分離を一体で行う
- result fieldの`evidence`・`options`・`recommendation`・`tests`・`test_obligations`・`issues`・`residual_risk`・`sol_question`・`requirement_coverage`を棚卸しし、自然な最小表現の維持、object/array/enum/bool/ID化、短いfree text維持、削除、artifact/reference分離のいずれかへ根拠付き分類する。Codexが文章から状態・分類・対象・選択肢・参照先を再解釈しているfieldを優先するが、typed化自体を目的にしない
- current statusごとのrequired field・risk constraint・targets・artifact・fix scope・report-only・終端契約を一覧化し、JSON Schemaの型/enum/object/array/basic requiredと、Go validator/state machineのworkflow semanticを対応付ける。producer schemaとconsumer parser/validatorの受理集合を一致させ、schema化しにくい新規意味情報だけをfree textへ残す
- glm-workerだけが生成・消費するstructured result、Codex向け出力、resume/checkpoint、telemetry/passive event JSONL、convergence/timeline/status観測schema、repo-search cache等は長期公開APIとして扱わず、current schemaだけを正とする。旧parser・legacy field・migration・fallback・suffix/phase推定・複数schema同時維持を恒久追加しない
- machine-only旧versionは用途に応じてskip/reset/rebuild/delete/resume不能の明示終了を選ぶ。active checkpointがあるschema変更はtask完了後、旧binaryでの完了、必要時のSol判断で保護し、恒久migrationと混同しない。telemetry/eventはversion不一致をskipし、必要なら今回限りのoffline集計だけを行う。再生成可能cacheはversion mismatchでdiscard/rebuildする
- 後方互換削除とcurrent validation緩和を混同せず、wrong repository・corrupt/incomplete payload・unknown invalid state・semantic violationはfail closedを維持する。同一version内で意味を変えず、意味変更時はversionを上げて旧versionを読まない
- 効果はglm-worker→Codex output bytes/token proxy、free text比率、structured field比率、legacy/migration code量、protocol branch数、format/semantic correction call数、semantic情報欠落で比較する。実Sol High本番A/Bは明示許可後だけ実行する
- この整理でMCP、daemon、Unix socket、persistent Claude process、長寿命stream-json sessionを導入せず、1 model call = 1 subprocessとsession IDによるconversation continuityを維持する

## multi-repository・stdin transport方針

- 異なるrepositoryの`glm-worker`は通常利用として同時実行可能にし、canonical repo root hash単位のstate/lock/session/cache分離とsubprocess cwd分離を維持する。global glm-worker lock、daemon、socket、repository横断queue/coordinatorを追加しない
- `--fix-stdin`・`--decision-stdin`はcaller側`stty raw -echo`を不要にする。pipe/fileはexact byte readを維持し、TTY/PTYだけglm-worker自身が当該invocationのterminal modeを変更する。外部`stty`呼出へ置換せず、変更前termios stateを正常受信・short read・hash mismatch・validation/command errorで復元する
- payload送信はprocess起動→TTY raw/noecho化→明示feedの順を一次証拠で確認し、Codex PTY APIがfeed前送信しないならREADY handshakeを追加しない。同じTTY deviceを意図的に共有する特殊caseのためのglobal coordinationは行わない
- 2つの一時Git repositoryを使うprocess-level integration testで、state/lock/task/worker/reviewer/checkpoint/telemetry/event/cache/PTy payload/reset/resume/statusの非混入、別repo同時実行、同一repo 2本目だけのlock拒否、PTY mode相互非影響を追加AI callなしで固定する
- `GLM_WORKER_HOME`、prompt directory、Claude config/settings、Codex automation TOML/SQLite、provider quota、temp directory、installed binaryをread-only共有・namespace分離・upstream管理・実競合候補へ分類し、具体的collision evidenceなしにglobal serializationを追加しない。provider quota共有はrepository state isolationと分離する
- stdin payloadをrepo lock前に読む現順序は、同一repo 2本目のpayload受信後lock拒否が実害小なら維持し、変更時もpayload validation前のstate/model call禁止を壊さない。実装後はmanaged caller instructionから`stty raw -echo` recipeを削除し、command・byte count・任意SHA-256・stdin feedだけをcaller contractにする

## 現在作業中

- Task: tracked canonical planのstale-by-one再発修正
- 前段完了: structured outputのstatus別`TARGETS`意味契約とproducer/consumer未知field受理集合を修正し、必要test・race・lint・vet・build・独立reviewer・HIGH変更のSol品質gate・指摘後再review・個別commitを完了
- 現在境界: final HEAD上のplanが完了済みcommitを「amend直前」「install前」と記述しないことを機械postconditionで保証し、運用instructionだけの対策に戻さない
- 進行状態: 前taskの完了証跡と次taskへのplan/history同期を完了し、新規GLM task開始前
- 次の操作: 親Codex orchestration失敗の一次証拠を固定し、final HEAD postconditionの実装・testを新規GLM taskへ委譲する

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

- [ ] tracked canonical planのstale-by-one再発を修正する。final HEAD上のplanが完了済みcommitを「amend直前」「install前」と記述していないことを機械postconditionで保証し、運用instructionだけで解消扱いしない
- [ ] `--fix-stdin`・`--decision-stdin`のcaller-side `stty raw -echo`依存を除去し、invocation-localなTTY/PTY mode設定と元state復元をglm-worker内部へ閉じる。同時に2 repositoryのprocess-level並列integrationでstate/lock/session/checkpoint/telemetry/event/cache/stdin payload/reset/resume/status分離を固定し、shared resourceを棚卸しする
- [ ] machine-oriented Codex出力とmachine-only schema単純化を完了する
  - [ ] current result fieldをfree text維持・typed化・削除・artifact/reference分離へ棚卸しし、status別semantic contractとproducer/consumer acceptance集合を一覧化する
  - [ ] 旧PACKET風textを正としないcompact structured JSON出力を実装し、人間向け表示を必要なら分離する
  - [ ] legacy/backward compatibility codeを棚卸しし、不要な旧parser/migration/fallback/推定を削除してcheckpoint/log/cacheのversion mismatchをskip/reset/rebuild/delete/resume不能へ単純化する
  - [ ] output/token proxy・free text/structured比率・legacy code・protocol branch・correction call・semantic情報欠落を変更前後比較し、独立reviewerと必要なSol品質gateを通す
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
