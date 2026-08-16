# 確認項目

- `./install.sh`を2回実行して2回目も成功する。
- `~/.codex/rules/default.rules`が残る。
- `~/.codex/config.toml`のPC固有設定が残る。
- `~/.claude/settings.json`の既存設定が残る。
- `glm-worker`がbuildされる。
- `glm-worker`のentrypointが`cmd/glm-worker`にあり、実装が`internal`の責務別packageに分離されている。
- cloneしたリポジトリでは`git pull`後に`install.sh`が動く。
- リポジトリ側で削除・改名された管理ファイルは次回install時に配置先から消える。
- sourceのtest/buildが失敗した場合、管理ファイルの配置を開始しない。
- install完了時に、新しいCodexタスクでのルール再読込が必要な旨を表示する。
- `GLM_WORKER_HOME`を指定した場合、その配下へ`sessions`を作成し、`~/.glm-worker`へ作成しない。
- `./tests/install_smoke.sh`で2回実行、隔離先への配置、preflight失敗時の無変更を確認する。



## Z.ai 5h limit復帰

- `429 + [1308] + Usage limit reached for 5 hour.`だけを5h limitとして識別する。
- generic 429は5h limit扱いしない。
- reset時刻を中国標準時（CST、UTC+8）として保存する。
- worker途中、reviewer途中、auto-fix途中のどこで止まってもresume stateが残る。
- `glm-worker --resume`で同じsession/phaseから継続する。
- rate limit中にsession ID、working tree、baselineをresetしない。
- rate limit出力にtask ID、repo root、2分の猶予を加えた自動再開時刻、重複防止keyを含める。
- 自動再開automationは同じローカルCodexタスクへ紐づき、別worktreeを使わない。
- wake時にtask IDと`rate-limited`状態を照合し、古い予約から`--resume`しない。
- 既存automationはRFC3339時刻をUTCへ変換し、`TZID`なしの1回限りの`DTSTART`を同一automation IDへ直接updateする。
- 新規作成は二段階とする。DTSTART付き即時createは`Immediate automation creates cannot include DTSTART`で拒否されるため、第一段階でDTSTARTなし・PAUSED・常にfuture occurrenceを持つ`RRULE:FREQ=HOURLY`のplaceholderを作成して成功応答から正確なIDを得て、第二段階で同一IDを目的のUTC絶対時刻DTSTARTと`COUNT=1`へupdateしてACTIVE化する。
- 二段階作成では、create失敗を成功扱いしない、成功前にIDを推測しない、create成功後のupdate失敗でplaceholderをbest-effort削除して半端な予約を残さない、最終verify失敗もfail closedとする。placeholderは特定の壁時計時刻に依存しない。
- automation toolの成功応答だけで完了扱いせず、SQLiteの`automations.next_run_at`またはCodex app上の次回実行が意図したJST時刻と一致することを確認する。
- `automation_update`の返り値全体を検査し、invalid/error/空/候補カード表示を作成失敗として扱う。過去にinvalid arguments文字列をcontentだけ読んで空出力と誤認した実障害がある。tool応答の行動検証はCodex tool境界の問題であり後続固定Evalの対象。
- 明示的な作成・更新成功とautomation IDだけを候補成功とし、`suggested_create`を使わない。
- 候補成功後`glm-worker --verify-auto-resume`で保存済みTOML実体とSQLite rowのid・status ACTIVE・target thread・rrule完全契約(DTSTART+RRULE:FREQ=DAILY;COUNT=1)・`next_run_at`絶対時刻を照合する。未作成・row欠損・ID/status/thread/time/rrule不一致を決定論検出し、返り値検査の見落としを問わずpostconditionがFAILとなる二段防御とする。
- `VERIFICATION: PASS`だけが予約成功の根拠となる。`VERIFICATION: FAIL`時はschema引数を修正してupdate(二段階作成の場合は第二段階)を最大1回再試行し、その後新規作成分のautomationを削除または停止して手動`glm-worker --resume`fallbackを明示する。
- `VERIFICATION: UNAVAILABLE`時はCodex app表示で同じID・対象task・時刻を確認できた場合だけ予約成功とし、確認不能なら作成失敗とする。
- 自動再開予約契約は`glm-worker/scenarios/autoresume.json`のescaped bug corpusと`internal/autoresume` test内の契約実装で固定Evalする。応答fixtureと検証結果に対する期待行動列(create/update/delete/verify/停止・成功報告可否)を決定論検証し、実障害5種を必須scenarioとして欠落時はtestが失敗する。成功応答は明示成功marker(Created/Updated automation in the app・success語幹)とautomation IDの両方を要求し、invalid/error/failed/候補カード/空・IDのみを失敗とする。
- `autoresume-manifest.json`は`glm-auto-resume.md`のSHA-256を固定し、内容変更でhash mismatchにより期待行動の再照合を強制する。


