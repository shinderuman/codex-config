package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	workerEndSnapshotFile       = "snapshot-worker-end.json"
	reviewStartSnapshotFile     = "snapshot-review-start.json"
	reportOnlyStartSnapshotFile = "snapshot-report-only-start.json"
	snapshotComparisonFile      = "snapshot-comparison.json"
)

// 親管理implementation metadata集合。RULES・PLAN・HISTORYのrepository root 3fileと
// IMPLEMENTATION_TASKS/配下の全fileからなり、編集できるのは親Codexだけである。model呼出前後の
// 不変guardとreview resumeのsnapshot例外が同じ対象を指すためここへ一元化する。
const (
	ParentRulesFile   = "IMPLEMENTATION_RULES.md"
	ParentPlanFile    = "IMPLEMENTATION_PLAN.local.md"
	ParentTasksDir    = "IMPLEMENTATION_TASKS"
	ParentHistoryFile = "IMPLEMENTATION_HISTORY.md"
)

var parentManagedFiles = []string{ParentRulesFile, ParentPlanFile, ParentHistoryFile}

// ParentFileStateは親管理metadata 1件の存在と内容hash。欠損はExists=falseで表現する。
type ParentFileState struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	SHA256 string `json:"sha256"`
}

// ParentFileStatesは親管理metadata集合の状態snapshot。review開始時基準とrate-limit/
// provider-unavailable停止保存時点で同じ形式を使い、review resumeが停止期間中の親更新だけを
// 承認deltaとして識別する。path昇順で整列し、集合比較を要素順比較で扱えるようにする。
type ParentFileStates []ParentFileState

// SameParentFileStatesは2つの集合snapshotが同一かを判定する。
func SameParentFileStates(a, b ParentFileStates) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FindParentFileStateは指定pathの状態を返す。集合に無いpathはExists=falseの零値で表現し、
// 停止期間中の新規作成・削除を同じ比較式で扱えるようにする。
func FindParentFileState(states ParentFileStates, path string) ParentFileState {
	for _, s := range states {
		if s.Path == path {
			return s
		}
	}
	return ParentFileState{Path: path}
}

// GitSnapshotはworker終了時・review開始時のrepo状態を3軸のdigestで識別する。
type GitSnapshot struct {
	Head        string `json:"head"`
	IndexDigest string `json:"index_digest"`
	// WorktreeDigestはunstaged tracked変更とuntracked(ignored除外)の内容/pathを反映する。
	WorktreeDigest string `json:"worktree_digest"`
	// WorktreeDigestExcludingParentは親管理metadata集合をdiff/untracked列挙から除外した
	// worktree digest。review resumeが親metadataだけのdeltaを識別する基準に使い、旧binaryの
	// snapshot fileでは空文字のため例外判定できずfail closedになる。
	WorktreeDigestExcludingParent string `json:"worktree_digest_excluding_parent,omitempty"`
	// ParentFilesはsnapshot保存時点の親管理metadata集合状態。review-start snapshotだけが
	// 設定し、resume例外が呼出中変更と停止期間中変更をfile単位で区別する基準にする。
	ParentFiles *ParentFileStates `json:"parent_files,omitempty"`
}

type SnapshotStage string

const (
	SnapshotStageWorkerEnd       SnapshotStage = "worker-end"
	SnapshotStageReviewStart     SnapshotStage = "review-start"
	SnapshotStageReviewResume    SnapshotStage = "review-resume"
	SnapshotStageReviewEnd       SnapshotStage = "review-end"
	SnapshotStageReportOnlyStart SnapshotStage = "report-only-start"
	SnapshotStageReportOnlyEnd   SnapshotStage = "report-only-end"
)

// SnapshotComparisonはworker-endとreview-start snapshotの一致判定結果を記録する。
// 値そのものは各snapshot fileへ、判定結果はcomparison fileへ区別して永続化する。
type SnapshotComparison struct {
	Stage         SnapshotStage `json:"stage"`
	Matched       bool          `json:"matched"`
	HeadMatch     bool          `json:"head_match"`
	IndexMatch    bool          `json:"index_match"`
	WorktreeMatch bool          `json:"worktree_match"`
	// ParentUpdateAcceptedは3軸不一致が停止期間中の親管理metadata更新だけだったためreview基準を
	// 現状へ再固定して再開したことを表す。Matchedは元の3軸判定のまま残す。
	ParentUpdateAccepted bool   `json:"parent_update_accepted,omitempty"`
	Reason               string `json:"reason,omitempty"`
}

