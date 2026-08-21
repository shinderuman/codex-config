# 実装task lifecycle規則

このfileは長期間変わらないtask lifecycle、ownership、compaction、commit、review、history移行の規則だけを保持する。個別task仕様は書かない。

## source-of-truth順位

作業開始・再開時は次の順を正とする。

1. Git / working treeの現物
2. `IMPLEMENTATION_RULES.md`
3. `IMPLEMENTATION_PLAN.local.md`
4. Planが示すACTIVE `IMPLEMENTATION_TASKS/*.md`
5. ACTIVE taskが明示参照する`IMPLEMENTATION_HISTORY.md`の必要箇所
6. conversation context、compaction summary、internal TODO

conversation memoryやcompaction summaryをtask requirementの正にしない。Git現物とPlan/Taskが矛盾する場合、親Codexが現物確認後にPlan/Taskを修正してから続行する。

## task受領と原要求保持

- ユーザーから実装・調査・review指示を受けたら、GLM call、長時間調査、実装開始より前に親Codexがtask fileを作成するか既存taskの`Amendments`へ追記する。「後でPlanへまとめる」は禁止する
- 各taskの`Original instruction`はimmutableとし、契機となったユーザー指示・詳細指示を意味要約せず可能な限り原文で保存する。後から書き換えず、追加変更は`Amendments`へ時系列追記する
- 「これ」「さっきの」「前のreview」等の会話依存参照はOriginal instructionを書き換えず、`Resolved references`へ具体化する
- task fileへ進捗日記を追加せず、requirement contractと最小の`Current boundary`だけを保持する。長い診断はartifact/telemetry、完了証跡はHistoryへ置く

## task file必須構造

全task fileは最低限、`Status`、`Original instruction`、`Amendments`、必要時の`Resolved references`、`Purpose`、`Contract`、`Must not`、`Acceptance criteria`、`Historical invariants`、`Dependencies`、未解決時の`Review findings`、`Current boundary`を持つ。

## task粒度とdispatch

- 原則は1 task file = 1つの独立review可能な変更責務 = 1個別commit候補
- acceptanceが独立、別commit可能、protocol/transport/observability/Eval等で責務が異なる、個別rollback可能、一方だけpermission待ち、1 worker callへ複数大規模責務を渡す場合は分割する
- 同一invariantを成立させる実装とintegration testは過剰分割しない
- umbrella見出しをそのままGLMへ渡さず、具体task file 1件だけをdispatchする

## 再読contract

新session、compaction後、rate limit/provider-unavailable後、長時間停止後、user追加指示後、`--resume`、internal TODO不一致時、reviewer差戻し後は、コードへ触る前に次を読む。

1. `IMPLEMENTATION_RULES.md`全文
2. `IMPLEMENTATION_PLAN.local.md`全文
3. ACTIVE task file全文（Original instructionとAmendmentsを省略しない）
4. taskが明示したHistory見出しだけ

NEXT taskは開始時まで全文を読む必要はない。

## worker / reviewer contract

- workerとreviewerは同じACTIVE task fileを要求定義として独立に読む
- reviewerはimplementer summaryだけでなくOriginal instruction、Amendments、Contract、Must not、Acceptance criteriaを評価する
- scripted runnerの期待packetだけでなくproduction prompt/dispatchとの因果をtestで固定する
- review結果はdefect、user-visible/workflow impact、why Codex+GLM missed it、要求由来/実装由来複雑性、preventionを区別する。原因層はparent orchestration、requirement preservation、worker、reviewer、Sol gate、production wiring、test/scenario、cross-cutting invariant compositionから一次証拠で分類する

## 親Codex専有metadata

次を親Codexだけが編集するtracked surfaceとする。

- `IMPLEMENTATION_RULES.md`
- `IMPLEMENTATION_PLAN.local.md`
- `IMPLEMENTATION_TASKS/*.md`
- `IMPLEMENTATION_HISTORY.md`

GLM worker/reviewerは編集・生成・復元・削除せず、更新候補をstructured resultで返す。model実行中は不変、model停止中の親更新だけをparent-managed deltaとして扱い、worker/reviewer implementation surfaceの外部変更はfail closedにする。pathごとの分岐を増殖させずparent-managed implementation metadataの単一集合へ収束する。

## task完了

task完了時は、必要証跡とescaped原因をHistoryへ追加し、task fileを削除し、Planからentryを削除し、NEXTをACTIVEへ昇格し、final HEAD上でPlan・ACTIVE file・Git境界が一致することを機械確認する。完了task fileを`IMPLEMENTATION_TASKS/`へ残さない。Git履歴が原要求を保持するためHistoryへ全文複製しない。

## commit / install

- GLMにcommitさせない。独立review、必要なSol gate、指摘後再review、acceptance確認後だけ親Codexが単一taskをcommitする。pushは禁止
- task metadata同期はstale-by-one taskの機械postconditionを正とし、文書手順だけで保証したことにしない
- 実行基盤へ影響するcommitは適切な区切りで`install.sh`本配置とinstalled/source一致を確認する

## machine-only data原則

glm-worker/Codex/GLMだけが生成・消費するmachine dataを長期公開APIとして扱わない。旧parser、migration、fallback、deprecated推定、version bridge、dual protocolを「一応読める」だけで恒久追加しない。current schema validationは厳格に保ち、old versionは用途に応じreject/skip/reset/rebuild/delete/resume不能を選ぶ。active task保護と恒久互換を混同しない。

## 禁止

- taskごとの独自state DB、filesystem watcher、daemonを追加しない
- task fileをHistoryや進捗日記にしない
- Planとtask fileへ詳細仕様を二重管理しない
- ユーザー許可のない実Sol High本番A/B、benchmark目的の追加AI call、pushを行わない