## GLM軽量化

- 新規タスク開始でworker/reviewer session IDが更新される。
- 同一タスク内のdecision/fix/resumeではsession IDが維持される。
- `--fix`は`NEEDS_SOL_REVIEW`状態だけで許可され、`PASS`後は拒否される。
- workerはopus alias、通常reviewerはhaiku alias、高リスク・Sol判断後・自動修正後・明示fix後reviewerはsonnet aliasを利用する。
- 通常effortはhigh、Sol判断後/明示fixはmax。
- auto-compact windowは500K。
- managed model mappingはopus=glm-5.3、haiku=glm-4.7、sonnet=glm-5.3。
- `RISK: LOW`の初回reviewは4.7、高リスク・Sol判断後・自動修正後・明示fix後のreviewは5.3を1回だけ選ぶ。
- reviewerはAgent/subagentを利用せず、reviewerモデルから上位モデルへの暗黙な再委譲を行わない。
- rate limit後のresumeでもcheckpointへ保存したreviewer modelを維持する。
- version 1のresume checkpointを受理せず、model欠落時にroleから補完しない。
- packetは15行・6 KiB・1行1536 bytes以内で、各fieldを1物理行に限定し、STATUS別必須field、RISK整合性、field重複を検証する。完全なPACKET_BEGIN/PACKET_ENDの組は1回の応答にちょうど1組だけとし、複数完成packet・marker前後の非空本文・入れ子や対応しないmarkerを拒否する。
- packet契約違反時は同一sessionへ再圧縮を1回だけ依頼し、作業を再実行しない。
- packet構造の1組制限は`packet.Parse`の単一受理入口で全roleへ機械強制する。LLM instructionだけの保証を機械contract扱いしていた点がescaped reviewの原因のため、wrapper強制scenario(corpus `packet-*`)を欠落させない。
- wrapperの最終stdoutは受理したpacketだけを1回出力する。再圧縮・risk floor再出力・resume前の旧応答を採用結果へ連結・再解析せず、model応答の受理は毎回新規の出力file経由でのみ行う。caller側echoの二重表示はrepo外のため検証対象外。
- packetへ収まらない正確な成果物はtask別artifactへ保存し、packetではtask専用dir配下に実在する通常ファイルの絶対パスだけを返す。artifact dir外・欠落・directory・symlinkを拒否し、所有者限定権限を検証する。
- worker errorの診断tailは6 KiBを超えない。


## provider障害回復・probe gate