// CaptureGitSnapshotはrepoRootの状態を3軸のdigestへ読み出す。index・object・worktreeへは書き込まず、
// untracked通常fileの生内容とsymlink target文字列を読む。commitが無いrepoではHeadを空文字とし、
// index/worktree digestで状態を識別する。
func CaptureGitSnapshot(repoRoot string) (GitSnapshot, error) {
	head, err := captureSnapshotHead(repoRoot)
	if err != nil {
		return GitSnapshot{}, err
	}
	indexDigest, err := captureSnapshotIndexDigest(repoRoot)
	if err != nil {
		return GitSnapshot{}, err
	}
	worktreeDigest, excludingParent, err := captureSnapshotWorktreeDigest(repoRoot)
	if err != nil {
		return GitSnapshot{}, err
	}
	return GitSnapshot{
		Head:                          head,
		IndexDigest:                   indexDigest,
		WorktreeDigest:                worktreeDigest,
		WorktreeDigestExcludingParent: excludingParent,
	}, nil
}

func captureSnapshotHead(repoRoot string) (string, error) {
	output, err := exec.Command("git", "-C", repoRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", nil
		}
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// git ls-files -sは<path>毎に<mode> <sha> <stage>をpath順で出力するため、出力全体のsha256が
// index同一性を決定論的に表す。
func captureSnapshotIndexDigest(repoRoot string) (string, error) {
	output, err := exec.Command("git", "-C", repoRoot, "ls-files", "-s").Output()
	if err != nil {
		return "", fmt.Errorf("git ls-files: %w", err)
	}
	sum := sha256.Sum256(output)
	return hex.EncodeToString(sum[:]), nil
}

// git I/Oとdigest計算を分離し、列挙結果を直接与える特殊file・消失・境界越えのtestを決定論的に扱う。
// captureSnapshotWorktreeDigestは親管理metadata集合を含む全体と除外した値の両方のworktree digestを
// 返す。除外値はreview resumeが親metadataだけのdeltaを識別する基準になる。
func captureSnapshotWorktreeDigest(repoRoot string) (string, string, error) {
	full, err := captureWorktreeDigestVariant(repoRoot, nil)
	if err != nil {
		return "", "", err
	}
	excluding, err := captureWorktreeDigestVariant(repoRoot, parentFileExcludePathspecs())
	if err != nil {
		return "", "", err
	}
	return full, excluding, nil
}

// parentFileExcludePathspecsは親管理metadata集合だけをgit列挙から外すanchored exclude pathspec。
// :(top)でrepository root直下の同名列とIMPLEMENTATION_TASKS/配下だけを除外し、subdirectory配下の
// 同名列は検出対象に残す。IMPLEMENTATION_TASKSは directory pathspec として配下全fileへ一致する。
func parentFileExcludePathspecs() []string {
	specs := make([]string, 0, len(parentManagedFiles)+1)
	for _, name := range parentManagedFiles {
		specs = append(specs, ":(top,exclude)"+name)
	}
	specs = append(specs, ":(top,exclude)"+ParentTasksDir)
	return specs
}

func captureWorktreeDigestVariant(repoRoot string, excludePathspecs []string) (string, error) {
	diffArgs := []string{"diff", "--binary", "--no-ext-diff"}
	untrackedArgs := []string{"ls-files", "-z", "--others", "--exclude-standard"}
	if len(excludePathspecs) > 0 {
		diffArgs = append(diffArgs, "--")
		diffArgs = append(diffArgs, excludePathspecs...)
		untrackedArgs = append(untrackedArgs, "--")
		untrackedArgs = append(untrackedArgs, excludePathspecs...)
	}
	diffOutput, err := gitSnapshotOutput(repoRoot, diffArgs)
	if err != nil {
		return "", err
	}
	untrackedOutput, err := gitSnapshotOutput(repoRoot, untrackedArgs)
	if err != nil {
		return "", err
	}
	return buildWorktreeDigest(diffOutput, untrackedOutput, repoRoot)
}

func gitSnapshotOutput(repoRoot string, args []string) ([]byte, error) {
	output, err := exec.Command("git", append([]string{"-C", repoRoot}, args...)...).Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return output, nil
}

// 列挙後に消失したpathを空扱いすると別状態を同一視するため、消失も取得失敗とする。
func buildWorktreeDigest(diffOutput, untrackedOutput []byte, repoRoot string) (string, error) {
	hasher := sha256.New()
	hasher.Write([]byte("diff\n"))
	hasher.Write(diffOutput)
	hasher.Write([]byte("\nuntracked\n"))

	paths := strings.Split(strings.TrimRight(string(untrackedOutput), "\x00"), "\x00")
	sort.Strings(paths)
	for _, path := range paths {
		if path == "" {
			continue
		}
		absPath, err := joinWithinRoot(repoRoot, path)
		if err != nil {
			return "", fmt.Errorf("untracked %s: %w", path, err)
		}
		info, err := os.Lstat(absPath)
		if err != nil {
			return "", fmt.Errorf("untracked file %sをstatできません: %w", path, err)
		}
		hasher.Write([]byte(path))
		hasher.Write([]byte{0})
		if err := hashUntrackedEntry(hasher, absPath, info.Mode()); err != nil {
			return "", err
		}
		hasher.Write([]byte("\n"))
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// symlinkはtarget文字列だけをhashし、指す先がrepo外・巨大file・特殊fileでも内容を読まない。
// FIFO・device・socket等はhangや無制限読込を避けるため通常file/symlink以外は失敗にする。
func hashUntrackedEntry(hasher hash.Hash, absPath string, mode os.FileMode) error {
	switch {
	case mode.IsRegular():
		content, err := os.ReadFile(absPath)
		if err != nil {
			return fmt.Errorf("untracked file %sを読めません: %w", absPath, err)
		}
		sum := sha256.Sum256(content)
		hasher.Write([]byte("regular\x00"))
		hasher.Write([]byte(hex.EncodeToString(sum[:])))
	case mode&os.ModeSymlink != 0:
		target, err := os.Readlink(absPath)
		if err != nil {
			return fmt.Errorf("untracked symlink %sを読めません: %w", absPath, err)
		}
		sum := sha256.Sum256([]byte(target))
		hasher.Write([]byte("symlink\x00"))
		hasher.Write([]byte(hex.EncodeToString(sum[:])))
	default:
		return fmt.Errorf("untracked file %sは取り扱えないfile type %sです", absPath, mode.Type())
	}
	return nil
}

// root配下へpathを結合し、repo境界を越えるpath文字列を拒否する。symlink target解決ではなく文字列判定で、
// root自身・root外へ向かうrelを弾く。
func joinWithinRoot(root, rel string) (string, error) {
	abs := filepath.Join(root, rel)
	relToRoot, err := filepath.Rel(root, abs)
	if err != nil {
		return "", err
	}
	if relToRoot == "." || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("pathがrepository境界を越えています: %s", rel)
	}
	return abs, nil
}

func EqualGitSnapshot(a, b GitSnapshot) bool {
	return a.Head == b.Head && a.IndexDigest == b.IndexDigest && a.WorktreeDigest == b.WorktreeDigest
}

func CompareGitSnapshot(previous, current GitSnapshot, stage SnapshotStage, reason string) SnapshotComparison {
	return SnapshotComparison{
		Stage:         stage,
		Matched:       EqualGitSnapshot(previous, current),
		HeadMatch:     previous.Head == current.Head,
		IndexMatch:    previous.IndexDigest == current.IndexDigest,
		WorktreeMatch: previous.WorktreeDigest == current.WorktreeDigest,
		Reason:        reason,
	}
}

func writeSnapshot(path string, snap GitSnapshot) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("snapshotをJSON化できません: %w", err)
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}

func readSnapshot(path string) (GitSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return GitSnapshot{}, err
	}
	var snap GitSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return GitSnapshot{}, fmt.Errorf("snapshotを読めません: %w", err)
	}
	return snap, nil
}

