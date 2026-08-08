# 確認項目

- `./install.sh`を2回実行して2回目も成功する。
- `~/.codex/rules/default.rules`が残る。
- `~/.codex/config.toml`のPC固有設定が残る。
- `~/.claude/settings.json`の既存設定が残る。
- `glm-worker`がbuildされる。
- cloneしたリポジトリでは`git pull`後に`install.sh`が動く。
- リポジトリ側で削除・改名された管理ファイルは次回install時に配置先から消える。



## Z.ai 5h limit復帰

- `429 + [1308] + Usage limit reached for 5 hour.`だけを5h limitとして識別する。
- generic 429は5h limit扱いしない。
- reset時刻を中国標準時（CST、UTC+8）として保存する。
- worker途中、reviewer途中、auto-fix途中のどこで止まってもresume stateが残る。
- `glm-worker --resume`で同じsession/phaseから継続する。
- rate limit中にsession ID、working tree、baselineをresetしない。