- probe成功はJSON正常・type=result・is_error=false・応答trim後sentinel `GLM_WORKER_PROBE_OK`完全一致・usage出力tokenありの全成立だけとする。probe promptはreasoning不要のsentinel返却だけを要求する固定文にする。
- process exit 0でもis_error=trueやsentinel不一致を成功扱いせずsaved taskのresumeへ進めない。semantic invalidだけで即fatalにせず通常のtransient probe失敗と同じ既存backoff/retryを継続し、probe上限・hard deadlineの先に到達した側でprobe-contract分類のprovider-unavailable停止へ移行して元task/session/checkpointを保持する。
- probe応答の分類優先度はtask呼出と共通に5h→transient→明示fatalの順とし、5h上限signatureはrate-limited経路へ、503等のtransient信号とauth語の混在応答はtransientとしてretryする(corpus `provider-transient-probe-mixed-transient-priority-resumes-task`)。明示fatalは裸の数字・一般語を除く限定信号(401 Unauthorized/403 Forbidden/400 Bad Requestの組合せ、HTTP/status/API error文脈付きの同status、authentication failed/required・invalid api key・invalid model等の明示表現)だけとし、検出時のみ既存fatal classificationでfail closedする。通常文中の裸400を含むsentinel mismatchはprobe-contract retryのままで、不可逆なcheckpoint/session破棄の偽陽性を防ぐ。
- 偽陽性がreviewを通過した原因はexit codeと非空responseのpositive testへの偏り、成功後resume境界のnegative caseとsentinel契約のscenario欠落であるため、gate変更ではfalse-positive caseを独立testとscenario(corpus `provider-resume-probe-*`)へ要求する。追加AI callやstatus page依存でprobeを補強しない。
- Task Work Call(worker/reviewerの本task呼出)とProvider Probe Callを明確に分離する。worker/reviewerのtask call数・実行時間・token集計へprobeを混ぜず、probe成功後の本task再開実行をrole別task callとして毎回数える。probe呼出数・transient retry数・resume回数・total AI call数(task+probe)が重複・欠落なく導出できる。
- probeはClaude CLIが既に返すinput/output/cache token・cost・resolved model・API/wall durationを追加AI callなしで既存telemetry(JSONL)へ記録する。取得不能値は未観測(零値)のまま推測しない。
- transient→probe失敗→backoff→probe成功→saved task resume→success、probe成功→resume→5h limit、transient→probe semantic invalid→backoff→再試行成功→saved task resume、transient→semantic invalid継続→hard deadline→resumable provider-unavailable、transient→probe応答5h signature→rate-limited優先の5経路を、checkpoint(停止分類込)・task status・task・probe呼出数・token/cost・final status込みでscenario corpus(`provider-transient-probe-fail-then-success-resumes-task`・`provider-resume-probe-success-then-five-hour-limit`・`provider-transient-probe-invalid-then-success-resumes-task`・`provider-transient-probe-invalid-until-deadline-unavailable`・`provider-transient-probe-five-hour-signature-routes-rate-limited`)へ固定する。明示的auth/config信号による即時fail closedはnew_task・resume両経路を`provider-transient-probe-auth-error-fails-closed`・`provider-resume-probe-auth-error-fails-closed`で固定する。


## 統計

- 新規タスク開始時とreset時に前タスク統計をarchiveする。
- worker/reviewerとmodel alias別の呼び出し回数・実行時間、Sol判断、明示fix、resume、自動fix、Sol向けpacket、model alias別rate limit、packet再圧縮を記録する。
- Claude JSON出力のtop-level usageと、subagentを含む`modelUsage`由来のtree usage・実モデル名を呼出単位で区別して記録する。
- `--stats`のalias別・実モデル別tokenはtree usageから集計し、top-level turn数は明示的に区別する。
- タスク別JSONLへphase、role、effort、session、system/dynamic prompt、最終response、両usage、結果を`0600`で保存する。本文保存は環境変数で無効化できる。
- statsとtelemetryはversion 3だけを集計・読込対象とし、意味の異なる旧version(model_callsへprobeを混ぜたv2 stats、call_typeを持たないv2 telemetry)を混在させない。過去telemetryを書き換えない。
- telemetry/stats変更では数値が書かれるだけでなく、各metricが何を1呼出として数えるかの意味と加法整合性(total AI calls = task calls + probe calls、worker+reviewer = task calls)をreviewする。escaped reviewの原因はprobeをtask call metricへ混ぜた既存metricの意味確認不足として固定する。
- `glm-worker --stats`だけが統計を表示し、通常packet出力へ統計を混在させない。
- stats mirrorまたはtelemetryが破損・書き込み不能でも通常workflowとresetを継続し、warningを出す。


