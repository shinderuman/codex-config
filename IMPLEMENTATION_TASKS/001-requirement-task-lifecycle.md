# Task: requirement/task lifecycleをtracked task file方式へ移行

## Status

active

## Original instruction

````text
# 0. この指示の位置づけ

現在の`IMPLEMENTATION_PLAN.local.md`には多くの方針・未完了項目が残っているが、長時間のCodex作業とcontext compactionを繰り返す中で、

- 元のユーザー要求
- なぜそのtaskを行うか
- 禁止事項
- 完了条件
- 過去のreviewで何がfalse-completeだったか

が圧縮され、単純なTODO名だけが残る危険が実際に発生している。

直近ではStructured Output対応について、本来は

`GLM → typed structured output → Go semantic validation → compact structured representation → Codex`

までを改善し、free text再解釈を減らしてCodex Reductionへ接続する要求だったにもかかわらず、

`GLM → structured JSON → Go struct → Display() → 旧PACKET風text → Codex`

という状態でも「Structured Output対応完了」と評価できる程度まで要求が局所化された。

stdin transportでも同様に、本来は`glm-worker --fix-stdin` / `--decision-stdin`が自己完結したCLIであるべきところ、caller側で

`stty raw -echo && glm-worker --fix-stdin <bytes>`

というrecipeを実行すればtransportが成立することを「CLI contract成立」と取り違えた。

このため、今後はconversation contextやcompaction summaryを要求の唯一の保持場所として信用しない。

**最初の作業は既存TODOをそのまま再開することではない。まず要求保持とtask lifecycleの構造を修正し、現在残っている未完了作業・review指摘・ユーザー追加要求を、compactionで劣化しないtracked task fileへ再構成すること。**

この再構成が終わるまで、新しいGLM実装taskを開始しないこと。

---

# 2. IMPLEMENTATION管理構造を先に再設計する

現在の

- `IMPLEMENTATION_PLAN.local.md`
- `IMPLEMENTATION_HISTORY.md`

の2file構造だけでは、Planが「現在状態」「未完了一覧」「詳細要求」「恒久ルール」「設計方針」を兼ね始めており、task単位の原要求がcompaction後に要約されやすい。

一方、現在すでに成立している

- Plan = 現在と未来
- History = 完了済み・過去調査・escaped原因

という分離は維持する。

Historyへ完了taskを退避する現行方針を逆戻りさせない。

## 2.1 新しい4層構造

以下へ整理すること。

```text
IMPLEMENTATION_RULES.md
IMPLEMENTATION_PLAN.local.md
IMPLEMENTATION_TASKS/
  001-....md
  002-....md
  ...
IMPLEMENTATION_HISTORY.md
```

### `IMPLEMENTATION_RULES.md`

長期間変わらないtask lifecycle / ownership / compaction / commit / review / history移行ルールだけを置く。

ここへ個別taskの仕様を書かない。

### `IMPLEMENTATION_PLAN.local.md`

**表紙・index・現在状態だけ**にする。

最低限以下だけを持つ。

- 最上位目的
- ACTIVE task file
- NEXT task files（優先順）
- BLOCKED / USER_PERMISSION_WAIT task files
- 現在のGit境界
- 現在の停止理由
- 次の親Codex操作

個別taskの詳細要求をPlanへ重複コピーしない。

### `IMPLEMENTATION_TASKS/*.md`

**未完了taskだけ**を置く。

ユーザー原要求・追加要求・解決済み参照・acceptance criteriaをcompactionから守るためのtracked requirement contract。

完了taskのarchiveとして使わない。

### `IMPLEMENTATION_HISTORY.md`

現在どおり、

- 完了証跡
- 過去の調査結果
- escaped bug/review原因
- false-complete再open履歴
- 過去commit / install / Eval証跡

を保持する。

通常resume時には全文を読まない。

---

# 3. IMPLEMENTATION_RULES.mdへ固定するルール

以下をそのまま意味契約として実装・文書化すること。

## 3.1 source-of-truth順位

作業再開時の優先順位は以下。

1. Git / working treeの現物
2. `IMPLEMENTATION_RULES.md`
3. `IMPLEMENTATION_PLAN.local.md`
4. ACTIVE `IMPLEMENTATION_TASKS/*.md`
5. ACTIVE taskが明示参照する`IMPLEMENTATION_HISTORY.md`の必要箇所
6. conversation context / compaction summary / internal TODO

conversation memoryやcompaction summaryを、task requirementの正として扱わない。

Git現物とPlan/Taskが矛盾したら、現物を確認し、親CodexがPlan/Taskを修正してから続行する。

