# Task: Task 001/002導入後に残ったcontract不整合を収束させる

## Status

active

## Original instruction

`````text
# Codexへの修正指示：Task 001/002導入後に残った不整合の修正

Task 001/002のproduction integrationは概ね成立しているが、実運用レビューで以下の未解決問題を確認した。新しい一般ルールを増やすのではなく、既存contractへ実装・prompt・task metadataを収束させること。

## 1. `--decision` のrepair後retry不能を修正

現在、`ExecuteDecision()`はdecisionをstateへ反映した後にACTIVE task解決を行う。

ACTIVE解決がfail closedすると、

- statusは`waiting-sol-review`
- pending decisionは残る

ため、その後Planを修復しても`--decision`も`--fix`も正規経路で進めなくなる。

**ACTIVE task解決をdecision消費/state mutationより前に行う**方向を第一候補として、失敗後に親がmetadataを修復し、同じdecisionを再実行できるようにする。

最低限、

`ACTIVE不正 → decision拒否 → 親が修復 → 同じdecisionで正常resume`

をproduction-path testで固定すること。

## 2. WORKER / REVIEWERの旧USER_REQUEST contractを現在の要求sourceへ同期

Task 001ではACTIVE task fileを要求の正としてproduction promptへ渡している一方、`WORKER.md` / `REVIEWER.md`にはまだ、

> USER_REQUESTを常に正とする

という旧contractが残っている。

この意味衝突を除去する。

要求の正はACTIVE task fileの、

- Original instruction
- Amendments
- Resolved references
- Contract
- Must not
- Acceptance criteria

とする。

USER_REQUESTやconversation summaryは補助contextとして利用してよいが、要求定義の代替にはしない。

reviewerは少なくとも、

1. `Derived Contract vs Original instruction`
2. `Implementation vs Contract`

の両方を確認する。

新しいpromptを追加するのではなく、旧contractを現在のcontractへ置換すること。

## 3. Plan schedule parserの未知記述をfail-openで無視しない

現在ACTIVE/NEXT/BLOCKED parserは`- `以外の行を無視するため、
```markdown
## ACTIVE
- `a.md`
* `b.md`
```

のような人間には複数taskに見える記述をmachine側が1件として受理し得る。

schedule sectionでは、blank等の明確に許可した記述以外のtask-like/未知list記法を黙って無視しないこと。

runtimeとinstallerの受理集合を一致させたまま、invalid schedule syntaxはfail closedにする。

ただしMarkdown一般parserを新設するほど複雑化しない。

## 4. task lifecycle metadataの二重管理を解消

現在、

- HistoryではTask 001/002完了
- task fileは残存
- task file内`Status`はPlanと不一致

という状態になっている。

PlanがACTIVE/NEXT/BLOCKEDの正なら、task file内`Status`を別のschedule stateとして維持する必要性を再評価する。

第一候補は**task fileのStatus二重管理を廃止し、schedule stateはPlanだけを正とする**こと。

また、完了task fileを削除するcontractと、後続taskがそのfile pathをDependenciesとして持つcontractが衝突している。

dependency完了時は、

- dependent taskからfulfilled dependencyを除去
- 後続作業に必要な成立済みinvariantだけHistorical invariantsへ残す
- 完了task fileを削除

という単純なlifecycleへ収束できるか確認する。

このために完了task archive機構や新しいstatus同期機構を追加しない。

## Review観点

今回の問題はTask 001/002自体の方向性が悪かったというより、

- 新contract追加後に旧prompt contractの撤去まで確認しなかった
- entrypointごとのfail-closed後retry可能性を横断確認しなかった
- Planとtask fileへ同じschedule stateを持たせた
- 「完了task削除」と「dependencyをfile pathで保持」の組合せを定義していなかった

ことによる。

最後の2点については、以前の設計指示側にも原因があるため、Codex独自の過剰設計として扱わないこと。

修正後、Task 003へ進む前にこれらのproduction/state/task lifecycle contractが矛盾していないことを確認する。

今回は既存RULESにある共通事項は再掲せず、今回新しく直すべき差分だけにしています。
`````

