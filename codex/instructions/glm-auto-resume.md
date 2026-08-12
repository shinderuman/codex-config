# GLM rate limit自動再開

`glm-worker`が`STATUS: RATE_LIMITED`かつ`LIMIT: ZAI_GLM_CODING_PLAN_5H`を返した場合だけ適用する。

## 予約

- `AUTO_RESUME_AVAILABLE: true`、`AUTO_RESUME_AT_RFC3339`、`AUTO_RESUME_KEY`、`TASK_ID`、`REPO_ROOT`が揃っていることを確認する。
- Codex appの`automation_update`を使い、現在のローカルタスクへ紐づくheartbeat automationを作成または更新する。standalone taskやworktree automationは使わない。
- automation名は`AUTO_RESUME_KEY`を使い、同名があれば新規作成せず更新する。
- 実行時刻は`AUTO_RESUME_AT_RFC3339`が表す絶対時刻とする。offsetを捨てずUTCへ変換し、時刻前の固定間隔pollingは行わない。
- heartbeat schedulerは`DTSTART`の`TZID`を`next_run_at`計算へ反映せず、壁時計部分をUTCとして扱う。`DTSTART;TZID=Asia/Tokyo`は使わない。
- 新規作成と既存更新のどちらでも、`AUTO_RESUME_AT_RFC3339`をUTCへ変換し、UTCの年月日時分秒を`DTSTART:YYYYMMDDTHHMMSS`、繰り返しを`RRULE:FREQ=DAILY;COUNT=1`とする1回限りの予約を同じautomation IDへ設定する。
- 既存heartbeatの時刻更新でも、同じautomation IDへUTCへ変換した`DTSTART`を指定する。JSTやCSTの壁時計時刻をそのまま渡さない。
- automationの実行環境は`REPO_ROOT`と同じローカルcheckoutを選ぶ。別worktreeではrepo hashが変わりresume stateを参照できない。
- 生のautomation directiveやRRULEを本文へ出力せず、利用可能なtool schemaに従う。

### automation応答の検査

- `automation_update`の返り値全体を文字列として必ず検査する。content欄だけを読み、空や短い出力を成功扱いしない。過去にinvalid arguments文字列をcontentだけ読んで空出力と誤認し、作成失敗を見落とした実障害がある。
- 返り値に`invalid`、`error`、`failed`、空文字列、`Rendered suggestion`、候補カード表示のいずれかを含む場合は作成失敗とする。これらは予約成功ではない。
- 明示的な作成・更新成功応答とautomation IDを含む場合だけ候補成功とする。候補成功は予約済みではなく、次項の実体検証を通る必要がある。
- `suggested_create`は候補カードの表示のみでありautomation作成完了ではない。`suggested_create`を呼ばない、予約成功や作成成功の根拠にしない。
- 返り値検査で見落としても、次項の実体検証が未作成・row欠損・不一致をFAILとして検出する二段防御である。automation tool応答はCodexのtool境界にあり`glm-worker`へ直接渡らないため、postcondition検証が最終的なfail-closed手段となる。

### 候補成功後の実体検証

- 候補成功後、ただちに同じtool orchestration内で`REPO_ROOT`において`glm-worker --verify-auto-resume <AUTO_RESUME_KEY> <AUTO_RESUME_AT_RFC3339> <現在のCodex task thread ID>`を実行する。thread IDは現在のCodexタスクのIDを使う。
- この検証は保存済みautomation TOML実体(`~/.codex/automations/<key>/automation.toml`)のid・name・status ACTIVE・target_thread_id・rrule完全契約(UTC DTSTART + 改行 + `RRULE:FREQ=DAILY;COUNT=1`の正確な2行)と、`~/.codex/sqlite/codex-dev.db`の`automations.next_run_at`・id・status・rruleを期待ID・対象thread・status ACTIVE・絶対時刻で照合する。Codex appのSQLite automations表にthread_id列はなく、対象threadはTOMLのtarget_thread_idでのみ検証する。
- `VERIFICATION: PASS`だけが予約成功の根拠となる。この時点で初めてrate limit停止を報告してよい。
- `VERIFICATION: FAIL`の場合、予約済みと報告しない。検証理由からschema引数の誤りを特定し、引数を修正して`automation_update`のcreate/updateを再試行する。再試行は最大1回まで。再試行後もFAILの場合、作成不能として手動`glm-worker --resume`fallbackを明示する。
- `VERIFICATION: UNAVAILABLE`の場合(sqlite3不在・DB/schema読取不能)、Codex app上のautomation表示で同じautomation ID・対象task・次回実行時刻が意図したJST時刻と一致することを確認した場合だけ予約成功とする。確認不能な場合は作成失敗とし、手動`glm-worker --resume`fallbackを明示する。

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