## 計画file bootstrap

- repository rootの`AGENTS.md`は、`IMPLEMENTATION_PLAN.local.md`が存在する場合だけ作業開始・再開前に必ず読み、未完了作業と進行状態の唯一の正として扱い、存在しない場合は推測・復元・自動生成せず通常のrepository状態から作業する規則を持つ。
- `IMPLEMENTATION_PLAN.local.md`はGit管理外(repository-local exclude)で運用し、公開`.gitignore`へ追加しない。global配布用`codex/AGENTS.md`へはこのrepository固有規則を入れない。
- root `AGENTS.md`変更はself-protectionのHIGH対象(`repo-agents`)とし、file有無両経路とHIGH分類をscenario corpus(`implementation-plan-*`・`repo-agents-root-change-escalates-self-protection-high`)と`internal/workflow` testで固定する。manifest hash pin変更時は該当scenarioの期待結果を現物へ再照合する。


## 自己保護critical surface

- orchestrator自己変更のHIGH判定は`internal/workflow/selfprotection.go`を単一契約とし、拡張子や「永続file・scriptであること」ではなく意味で分類する。対象はCodex/GLMの委譲・model routing・prompt/instruction・PACKET・session/resume・provider recovery/autoresume・権限/隔離・managed settings/installer適用意味を変更できるproduction surface。
- `glm-worker/internal/`配下のproduction `.go`はpackage既知・未知を問わず既定HIGHとし、未知packageは`internal-production`へ分類する。将来のinternal package追加がfail-openにならない。観測専用の`state/stats.go`・`state/telemetry.go`だけ非対象(`observation`)。
- `glm-worker/cmd/`配下のproduction `.go`(CLI entrypoint)もHIGH(`worker-entrypoint`)。現状薄くてもCLI routing・flag処理・app/workflow gate呼出を直接変更でき、provider/session/resume/packet gateの迂回・意味変更の入り得る境界であるため。
- installer適用経路(`install.sh`・`.githooks/post-merge`・`tools/merge-json/`のmerge engine)・管理settings内容(`claude/settings-managed.json`・`codex/config-managed.toml`)・依存manifest(`glm-worker/go.mod`・`tools/merge-json/go.mod`)はHIGH。installer・merge engineは全管理surfaceの適用意味を、管理settings内容はmodel routing・provider接続・実行envelopeを直接変更する。
- scenario corpus(`glm-worker/scenarios/`)・`codex/instructions/`・`codex/rules/`・`codex/glm-worker/prompts/`・`codex/AGENTS.md`・root `AGENTS.md`は従来どおりHIGH。
- 非対象はtest file(`*_test.go`)・検証harness(`tests/`・`glm-worker/scripts/`)・docs(`README.md`・`EVAL.md`・`LICENSE`)・repo metadata(`.gitignore`)に限定し、docs/testだけ・観測値だけの変更をHIGHにしない。
- repoの全tracked fileがcritical・非対象いずれかの分類を持つことをunit test(`TestSelfProtectionClassifiesEveryTrackedFile`)が強制する。未分類fileはtestを失敗させ、追加時の意味判断(どちらかへ分類)を強制する。分類の変更自体はpolicy fileがworkflow packageに含まれるため自動HIGHになる。
- 漏れ側の行動固定はscenario corpus(`orchestrator-critical-low-self-declare`・`repo-agents-root-change-escalates-self-protection-high`・`install-merge-path-escalates-self-protection-high`・`managed-settings-content-escalates-self-protection-high`・`autoresume-verifier-escalates-self-protection-high`・`future-internal-package-escalates-self-protection-high`・`cmd-entrypoint-escalates-self-protection-high`)、非対象側は`low-risk-non-critical-pass`・`test-and-docs-only-stay-low-risk`で固定する。manifest hash pin変更時は該当scenarioの期待結果を現物へ再照合する。


