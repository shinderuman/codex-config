# GLM実行と待機

`glm-worker`を実行または待機する場合だけ適用する。目的は無意味なSol High再起動とpollingによるトークン消費を防ぐこと。

## 実行

- 外部GLM通信とClaude Codeユーザー設定アクセスが必要なため、最初からsandbox外で実行し、sandbox内へfallbackしない。
- `~/.codex/config.toml`の`background_terminal_max_timeout`は`21600000`ms（6時間）を前提とする。
- 同じ依頼を重複起動せず、GLM処理中にCodex自身が同じ調査・実装を代行しない。release・deploy等の直接許可が既にある場合でも、その途中で新たに必要になった開発変更は`~/.codex/instructions/direct-edit.md`の境界に従い新規taskへ切り出す。
- 1回の新規taskには、同じ責務・変更理由・検証単位に属する要求だけを渡す。相互に独立したsubsystem・workstream・不具合群は別taskへ分けるが、同時変更しないと整合しない要求は分断しない。
- 外部service・取得方式・実行環境等の未検証成立性が本番設計の前提になる依頼は、`~/.codex/instructions/feasibility-gate.md`を読んでから委譲内容を構成する。
- 外部取得・parser・integration failureの原因診断にstatus・size・error分類だけでは足りない依頼は、`~/.codex/instructions/failure-evidence.md`を読んでから委譲内容を構成する。
- 外部review・実運用で見つかったescaped bug・escaped reviewの原因分析を委譲する場合は、`~/.codex/instructions/escaped-cause-layer.md`を読んでから委譲内容を構成する。
- worker依頼には調査・実装・必要テスト・lint/build・自己レビューまでを含め、独立reviewerの起動や「独立reviewまで」は要求しない。wrapperがworker完了後に別sessionのreviewerを自動実行する。
- `AGENTS.md`や既存規約にある一般品質ゲートを依頼文へ列挙し直さず、タスク固有の完了条件・対象・除外事項・必要テストだけを明記する。
- 正確な長い一覧や監査報告がpacket上限へ収まらない場合は、実行時に渡される`REPORT_ARTIFACT_DIR`へ保存させ、packetでは`ARTIFACTS`の絶対パスだけを受け取る。
- 同一taskがSol判断待ち・review fix・rate limit中なら分割や新規起動へ切り替えず、保存済みtaskとsessionを継続する。
- モデル配分・token節約・品質バランスの調整を依頼された場合だけ`glm-worker --stats`を実行し、出力の`TELEMETRY_DIR`にあるタスク別JSONLを対象に、phase・role・effort・alias・実モデル・tree usage・top-level usage・prompt・response・結果を比較する。総消費量にはsubagentを含むtree usageを使う。通常作業では調整目的のためだけに詳細ログを読まない。

## decision/fix本文の送信

- `--decision`・`--fix`へ渡す判断本文・修正指示本文を、`JSON.stringify`等でshell command文字列へ埋め込むことを禁止する。shellの二重引用符内ではbacktickと`$`がcommand substitution・展開され、本文の一部が失われたりcommand出力が本文へ混入した上で最初のNUL byteで切断されたりする。2026-08-20のBM25 taskではこの経路でdecision本文の過半が静かに喪失した。
- 本文はstdin modeで送る。exec_commandは`tty: true`で起動する。`tty: false`のprocessはstdinが即時EOFとなり本文送信前にbyte数不足でfail closedするため、`tty: false`へのfallbackを禁止する。
- 起動commandは本文を含まない固定形`stty raw -echo && glm-worker --decision-stdin <payload-bytes>`（fixは`--fix-stdin`）とし、shell interpolationを使わない固定文字列だけを構築する。`raw`でinput/output processing・canonical・signals・flow controlをまとめて無効化し、CR/NL置換（ICRNL/INLCR）・制御byteの信号化（ISIG）・flow control（IXON）・stdoutのPTY側加工（OPOST/ONLCR）を防ぐ。`-icanon -echo`だけの弱い設定ではbyte数が同じまま本文が1:1でなく置換され、sha256未指定時にsilent corruptionとなるため使わない。glm-worker起動前にこの`stty`を適用し、`stty`設定が失敗した場合はglm-workerを起動せずfail closedする。canonical modeが有効なままだと末尾改行のないchunkがline bufferに滞留し、byte数へ到達しない。command文字列へ入れてよい本文由来の情報はUTF-8 byte長と任意のSHA-256だけに限る。`<payload-bytes>`は本文のUTF-8 byte長で、tool orchestration内で`TextEncoder`相当から送信前に計算する。
- 呼び出しがsession化されてsession IDを返した場合は、末尾改行の有無に依存せず非emptyの`write_stdin`で本文全体を1回だけ送る。echo有効のままだと本文全文がtool outputへ複製される。echo有効・`raw`未適用のままの本文送信と、改行だけの追加writeを禁止する。byte数が不足する入力はglm-workerがstate変更・model呼出前にfail closedする。
- 送信前に本文のSHA-256を計算できる場合は`--sha256 <hex>`を併せ指定し、同じく送信前に照合させる。
- 本文中のbacktick、dollar、single quote、double quote、改行を無変換で保持する。shell向けのescape・encode・quoteをやり直さない。
- glm-workerがbyte数不足・sha256不一致で非zero終了した場合、本文の分割再送・短文化・`--decision`/`--fix`へのargv埋込みfallbackを行わない。byte長・hashと送信内容の整合だけを確認し、同じstdin modeで再送する。
- この固定wrapper command自体はCodex tool側でsandbox外実行する。glm-workerが既存task state/checkpoint/sessionを更新するためである。ただし毎回の再承認要求を本契約へ含めない。
- 本文送信後は短時間pollingを挟まず、最大待機時間のblocking waitで完了を待つ。