## 3.2 task受領時

ユーザーから新しい実装・調査・review指示を受けたら、**GLM call、長時間調査、実装開始より前に**親Codexがtask fileを作成または既存taskへAmendmentを追加する。

「後でPlanへまとめる」は禁止。

compactionが起きる前にtracked外部記憶へ固定する。

## 3.3 Original instructionはimmutable

各task fileに、

`## Original instruction`

を持つ。

ここにはtaskを作った契機となるユーザー指示・Codexへ渡された詳細指示を、意味を要約せず可能な限り原文のまま保存する。

親Codexが後から「分かりやすく書き換える」ことを禁止する。

変更要求は上書きせず、

`## Amendments`

へ時系列で追記する。

Amendmentにもユーザー追加指示を可能な限り原文で保存する。

## 3.4 会話依存参照は別欄で解決する

原文が、

- 「さっきのやつ」
- 「これ」
- 「前のreview」
- 「それも合わせて」

等を含む場合、Original instruction自体を書き換えない。

別欄、

`## Resolved references`

へ、その時点で参照対象を具体化して保存する。

例:

```markdown
## Original instruction

「さっきのJSON化とログの話をまとめてやれ」

## Resolved references

- 「さっきのJSON化」=
  glm-worker→Codexまでcompact structured outputにし、
  free text再解釈を削減する要求
- 「ログ」=
  telemetry/event JSONLだけでなく、
  structured result / checkpoint / cache等のmachine-only schemaも含む
```

## 3.5 task fileの必須構造

全task fileは最低限以下を持つ。

```markdown
# Task: <単一責務の名前>

## Status
planned / active / waiting-decision / waiting-review / blocked-user-permission

## Original instruction
<immutable>

## Amendments
<append only>

## Resolved references
<必要な場合のみ>

## Purpose
このtaskが最上位目的へどう接続するか

## Contract
成立させる外部・内部contract

## Must not
禁止事項、scope外、やってはいけない代替実装

## Acceptance criteria
機械的または実証可能な完了条件

## Historical invariants
必要なHistory見出し/commitだけを参照

## Dependencies
先行task file

## Review findings
このtaskに未解決review指摘がある場合のみ

## Current boundary
実装・調査の現在地点。進捗ログではなく再開境界だけ
```

## 3.6 task fileに進捗日記を書かない

長い作業ログ、毎回の試行錯誤、完了した細かいstepをtask fileへ追加し続けない。

task fileはrequirement contractであり、History代替ではない。

作業途中に保持すべき診断artifactは既存artifact/telemetryへ置く。

`Current boundary`だけ必要最小限更新する。

## 3.7 taskの粒度

原則、**1 task file = 1つの独立review可能な変更責務 = 1個別commit候補**。

以下のどれかを満たすなら分割する。

- acceptance criteriaが独立している
- 別commitで安全に導入できる
- protocol / transport / observability / Evalなど責務が異なる
- 片方が失敗しても他方を採用可能
- 片方だけrollback可能
- 一方がユーザー許可待ち
- 1 worker callへ複数の大きな実装責務を同時に渡すことになる

単に同じ会話で指示されたという理由で巨大taskへまとめない。

一方、同一invariantを成立させる実装とそのintegration testを別taskへ過剰分割しない。

## 3.8 umbrella TODOを実装taskとして渡さない

Plan上で複数taskをまとめる見出しはあってよいが、GLMへ

「machine protocol改善を全部やれ」

のようなumbrella taskをそのまま渡さない。

必ず具体task file 1件をdispatchする。

## 3.9 作業開始・resume・compaction後の再読

以下の場合、必ず:

1. `IMPLEMENTATION_RULES.md`
2. `IMPLEMENTATION_PLAN.local.md`
3. ACTIVE task file全文
4. task fileが明示したHistory必要箇所

を再読してからコードへ触る。

対象:

- 新しいCodex session
- context compaction後
- rate limit/provider-unavailable後
- 長時間停止後
- user追加指示受領後
- `--resume`
- internal TODOとの不一致時
- reviewer差戻し後

ACTIVE task fileの`Original instruction`と`Amendments`を飛ばして、`Current boundary`だけ読んで再開してはいけない。

## 3.10 worker / reviewerも同じtask contractを基準にする

GLM workerへtaskを委譲するとき、ACTIVE task fileを必ず読むようproduction prompt / caller contractへ含める。

reviewerも実装者のsummaryだけを評価せず、同じtask fileの

