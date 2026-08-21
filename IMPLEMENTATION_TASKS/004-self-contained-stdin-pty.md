# Task: stdin transportをcaller-side stty不要の自己完結CLIへする

## Status

planned

## Original instruction

````text
# 6. PTY / stdin transportを細分化して実装する

現在PlanにあるPTY/multi-repo方針を、以下2 taskへ分割する。

---

## Task 004: `--fix-stdin` / `--decision-stdin`をcaller-side stty不要の自己完結CLIへする

### Original requirementの中心

現在Codex側では、

`stty raw -echo && glm-worker --fix-stdin 1260`

のように、callerがPTYをraw/noechoへ設定してからpayloadをfeedしている。

正式な`glm-worker --fix-stdin` / `--decision-stdin`機能を使うためにcallerが、

- `stty`
- raw mode
- echo
- termios
- canonical mode
- terminal設定順序

を知る必要があるのはCLI責務として不自然。

### Contract

callerは、

`glm-worker --fix-stdin <UTF-8 bytes> [--sha256 ...]`

または

`glm-worker --decision-stdin <UTF-8 bytes> [--sha256 ...]`

を起動してpayloadをstdinへ渡すだけでよい。

stdinがpipe/fileならtermios処理なしでexact read。

stdinがTTY/PTYならglm-worker自身が必要なterminal modeを当該invocation内で設定する。

### 実装要求

- Goから外部`stty` commandをexecするだけの置換は禁止
- stdinがterminalか判定
- TTY/PTYだけraw/noecho相当を内部設定
- 変更前termios stateを保存
- 正常終了時に復元
- short read時に復元
- SHA mismatch時に復元
- validation error時に復元
- command error時に復元
- pipe/fileではtermiosを触らない
- byte count / SHA validationは既存どおりstate変更/model call前fail closed
- payload本文をargv/shell commandへ載せない
- NUL/backtick/`$`/quote/newline/UTF-8をbyte列として保持

### process startとfeedのrace

ここで言うraceは複数repository間ではない。

1 invocation内の、

process start
→ terminal mode設定
→ caller feed

順序だけを確認する。

Codex PTY APIが「command起動後、明示feedまでpayloadを送らない」ことを一次証拠で確認できるならREADY handshakeを追加しない。

本当にfeed開始raceが成立する場合だけ最小handshakeを検討する。

handshakeを先回りして追加しない。

### managed instruction修正

実装完了後、AGENTS / caller instruction / EVAL等から

`stty raw -echo && ...`

recipeを削除。

caller contractは、

- command
- byte count
- optional SHA
- stdin feed

だけにする。

### test

実PTY integrationを含める。

- callerが事前sttyしなくても成功
- pipe成功
- exact bytes
- multiline
- backtick
- `$`
- quote
- NULを含む場合の仕様
- UTF-8
- short read
- SHA match/mismatch
- payload echo漏洩なし
- state/model call前validation
- terminal state restoration

fakeだけで完了にしない。

### escaped原因

Historyへ、

「payload完全性という局所要件を満たしたが、caller-side recipe込みの成功をCLI自己完結contractと取り違えた」

ことを既存原因記録と統合する。

---
````

## Amendments

- 2026-08-22 parent maintenance:

````text
#### PTY

`004-self-contained-stdin-pty.md`

はTARGETS semanticとは独立しています。

Task 003をdependencyにしないでください。

Task 001後の新task lifecycleだけで十分です。
````

## Purpose

caller recipe込みの輸送成功ではなく、CLI単体でpayload transport contractを自己完結させる。

## Contract

- TTY判定・invocation-local termios変更・元state復元をGo内部で行う
- pipe/file exact-byte経路と既存hash/state-before-validation契約を維持
- caller contractをcommand、byte count、任意SHA、stdin feedへ縮小

## Must not

- 外部stty、global terminal manager、daemon、先回りREADY handshakeを追加しない
- process kill等へ過剰signal frameworkを追加しない

## Acceptance criteria

- caller事前sttyなしPTY、pipe、exact bytes、multiline、backtick、$、quote、NUL仕様、UTF-8
- short read、SHA match/mismatch、echo漏洩なし、state/model call前validation、全error path state復元
- fakeだけでなく実PTY integration
- managed recipe削除、test/race/vet/build/gofmt、独立reviewer、risk/contractに応じて必要なSol品質gate、commit

## Historical invariants

- Historyの「stdin PTY transportのcaller-side stty依存」
- commit `1dbfda5`と`3c263a6`

## Dependencies

- `IMPLEMENTATION_TASKS/001-requirement-task-lifecycle.md`

## Review findings

- payload完全性の局所要件を満たしたが、caller-side stty recipe込みの成功をCLI自己完結と取り違えた

## Current boundary

未着手。現caller instructionは引き続きstty recipeを使用中。