## 対象repoの生存判定

- glm-worker taskの生存判定は`glm-worker --status`の`REPOSITORY_LOCK`(held/free/unknown)と、`TASK_STATUS: active`時の`TASK_LIVENESS`(running/stale/unknown)だけを使う。global process一覧・`pgrep`・Claude Code processの存在を生存判定や起動可否の根拠にしない。lock file内のPIDは診断情報であり、stale PIDやPID reuseでrunning扱いしない。
- `REPOSITORY_LOCK`は対象repoのlockだけを意味する。別repositoryのglm-worker processやlockの解放を待たず、「GLM全体で同時実行不可」と推測して待機しない。global mutexは追加しない。
- 同じrepoの`REPOSITORY_LOCK: held`だけが重複起動の待避理由になる。
- `TASK_STATUS: active`・`REPOSITORY_LOCK: free`・`RESUME_AVAILABLE: no`を別repo終了待ちにせず、stale候補として扱う。対象repoのworking treeとstateを確認し、repo固有の復旧へ進む。checkpointがある場合は既存のresume手順に従う。
- status観測と次command実行の間のraceは、次command自身が同じrepoのlockを取り直すことで安全に収束する。lock取得失敗だけを重複起動の根拠にする。

## 待機

- 最初の`functions.exec`等の呼び出しからbackground terminalで利用可能な最大待機時間を指定し、可能な限り同一tool orchestration内で完了までblocking waitする。
- tool内部上限でcell ID（session ID）が返る場合も、1回のwaitに最大待機時間を使い、短時間・固定間隔でwaitを掛け直さない。同じtool orchestration内で最大待機を再開し、Sol Highへ制御を戻して`write_stdin`等を呼ぶ方式へ変換しない。
- tool orchestrationやexec cellに対する短時間・固定間隔の反復wait、固定間隔の`write_stdin`、status・端末出力・生存確認を行わない。一定時間無出力であることだけを理由に失敗・再実行しない。
- 無出力を理由にした定期進捗発言、進捗報告目的のwake・待機短縮・中断・GLMへの問い合わせをしない。必要な報告は最後に確認済みの状態だけで行う。前回報告からの経過時刻や待機継続だけを理由に、確認済みの状態に変化がなくても発言しない。
- ユーザーが状態確認を明示した場合だけ中間状態を確認してよい。
- 最大待機時間後も生存していれば、再調査・代替作業・重複起動をせず再び最大時間で待つ。完了や`RATE_LIMITED`を見逃さない現行動作を維持する。
- 完了時はユーザーの追加入力を待たず、packet処理と可能な次工程を進める。ユーザーの判断・追加情報・許可が本当に必要な場合だけ停止する。
- packet受理・個別commit・install完了は局所終端であり、親USER_REQUESTの完了か次の継続操作かは`~/.codex/instructions/task-lifecycle.md`を読んで判断する。

## `STATUS: RATE_LIMITED`

- `LIMIT: ZAI_GLM_CODING_PLAN_5H`は正常な一時停止であり、`WORKER_ERROR`にしない。`RESET_AT_CST`は中国標準時（UTC+8）。
- working tree、task state、session、resume checkpointを破棄・resetせず、新規taskとしてやり直さない。
- `AUTO_RESUME_AVAILABLE: true`なら`~/.codex/instructions/glm-auto-resume.md`を読み、現在のCodexタスクへ自動再開automationを作成または更新する。作成不能な場合だけ手動再開を案内する。
- 手動再開指示では`glm-worker --resume`を使い、保存済みの同一task・phase・sessionから継続する。元依頼を再構成しない。
- 枠が未回復なら再び`RATE_LIMITED`として状態を保持する。
