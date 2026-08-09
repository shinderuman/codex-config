# codex-config

Codex + GLM worker環境の配布元。

## 初回

```sh
git clone https://github.com/shinderuman/codex-config.git
cd codex-config
./install.sh
```

ZIPからでも同じで、任意の場所へ展開して`./install.sh`を実行する。

`install.sh`は次だけを行う。

- `codex/`配下の管理対象を`~/.codex`へ配置
- `config-managed.toml`の管理対象キーだけ`~/.codex/config.toml`へ反映
- `glm-worker`をGoでtest/buildし`~/.local/bin/glm-worker`へ配置
- Claudeのmanaged設定だけ`~/.claude/settings.json`へmerge
- Git clone上で実行した場合は`post-merge` hookを有効化

`rules/default.rules`、auth、sessions、SQLite、cache等の既存runtimeには触れない。
一方、過去に`install.sh`が配置した管理ファイルはmanifestで追跡し、リポジトリ側で削除・改名された場合は次回install時に旧ファイルを削除する。
バックアップは作成しない。

管理ファイルを変更する前に、`glm-worker`とJSON merge toolのtest/buildをpreflightとして実行する。preflight失敗時は管理ファイルを更新しない。
install完了後、既に開いているCodexタスクが`AGENTS.md`を再読込する保証はない。ルール反映を保証するには新しいCodexタスクを開始する。

## 2回目以降

```sh
git pull --ff-only
```

初回`install.sh`で設定した`post-merge`から、自動的に`install.sh`が実行される。

hookを使いたくない場合は:

```sh
git pull --ff-only
./install.sh
```

## 構成

```text
codex-config/
├── install.sh
├── codex/
│   ├── AGENTS.md
│   ├── config-managed.toml
│   ├── instructions/
│   ├── rules/
│   │   └── glm-worker.rules
│   └── glm-worker/
│       └── prompts/
├── glm-worker/
│   ├── cmd/
│   │   └── glm-worker/       # CLI entrypoint
│   └── internal/
│       ├── app/              # 引数解析・ロック・出力
│       ├── config/           # 環境変数・リポジトリ設定
│       ├── packet/           # PACKET解析・検証
│       ├── runner/           # Claude Codeプロセス実行
│       ├── state/            # task・session・stats・resume状態
│       └── workflow/         # worker/reviewer状態機械
├── claude/
│   └── settings-managed.json
├── tools/
│   └── merge-json/
├── tests/
│   └── install_smoke.sh
└── .githooks/
    └── post-merge
```

`cmd/glm-worker`は薄いentrypointとし、外部公開しない実装は`internal`配下へ置く。
package間の依存は`app`から各機能へ向け、状態永続化とworkflowを分離する。


## glm-worker CLI

```sh
glm-worker "<新規タスク>"
glm-worker --decision "<Sol判断>"
glm-worker --fix "<NEEDS_SOL_REVIEWへの修正指示>"
glm-worker --resume
glm-worker --status
glm-worker --stats
glm-worker --reset
```

- `--decision`は`NEEDS_SOL_DECISION`で停止した同一タスクを継続する。
- `--fix`は`NEEDS_SOL_REVIEW`後だけ利用できる。
- `--resume`はZ.ai 5時間上限で停止した同一phase・sessionを再開する。
- `--status`と`--stats`は参照専用、`--reset`は現在の統計をarchiveして実行状態を消去する。

主な環境変数:

| 変数 | 既定値 | 用途 |
|---|---|---|
| `GLM_WORKER_HOME` | `~/.glm-worker` | task・session・statsの保存先 |
| `GLM_WORKER_PROMPT_DIR` | `~/.codex/glm-worker/prompts` | worker/reviewer prompt |
| `GLM_WORKER_CLAUDE_BIN` | `claude` | Claude Code実行ファイル |
| `GLM_WORKER_WORKER_MODEL` | `opus` | worker model alias |
| `GLM_WORKER_REVIEWER_MODEL` | `haiku` | reviewer model alias |
| `GLM_WORKER_EFFORT` | `high` | 通常実行effort |
| `GLM_WORKER_ESCALATED_EFFORT` | `max` | Sol判断後・明示fixのeffort |
| `GLM_WORKER_MAX_AUTO_FIX_ROUNDS` | `2` | 自動修正の上限回数 |

