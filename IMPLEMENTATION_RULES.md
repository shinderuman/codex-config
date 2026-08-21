# 実装task lifecycle規則

このfileは長期間変わらないtask lifecycle、ownership、compaction、commit、review、history移行の規則だけを保持する。個別task仕様は書かない。

## source-of-truth順位

作業開始・再開時は次の順を正とする。

- Git / working treeの現物
- `IMPLEMENTATION_RULES.md`
- `IMPLEMENTATION_PLAN.local.md`
- Planが示すACTIVE `IMPLEMENTATION_TASKS/*.md`
- ACTIVE taskが明示参照する`IMPLEMENTATION_HISTORY.md`の必要箇所
- conversation context、compaction summary、internal TODO

conversation memoryやcompaction summaryをtask requirementの正にしない。Git現物とPlan/Taskが矛盾する場合、親Codexが現物確認後にPlan/Taskを修正してから続行する。

## 要求受領とtracked化

- すべての新規ユーザー要求はconversation contextだけに保持したまま作業を続けず、compaction、GLM call、長時間調査、実装開始より前に親Codexがtrackedな正へ固定する。「後でPlanへまとめる」は禁止する
- tracked化は独立task作成やACTIVE切替と同義ではない。要求内容をparent-managed surface自体へ完全に表現できるか、現ACTIVEの同一責務か、独立taskかを別々に判断する
- 現ACTIVEそのものへの追加指示は、新task分離を判断する前にACTIVE taskの`Amendments`へ原文を時系列追記する。同一責務のacceptance追加は同じACTIVEで継続し、独立責務は新taskへ分割し、別問題を優先実装する場合だけPlan上のACTIVEを切り替える
- 各taskの`Original instruction`はimmutableなlossless requirement sourceとし、契機となったユーザー/親Codexのtask該当指示を可能な限り原文のまま保存する。要約、重複除去、理由の省略、実装TODOだけへの圧縮、「意味は同じ」という書換えを禁止する
- 追加要求は旧本文を上書き・削除せず、日時または順序と原文を`Amendments`へappend-onlyで追記する。新旧要求が矛盾する場合も両方を保持し、最新Amendmentによるoverrideをderived `Contract`へ明示する
- 「これ」「さっきの」「前のreview」等の会話依存参照はOriginal instructionを書き換えず、当時の解決結果を`Resolved references`へ分離して具体化する
- task fileへ進捗日記を追加せず、requirement contractと最小の`Current boundary`だけを保持する。長い診断はartifact/telemetry、完了証跡はHistoryへ置く

## lossless sourceとderived contract

- `Original instruction` / `Amendments` / `Resolved references`を一次要求source、`Purpose` / `Contract` / `Must not` / `Acceptance criteria`を実装・review用のderived informationとして明確に分離する
- derived sectionが十分詳細でもOriginal instructionを要約してよい理由にはしない。token削減はderived sectionと通常resume経路で行い、一次要求sourceを削らない
- 長い指示を複数taskへ分割する場合、各taskの要求・理由・禁止事項・完了条件を理解するために必要な原文sectionをlosslessに保存する。共通前提を暗黙依存にせず、必要部分を含めるかtrackedな共通sourceを明示参照する
- derived sectionへOriginal instruction全文を複製せず、compactな作業・review indexとして維持する

## parent maintenance

次をすべて満たす変更は、独立taskを作成・ACTIVE化せず、現在のACTIVEを維持したまま親Codexが直接処理できる。

- production behaviorとimplementation codeを変更しない
- GLM workerへの独立実装委譲、独立した長時間調査、通常実装task相当のreview/testを必要としない
- 現ACTIVEの意味契約・acceptance criteriaを変更しない
- 独立したrollback単位として管理する必要がない
- Rules、Plan、Task metadata、History等のparent-managed surfaceだけで完結する
- 変更後の意味契約をそのtracked surface自体へ完全に保存できる

命名規則、Plan priority/ACTIVE/NEXT metadata、typo、意味を変えない明確化、History証跡、意味契約を変えないAmendment、parent-managed metadataの参照修正は代表例とする。parent maintenance中もACTIVEは主要な実装・調査・review対象を示し続け、一時task作成、ACTIVE退避、maintenance完了処理、元ACTIVE復帰を行わない。

