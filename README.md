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
│   └── Goソース
├── claude/
│   └── settings-managed.json
├── tools/
│   └── merge-json/
└── .githooks/
    └── post-merge
```


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
RESET_AT_CST: YYYY-MM-DD HH:MM:SS
RESET_TIMEZONE: CST (China Standard Time, UTC+8)
RESET_AT_RFC3339: YYYY-MM-DDTHH:MM:SS+08:00
RESUME_AVAILABLE: true
RESUME_COMMAND: glm-worker --resume
```

枠回復後:

```sh
glm-worker --resume
```

同じworker/reviewer sessionと保存済みphaseから再開する。
