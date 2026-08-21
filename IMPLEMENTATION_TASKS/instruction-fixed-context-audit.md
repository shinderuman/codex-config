# Task: Codex/GLM instruction固定contextを監査・削減

## Status

planned

## Original instruction

````text
# Codex/GLM instruction固定contextの監査

Codex/GLMが通常経路で毎回読むinstruction量を監査し、必要な意味契約を維持したまま固定contextを削減するtaskを追加する。

特に`glm-execution.md`を優先して確認すること。現在は過去事故の経緯や、PTY/input輸送・二重表示問題の調査詳細まで恒久instructionに含まれている。

整理方針:

- 現在のworkflow成立に毎回必要なcontractだけを恒久instructionへ残す
- 過去事故・調査経緯はHistoryへ移す
- task固有事項はtask側へ置く
- 同じcontractの重複を減らす
- `WORKER.md` / `REVIEWER.md`はサイズだけを理由に大規模rewriteしない
- Task 004でPTY責務をglm-worker内部へ移した後、caller側から不要になった`stty`やterminal mode詳細を削除する

変更前後で、Parent Codex / GLM worker / reviewerが実際に通常経路で読むbytes/token proxyを比較する。

Markdown総量削減ではなく、通常利用時の固定context削減を評価基準とする。

新しいcontext管理機構は作らない。
````

## Amendments

none

## Resolved references

- `glm-execution.md` = repository source `codex/instructions/glm-execution.md`と、そのmanaged配置先`~/.codex/instructions/glm-execution.md`
- `WORKER.md` / `REVIEWER.md` = `codex/glm-worker/prompts/WORKER.md` / `codex/glm-worker/prompts/REVIEWER.md`
- Task 004 = `IMPLEMENTATION_TASKS/004-self-contained-stdin-pty.md`

## Purpose

通常のParent Codex / GLM worker / reviewer経路へ毎回注入される固定contextを、workflow成立に必要な意味契約を維持して削減する。

## Contract

- Parent Codex、worker、reviewerの通常経路ごとに、常時読むinstruction/promptと条件付きで読むfileを実際のwiringから分類する
- 変更前後の通常経路についてfile別・role別のbytesと同一方式のtoken proxyを測定し、Markdown総量ではなく固定context差分を評価する
- `codex/instructions/glm-execution.md`を優先監査し、恒久contract、過去事故/調査経緯、task固有事項、重複へ分類する
- 過去事故と調査経緯は対応するHistoryへ移し、task固有事項は該当taskへ保持して、通常経路に必要なcontractだけを恒久instructionへ残す
- Task 004完了後のCLI contractを基準に、caller側で不要になった`stty`、raw/noecho、terminal mode設定順序等の詳細を削除する
- `WORKER.md` / `REVIEWER.md`は実際の重複と通常固定contextへの寄与を根拠に局所整理する

## Must not

- line数、KB、Markdown総量だけを成功指標にしない
- `WORKER.md` / `REVIEWER.md`をサイズだけで大規模rewriteしない
- 必要なworkflow、safety、ownership、fail-closed contractを削らない
- 新しいcontext管理機構、動的loader、cache、state、daemonを追加しない
- Task 004完了前のcaller contractを将来仕様だけで先行削除しない

## Acceptance criteria

- Parent Codex / worker / reviewerの通常読込経路と条件付き読込経路をwiring根拠付きでinventory化
- 変更前後の通常固定context bytes/token proxyを同一入力・同一算出方法で比較
- `glm-execution.md`から不要な事故経緯、PTY/input輸送・二重表示の調査detail、task固有事項、重複を移管または削除し、残した恒久contractを説明可能
- Task 004後にcaller側`stty`/terminal mode detailが通常instructionから除去され、自己完結CLI contractと一致
- `WORKER.md` / `REVIEWER.md`は意味契約保持を確認し、サイズだけを根拠とする大規模rewriteなし
- instruction/promptのproduction wiringと必要scenario、独立reviewer、risk/contractに応じて必要なSol品質gate、commit、本配置後のsource一致と必要なsmoke

## Historical invariants

- `IMPLEMENTATION_HISTORY.md`の「stdin PTY transportのcaller-side `stty raw -echo`依存」
- `IMPLEMENTATION_HISTORY.md`の「実運用PACKET二重表示再現」
- `IMPLEMENTATION_HISTORY.md`の「複雑性の責任評価」

## Dependencies

- `IMPLEMENTATION_TASKS/004-self-contained-stdin-pty.md`

## Review findings

none

## Current boundary

未着手。通常読込wiring、現行bytes/token proxy、移管先History/taskを確認してからinstruction本文を変更する。