parent maintenanceは記録不要を意味しない。ユーザー要求をcompaction前に対象のRules / Plan / Task / Historyへ直接保存し、内容に応じた最小確認を行う。変更が単独で意味を持ち、即時固定が安全なら親Codexが小さな個別commitにできるが、GLMへcommit/pushさせずpushしない。

parent-managed metadataを扱うguard、self-protection、production wiring自体の変更は、編集対象がparent-managed surfaceに関係していてもproduction behavior変更であるためparent maintenanceにしない。

## 通常taskへの分離

次のいずれかに該当する要求はparent maintenanceにせず、内容を表すsemantic slugの独立task fileへ固定する。

- production code、CLI / API / protocol behavior、state / checkpoint / telemetry semanticsを変更する
- worker / reviewer promptまたはproduction wiringを意味的に変更する
- test / integration scenario追加が主要成果になる
- 独立reviewが必要な設計変更、複数fileにまたがる実装責務、長時間調査を含む
- 現ACTIVEとは独立したacceptance criteriaを持つ、または途中実装でrollback境界が曖昧になる
- ユーザー許可待ちを独立管理する

独立taskを作成しただけではACTIVEを自動変更しない。現在の主要作業より優先する根拠がある場合だけPlanのACTIVE / NEXT / BLOCKEDを更新する。

## task file必須構造

全task fileは最低限、`Status`、lossless sourceである`Original instruction`、append-onlyの`Amendments`、必要時の`Resolved references`、derived informationである`Purpose`、`Contract`、`Must not`、`Acceptance criteria`、および`Historical invariants`、`Dependencies`、未解決時の`Review findings`、`Current boundary`を持つ。

## task filename

- 新規`IMPLEMENTATION_TASKS/*.md`は内容を表すsemantic slugだけをfilenameに使用し、sequence、priority、status、dependency、completion順、作成順、permission wait分類を表すnumeric prefixを付けない
- 実行順序とpriorityは`IMPLEMENTATION_PLAN.local.md`のACTIVE / NEXT / BLOCKEDを正とし、dependencyは各task fileの`Dependencies`へpathで明示する。filenameや番号大小から推論しない
- 割り込みtaskもsemantic filenameを追加してPlan上の位置だけを変更する。順序へ割り込むための番号、枝番、BLOCKED専用番号帯を作らない
- この規則導入前から存在する番号付きtask fileはrenameせず、reopen時も既存filenameを維持する
- numeric prefixを禁止するためだけの複雑なvalidatorは追加せず、新規taskを作る親instructionと生成経路でsemantic filenameを固定する
- Planを含むtask scheduling listはunordered marker `-`を使い、source上の行順をpriorityとする。割り込み時は項目の移動・追加だけを行い、numeric markerを付けない

## task粒度とdispatch

- 原則は1 task file = 1つの独立review可能な変更責務 = 1個別commit候補
- acceptanceが独立、別commit可能、protocol/transport/observability/Eval等で責務が異なる、個別rollback可能、一方だけpermission待ち、1 worker callへ複数大規模責務を渡す場合は分割する
- 同一invariantを成立させる実装とintegration testは過剰分割しない
- umbrella見出しをそのままGLMへ渡さず、具体task file 1件だけをdispatchする

## priorityとhard dependency

- PlanのACTIVE / NEXTにおけるsource上の順序は変更可能な実行priorityだけを表す
- task fileの`Dependencies`は、そのtaskを正しく実装・検証するために先行taskの成果物が実際に必要なcorrectness prerequisiteだけを、`IMPLEMENTATION_TASKS/<semantic-or-existing-name>.md`形式のpathで明示する
- Planで先行予定、作成順、番号大小、隣接taskという理由だけでdependencyを追加しない。priority変更はPlanだけを変更し、hard dependencyと同一視しない
- final verificationの開始条件は固定番号rangeやtask filename列挙ではなく、開始時点のPlan上で自身以外の実行可能なunblocked implementation / evaluation taskが残っていないこととする。BLOCKED / USER_PERMISSION_WAITは除外し、decision gateから生成された採用taskも完了対象に含める

## parent decision gate

- 測定artifactを採用・棄却・data不足へ分類するumbrella taskはparent decision gateとし、通常のGLM implementation taskとしてdispatchしない
- 親Codexが根拠artifactを読み、採用する実装はsemantic filenameの独立taskへ固定し、棄却はHistoryへ記録する。大規模analysisが必要な場合だけread-only workerへ委譲できる
- parent decision gate自体へ通常implementationと同じ独立reviewerやSol callを機械適用しない。変更のrisk / contractに基づく既存品質gateを正とする