func (s *StateStore) SaveWorkerEndSnapshot(snap GitSnapshot) error {
	if err := writeSnapshot(s.Path(workerEndSnapshotFile), snap); err != nil {
		return fmt.Errorf("worker-end snapshotを書き込めません: %w", err)
	}
	return nil
}

func (s *StateStore) LoadWorkerEndSnapshot() (GitSnapshot, error) {
	return readSnapshot(s.Path(workerEndSnapshotFile))
}

func (s *StateStore) SaveReviewStartSnapshot(snap GitSnapshot) error {
	if err := writeSnapshot(s.Path(reviewStartSnapshotFile), snap); err != nil {
		return fmt.Errorf("review-start snapshotを書き込めません: %w", err)
	}
	return nil
}

func (s *StateStore) LoadReviewStartSnapshot() (GitSnapshot, error) {
	return readSnapshot(s.Path(reviewStartSnapshotFile))
}

// SaveReportOnlyStartSnapshotはreport-only PACKET再出力workerの開始直前状態を保存する。
// 通常worker-end/review-start snapshotとは異なり、resumeを跨いでも再保存せず
// 同一基準として読み続ける。
func (s *StateStore) SaveReportOnlyStartSnapshot(snap GitSnapshot) error {
	if err := writeSnapshot(s.Path(reportOnlyStartSnapshotFile), snap); err != nil {
		return fmt.Errorf("report-only開始前snapshotを書き込めません: %w", err)
	}
	return nil
}

