# Task: stdin transportをcaller-side stty不要の自己完結CLIへする

## Status

planned

## Original instruction

`glm-worker --fix-stdin <UTF-8 bytes> [--sha256 ...]`と`--decision-stdin`はcallerがstty/raw/echo/termios/canonical modeを知らず、command起動後payloadをstdinへ渡すだけで成立させる。pipe/fileはtermiosを触らずexact read、TTY/PTYだけglm-worker内部でraw/noecho相当へ設定し、変更前stateを正常、short read、SHA mismatch、validation error、command errorで復元する。外部`stty` execは禁止。byte count/SHAはstate変更/model call前fail closed、payloadをargv/shellへ載せずNUL/backtick/$/quote/newline/UTF-8を保持する。

process起動→terminal mode設定→caller feedの順を一次証拠で確認し、Codex PTY APIがfeed前に送らないならREADY handshakeを追加しない。実装後はmanaged instruction/EVALから`stty raw -echo &&` recipeを削除する。

## Amendments

none

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
- managed recipe削除、test/race/vet/build/gofmt、独立reviewer、Sol gate、commit

## Historical invariants

- Historyの「stdin PTY transportのcaller-side stty依存」
- commit `1dbfda5`と`3c263a6`

## Dependencies

- 001-requirement-task-lifecycle.md
- 003-targets-element-semantics.md

## Review findings

- payload完全性の局所要件を満たしたが、caller-side stty recipe込みの成功をCLI自己完結と取り違えた

## Current boundary

未着手。現caller instructionは引き続きstty recipeを使用中。