## 再読contract

新session、compaction後、rate limit/provider-unavailable後、長時間停止後、user追加指示後、`--resume`、internal TODO不一致時、reviewer差戻し後、false-complete再open時、acceptance最終確認時、ユーザーから指示適合性を確認された時は、コードへ触る前に次を読む。

- `IMPLEMENTATION_RULES.md`全文
- `IMPLEMENTATION_PLAN.local.md`全文
- ACTIVE task file全文（Original instruction、Amendments、Resolved referencesを省略しない）
- taskが明示したHistory見出しだけ

NEXT taskは開始時まで全文を読む必要はない。

## worker / reviewer contract

- workerとreviewerは同じACTIVE task fileを要求定義として独立に読む
- reviewerは開始時にimplementer summaryだけでなくOriginal instruction、Amendments、Resolved references、Contract、Must not、Acceptance criteriaを確認する
- reviewerは`実装 vs Contract`だけでなく`Contract vs Original instruction / Amendments`も比較し、derived contract作成時の要求欠落自体をreview対象にする
- scripted runnerの期待packetだけでなくproduction prompt/dispatchとの因果をtestで固定する
- review結果はdefect、user-visible/workflow impact、why Codex+GLM missed it、要求由来/実装由来複雑性、preventionを区別する。原因層はparent orchestration、requirement preservation、worker、reviewer、Sol gate、production wiring、test/scenario、cross-cutting invariant compositionから一次証拠で分類する
- task acceptanceにSol gateの文言があっても全task一律の固定儀式とは解釈しない。HIGH risk、cross-cutting invariant、外部成立性、parent lifecycle、machine protocol等は必要なSol品質gateを通し、低risk metadata集計等は既存risk floor / gate判定に従う。消費削減だけを理由に必要なgateを省略しない

## evaluation evidence

- deterministic workflow evidence、semantic regression evidence、real operation observationを分離し、file read等の観測事実から理解・品質保持を推定する単一scoreを発明しない
- telemetryに存在しない、またはdeterministic sourceと定義を説明できないmetric/categoryを名前から新設しない。取得不能な粒度は`unknown` / `insufficient data`とし、不十分な証拠から省略・品質保持を結論しない
- exhaustive確認はfull corpus enumerationとdeterministic predicateによる全候補走査、または網羅性を説明できる別のdeterministic mechanismを必要とする。BM25 top-Nはnavigation / orderingには使えるがexhaustive evidenceにはしない
- observabilityは目的taskが読み取れる最小の保存・集計surfaceへ留め、status / timeline / watch等のpresentation追加は具体的な必要性が確認できた場合だけ行う

## blocked taskのactivation

- BLOCKED / USER_PERMISSION_WAIT taskはpermission受領だけでACTIVEへ移さない
- 許可原文を`Amendments`へlosslessに追記し、prerequisite evaluation artifactを読んだ上で、concrete `Contract`、`Must not`、`Acceptance criteria`を確定してからACTIVE候補にする
- placeholder contractのままGLMへdispatchしない

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
- runtimeへ影響するtaskはimplementation、test/review、commit後、適切な区切りで`install.sh`本配置、installed/source一致、そのinstalled状態で必要なproduction smokeまでをtask completion flowとして行う。複数task分を未配置のまま後続実運用へ進めず、最終taskまでinstall義務を延期しない
- source-only metadata変更はruntime install対象から除外する。最終verificationではinstalled binary / managed instructions / source HEADの一致をfreshに再監査する

## machine-only data原則

glm-worker/Codex/GLMだけが生成・消費するmachine dataを長期公開APIとして扱わない。旧parser、migration、fallback、deprecated推定、version bridge、dual protocolを「一応読める」だけで恒久追加しない。current schema validationは厳格に保ち、old versionは用途に応じreject/skip/reset/rebuild/delete/resume不能を選ぶ。active task保護と恒久互換を混同しない。

## 禁止

- taskごとの独自state DB、filesystem watcher、daemonを追加しない
- task fileをHistoryや進捗日記にしない
- Planとtask fileへ詳細仕様を二重管理しない
- ユーザー許可のない実Sol High本番A/B、benchmark目的の追加AI call、pushを行わない