## 外部成立性feasibility gate

- `codex/instructions/feasibility-gate.md`は、外部service・取得方式・実行環境等の未検証critical assumptionがproduction code・IaC・運用展開の設計前提になり後続コストが大きい場合だけ適用する親Codex orchestration contractである。worker/reviewerへの個別checklist追加で解決しない。
- gateはcritical assumption列挙・最小PoC・代表case・transport成功を含まない意味的成功条件・対象固有の必要試行回数/観測期間・Go/No-Go・撤退条件を、対象の不確実性・変動性・継続成立性の重要度に応じて明示する。
- HTTP 200・process exit 0・単発取得等のtransport成功だけを成立性の証明にしない。Amazon取得PoCの48〜72時間は対象固有の観測条件であり一般contractへ固定しない。外部API schema確認・実行環境からの到達確認・認証方式の成立確認など短時間の意味的検証で足りる対象へ長時間試験を要求せず、通常の局所変更・確立済み前提へ形式的PoCを要求しない。
- 観測中に前提が崩れた場合はworkaroundの追加実装を止め、観測事実をSol/ユーザー判断へ戻す。PoC・観測taskとproduction実装taskを分離し、Go/No-GoをGLMだけで確定させない。
- 親側production routingの決定論検証は`internal/workflow`の`TestFeasibilityGateContractWiring`が担う。`codex/AGENTS.md`の条件付きrouting・品質gate項目、`codex/instructions/glm-execution.md`の委譲前読込指示、`feasibility-gate.md`本文の必須契約文のいずれかが欠けるとtestが失敗する。install後の3 file配置・相互参照は`tests/install_smoke.sh`の配置grepが検証する。
- scenario corpus(`feasibility-gate-*`)はwrapperのSTATUS/risk終端検証例に限定する。未検証外部成立性を越えたPoCからproduction/IaCへの進行に対する`NEEDS_SOL_REVIEW`終端とPASS完結拒否(`feasibility-gate-production-beyond-unverified-viability-returns-to-sol`)、前提崩壊時の`NEEDS_SOL_DECISION`終端(`feasibility-gate-premise-collapse-stops-further-implementation`)、短時間の意味的検証と確立済み前提変更の通常完遂(`feasibility-gate-short-semantic-verification-completes`・`feasibility-gate-established-premise-change-completes`)。scripted packet列に対するwrapper終端検証であり、親Codexがgateを読み委譲・受領を制御する行動の証明ではない。根拠instructionとして`codex/AGENTS.md`・`codex/instructions/glm-execution.md`・`codex/instructions/feasibility-gate.md`の3 fileをmanifest hash pinし、変更時は該当scenarioの期待結果を現物へ再照合する。
- 親Codex behavioral Evalは未実行の固定Eval caseとする。positive case: 未検証外部成立性のproduction/IaC委譲をfeasibility gate根拠で止めPoC・観測taskへ分割する。negative case: 短時間の意味的検証で足りる対象へ固定観測期間を要求しない。完了条件: 親Codexのrouting判断・委譲内容・gate読込をraw telemetry・task log等の一次証拠で照合できる検証形態が整備されること。live model呼出しを要するためユーザーの明示指示後だけ実行し、完了条件を満たすまでは本項を完了扱いにしない。新規巨大harness・無意味なlive callは作らない。


## 親USER_REQUEST lifecycle contract

