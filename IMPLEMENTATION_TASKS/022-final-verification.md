# Task: 全体verification

## Status

planned

## Original instruction

全未完了implementation完了後だけ、go test ./...、race、vet、build、gofmt、install smoke、self-protection、provider accounting、packet/result semantic、parent metadata guards、task lifecycle、PTY、multi-repo、repo-search、fixed Eval offline corpus、clean worktree、Plan/Task/History整合を確認する。このtask自身で新機能を追加せず、failureは該当taskをreopenする。

## Amendments

- 2026-08-22 parent maintenance:

````text
## 2. Task 023「本配置一致確認」を最終taskとして扱わない

現在Task 023は、

> runtimeへ影響する個別commitを適切な区切りでinstallし、installed/source一致を確認する

というcontractです。

これは最終段階に1回行うtaskではありません。

runtimeへ影響するtaskごとのcross-cutting invariantです。

現在のRULESにも、

> 実行基盤へ影響するcommitは適切な区切りで`install.sh`本配置とinstalled/source一致を確認する

という契約があります。

こちらを正にしてください。

### 修正方針

Task 023を、

> Task 022完了後に初めて実行する通常NEXT task

として扱わないでください。

runtime変更taskでは各taskのacceptance / completion flowとして、

1. implementation
2. test/review
3. commit
4. 適切な区切りでinstall
5. installed/source一致
6. そのinstalled状態で必要なproduction smoke

までを行ってください。

改善を数task分commitしたまま、Task 023まで未installで進めないでください。

### Task 023の扱い

以下のどちらかへ単純化してください。

第一候補:

* cross-cutting install ruleは`IMPLEMENTATION_RULES.md`を正とする
* Task 023は独立NEXTから削除
* 最終installed/source一致だけTask 022へ含める

または、

* Task 023を「最終installed-state audit」だけへ縮小
* 各commitでのinstall義務はTask 023へ依存させずRULESで行う

どちらでも構いません。

ただし、

> Task 023が来るまでruntime変更をinstallしない

という解釈が成立する構造はなくしてください。

---

## 3. Final verificationのdependencyを固定番号rangeにしない

Task 022に、

`002〜021の非blocked implementation完了`

というdependencyがあります。

これは今後semantic filenameへ移行し、割り込みtaskが普通に追加される運用と合いません。

将来、

`zai-generic-429-handling.md`

等が追加されても、`002〜021`には入りません。

### 修正

Task 022の開始条件をdynamic invariantにしてください。

例えば:

> Final verification開始時点で、Plan上にTask 022自身以外の実行可能なunblocked implementation/evaluation taskが残っていないこと。BLOCKED / USER_PERMISSION_WAITは除外する。Task 021等から新たに生成された採用taskも完了していること。

これを正としてください。

固定番号rangeやfilename列挙をfinal gateにしないでください。

semantic filename導入後のtaskにも自動的に適用されるcontractにしてください。
````

## Purpose

局所PASSを全体contract完了と誤認せずrelease可能性を確認する。

## Contract

- 列挙gateをfreshに実行し証跡化
- failureを原因taskへ戻す
- installed binary / managed instructions / source HEADの最終一致とinstalled状態での必要なproduction smokeを再監査する

## Must not

- 新機能追加、failureのその場scope拡張、無許可live Evalを行わない

## Acceptance criteria

- 全gate成功、clean worktree、metadata整合
- installed binary / managed instructions / source HEAD一致と必要なproduction smoke
- 独立reviewer、risk/contractに応じて必要なSol品質gate、commit

## Historical invariants

- 全完了証跡の必要見出し

## Dependencies

- Final verification開始時点で、Plan上にTask 022自身以外の実行可能なunblocked implementation / evaluation taskが残っていないこと。BLOCKED / USER_PERMISSION_WAITは除外し、parent decision gateから生成された採用taskも完了していること

## Review findings

none

## Current boundary

最終段階まで開始禁止。
