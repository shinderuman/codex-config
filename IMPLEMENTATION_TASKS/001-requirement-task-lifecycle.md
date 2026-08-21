# Task: requirement/task lifecycleをtracked task file方式へ移行

## Status

active

## Original instruction

最初の作業は既存TODOをそのまま再開することではない。まず要求保持とtask lifecycleの構造を修正し、現在残っている未完了作業・review指摘・ユーザー追加要求を、compactionで劣化しないtracked task fileへ再構成すること。この再構成が終わるまで、新しいGLM実装taskを開始しないこと。

新しい4層構造を`IMPLEMENTATION_RULES.md`、表紙/index/現在状態だけの`IMPLEMENTATION_PLAN.local.md`、未完了taskだけの`IMPLEMENTATION_TASKS/*.md`、完了証跡・過去調査・escaped原因だけの`IMPLEMENTATION_HISTORY.md`として実装する。Historyへ完了taskを退避する現行方針を維持する。

Task受領時はGLM call・長時間調査・実装前にtask fileを作る。Original instructionはimmutable、追加はAmendmentsへappend-only、会話依存参照はResolved referencesへ固定する。worker/reviewerも同じACTIVE task contractを独立に読む。親専有surfaceをRULES/PLAN/TASKS/HISTORYへ一般化し、model実行中不変・停止中親delta許容・implementation surface外部変更fail closedを維持する。

## Amendments

- 2026-08-22: latest review findingのTARGETS element semantic、PTY self-contained、multi-repository isolation、machine protocol/legacy/measurement、既存Plan残作業、permission waitをすべて個別task fileへ移す
- 2026-08-22 user: 「現在のMarkdown類だけでコミットするべきなんじゃないの」— 4層metadataをTask 001 production実装より先にMarkdown-only commitしてtracked requirement baselineへ固定する

## Resolved references

- 「現在残っている未完了」= Task 002〜023とblocked A〜F
- review境界は`4cedc91..ce86313`、migration開始HEADは`ce86313`

## Purpose

compaction後も元要求・禁止事項・acceptanceが失われず、Codexとreviewerが同じ誤要約で自己充足PASSすることを防ぎ、Plan再読tokenを抑える。

## Contract

- RULESは恒久lifecycle、PlanはACTIVE/NEXT/BLOCKED/Git境界/停止理由/次操作だけ、TASKSは未完了requirement、Historyはarchiveだけ
- source-of-truth順位と再読contractをAGENTS、managed instruction、EVAL、production prompt/callerへ配線する
- worker/reviewerはACTIVE task file全文を独立に読む
- parent-managed metadataを単一集合としてself-protection・model-call guard・review-resume snapshotへ統合する
- task完了時はHistory追加、task file削除、Plan昇格、final HEAD整合を行う

## Must not

- task fileを新Historyにしない
- 完了taskをTASKSへ残さない
- Planへtask詳細を重複コピーしない
- conversation summaryを要求正本にしない
- worker/reviewerへmetadataを編集させない
- taskごとのDB、watcher、daemonを追加しない

## Acceptance criteria

- 4層責務とsource-of-truth順位が文書・production wiringで一致する
- 現行未完了がすべて個別task fileに移りPlanから一意に参照できる
- ACTIVE fileだけで原要求・禁止・acceptanceを復元できる
- worker/reviewerが同じACTIVE contractを読むproduction-path testがある
- compaction/resume scenarioでOriginal instruction/Amendments/Acceptance criteriaが参照される
- RULES/TASKSを含む親専有guardと停止中親deltaが単一集合で機能する
- 完了task削除・History移行・Plan昇格のscenarioがある
- test/race/vet/build/gofmt、独立reviewer、HIGHならSol gate、個別commit

## Historical invariants

- `IMPLEMENTATION_HISTORY.md`の「review-resume snapshotと親専有file更新の衝突」
- `IMPLEMENTATION_HISTORY.md`の「tracked plan stale-by-one」
- `IMPLEMENTATION_HISTORY.md`の「複雑性の責任評価」

## Dependencies

none

## Review findings

- Plan/History 2fileだけでは現在状態・未完了一覧・詳細要求・恒久規則・設計方針が混在し、compactionで原要求が局所TODOへ圧縮された

## Current boundary

親CodexがRULES、Plan index、Task 001〜023、blocked task filesを作成し、必須見出し・参照数・Git境界を確認して、このcommitでMarkdown-onlyのtracked baselineへ固定済み。production統合のworker-newはmodel work前にrate limit停止しており、reset後に同一checkpointから再開する。