- `codex/instructions/task-lifecycle.md`は、monitor/automationの安全停止・GLM child task終端・個別commit/installを親USER_REQUEST全体の完了と同一視しない親Codex orchestration contractである。Kindle escaped caseと停止ミスの原因をworker/reviewer pipelineの個別checklist不足ではなく親lifecycle不足と分類しており、worker/reviewer promptへの個別checklist追加で解決しない。
- 終端は3分類する。monitorのscheduler停止・queue/checkpoint保全・alarm報告の完了、GLM child taskのtask・review・commit・install個別完了は局所終端であり、親依頼本体とユーザー・automationが明示継続対象とした実装計画範囲の未解決作業解消だけが親USER_REQUEST完了。
- 局所終端の直後に親依頼・計画の未解決作業と次の安全なin-scope操作を再評価し、原因修正・再開確認・後続改善等が残るなら同一Codex taskで継続する。monitorの安全停止完了後も元依頼に診断・修正・再開確認が残るcase、個別commit/install完了後も明示継続planが残るcaseを完了扱いしない。停止は新しい権限・Codex外の外部状態変化・意味あるユーザー判断が本当に必要な場合だけとし、checkpoint・session・working treeを保持して残作業とblockerを報告する。
- 実装計画に長期roadmapが存在するだけで現在の親依頼範囲へ作業を勝手に拡張せず、ユーザー・automationが「後続へ継続」「停止しない」と明示した範囲を直近subtaskの局所終端で打ち切らない。
- 親側production routingの決定論検証は`internal/workflow`の`TestTaskLifecycleContractWiring`が担う。`codex/AGENTS.md`のrouting、`codex/instructions/glm-execution.md`のpacket受理後読込指示、`task-lifecycle.md`本文の必須契約文のいずれかが欠けるとtestが失敗する。install後の3 file配置・相互参照は`tests/install_smoke.sh`の配置grepが検証する。
- scenario corpus(`task-lifecycle-*`)はwrapperの局所終端例へのSTATUS/risk終端検証に限定する。monitor安全停止sub-deliverableの`NEEDS_SOL_REVIEW`終端とPASS完結拒否(`task-lifecycle-monitor-safe-stop-local-terminal-returns-to-sol`)、外部判断blockerの`NEEDS_SOL_DECISION`終端(`task-lifecycle-external-judgment-blocker-stops-with-state`)、依頼明示限定の局所成果物の通常完遂(`task-lifecycle-explicitly-limited-deliverable-completes`)。scripted packet列に対するwrapper終端検証であり、親Codexが局所終端後に未解決作業を再評価し同一taskで継続する行動の証明ではない。根拠instructionとして`codex/AGENTS.md`・`codex/instructions/glm-execution.md`・`codex/instructions/task-lifecycle.md`の3 fileをmanifest hash pinし、変更時は該当scenarioの期待結果を現物へ再照合する。
- 親Codex behavioral Evalは未実行の固定Eval caseとする。positive case: monitorのscheduler停止・queue保全・alarm報告完了後も元依頼の診断・修正・再開確認へ同一Codex taskで継続する。個別commit/install完了後も明示継続planの次項へ継続する。negative case: 依頼が単一局所成果物へ明示限定される場合へ長期roadmapや依頼外診断を拡張しない。完了条件: 親Codexの継続・停止判断をraw telemetry・task log等の一次証拠で照合できる検証形態が整備されること。live model呼出しを要するためユーザーの明示指示後だけ実行し、完了条件を満たすまでは本項を完了扱いにしない。安全停止・状態保全だけで未解決の親USER_REQUESTを完了扱いする拒否caseは固定Eval残項目として本corpus外で管理する。


## 原因不明runtime failureの最小evidence管理

