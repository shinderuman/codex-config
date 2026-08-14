# GLM実行と待機

`glm-worker`を実行または待機する場合だけ適用する。目的は無意味なSol High再起動とpollingによるトークン消費を防ぐこと。

## 実行

- 外部GLM通信とClaude Codeユーザー設定アクセスが必要なため、最初からsandbox外で実行し、sandbox内へfallbackしない。
- `~/.codex/config.toml`の`background_terminal_max_timeout`は`21600000`ms（6時間）を前提とする。
- 同じ依頼を重複起動せず、GLM処理中にCodex自身が同じ調査・実装を代行しない。release・deploy等の直接許可が既にある場合でも、その途中で新たに必要になった開発変更は`~/.codex/instructions/direct-edit.md`の境界に従い新規taskへ切り出す。
- 1回の新規taskには、同じ責務・変更理由・検証単位に属する要求だけを渡す。相互に独立したsubsystem・workstream・不具合群は別taskへ分けるが、同時変更しないと整合しない要求は分断しない。
- worker依頼には調査・実装・必要テスト・lint/build・自己レビューまでを含め、独立reviewerの起動や「独立reviewまで」は要求しない。wrapperがworker完了後に別sessionのreviewerを自動実行する。
- `AGENTS.md`や既存規約にある一般品質ゲートを依頼文へ列挙し直さず、タスク固有の完了条件・対象・除外事項・必要テストだけを明記する。
- 正確な長い一覧や監査報告がpacket上限へ収まらない場合は、実行時に渡される`REPORT_ARTIFACT_DIR`へ保存させ、packetでは`ARTIFACTS`の絶対パスだけを受け取る。
- 同一taskがSol判断待ち・review fix・rate limit中なら分割や新規起動へ切り替えず、保存済みtaskとsessionを継続する。
- モデル配分・token節約・品質バランスの調整を依頼された場合だけ`glm-worker --stats`を実行し、出力の`TELEMETRY_DIR`にあるタスク別JSONLを対象に、phase・role・effort・alias・実モデル・tree usage・top-level usage・prompt・response・結果を比較する。総消費量にはsubagentを含むtree usageを使う。通常作業では調整目的のためだけに詳細ログを読まない。

## 待機

- 最初の`functions.exec`等の呼び出しからbackground terminalで利用可能な最大待機時間を指定し、可能な限り同一tool orchestration内で完了までblocking waitする。
- tool内部上限でcell ID（session ID）が返る場合も、1回のwaitに最大待機時間を使い、短時間・固定間隔でwaitを掛け直さない。同じtool orchestration内で最大待機を再開し、Sol Highへ制御を戻して`write_stdin`等を呼ぶ方式へ変換しない。
- tool orchestrationやexec cellに対する短時間・固定間隔の反復wait、固定間隔の`write_stdin`、status・端末出力・生存確認を行わない。一定時間無出力であることだけを理由に失敗・再実行しない。
- 無出力を理由にした定期進捗発言、進捗報告目的のwake・待機短縮・中断・GLMへの問い合わせをしない。必要な報告は最後に確認済みの状態だけで行う。
- ユーザーが状態確認を明示した場合だけ中間状態を確認してよい。
- 最大待機時間後も生存していれば、再調査・代替作業・重複起動をせず再び最大時間で待つ。完了や`RATE_LIMITED`を見逃さない現行動作を維持する。
- 完了時はユーザーの追加入力を待たず、packet処理と可能な次工程を進める。ユーザーの判断・追加情報・許可が本当に必要な場合だけ停止する。

## `STATUS: RATE_LIMITED`

- `LIMIT: ZAI_GLM_CODING_PLAN_5H`は正常な一時停止であり、`WORKER_ERROR`にしない。`RESET_AT_CST`は中国標準時（UTC+8）。
- working tree、task state、session、resume checkpointを破棄・resetせず、新規taskとしてやり直さない。
- `AUTO_RESUME_AVAILABLE: true`なら`~/.codex/instructions/glm-auto-resume.md`を読み、現在のCodexタスクへ自動再開automationを作成または更新する。作成不能な場合だけ手動再開を案内する。
- 手動再開指示では`glm-worker --resume`を使い、保存済みの同一task・phase・sessionから継続する。元依頼を再構成しない。
- 枠が未回復なら再び`RATE_LIMITED`として状態を保持する。
