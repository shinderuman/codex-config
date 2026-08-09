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


## GLM軽量化

- 新規タスク開始でworker/reviewer session IDが更新される。
- 同一タスク内のdecision/fix/resumeではsession IDが維持される。
- `--fix`は`NEEDS_SOL_REVIEW`状態だけで許可され、`PASS`後は拒否される。
- workerはopus alias、通常reviewerはhaiku alias、高リスク・自動修正後reviewerはsonnet aliasを利用する。
- 通常effortはhigh、Sol判断後/明示fixはmax。
- auto-compact windowは500K。
- managed model mappingはopus=glm-5.2、haiku=glm-4.7、sonnet=glm-5.1。
- `RISK: LOW`の初回reviewは4.7、`RISK: HIGH`または自動修正後のreviewは5.1を1回だけ選ぶ。
- rate limit後のresumeでもcheckpointへ保存したreviewer modelを維持する。
- packetは15行・6 KiB・1行1536 bytes以内で、STATUS別必須fieldを検証する。
- packet契約違反時は同一sessionへ再圧縮を1回だけ依頼し、作業を再実行しない。


## 統計

- 新規タスク開始時とreset時に前タスク統計をarchiveする。
- worker/reviewer呼び出し、Sol判断、明示fix、resume、自動fix、Sol向けpacket、rate limit、packet再圧縮を記録する。
- `glm-worker --stats`だけが統計を表示し、通常packet出力へ統計を混在させない。
- stats mirrorが破損・書き込み不能でも通常workflowとresetを継続し、warningを出す。


## Go品質ゲート

- `go test ./...`が成功する。
- `go test -race ./...`が成功する。
- `go vet ./...`が成功する。
- `go build -o /dev/null ./cmd/glm-worker`が成功する。
- package別coverageでCLI、config、runner、state遷移、workflowの主要分岐に未検証箇所がないか確認する。
