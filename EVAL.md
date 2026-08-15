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
- packetは15行・6 KiB・1行1536 bytes以内で、各fieldを1物理行に限定し、STATUS別必須field、RISK整合性、field重複を検証する。
- packet契約違反時は同一sessionへ再圧縮を1回だけ依頼し、作業を再実行しない。
- packetへ収まらない正確な成果物はtask別artifactへ保存し、packetではtask専用dir配下に実在する通常ファイルの絶対パスだけを返す。artifact dir外・欠落・directory・symlinkを拒否し、所有者限定権限を検証する。
- worker errorの診断tailは6 KiBを超えない。


## 統計

- 新規タスク開始時とreset時に前タスク統計をarchiveする。
- worker/reviewerとmodel alias別の呼び出し回数・実行時間、Sol判断、明示fix、resume、自動fix、Sol向けpacket、model alias別rate limit、packet再圧縮を記録する。
- Claude JSON出力のtop-level usageと、subagentを含む`modelUsage`由来のtree usage・実モデル名を呼出単位で区別して記録する。
- `--stats`のalias別・実モデル別tokenはtree usageから集計し、top-level turn数は明示的に区別する。
- タスク別JSONLへphase、role、effort、session、system/dynamic prompt、最終response、両usage、結果を`0600`で保存する。本文保存は環境変数で無効化できる。
- statsとtelemetryはversion 2だけを集計・読込対象とし、意味の異なるversion 1を混在させない。
- `glm-worker --stats`だけが統計を表示し、通常packet出力へ統計を混在させない。
- stats mirrorまたはtelemetryが破損・書き込み不能でも通常workflowとresetを継続し、warningを出す。


## Go品質ゲート

- `go test ./...`が成功する。
- `go test -race ./...`が成功する。
- `go vet ./...`が成功する。
- `go build -o /dev/null ./cmd/glm-worker`が成功する。
- package別coverageでCLI、config、runner、state遷移、workflowの主要分岐に未検証箇所がないか確認する。