- `codex/instructions/failure-evidence.md`は、外部取得・parser・integration failureでstatus/size/error分類だけでは根本原因や再現条件を判定できない場合だけ、response本文・header・payload断片・parser入力等から再現に必要な最小evidenceをtask artifactへ保存させる親Codex orchestration contractである。Kindle escaped caseの原因をworker/reviewer pipelineの個別checklist不足ではなく親Codexのruntime evidence管理不足と分類しており、worker/reviewer promptへの一般checklist追加で解決しない。
- 保存前のcredential・token・cookie・session ID・個人情報等の除去・置換、再現に必要な最小範囲への切り出し、容量上限・retention/削除時期・access範囲の対象リスク応じた明示を委譲時に構成する。全response・全成功応答の無条件保存、巨大payload、秘密情報の生保存、telemetryへの本文混入、診断に不要な長期保存は禁止する。保存先は既存`REPORT_ARTIFACT_DIR`/`ARTIFACTS`だけとし、新しいstorage・telemetry schemaを作らない。
- artifact保存失敗はbest-effort warningとして残し、それだけでは本taskを失敗させない。原因判定に証拠が必須なのに取得不能なら「判定不能」としてSol/ユーザーへ戻し、推測修正を続けない。通常の十分診断可能なerror、成功応答、局所bugへ形式的なartifact保存を要求しない。
- 親側production routingの決定論検証は`internal/workflow`の`TestFailureEvidenceContractWiring`が担う。`codex/AGENTS.md`のrouting、`codex/instructions/glm-execution.md`の委譲前読込指示、`codex/instructions/glm-packets.md`の受理時指示、`failure-evidence.md`本文の必須契約文のいずれかが欠けるとtestが失敗する。worker/reviewer promptへ一般checklistを追加しない方針も本testが固定する。install後の4 file配置・相互参照は`tests/install_smoke.sh`の配置grepが検証する。
- scenario corpus(`failure-evidence-*`)はwrapperのartifact packet/終端例への検証に限定する。sanitize済み最小evidenceの`ARTIFACTS`参照packetがartifact dir配下実在file検証を通じ`NEEDS_SOL_REVIEW`終端へ至る例(`failure-evidence-minimal-sanitized-evidence-packet-returns-to-sol`)、証拠取得不能の「判定不能」`NEEDS_SOL_DECISION`終端(`failure-evidence-unobtainable-evidence-returns-undecidable-to-sol`)、十分診断可能な分類だけの通常完遂(`failure-evidence-sufficient-classification-completes-without-artifact`)。scripted packet列に対するwrapper終端検証であり、親Codexが委譲前にevidence条件を構成し受理時に必要範囲だけ確認する行動の証明ではない。根拠instructionとして`codex/AGENTS.md`・`codex/instructions/glm-execution.md`・`codex/instructions/glm-packets.md`・`codex/instructions/failure-evidence.md`の4 fileをmanifest hash pinし、変更時は該当scenarioの期待結果を現物へ再照合する。scenario harnessの`artifact_files`・`{{ARTIFACT_DIR}}`予約tokenはtask artifact dir配下の実在file検証を通るpacket例の作成だけへ使う。
- 親Codex behavioral Evalは未実行の固定Eval caseとする。positive case: status/size/error分類だけでは原因判定不能な外部取得・parser・integration failure依頼へ委譲前に必要証拠・sanitization・保存先・retentionを構成する。受理時に`ARTIFACTS`参照先を必要範囲だけ確認する。negative case: 十分診断可能なerror・成功応答・局所bugへ形式的artifact保存を要求しない。完了条件: 親Codexの委譲内容・受理確認をraw telemetry・task log・artifact実体等の一次証拠で照合できる検証形態が整備されること。live model呼出しを要するためユーザーの明示指示後だけ実行し、完了条件を満たすまでは本項を完了扱いにしない。原因判定に本文等が必要なのにstatus/sizeだけを残して修正を重ねる拒否caseは固定Eval残項目として本corpus外で管理する。


## Go品質ゲート

- `go test ./...`が成功する。
- `go test -race ./...`が成功する。
- `go vet ./...`が成功する。
- `go build -o /dev/null ./cmd/glm-worker`が成功する。
- package別coverageでCLI、config、runner、state遷移、workflowの主要分岐に未検証箇所がないか確認する。