- Original instruction
- Amendments
- Contract
- Must not
- Acceptance criteria

を独立評価基準として読む。

implementerの要約をreviewerの唯一の要求定義にしない。

これにより、

誤解されたcompaction summary
→ 実装
→ 同じ誤解でreview
→ PASS

という自己充足reviewを防ぐ。

## 3.11 親Codex専有surface

以下を親Codex専有tracked surfaceとする。

- `IMPLEMENTATION_RULES.md`
- `IMPLEMENTATION_PLAN.local.md`
- `IMPLEMENTATION_TASKS/*.md`
- `IMPLEMENTATION_HISTORY.md`

GLM worker/reviewerは編集禁止。

必要な変更候補はstructured resultへ返す。

既存self-protection / snapshot / review-resume guardがPlan/History 2fileだけを特別扱いしている場合、新しいRULES/TASKS surfaceを追加したことでcontractが破綻しないか確認する。

親Codexがrate-limit/reviewer停止中にACTIVE taskへユーザーAmendmentを追記することはあり得る。

そのため、

- model実行中は不変
- model停止中の親専有file更新は親管理deltaとして扱う
- worker/reviewer implementation surfaceの外部変更はfail closed

という既存review-resume思想を、新しい親専有surfaceへ一般化する。

path名を4箇所hard-codeして分岐を増殖させず、「parent-managed implementation metadata」という単一集合へ収束させる。

## 3.12 task完了時

taskが完了したら、

1. 必要な完了証跡をHistoryへ追加
2. escaped原因があればHistoryへ追加
3. task fileを削除
4. Planからtask entryを削除
5. NEXT taskをACTIVEへ昇格
6. final HEAD上でPlanが現在状態を正しく示すことを確認

完了task fileを`IMPLEMENTATION_TASKS/`へ残し続けない。

Git履歴には元task fileが残るため、必要なら過去原文を追跡できる。

HistoryへOriginal instruction全文をコピーする必要はない。Historyは完了証跡・原因要約に留める。

## 3.13 commit

GLMにcommitさせない。

独立review、必要なSol品質gate、指摘後再reviewを通過し親Codexが承認した単一taskをCodexがcommitする。

pushは禁止。

task完了時のmetadata同期については後述するstale-by-one taskの機械postconditionを正とし、文書手順だけで保証したことにしない。

## 3.14 新しい指示が既存taskを変更した場合

既存taskがactiveでも、ユーザー追加指示を受けたら実装を続ける前にAmendmentへ固定する。

追加指示が別責務なら、新task fileへ分割しPlanのpriority/dependencyを更新する。

その判断自体をcompaction contextだけに保持しない。

---

# 4. 既存Planを新構造へmigrationする最初のtask

## Task 001: requirement/task lifecycleをtracked task file方式へ移行

### Purpose

context compactionで元要求・禁止事項・完了条件が失われ、局所TODOだけが残る問題を解消する。

### 実施

- `IMPLEMENTATION_RULES.md`を新設
- `IMPLEMENTATION_TASKS/`を新設
- 現在のPlanにある恒久workflow ruleをRULESへ整理
- 現在のPlanにある個別未完了仕様を以下のtask fileへ細分化
- Planを表紙/indexへ縮小
- Historyの現行archive責務は維持
- parent-managed self-protection / review-resume guardへRULES/TASKSを安全に統合
- AGENTS / EVAL / managed Codex instructionで再読contractを固定
- worker/reviewerがACTIVE task contractを独立に読むproduction wiringを固定
- compaction後の再開を模したtest / scenarioで、task名だけでなくOriginal instruction / Acceptance criteriaが参照されることを確認

### Must not

- task fileを新しいHistoryにしない
- 完了taskをTASKSへ残さない
- Planとtask fileへ同じ詳細仕様を二重管理しない
- conversation summaryをtask fileの代わりにしない
- worker/reviewerへtask fileを書かせない
- taskごとに新しい独自state DBを作らない
- filesystem watcherやdaemonを追加しない

### Acceptance

- 4層責務が文書上明確
- current unfinished項目がすべてtask fileへ移行
- Planだけ読めばどのtask fileを読むべきか一意
- ACTIVE task fileを読めばcompaction前の要求を再構築できる
- reviewerも同じ原要求を読む
- 完了taskはHistoryへ落ちTASKSから消える
- self-protection / snapshot contractが新surfaceで破綻しない
- unit/scenario/wiring test
- independent reviewer
- HIGHならSol品質gate
- commit

このmigrationを完了するまでは以下の実装taskをGLMへ開始しないこと。

---
````

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
