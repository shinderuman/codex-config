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
// implementationHistoryFileは完了証跡とescaped bug/review原因分析を分離した親Codex専有の
// tracked archiveであり、通常の作業開始・再開時には全文を読まず必要な見出しだけを検索して読む。
// 両fileとも本文を更新できるのは親Codexだけであり、GLM worker/reviewerは読み取り専用で、
// 欠損時も生成しない。wrapperはworker呼出前後の内容不変を機械強制する。
// glm-workerはplanを置かない他repositoryでも使うため、欠損の意味はGit indexの追跡有無で
// 区別する。追跡中fileのworking tree欠損は親Codexが置いた正が失われた状態のため呼出前に
// fail closedする。未追跡欠損の通常作業を許可するのはGit管理外directoryと確認できた場合と
// Git repository内で未追跡と正常判定できた場合だけで、repo内で判定不能なGit異常は
// baseline取得不能として同じく呼出前にfail closedする。history契約の強制はplanが置かれた
// repositoryだけとし、planの無い旧repositoryおよびhistory未作成状態の通常作業を許可する。
const (
	implementationPlanFile    = state.ParentPlanFile
	implementationHistoryFile = state.ParentHistoryFile
)

// errParentFileGuardStoppedは親Codex専有file不変性確認によるfail closed停止が完了したことを
// 呼出元へ伝えるsentinel。packet出力・checkpoint清除・task status更新は既に終わっているため、
// 呼出元は追加のerror出力をしない。
var errParentFileGuardStopped = errors.New("parent-owned file guard stopped workflow")

// parentFileStateは親Codex専有fileの存在と内容hash。欠損はexists=falseで表現し、呼出中の
// 新規作成も不変性違反として検出する。
type parentFileState struct {
	exists bool
	sha256 string
}

// parentFileGuardはworker task呼出直前に固定した親Codex専有file群のbaseline。
// historyGuardedはplan baselineが存在しhistory契約がこの呼出で有効かを表す。
type parentFileGuard struct {
	plan           parentFileState
	history        parentFileState
	historyGuarded bool
}

// guardSurfaceは親Codex専有file1件のguard設定。event logのphase suffix・telemetry outcome
// 接頭辞・fail closed packetの契約文をfileごとに切り替える。
type guardSurface struct {
	file          string
	label         string
	eventSuffix   string
	outcomePrefix string
	invariants    string
	targets       string
}

var planGuardSurface = guardSurface{
	file:          implementationPlanFile,
	label:         "plan file",
	eventSuffix:   "plan-file-check",
	outcomePrefix: "plan_file",
	invariants:    "IMPLEMENTATION_PLAN.local.mdはtracked canonical sourceであり本文・checkbox・優先順・現在状態を更新できるのは親Codexだけ。GLM worker/reviewerは読み取り専用で、更新候補と根拠をPACKETへ報告する",
	targets:       "IMPLEMENTATION_PLAN.local.mdの現在内容とgit index/working tree状態",
}

var historyGuardSurface = guardSurface{
	file:          implementationHistoryFile,
	label:         "history file",
	eventSuffix:   "history-file-check",
	outcomePrefix: "history_file",
	invariants:    "IMPLEMENTATION_HISTORY.mdは完了証跡とescaped bug/review原因分析を置く親Codex専有のtracked archiveであり、編集・生成・削除できるのは親Codexだけ。GLM worker/reviewerは通常の作業開始・再開時に全文を読まず必要な見出しだけ検索して読む",
	targets:       "IMPLEMENTATION_HISTORY.mdの現在内容とgit index/working tree状態",
}

func (s guardSurface) unavailableOutcome() string { return s.outcomePrefix + "_unavailable" }
func (s guardSurface) missingOutcome() string     { return s.outcomePrefix + "_missing" }
func (s guardSurface) mismatchOutcome() string    { return s.outcomePrefix + "_mismatch" }
func (s guardSurface) violationOutcome() string   { return s.outcomePrefix + "_violation" }

