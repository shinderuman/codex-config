package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/packet"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/runner"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// implementationPlanFileはrepository rootへ置くtracked canonical sourceの実施計画file。
// implementationRulesFileはtask lifecycle規則、implementationHistoryFileは完了証跡とescaped
// bug/review原因分析のtracked archiveであり、implementationTasksDir配下は未完了taskのrequirement
// contractを置く。これらは「parent-managed implementation metadata」という単一集合であり、本文を
// 更新できるのは親Codexだけで、GLM worker/reviewerは読み取り専用で、欠損時も生成しない。
// wrapperはworker呼出前後の集合内容不変を機械強制する。
// glm-workerはplanを置かない他repositoryでも使うため、集合契約の有効無効はplanの存在で区別する。
// 追跡中fileのworking tree欠損は親Codexが置いた正が失われた状態のため呼出前にfail closedする。
// 未追跡欠損の通常作業を許可するのはGit管理外directoryと確認できた場合と、Git repository内で
// 未追跡と正常判定できた場合だけで、repo内で判定不能なGit異常はbaseline取得不能として同じく
// 呼出前にfail closedする。
const (
	implementationPlanFile    = state.ParentPlanFile
	implementationRulesFile   = state.ParentRulesFile
	implementationTasksDir    = state.ParentTasksDir
	implementationHistoryFile = state.ParentHistoryFile
)

// errParentFileGuardStoppedは親管理metadata不変性確認によるfail closed停止が完了したことを
// 呼出元へ伝えるsentinel。packet出力・checkpoint清除・task status更新は既に終わっているため、
// 呼出元は追加のerror出力をしない。
var errParentFileGuardStopped = errors.New("parent-owned file guard stopped workflow")

// parentFileGuardはworker task呼出直前に固定した親管理metadata集合のbaseline。
// guardedはplanが存在し集合契約がこの呼出で有効かを表す。planOnlyはplanが存在しない
// repositoryで集合契約ではなくplan新規生成検出だけを強制する縮小modeを表す。
type parentFileGuard struct {
	files    state.ParentFileStates
	guarded  bool
	planOnly bool
}

// guardSurfaceは親管理metadata集合guardの設定。event logのphase suffix・telemetry outcome
// 接頭辞・fail closed packetの契約文へ使う。集合を1単位として扱うためfile単位の分岐を持たない。
type guardSurface struct {
	label         string
	files         string
	eventSuffix   string
	outcomePrefix string
	invariants    string
	targets       string
}

var parentMetadataGuardSurface = guardSurface{
	label:         "親管理implementation metadata",
	files:         "IMPLEMENTATION_RULES.md・IMPLEMENTATION_PLAN.local.md・IMPLEMENTATION_TASKS/配下全file・IMPLEMENTATION_HISTORY.md",
	eventSuffix:   "parent-metadata-check",
	outcomePrefix: "parent_metadata",
	invariants:    "IMPLEMENTATION_RULES.md・IMPLEMENTATION_PLAN.local.md・IMPLEMENTATION_TASKS/配下・IMPLEMENTATION_HISTORY.mdは親Codexだけが編集するparent-managed implementation metadataの単一集合であり、GLM worker/reviewerは編集・生成・復元・削除を行わず更新候補を結果fieldで報告する。model呼出中は集合全体が不変である",
	targets:       "IMPLEMENTATION_RULES.md・IMPLEMENTATION_PLAN.local.md・IMPLEMENTATION_TASKS/配下全file・IMPLEMENTATION_HISTORY.mdの現在内容とgit index/working tree状態、およびPlan ACTIVE欄から解決したACTIVE task file",
}

func (s guardSurface) unavailableOutcome() string { return s.outcomePrefix + "_unavailable" }
func (s guardSurface) missingOutcome() string     { return s.outcomePrefix + "_missing" }
func (s guardSurface) mismatchOutcome() string    { return s.outcomePrefix + "_mismatch" }
func (s guardSurface) violationOutcome() string   { return s.outcomePrefix + "_violation" }
func (s guardSurface) activeUnresolvableOutcome() string {
	return s.outcomePrefix + "_active_unresolvable"
}

func readParentFileState(repoRoot string, name string) (state.ParentFileState, error) {
	b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(name)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state.ParentFileState{Path: name}, nil
		}
		return state.ParentFileState{}, fmt.Errorf("read %s: %w", name, err)
	}
	sum := sha256.Sum256(b)
	return state.ParentFileState{Path: name, Exists: true, SHA256: hex.EncodeToString(sum[:])}, nil
}