## Amendments

- none

## Resolved references

- `Task 001/002`は、`IMPLEMENTATION_HISTORY.md`の完了済みTask 001 requirement/task lifecycle production integrationとTask 002 final HEAD postconditionを指す。
- 指示受領時点でTask 003は実装・review・Sol gateを完了し、初回commit後の同一commit metadata同期中だった。Task 003をrollbackせず完了処理し、本taskをTask 004より先に割り込みACTIVEとする。
- 「Task 003へ進む前」は指示受領時系列とrepository現物が一致しないため、Task 003完了後かつTask 004開始前に本taskを完了し、同じproduction/state/task lifecycle整合性を確認するものとして解決する。

## Purpose

Task 001/002で導入した要求source、fail-closed、Plan scheduling、task完了lifecycleを、旧promptとretry不能なentrypointと二重管理metadataを残さない一貫したproduction contractへ収束させる。

## Contract

- `ExecuteDecision()`はACTIVE task解決をdecision消費・state mutationより前に行い、metadata修復後に同じdecisionを正規再実行できるようにする
- `WORKER.md` / `REVIEWER.md`の旧USER_REQUEST正本記述をACTIVE task file正本へ置換し、reviewerは要求source対derived contractとderived contract対実装を確認する
- ACTIVE / NEXT / BLOCKED schedule sectionの未知・task-like list記法をfail closedにし、runtimeとinstallerの受理集合を一致させる
- schedule stateはPlanだけを正とする方向でtask fileの`Status`を廃止し、fulfilled dependencyをdependent taskから除去して必要な成立済みinvariantだけ`Historical invariants`へ残す単純な完了lifecycleへ既存task corpusをmigrationする

## Must not

- 新しい一般RULE、完了task archive機構、status同期機構、一般Markdown parserを追加しない
- USER_REQUESTやconversation summaryを要求定義の代替に戻さない
- Task 001/002の成立済み方向性を不要に作り直さない
- 最後の2問題をCodex独自の過剰設計だけに帰属させない
- GLMにcommit/pushさせない。pushしない

## Acceptance criteria

- `ACTIVE不正 → decision拒否 → 親修復 → 同じdecisionで正常resume`をproduction-path testで固定
- WORKER / REVIEWERの要求sourceがACTIVE task file sectionsへ同期し、reviewの二段比較が明記される
- unknown schedule syntaxをruntime/installer双方が同じ受理集合でfail closedにし、正当なblank/既存scheduleを壊さない
- task fileのschedule `Status`二重管理を廃止し、全未完了task corpusとtask生成・validation・prompt・testをmigrationする
- 完了済みTask 001/002を参照するfulfilled dependencyを除去し、必要な成立済みcontractだけを各taskの`Historical invariants`へ残す
- 完了task削除と後続dependency contractが矛盾せず、Task 004開始前にproduction/state/task lifecycle contractの整合性を確認する
- 全test、race、vet、build、gofmt、独立review、risk/contractに応じたSol品質gateを通し、親Codexがcommitする

## Historical invariants

- `IMPLEMENTATION_HISTORY.md`のTask 001、Task 002、Task 003完了証跡とescaped原因分析
- Task 001で成立したparent-managed metadata guardとACTIVE task requirement source wiring
- Task 002で成立したruntime/installer schedule parser parityとfinal HEAD postcondition

## Dependencies

- none（Task 001/002/003は完了済みで、成立済みinvariantはHistoryとproduction実装を参照する）

## Review findings

- 新contract導入後の旧prompt撤去、fail-closed後retry可能性、schedule stateの単一owner、完了task削除とdependency pathの合成確認が不足していた

## Current boundary

割り込み要求をlosslessに固定してACTIVE化。production実装・task corpus migration・testは未着手。