func (s *StateStore) LoadReportOnlyStartSnapshot() (GitSnapshot, error) {
	return readSnapshot(s.Path(reportOnlyStartSnapshotFile))
}

func (s *StateStore) SaveSnapshotComparison(comparison SnapshotComparison) error {
	data, err := json.MarshalIndent(comparison, "", "  ")
	if err != nil {
		return fmt.Errorf("snapshot comparisonをJSON化できません: %w", err)
	}
	return writeFileAtomic(s.Path(snapshotComparisonFile), append(data, '\n'), 0o600)
}

func (s *StateStore) LoadSnapshotComparison() (SnapshotComparison, error) {
	data, err := os.ReadFile(s.Path(snapshotComparisonFile))
	if err != nil {
		return SnapshotComparison{}, err
	}
	var comparison SnapshotComparison
	if err := json.Unmarshal(data, &comparison); err != nil {
		return SnapshotComparison{}, fmt.Errorf("snapshot comparisonを読めません: %w", err)
	}
	return comparison, nil
}

// SnapshotDigestはGitSnapshotのdigest群をtelemetry記録用へ切り出したもの。
// 生diffやfile内容は持たず、HEAD・index・worktree(全体と親管理metadata集合除外)のdigestだけを残す。
type SnapshotDigest struct {
	Head           string `json:"head,omitempty"`
	IndexDigest    string `json:"index_digest,omitempty"`
	WorktreeDigest string `json:"worktree_digest,omitempty"`
	// WorktreeDigestExcludingParentはreview resume承認判断のtelemetry証跡用。
	WorktreeDigestExcludingParent string `json:"worktree_digest_excluding_parent,omitempty"`
}

func snapshotDigest(s GitSnapshot) SnapshotDigest {
	return SnapshotDigest{
		Head:                          s.Head,
		IndexDigest:                   s.IndexDigest,
		WorktreeDigest:                s.WorktreeDigest,
		WorktreeDigestExcludingParent: s.WorktreeDigestExcludingParent,
	}
}

// SnapshotDiagnosticは1回のreview工程に付与するGit snapshot診断。Previousは比較基準
// (worker-endまたは保存review-start)、Currentは比較対象の現在snapshot。Matchedはpointerで
// nil=比較未実施(取得失敗等)、true=一致、false=不一致を区別し、bool零値(false=不一致)との
// 混同を防ぐ。MismatchAxis/Reasonは不一致または取得失敗時だけ設定される。
type SnapshotDiagnostic struct {
	Stage        string          `json:"stage"`
	Previous     *SnapshotDigest `json:"previous,omitempty"`
	Current      *SnapshotDigest `json:"current,omitempty"`
	Matched      *bool           `json:"matched,omitempty"`
	MismatchAxis string          `json:"mismatch_axis,omitempty"`
	Reason       string          `json:"reason,omitempty"`
}

// MismatchAxisはcomparisonの不一致軸を"head,index,worktree"形式で返す。一致時は空文字。
// SnapshotMismatchByAxis集計で各軸のmismatch件数へ用いる。
func MismatchAxis(c SnapshotComparison) string {
	if c.Matched {
		return ""
	}
	var axes []string
	if !c.HeadMatch {
		axes = append(axes, "head")
	}
	if !c.IndexMatch {
		axes = append(axes, "index")
	}
	if !c.WorktreeMatch {
		axes = append(axes, "worktree")
	}
	return strings.Join(axes, ",")
}

// BuildSnapshotDiagnosticは2 snapshotと比較結果からtelemetry記録用diagnosticを構築する。
// previous/currentのいずれかが空(取得失敗等)のときはmatchedをnil=未比較とし不一致軸集計から
// 除外する。両方揃っていればcomparisonからmatched/mismatch軸を反映する。
func BuildSnapshotDiagnostic(stage SnapshotStage, previous, current GitSnapshot, comparison SnapshotComparison, reason string) SnapshotDiagnostic {
	diag := SnapshotDiagnostic{Stage: string(stage), Reason: reason}
	if !isEmptySnapshot(previous) {
		d := snapshotDigest(previous)
		diag.Previous = &d
	}
	if !isEmptySnapshot(current) {
		d := snapshotDigest(current)
		diag.Current = &d
	}
	if diag.Previous != nil && diag.Current != nil {
		matched := comparison.Matched
		diag.Matched = &matched
		diag.MismatchAxis = MismatchAxis(comparison)
	}
	return diag
}

func isEmptySnapshot(s GitSnapshot) bool {
	return s.Head == "" && s.IndexDigest == "" && s.WorktreeDigest == ""
}
