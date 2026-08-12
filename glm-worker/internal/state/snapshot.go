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
	workerEndSnapshotFile   = "snapshot-worker-end.json"
	reviewStartSnapshotFile = "snapshot-review-start.json"
	snapshotComparisonFile  = "snapshot-comparison.json"
)

// GitSnapshotはworker終了時・review開始時のrepo状態を3軸のdigestで識別する。
type GitSnapshot struct {
	Head        string `json:"head"`
	IndexDigest string `json:"index_digest"`
	// WorktreeDigestはunstaged tracked変更とuntracked(ignored除外)の内容/pathを反映する。
	WorktreeDigest string `json:"worktree_digest"`
}

type SnapshotStage string

const (
	SnapshotStageWorkerEnd    SnapshotStage = "worker-end"
	SnapshotStageReviewStart  SnapshotStage = "review-start"
	SnapshotStageReviewResume SnapshotStage = "review-resume"
)

// SnapshotComparisonはworker-endとreview-start snapshotの一致判定結果を記録する。
// 値そのものは各snapshot fileへ、判定結果はcomparison fileへ区別して永続化する。
type SnapshotComparison struct {
	Stage         SnapshotStage `json:"stage"`
	Matched       bool          `json:"matched"`
	HeadMatch     bool          `json:"head_match"`
	IndexMatch    bool          `json:"index_match"`
	WorktreeMatch bool          `json:"worktree_match"`
	Reason        string        `json:"reason,omitempty"`
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
	worktreeDigest, err := captureSnapshotWorktreeDigest(repoRoot)
	if err != nil {
		return GitSnapshot{}, err
	}
	return GitSnapshot{
		Head:           head,
		IndexDigest:    indexDigest,
		WorktreeDigest: worktreeDigest,
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
func captureSnapshotWorktreeDigest(repoRoot string) (string, error) {
	diffOutput, err := exec.Command("git", "-C", repoRoot, "diff", "--binary", "--no-ext-diff").Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	untrackedOutput, err := exec.Command("git", "-C", repoRoot, "ls-files", "-z", "--others", "--exclude-standard").Output()
	if err != nil {
		return "", fmt.Errorf("git ls-files --others: %w", err)
	}
	return buildWorktreeDigest(diffOutput, untrackedOutput, repoRoot)
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
