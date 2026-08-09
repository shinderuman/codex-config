# GLM rate limit自動再開

`glm-worker`が`STATUS: RATE_LIMITED`かつ`LIMIT: ZAI_GLM_CODING_PLAN_5H`を返した場合だけ適用する。

## 予約

- `AUTO_RESUME_AVAILABLE: true`、`AUTO_RESUME_AT_RFC3339`、`AUTO_RESUME_KEY`、`TASK_ID`、`REPO_ROOT`が揃っていることを確認する。
- Codex appの`automation_update`を使い、現在のローカルタスクへ紐づくheartbeat automationを作成または更新する。standalone taskやworktree automationは使わない。
- automation名は`AUTO_RESUME_KEY`を使い、同名があれば新規作成せず更新する。
- 実行時刻は`AUTO_RESUME_AT_RFC3339`が表す絶対時刻とする。offsetを捨てずUTCへ変換し、時刻前の固定間隔pollingは行わない。
- heartbeat schedulerは`DTSTART`の`TZID`を`next_run_at`計算へ反映せず、壁時計部分をUTCとして扱う。`DTSTART;TZID=Asia/Tokyo`は使わない。
- 新規作成と既存更新のどちらでも、`AUTO_RESUME_AT_RFC3339`をUTCへ変換し、UTCの年月日時分秒を`DTSTART:YYYYMMDDTHHMMSS`、繰り返しを`RRULE:FREQ=DAILY;COUNT=1`とする1回限りの予約を同じautomation IDへ設定する。
- `suggested_create`は作成提案の表示であり、automation作成完了として扱わない。`Created automation`とautomation IDを確認する。
- 既存heartbeatの時刻更新でも、同じautomation IDへUTCへ変換した`DTSTART`を指定する。JSTやCSTの壁時計時刻をそのまま渡さない。
- automationの実行環境は`REPO_ROOT`と同じローカルcheckoutを選ぶ。別worktreeではrepo hashが変わりresume stateを参照できない。
- 生のautomation directiveやRRULEを本文へ出力せず、利用可能なtool schemaに従う。
- automation toolの成功応答だけを予約成功の根拠にしない。`~/.codex/sqlite/codex-dev.db`の`automations.next_run_at`をJSTへ変換し、意図した絶対時刻と一致することを確認してからrate limit停止を報告する。DBを確認できない場合はCodex app上の次回実行時刻を確認する。作成不能または時刻不一致の場合だけ手動`glm-worker --resume`をfallbackとして案内する。

## wake時

1. `REPO_ROOT`で`glm-worker --status`を実行する。
2. 出力の`TASK_ID`が予約時の値と一致し、`TASK_STATUS: rate-limited`、`RESUME_AVAILABLE: yes`であることを確認する。
3. task ID不一致、reset済み、rate limit以外のstatusなら`glm-worker --resume`を実行せず、該当automationを削除または停止する。
4. 条件が一致した場合だけ、同じcheckoutで`glm-worker --resume`をsandbox外実行する。
5. 再び`STATUS: RATE_LIMITED`になった場合は、新しい`AUTO_RESUME_AT_RFC3339`で同じ`AUTO_RESUME_KEY`のautomationを更新する。
6. `PASS`、`NEEDS_SOL_DECISION`、`NEEDS_SOL_REVIEW`、`WORKER_ERROR`のいずれかへ進んだらautomationを削除または停止し、通常のGLM packet処理を同じCodexタスクで継続する。

## 不変条件

- 新しい`glm-worker "<元依頼>"`を起動しない。
- working tree、task state、worker/reviewer session、resume checkpointを破棄・resetしない。
- 元依頼やSol判断を再構成せず、保存済みcheckpointからだけ再開する。
- ユーザーが自動再開前に明示的に`--reset`した場合、古いautomationから再開しない。