func readParentFileState(repoRoot string, name string) (parentFileState, error) {
	b, err := os.ReadFile(filepath.Join(repoRoot, name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return parentFileState{}, nil
		}
		return parentFileState{}, fmt.Errorf("read %s: %w", name, err)
	}
	sum := sha256.Sum256(b)
	return parentFileState{exists: true, sha256: hex.EncodeToString(sum[:])}, nil
}

// readParentFileStatesは親管理2fileの現在状態を読む。review-start基準の記録とreview resumeの
// 承認判定が同じ観測を共有する。
func readParentFileStates(repoRoot string) (state.ParentFileStates, error) {
	plan, err := readParentFileState(repoRoot, implementationPlanFile)
	if err != nil {
		return state.ParentFileStates{}, err
	}
	history, err := readParentFileState(repoRoot, implementationHistoryFile)
	if err != nil {
		return state.ParentFileStates{}, err
	}
	return state.ParentFileStates{Plan: parentFileStateValue(plan), History: parentFileStateValue(history)}, nil
}

func parentFileStateValue(s parentFileState) state.ParentFileState {
	return state.ParentFileState{Exists: s.exists, SHA256: s.sha256}
}

// captureStopParentFilesはrate-limit/provider-unavailable停止を保存する直前の親管理2file状態を
// checkpoint記録値へ変換する。読込失敗時はnilを返し、resume時の承認識別をfail closed側へ倒す。
func captureStopParentFiles(repoRoot string) *state.ParentFileStates {
	states, err := readParentFileStates(repoRoot)
	if err != nil {
		return nil
	}
	return &states
}

func parentFileChangeReason(before, after parentFileState) string {
	switch {
	case before.exists && after.exists:
		return "内容が変化しました"
	case !before.exists && after.exists:
		return "存在しない状態から新規作成されました"
	default:
		return "削除されました"
	}
}

// quietWhenParentFileGuardStoppedは親Codex専有file guardのfail closed終端が既にpacket出力・
// 状態遷移を完了している場合、追加のerror出力をせず正常終了として扱う。
func quietWhenParentFileGuardStopped(err error) error {
	if errors.Is(err, errParentFileGuardStopped) {
		return nil
	}
	return err
}

// parentFileTrackingは親Codex専有fileのGit追跡判定の確定結果。判定errorは値で表現せずerrorへ
// 分離し、未追跡へ畳まない。
type parentFileTracking int

