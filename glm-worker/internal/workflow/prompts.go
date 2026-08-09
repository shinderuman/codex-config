package workflow

import (
	"fmt"
	"strings"

	"github.com/shinderuman/codex-config/glm-worker/internal/packet"
	"github.com/shinderuman/codex-config/glm-worker/internal/state"
)

const artifactPromptMarker = "REPORT_ARTIFACT_DIR:"

func withArtifactContext(prompt string, artifactDir string) string {
	if strings.Contains(prompt, artifactPromptMarker) {
		return prompt
	}
	return fmt.Sprintf(`%s

REPORT_ARTIFACT_DIR: %s
PACKETへ収まらない正確な一覧・レポート・生成物だけをこのディレクトリへ保存してください。リポジトリへ追加せず、PACKETのARTIFACTSには絶対パスだけを記載してください。大容量成果物が不要ならARTIFACTS: noneとしてください。
`, strings.TrimRight(prompt, "\n"), artifactDir)
}

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

func reviewerPrompt(request string, decision string, workerPacket packet.Packet, reviewNumber int, baseline string) string {
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

func automaticFixPrompt(request string, decision string, reviewPacket packet.Packet) string {
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
PACKET_BEGINを最初の物理行、PACKET_ENDを最後の物理行にし、前後の説明・空行・箇条書き・継続行を出力しないでください。
各fieldはちょうど1つの物理行へKEY: value形式で一度だけ記載し、複数事項は同じvalue内でセミコロン区切りにしてください。
大容量成果物の内容は再掲せず、ARTIFACTSには既に保存済みの絶対パスだけを記載してください。

違反内容:
%s
`, reason)
}

// resumePromptは5h上限中断タスクの同一session再開用の指示を組み立てる。
func resumePrompt(checkpoint state.ResumeCheckpoint) string {
	originalPrompt := checkpoint.OriginalPrompt
	if originalPrompt == "" {
		originalPrompt = checkpoint.Prompt
	}

	return fmt.Sprintf(`前回のClaude Code呼び出しはZ.ai GLM Coding Planの5時間利用上限で中断しました。

同じタスク・同じsessionの中断箇所から作業を再開してください。
現在のworking treeには前回の途中変更が残っている可能性があります。破棄せず、session文脈と照合して続行してください。
最初から調査・実装をやり直さず、未完了部分だけを進めて所定のPACKETまで完了してください。

前回の指示:
%s
`, originalPrompt)
}
