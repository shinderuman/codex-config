# 親USER_REQUEST完了と局所終端のlifecycle contract

monitor/automationの安全停止、GLM child task、個別commit・install等の局所的な終端を、現在のCodexタスク(親USER_REQUEST)全体の完了と同一視しない場合に適用する。親Codexのorchestration contractであり、worker/reviewerへの個別checklist追加で代替しない。

## 終端の3分類

- automation/monitorの安全停止: scheduler停止・queue/checkpoint保全・alarm報告の完了は局所終端である。
- GLM child task終端: task・review・commit・installの個別完了は局所終端である。
- 親USER_REQUESTの完了: 親依頼本体と、ユーザー・automationが明示的に継続対象とした実装計画範囲の未解決作業がすべて解消した時だけを指す。

## 局所終端後の再評価

- 各局所終端の直後に、親依頼と明示継続対象計画の未解決作業と次の安全なin-scope操作を再評価する。原因修正・再開確認・後続改善等が残るなら、同じCodexタスクで次の操作へ継続する。
- monitorがscheduler停止・queue保全・alarm報告を完了しても、元依頼に診断・修正・再開確認が残る場合は親USER_REQUESTを完了扱いしない。
- 個別commit・installが完了しても、明示的に継続対象とした計画範囲が残る場合は親USER_REQUESTを完了扱いしない。

## 停止条件

- 新しい権限、Codexの外で変わる外部状態、意味のあるユーザー判断が本当に必要な場合だけ停止する。
- 停止時はcheckpoint・session・working treeを保持し、残作業とblockerを報告する。局所終端の成功報告で親USER_REQUESTの完了報告を代用しない。

## 範囲規律

- 実装計画に長期roadmapが存在するだけで、現在の親依頼範囲へ作業を勝手に拡張しない。
- ユーザー・automationが「後続へ継続」「停止しない」と明示した範囲を、直近subtaskの局所終端で打ち切らない。