// readParentFileStatesは親管理metadata集合の現在状態をroot 3fileとIMPLEMENTATION_TASKS/配下
// 全fileから読む。review-start基準の記録とreview resumeの承認判定が同じ観測を共有する。
func readParentFileStates(repoRoot string) (state.ParentFileStates, error) {
	rootFiles := []string{implementationRulesFile, implementationPlanFile, implementationHistoryFile}
	states := make(state.ParentFileStates, 0, len(rootFiles)+8)
	for _, name := range rootFiles {
		s, err := readParentFileState(repoRoot, name)
		if err != nil {
			return nil, err
		}
		states = append(states, s)
	}
	taskStates, err := readParentTaskFileStates(repoRoot)
	if err != nil {
		return nil, err
	}
	states = append(states, taskStates...)
	sort.Slice(states, func(i, j int) bool { return states[i].Path < states[j].Path })
	return states, nil
}

// readParentTaskFileStatesはIMPLEMENTATION_TASKS/配下の全file状態を列挙する。directoryごと
// 無いrepositoryでは空集合を返す。親が停止中へ追加した新規task fileも集合要素として扱うため、
// file名のfilterは行わず配下の全fileを親管理として数える。
func readParentTaskFileStates(repoRoot string) (state.ParentFileStates, error) {
	dir := filepath.Join(repoRoot, implementationTasksDir)
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", implementationTasksDir, err)
	}
	var states state.ParentFileStates
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		s, readErr := readParentFileState(repoRoot, filepath.ToSlash(rel))
		if readErr != nil {
			return readErr
		}
		states = append(states, s)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("enumerate %s: %w", implementationTasksDir, err)
	}
	return states, nil
}

// captureStopParentFilesはrate-limit/provider-unavailable停止を保存する直前の親管理metadata
// 集合状態をcheckpoint記録値へ変換する。読込失敗時はnilを返し、resume時の承認識別をfail closed側へ倒す。
func captureStopParentFiles(repoRoot string) *state.ParentFileStates {
	states, err := readParentFileStates(repoRoot)
	if err != nil {
		return nil
	}
	return &states
}

// parentFileChangeReasonは対象file 1件の前後差分を日本語で分類する。
func parentFileChangeReason(before, after state.ParentFileState) string {
	switch {
	case before.Exists && after.Exists:
		return "内容が変化しました"
	case !before.Exists && after.Exists:
		return "存在しない状態から新規作成されました"
	default:
		return "削除されました"
	}
}