const (
	parentFileTrackingTracked parentFileTracking = iota + 1
	parentFileTrackingUntracked
	parentFileTrackingOutsideGit
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

// classifyParentFileTrackingは対象fileの追跡状態をrepository/index現物から判定する。
// 追跡判定を特定repository pathの前提へhardcodeせず対象repositoryへ問い合わせる。
// Git管理外directoryは未追跡欠損の通常作業を許可できる唯一の無条件許可枠であり、
// Git repository内ではls-filesの失敗を判定不能errorとして呼出元へ返す。
func classifyParentFileTracking(repoRoot string, name string) (parentFileTracking, error) {
	insideGit, err := gitWorktreePresent(repoRoot)
	if err != nil {
		return 0, err
	}
	if !insideGit {
		return parentFileTrackingOutsideGit, nil
	}
	output, err := exec.Command("git", "-C", repoRoot, "ls-files", "--", name).Output()
	if err != nil {
		return 0, fmt.Errorf("git ls-files: %w", err)
	}
	if strings.TrimSpace(string(output)) != "" {
		return parentFileTrackingTracked, nil
	}
	return parentFileTrackingUntracked, nil
}

// captureParentFileGuardはworker task呼出直前の親Codex専有file状態をbaselineとして固定する。
// 親Codexがcall前に更新したworking tree内容をそのまま基準にし、wrapperは復元・編集を行わない。
// Git indexで追跡中のfileがworking treeへ欠損している場合は未追跡repoの初期欠損と区別して
// model呼出前にfail closedする。読込失敗・repo内での追跡判定不能も不変性の基準自体が
// 確認できないため同じく呼出前にfail closedする。historyはplanが存在するrepositoryだけで
// guard対象とし、planの無い旧repositoryでは契約自体を適用しない。reviewer呼出とprobeは
// 既存read-only invariant(review-start/end snapshot)の対象のため外す。
func (w *Workflow) captureParentFileGuard(role state.SessionRole) (parentFileGuard, bool, error) {
	if role != state.WorkerRole {
		return parentFileGuard{}, false, nil
	}
	plan, err := readParentFileState(w.config.RepoRoot, implementationPlanFile)
	if err != nil {
		return parentFileGuard{}, true, w.failClosedParentFileGuard("plan-file-capture", planGuardSurface, planGuardSurface.unavailableOutcome(), "plan file baseline取得失敗のため不変性を確認できません", err)
	}
	if !plan.exists {
		tracking, trackErr := classifyParentFileTracking(w.config.RepoRoot, implementationPlanFile)
		switch {
		case trackErr != nil:
			return parentFileGuard{}, true, w.failClosedParentFileGuard("plan-file-capture", planGuardSurface, planGuardSurface.unavailableOutcome(), "plan fileのGit追跡判定に失敗したため欠損を安全に扱えません", trackErr)
		case tracking == parentFileTrackingTracked:
			return parentFileGuard{}, true, w.failClosedParentFileGuard("plan-file-capture", planGuardSurface, planGuardSurface.missingOutcome(), "Git indexで追跡されている"+implementationPlanFile+"がworking treeへ存在しません", nil)
		}
		return parentFileGuard{plan: plan}, false, nil
	}
	history, err := readParentFileState(w.config.RepoRoot, implementationHistoryFile)
	if err != nil {
		return parentFileGuard{plan: plan}, true, w.failClosedParentFileGuard("plan-file-capture", historyGuardSurface, historyGuardSurface.unavailableOutcome(), "history file baseline取得失敗のため不変性を確認できません", err)
	}
	if !history.exists {
		tracking, trackErr := classifyParentFileTracking(w.config.RepoRoot, implementationHistoryFile)
		switch {
		case trackErr != nil:
			return parentFileGuard{plan: plan}, true, w.failClosedParentFileGuard("plan-file-capture", historyGuardSurface, historyGuardSurface.unavailableOutcome(), "history fileのGit追跡判定に失敗したため欠損を安全に扱えません", trackErr)
		case tracking == parentFileTrackingTracked:
			return parentFileGuard{plan: plan}, true, w.failClosedParentFileGuard("plan-file-capture", historyGuardSurface, historyGuardSurface.missingOutcome(), "Git indexで追跡されている"+implementationHistoryFile+"がworking treeへ存在しません", nil)
		}
	}
	return parentFileGuard{plan: plan, history: history, historyGuarded: true}, false, nil
}

// verifyParentFileAfterCallはworker task呼出直後にbaselineへ再照合する。GLM workerによる
// 変更・生成・削除をreviewer開始前にfail closed検出し、resume前提の停止状態へ保存しない。
func (w *Workflow) verifyParentFileAfterCall(
	checkpoint state.ResumeCheckpoint,
	before parentFileGuard,
	runResult runner.RunResult,
	startedAt time.Time,
	completedAt time.Time,
	runErr error,
	outputPath string,
) (bool, error) {
	if checkpoint.Role != state.WorkerRole {
		return false, nil
	}
	if stopped, err := w.verifyGuardedFileAfterCall(checkpoint, planGuardSurface, before.plan, runResult, startedAt, completedAt, runErr, outputPath); stopped {
		return true, err
	}
	if !before.historyGuarded {
		return false, nil
	}
	return w.verifyGuardedFileAfterCall(checkpoint, historyGuardSurface, before.history, runResult, startedAt, completedAt, runErr, outputPath)
}

// verifyGuardedFileAfterCallは対象file1件の終了状態をbaselineへ再照合する。runnerを実際に
// 呼んだtask呼出は終了状態読込失敗を含む全terminal pathでraw telemetryへexactly once記録する。
// この記録を飛ばすとstatsのTask Work Call計上だけが残り加法整合が崩れるため、読込失敗経路でも
// recordModelCallを必ず1回実行してからfail closedへ遷移する。
func (w *Workflow) verifyGuardedFileAfterCall(
	checkpoint state.ResumeCheckpoint,
	surface guardSurface,
	before parentFileState,
	runResult runner.RunResult,
	startedAt time.Time,
	completedAt time.Time,
	runErr error,
	outputPath string,
) (bool, error) {
	after, err := readParentFileState(w.config.RepoRoot, surface.file)
	if err != nil {
		w.recordModelCall(checkpoint, runResult, startedAt, completedAt, surface.unavailableOutcome(), "", err, outputPath, callDiagnostics{})
		return true, w.failClosedParentFileGuard(checkpoint.Phase, surface, surface.unavailableOutcome(), surface.label+"終了状態取得失敗のため不変性を確認できません", err)
	}
	if after == before {
		return false, nil
	}
	reason := parentFileChangeReason(before, after)
	violation := fmt.Errorf("worker呼出開始前に対し%s", reason)
	if runErr != nil {
		violation = fmt.Errorf("%v; 呼出error: %w", violation, runErr)
	}
	w.recordModelCall(checkpoint, runResult, startedAt, completedAt, surface.violationOutcome(), "", violation, outputPath, callDiagnostics{})
	return true, w.failClosedParentFileGuard(checkpoint.Phase, surface, surface.mismatchOutcome(), violation.Error(), nil)
}

// failClosedParentFileGuardは親Codex専有file不変性確認失敗時の停止semantics。resume checkpointを
// 消してWaitingSolReviewへ移行し、Sol確認packetを出力する。GLM変更内容はbaselineへ
// 巻き戻さず現物のままSolへ引き渡す。
func (w *Workflow) failClosedParentFileGuard(phase string, surface guardSurface, outcome string, reason string, cause error) error {
	w.recordParentFileEvent(phase, surface, outcome, reason, cause)
	if err := w.state.ClearResumeCheckpoint(); err != nil {
		return err
	}
	if err := w.state.SetTaskStatus(state.TaskStatusWaitingSolReview); err != nil {
		return err
	}
	if cause != nil {
		reason = fmt.Sprintf("%s: %v", reason, cause)
	}
	if err := w.emitResult(parentFileFailClosedResult(phase, surface, reason)); err != nil {
		return err
	}
	return errParentFileGuardStopped
}

// recordParentFileEventは親Codex専有file不変性確認失敗をtelemetryへ記録する。token消費は持たない
// (best-effort)。task呼出自身の記録はverifyGuardedFileAfterCallがviolation/unavailable outcomeで
// 残すため、二重計上しない。
func (w *Workflow) recordParentFileEvent(phase string, surface guardSurface, outcome string, reason string, cause error) {
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
		Phase:       phase + "-" + surface.eventSuffix,
		Role:        state.WorkerRole,
		Outcome:     outcome,
		Error:       boundedText(errorText, packet.MaxDiagnosticBytes),
	})
}

func parentFileFailClosedResult(phase string, surface guardSurface, reason string) packet.Result {
	return packet.Result{
		Status:              packet.StatusNeedsSolReview,
		Risk:                packet.RiskHigh,
		Summary:             fmt.Sprintf("worker呼出(%s)開始前後で%sの不変を確認できず、reviewerを呼ばずSol確認へ昇格", phase, surface.file),
		RequirementCoverage: fmt.Sprintf("%s読み取り専用契約を機械強制できなかったため親Codexが直接確認する必要あり", surface.file),
		Invariants:          surface.invariants,
		TestEvidence:        fmt.Sprintf("worker呼出開始前後の%s存在・内容比較で欠損・不一致または読込失敗を検出", surface.file),
		Issues:              reason,
		ResidualRisk:        fmt.Sprintf("%sの現在状態(変更・生成・削除・欠損)はorchestratorが復元せずそのまま残っている", surface.file),
		Targets:             []string{surface.targets},
		SolQuestion:         fmt.Sprintf("変更された%s内容の取扱い(親Codexによる再編集・復元)をSolが判断する", surface.file),
	}
}
