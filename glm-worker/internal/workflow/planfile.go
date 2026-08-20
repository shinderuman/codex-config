package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/packet"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/runner"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// implementationPlanFileはrepository rootへ置くtracked canonical sourceの実施計画file。
// 本文・checkbox・優先順・現在状態を更新できるのは親Codexだけであり、GLM worker/reviewerは
// 読み取り専用、欠損時も生成しない。wrapperはworker呼出前後の内容不変を機械強制する。
// glm-workerはplanを置かない他repositoryでも使うため、欠損の意味はGit indexの追跡有無で
// 区別する。追跡中planのworking tree欠損は親Codexが置いた正が失われた状態のため呼出前に
// fail closedする。未追跡欠損の通常作業を許可するのはGit管理外directoryと確認できた場合と
// Git repository内で未追跡と正常判定できた場合だけで、repo内で判定不能なGit異常は
// baseline取得不能として同じく呼出前にfail closedする。
const implementationPlanFile = "IMPLEMENTATION_PLAN.local.md"

// errPlanFileGuardStoppedはplan file不変性確認によるfail closed停止が完了したことを
// 呼出元へ伝えるsentinel。packet出力・checkpoint清除・task status更新は既に終わっているため、
// 呼出元は追加のerror出力をしない。
var errPlanFileGuardStopped = errors.New("implementation plan file guard stopped workflow")

// planFileStateはplan fileの存在と内容hash。欠損はexists=falseで表現し、呼出中の新規作成も
// 不変性違反として検出する。
type planFileState struct {
	exists bool
	sha256 string
}

func readPlanFileState(repoRoot string) (planFileState, error) {
	b, err := os.ReadFile(filepath.Join(repoRoot, implementationPlanFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return planFileState{}, nil
		}
		return planFileState{}, fmt.Errorf("read %s: %w", implementationPlanFile, err)
	}
	sum := sha256.Sum256(b)
	return planFileState{exists: true, sha256: hex.EncodeToString(sum[:])}, nil
}

func planFileChangeReason(before, after planFileState) string {
	switch {
	case before.exists && after.exists:
		return "内容が変化しました"
	case !before.exists && after.exists:
		return "存在しない状態から新規作成されました"
	default:
		return "削除されました"
	}
}

// quietWhenPlanFileGuardStoppedはplan file guardのfail closed終端が既にpacket出力・状態遷移を
// 完了している場合、追加のerror出力をせず正常終了として扱う。
func quietWhenPlanFileGuardStopped(err error) error {
	if errors.Is(err, errPlanFileGuardStopped) {
		return nil
	}
	return err
}

// planFileTrackingはplan fileのGit追跡判定の確定結果。判定errorは値で表現せずerrorへ
// 分離し、未追跡へ畳まない。
type planFileTracking int

const (
	planFileTrackingTracked planFileTracking = iota + 1
	planFileTrackingUntracked
	planFileTrackingOutsideGit
)

// gitWorktreePresentはrepoRootから上位へ.git markerを探索し、Git管理下にあるかを
// file構造で確定する。git commandのerror文面へ依存しないため、Git管理外の判定を
// command異常と区別できる。
func gitWorktreePresent(repoRoot string) (bool, error) {
	for dir := repoRoot; ; dir = filepath.Dir(dir) {
		_, err := os.Stat(filepath.Join(dir, ".git"))
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("stat %s: %w", filepath.Join(dir, ".git"), err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false, nil
		}
	}
}

// classifyPlanFileTrackingはplan fileの追跡状態をrepository/index現物から判定する。
// 追跡判定を特定repository pathの前提へhardcodeせず対象repositoryへ問い合わせる。
// Git管理外directoryは未追跡欠損の通常作業を許可できる唯一の無条件許可枠であり、
// Git repository内ではls-filesの失敗を判定不能errorとして呼出元へ返す。
func classifyPlanFileTracking(repoRoot string) (planFileTracking, error) {
	insideGit, err := gitWorktreePresent(repoRoot)
	if err != nil {
		return 0, err
	}
	if !insideGit {
		return planFileTrackingOutsideGit, nil
	}
	output, err := exec.Command("git", "-C", repoRoot, "ls-files", "--", implementationPlanFile).Output()
	if err != nil {
		return 0, fmt.Errorf("git ls-files: %w", err)
	}
	if strings.TrimSpace(string(output)) != "" {
		return planFileTrackingTracked, nil
	}
	return planFileTrackingUntracked, nil
}

// capturePlanFileGuardはworker task呼出直前のplan file状態をbaselineとして固定する。
// 親Codexがcall前に更新したworking tree内容をそのまま基準にし、wrapperは復元・編集を行わない。
// Git indexで追跡中のplanがworking treeへ欠損している場合は未追跡repoの初期欠損と区別して
// model呼出前にfail closedする。plan読込失敗・repo内での追跡判定不能も不変性の基準自体が
// 確認できないため同じく呼出前にfail closedする。reviewer呼出とprobeは既存read-only
// invariant(review-start/end snapshot)の対象のため外す。
func (w *Workflow) capturePlanFileGuard(role state.SessionRole) (planFileState, bool, error) {
	if role != state.WorkerRole {
		return planFileState{}, false, nil
	}
	before, err := readPlanFileState(w.config.RepoRoot)
	if err != nil {
		return planFileState{}, true, w.failClosedPlanFileGuard("plan-file-capture", "plan_file_unavailable", "plan file baseline取得失敗のため不変性を確認できません", err)
	}
	if !before.exists {
		tracking, trackErr := classifyPlanFileTracking(w.config.RepoRoot)
		switch {
		case trackErr != nil:
			return planFileState{}, true, w.failClosedPlanFileGuard("plan-file-capture", "plan_file_unavailable", "plan fileのGit追跡判定に失敗したため欠損を安全に扱えません", trackErr)
		case tracking == planFileTrackingTracked:
			return planFileState{}, true, w.failClosedPlanFileGuard("plan-file-capture", "plan_file_missing", "Git indexで追跡されている"+implementationPlanFile+"がworking treeへ存在しません", nil)
		}
	}
	return before, false, nil
}