リポジトリごとの状態は`$GLM_WORKER_HOME/sessions/<repo SHA-256>/`へ保存する。
`task.status`を正規状態とし、`task-stats.json`は観測用mirrorとして扱う。
statsの破損・書き込み失敗はwarningを出してworkflowを継続し、明示的な`--stats`だけは読み込みエラーを返す。


## Z.ai 5時間上限からの再開

次のZ.ai実エラーを5時間上限として判定する。

```text
API Error: Request rejected (429) · [1308][Usage limit reached for 5 hour. Your limit will reset at YYYY-MM-DD HH:MM:SS][...]
```

genericな429だけでは5時間上限と判定しない。

停止時:

```text
STATUS: RATE_LIMITED
LIMIT: ZAI_GLM_CODING_PLAN_5H
PHASE: ...
TASK_ID: ...
REPO_ROOT: ...
RESET_AT_CST: YYYY-MM-DD HH:MM:SS
RESET_TIMEZONE: CST (China Standard Time, UTC+8)
RESET_AT_RFC3339: YYYY-MM-DDTHH:MM:SS+08:00
AUTO_RESUME_AVAILABLE: true
AUTO_RESUME_AT_RFC3339: YYYY-MM-DDTHH:MM:SS+08:00
AUTO_RESUME_KEY: glm-worker-resume-...
RESUME_AVAILABLE: true
RESUME_COMMAND: glm-worker --resume
```

枠回復後:

```sh
glm-worker --resume
```

同じworker/reviewer sessionと保存済みphaseから再開する。

Codex appでthread heartbeat automationを利用できる場合は、reset時刻の2分後に現在のCodexタスクを自動でwakeする。
wake時は同じローカルcheckoutでtask IDと`rate-limited`状態を照合してから`glm-worker --resume`を実行する。
別worktree、reset済みtask、task IDが変わった状態では再開しない。再度rate limitになった場合は同じautomationを新しい時刻へ更新する。


## GLM実行の軽量化

- worker: `opus` alias → `glm-5.2`
- reviewer: `haiku` alias → `glm-5-turbo`
- 通常worker/reviewer/自動fix: effort `high`
- Sol判断後の継続とSolからの明示fix: effort `max`
- auto-compact window: 500K
- Claude Code sessionはリポジトリ永久ではなくタスク単位。新規タスク開始時にworker/reviewer session IDを更新する。
- 同一タスク内の`--decision`、自動fix、Z.ai 5h limit後の`--resume`ではsessionを維持する。
- `--fix`は`NEEDS_SOL_REVIEW`後だけ使用できる。`PASS`後の追加依頼は新規タスクとして開始し、worker/reviewer sessionを更新する。
- worker/reviewer packetは最大15行・6 KiB・1行1536 bytes。field欠落を含む契約違反時は、同じsessionへ作業をやり直さない再圧縮を1回だけ要求する。

## タスク状態と統計

```sh
glm-worker --status
glm-worker --stats
```

`--status`は現在のtask ID、task status、session、判断待ち、rate limit状態を表示する。
`--stats`は通常のworker packetへ混ぜず、完了済みと現在のタスクを集計して次を表示する。

- worker/reviewerのmodel呼び出し回数
- Sol判断・明示fix・resume・自動fixの回数
- `NEEDS_SOL_DECISION`、`NEEDS_SOL_REVIEW`、`PASS`の件数
- rate limit、packet再圧縮、Solへ返したpacket bytes

新規タスク開始時に前タスクの統計をarchiveし、`--reset`時も現在値を破棄せずarchiveする。


## 開発時の検証

```sh
cd glm-worker
go test ./...
go test -race ./...
go vet ./...
go build -o /dev/null ./cmd/glm-worker
cd ..
./tests/install_smoke.sh
```