// describeParentFileChangesは集合の前後差分をpathごとの変化理由へ展開する。
func describeParentFileChanges(before, after state.ParentFileStates) string {
	paths := make(map[string]struct{}, len(before)+len(after))
	for _, s := range before {
		paths[s.Path] = struct{}{}
	}
	for _, s := range after {
		paths[s.Path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)
	reasons := make([]string, 0, len(ordered))
	for _, p := range ordered {
		b := state.FindParentFileState(before, p)
		a := state.FindParentFileState(after, p)
		if b == a {
			continue
		}
		reasons = append(reasons, fmt.Sprintf("%s(%s)", p, parentFileChangeReason(b, a)))
	}
	return strings.Join(reasons, ", ")
}

// quietWhenParentFileGuardStoppedは親管理metadata guardのfail closed終端が既にpacket出力・
// 状態遷移を完了している場合、追加のerror出力をせず正常終了として扱う。
func quietWhenParentFileGuardStopped(err error) error {
	if errors.Is(err, errParentFileGuardStopped) {
		return nil
	}
	return err
}

// parentFileTrackingは親管理metadata fileのGit追跡判定の確定結果。判定errorは値で表現せず
// errorへ分離し、未追跡へ畳まない。
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

// missingTrackedTaskFilesはGit indexがIMPLEMENTATION_TASKS/配下へ追跡するfileのうちworking
// treeへ存在しないものを返す。directory列挙はworktree現物だけを見るため、追跡中fileの削除は
// この比較でのみ検出できる。Git管理外directoryでは追跡概念自体が無いため空を返す。
func missingTrackedTaskFiles(repoRoot string, worktreeStates state.ParentFileStates) ([]string, error) {
	insideGit, err := gitWorktreePresent(repoRoot)
	if err != nil {
		return nil, err
	}
	if !insideGit {
		return nil, nil
	}
	output, err := exec.Command("git", "-C", repoRoot, "ls-files", "-z", "--", implementationTasksDir).Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	present := make(map[string]bool, len(worktreeStates))
	for _, s := range worktreeStates {
		if s.Exists {
			present[s.Path] = true
		}
	}
	var missing []string
	for _, p := range strings.Split(string(output), "\x00") {
		if p == "" || present[p] {
			continue
		}
		missing = append(missing, p)
	}
	return missing, nil
}

// captureParentFileGuardはworker task呼出直前の親管理metadata集合状態をbaselineとして固定する。
// 親Codexがcall前に更新したworking tree内容をそのまま基準にし、wrapperは復元・編集を行わない。
// planが存在しないrepositoryでは契約自体を適用せず通常作業を許可する。planが存在する場合は
// root 3fileの追跡中欠損、IMPLEMENTATION_TASKS/配下の追跡中欠損、task開始時に固定したACTIVE
// task fileの消失を呼出前にfail closedする。読込失敗・repo内での追跡判定不能も不変性の基準自体が
// 確認できないため同じく呼出前にfail closedする。reviewer呼出とprobeは既存read-only invariant
// (review-start/end snapshot)の対象のため外す。
func (w *Workflow) captureParentFileGuard(role state.SessionRole) (parentFileGuard, bool, error) {
	if role != state.WorkerRole {
		return parentFileGuard{}, false, nil
	}
	plan, err := readParentFileState(w.config.RepoRoot, implementationPlanFile)
	if err != nil {
		return parentFileGuard{}, true, w.failClosedParentFileGuard("parent-metadata-capture", parentMetadataGuardSurface, parentMetadataGuardSurface.unavailableOutcome(), "plan file baseline取得失敗のため不変性を確認できません", err)
	}
	if !plan.Exists {
		tracking, trackErr := classifyParentFileTracking(w.config.RepoRoot, implementationPlanFile)
		switch {
		case trackErr != nil:
			return parentFileGuard{}, true, w.failClosedParentFileGuard("parent-metadata-capture", parentMetadataGuardSurface, parentMetadataGuardSurface.unavailableOutcome(), "plan fileのGit追跡判定に失敗したため欠損を安全に扱えません", trackErr)
		case tracking == parentFileTrackingTracked:
			return parentFileGuard{}, true, w.failClosedParentFileGuard("parent-metadata-capture", parentMetadataGuardSurface, parentMetadataGuardSurface.missingOutcome(), "Git indexで追跡されている"+implementationPlanFile+"がworking treeへ存在しません", nil)
		}
		// planの無い旧repositoryでは集合契約(RULES/TASKS/HISTORY読み取り専用)を適用しないが、
		// plan自身の新規生成検出だけは継続する。親Codexがplanを置いていないrepoへGLMがplanを
		// 生成すると以降の呼出が存在しない親契約の下へ置かれるため、呼出後検出でfail closedする。
		return parentFileGuard{files: state.ParentFileStates{{Path: implementationPlanFile}}, guarded: true, planOnly: true}, false, nil
	}
	states, err := readParentFileStates(w.config.RepoRoot)
	if err != nil {
		return parentFileGuard{}, true, w.failClosedParentFileGuard("parent-metadata-capture", parentMetadataGuardSurface, parentMetadataGuardSurface.unavailableOutcome(), "親管理metadata baseline取得失敗のため不変性を確認できません", err)
	}
	for _, name := range []string{implementationRulesFile, implementationHistoryFile} {
		if state.FindParentFileState(states, name).Exists {
			continue
		}
		tracking, trackErr := classifyParentFileTracking(w.config.RepoRoot, name)
		switch {
		case trackErr != nil:
			return parentFileGuard{}, true, w.failClosedParentFileGuard("parent-metadata-capture", parentMetadataGuardSurface, parentMetadataGuardSurface.unavailableOutcome(), name+"のGit追跡判定に失敗したため欠損を安全に扱えません", trackErr)
		case tracking == parentFileTrackingTracked:
			return parentFileGuard{}, true, w.failClosedParentFileGuard("parent-metadata-capture", parentMetadataGuardSurface, parentMetadataGuardSurface.missingOutcome(), "Git indexで追跡されている"+name+"がworking treeへ存在しません", nil)
		}
	}
	missing, err := missingTrackedTaskFiles(w.config.RepoRoot, states)
	if err != nil {
		return parentFileGuard{}, true, w.failClosedParentFileGuard("parent-metadata-capture", parentMetadataGuardSurface, parentMetadataGuardSurface.unavailableOutcome(), "IMPLEMENTATION_TASKS配下のGit追跡判定に失敗したため欠損を安全に扱えません", err)
	}
	if len(missing) > 0 {
		return parentFileGuard{}, true, w.failClosedParentFileGuard("parent-metadata-capture", parentMetadataGuardSurface, parentMetadataGuardSurface.missingOutcome(), "Git indexで追跡されているIMPLEMENTATION_TASKS配下fileがworking treeへ存在しません: "+strings.Join(missing, ", "), nil)
	}
	if activePath := w.readActiveTaskState(); activePath != "" && !activeTaskFileExists(w.config.RepoRoot, activePath) {
		return parentFileGuard{}, true, w.failClosedParentFileGuard("parent-metadata-capture", parentMetadataGuardSurface, parentMetadataGuardSurface.missingOutcome(), "task開始時に固定したACTIVE task file "+activePath+"がworking treeへ存在しません", nil)
	}
	return parentFileGuard{files: states, guarded: true}, false, nil
}

// verifyParentFileAfterCallはworker task呼出直後にbaselineへ再照合する。GLM workerによる
// 集合への変更・生成・削除をreviewer開始前にfail closed検出し、resume前提の停止状態へ保存しない。
func (w *Workflow) verifyParentFileAfterCall(
	checkpoint state.ResumeCheckpoint,
	before parentFileGuard,
	runResult runner.RunResult,
	startedAt time.Time,
	completedAt time.Time,
	runErr error,
	outputPath string,
) (bool, error) {
	if checkpoint.Role != state.WorkerRole || !before.guarded {
		return false, nil
	}
	after, err := readParentFileStates(w.config.RepoRoot)
	if err != nil {
		w.recordModelCall(checkpoint, runResult, startedAt, completedAt, parentMetadataGuardSurface.unavailableOutcome(), "", err, outputPath, callDiagnostics{})
		return true, w.failClosedParentFileGuard(checkpoint.Phase, parentMetadataGuardSurface, parentMetadataGuardSurface.unavailableOutcome(), "親管理metadata終了状態取得失敗のため不変性を確認できません", err)
	}
	if before.planOnly {
		// plan縮小modeではplan自身の生成だけを検出する。他の集合構成fileは契約対象外のため
		// 比較しない。
		if plan := state.FindParentFileState(after, implementationPlanFile); plan != state.FindParentFileState(before.files, implementationPlanFile) {
			violation := fmt.Errorf("worker呼出開始前に対し親管理implementation metadataが変化しました: %s(%s)", implementationPlanFile, parentFileChangeReason(state.FindParentFileState(before.files, implementationPlanFile), plan))
			if runErr != nil {
				violation = fmt.Errorf("%v; 呼出error: %w", violation, runErr)
			}
			w.recordModelCall(checkpoint, runResult, startedAt, completedAt, parentMetadataGuardSurface.violationOutcome(), "", violation, outputPath, callDiagnostics{})
			return true, w.failClosedParentFileGuard(checkpoint.Phase, parentMetadataGuardSurface, parentMetadataGuardSurface.mismatchOutcome(), violation.Error(), nil)
		}
		return false, nil
	}
	if state.SameParentFileStates(before.files, after) {
		return false, nil
	}
	violation := fmt.Errorf("worker呼出開始前に対し親管理implementation metadataが変化しました: %s", describeParentFileChanges(before.files, after))
	if runErr != nil {
		violation = fmt.Errorf("%v; 呼出error: %w", violation, runErr)
	}
	w.recordModelCall(checkpoint, runResult, startedAt, completedAt, parentMetadataGuardSurface.violationOutcome(), "", violation, outputPath, callDiagnostics{})
	return true, w.failClosedParentFileGuard(checkpoint.Phase, parentMetadataGuardSurface, parentMetadataGuardSurface.mismatchOutcome(), violation.Error(), nil)
}

// failClosedParentFileGuardは親管理metadata不変性確認失敗時の停止semantics。resume checkpointを
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

// failClosedActiveTaskResolutionはACTIVE task file解決失敗時の同一停止semantics。要求正本を
// 特定できないままmodelを呼ばせることを防ぐため、packet出力まで同じ経路へ載せる。
func (w *Workflow) failClosedActiveTaskResolution(phase string, cause error) error {
	return w.failClosedParentFileGuard(phase, parentMetadataGuardSurface, parentMetadataGuardSurface.activeUnresolvableOutcome(), "PlanのACTIVE欄からACTIVE task fileを一意に解決できません", cause)
}

// recordParentFileEventは親管理metadata不変性確認失敗をtelemetryへ記録する。token消費は持たない
// (best-effort)。task呼出自身の記録はverifyParentFileAfterCallがviolation/unavailable outcomeで
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
		Summary:             fmt.Sprintf("worker呼出(%s)の前後で親管理implementation metadata(%s)の不変を確認できず、reviewerを呼ばずSol確認へ昇格", phase, surface.files),
		RequirementCoverage: "親管理metadata読み取り専用契約、またはACTIVE task file要求正本の特定を機械強制できなかったため親Codexが直接確認する必要あり",
		Invariants:          surface.invariants,
		TestEvidence:        "worker呼出開始前後の親管理metadata集合存在・内容比較、Plan ACTIVE欄解決で欠損・不一致・非一意・読込失敗を検出",
		Issues:              reason,
		ResidualRisk:        "親管理metadataの現在状態(変更・生成・削除・欠損)はorchestratorが復元せずそのまま残っている",
		Targets:             []string{surface.targets},
		SolQuestion:         "変更・欠損した親管理metadata内容とACTIVE task fileの取扱い(親Codexによる再編集・復元)をSolが判断する",
	}
}