// verifyPlanFileAfterCallはworker task呼出直後にbaselineへ再照合する。GLM workerによる
// plan変更・生成・削除をreviewer開始前にfail closed検出し、resume前提の停止状態へ保存しない。
func (w *Workflow) verifyPlanFileAfterCall(
	checkpoint state.ResumeCheckpoint,
	before planFileState,
	runResult runner.RunResult,
	startedAt time.Time,
	completedAt time.Time,
	runErr error,
	outputPath string,
) (bool, error) {
	if checkpoint.Role != state.WorkerRole {
		return false, nil
	}
	after, err := readPlanFileState(w.config.RepoRoot)
	if err != nil {
		return true, w.failClosedPlanFileGuard(checkpoint.Phase, "plan_file_unavailable", "plan file終了状態取得失敗のため不変性を確認できません", err)
	}
	if after == before {
		return false, nil
	}
	reason := planFileChangeReason(before, after)
	violation := fmt.Errorf("worker呼出開始前に対し%s", reason)
	if runErr != nil {
		violation = fmt.Errorf("%v; 呼出error: %w", violation, runErr)
	}
	w.recordModelCall(checkpoint, runResult, startedAt, completedAt, "plan_file_violation", "", violation, outputPath, callDiagnostics{})
	return true, w.failClosedPlanFileGuard(checkpoint.Phase, "plan_file_mismatch", violation.Error(), nil)
}

// failClosedPlanFileGuardはplan file不変性確認失敗時の停止semantics。resume checkpointを
// 消してWaitingSolReviewへ移行し、Sol確認packetを出力する。GLM変更内容はbaselineへ
// 巻き戾さず現物のままSolへ引き渡す。
func (w *Workflow) failClosedPlanFileGuard(phase string, outcome string, reason string, cause error) error {
	w.recordPlanFileEvent(phase, outcome, reason, cause)
	if err := w.state.ClearResumeCheckpoint(); err != nil {
		return err
	}
	if err := w.state.SetTaskStatus(state.TaskStatusWaitingSolReview); err != nil {
		return err
	}
	if cause != nil {
		reason = fmt.Sprintf("%s: %v", reason, cause)
	}
	if err := w.emitPacket(planFileFailClosedPacket(phase, reason)); err != nil {
		return err
	}
	return errPlanFileGuardStopped
}

// recordPlanFileEventはplan file不変性確認失敗をtelemetryへ記録する。token消費は持たない
// (best-effort)。task呼出自身の記録はverifyPlanFileAfterCallがplan_file_violation outcomeで
// 残すため、二重計上しない。
func (w *Workflow) recordPlanFileEvent(phase string, outcome string, reason string, cause error) {
	now := w.now().UTC()
	errorText := reason
	if cause != nil {
		errorText = fmt.Sprintf("%s: %v", reason, cause)
	}
	w.state.RecordModelCallLog(state.ModelCallLog{
		TaskID:      w.state.ReadOr("task.id", "unknown"),
		CallType:    state.CallTypeEvent,
		StartedAt:   now,
		CompletedAt: now,
		Phase:       phase + "-plan-file-check",
		Role:        state.WorkerRole,
		Outcome:     outcome,
		Error:       boundedText(errorText, packet.MaxDiagnosticBytes),
	})
}

func planFileFailClosedPacket(phase string, reason string) packet.Packet {
	return packet.FromLines([]string{
		"STATUS: NEEDS_SOL_REVIEW",
		"RISK: HIGH",
		fmt.Sprintf("SUMMARY: worker呼出(%s)開始前後でIMPLEMENTATION_PLAN.local.mdの不変を確認できず、reviewerを呼ばずSol確認へ昇格", phase),
		"REQUIREMENT_COVERAGE: plan読み取り専用契約を機械強制できなかったため親Codexが直接確認する必要あり",
		"INVARIANTS: IMPLEMENTATION_PLAN.local.mdはtracked canonical sourceであり本文・checkbox・優先順・現在状態を更新できるのは親Codexだけ。GLM worker/reviewerは読み取り専用で、更新候補と根拠をPACKETへ報告する",
		"TEST_EVIDENCE: worker呼出開始前後のplan file存在・内容比較で欠損・不一致または読込失敗を検出",
		fmt.Sprintf("ISSUES: %s", reason),
		"RESIDUAL_RISK: plan fileの現在状態(変更・生成・削除・欠損)はorchestratorが復元せずそのまま残っている",
		"TARGETS: IMPLEMENTATION_PLAN.local.mdの現在内容とgit index/working tree状態",
		"ARTIFACTS: none",
		"SOL_QUESTION: 変更されたplan内容の取扱い(親Codexによる再編集・復元)をSolが判断する",
	})
}
