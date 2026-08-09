package main

import "fmt"

func newTaskPrompt(request string) string {
	return fmt.Sprintf(`MODE: NEW_TASK

USER_REQUEST:
%s
`, request)
}

func decisionPrompt(request string, decision string) string {
	return fmt.Sprintf(`MODE: CONTINUE_WITH_SOL_DECISION

ORIGINAL_USER_REQUEST:
%s

SOL_DECISION:
%s

直前の同一タスクの調査文脈を利用し、この判断に従って作業を継続してください。
`, request, decision)
}

func explicitFixPrompt(request string, decision string, previousReview string, instruction string) string {
	return fmt.Sprintf(`MODE: APPLY_REVIEW_FIX

ORIGINAL_USER_REQUEST:
%s

PREVIOUS_SOL_DECISION:
%s

PREVIOUS_REVIEW:
%s

REVIEW_FEEDBACK:
%s

同一タスクの既存文脈を利用し、指摘範囲を修正してください。
`, request, decision, previousReview, instruction)
}

func reviewerPrompt(request string, decision string, workerPacket packet, reviewNumber int, baseline string) string {
	return fmt.Sprintf(`REVIEW_MODE: INDEPENDENT_REVIEW

USER_REQUEST:
%s

SOL_DECISION:
%s

WORKER_REPORT:
%s

REVIEW_NUMBER: %d

PRE_TASK_BASELINE:
%s

現在のworking treeを実際に独立確認して判定してください。
過去sessionの記憶より現在のコードを優先してください。
PRE_TASK_BASELINEのファイルはworker開始前の状態です。既存未コミット変更と今回変更を区別する必要がある場合に参照してください。
`, request, decision, workerPacket.String(), reviewNumber, baseline)
}

func automaticFixPrompt(request string, decision string, reviewPacket packet) string {
	return fmt.Sprintf(`MODE: APPLY_REVIEW_FIX

ORIGINAL_USER_REQUEST:
%s

PREVIOUS_SOL_DECISION:
%s

INDEPENDENT_REVIEW:
%s

独立reviewerの指摘を修正してください。
新しい要求を追加せず、元要求・既存Sol判断・レビュー指摘の範囲だけを変更してください。
修正後に必要なテスト・lint・build・自己レビューまで行ってください。
`, request, decision, reviewPacket.String())
}

func packetCompressionPrompt(reason string) string {
	return fmt.Sprintf(`直前の作業結果は有効ですが、最終PACKETが出力契約を満たしていません。
作業・調査・テストをやり直さず、直前の結果を意味を失わない範囲で再圧縮し、PACKETだけを再出力してください。
最大15行・全体6 KiB・1行1536 bytes以内です。system promptで指定されたSTATUS別fieldを省略しないでください。

違反内容:
%s
`, reason)
}
