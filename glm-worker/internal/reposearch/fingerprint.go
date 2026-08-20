package reposearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// captureFingerprintは検索中のstate変化をtestで再現するため差し替え可能にした
// fingerprint取得関数。本番はcaptureRepositoryFingerprintそのものを使う。
var captureFingerprint = captureRepositoryFingerprint

// fingerprintはfreshness判定の実体。HEAD・index・worktree(tracked変更とuntracked内容)
// の3軸digestでrepository状態を識別し、.gitignore変更もuntracked列挙変化経由で
// worktree digestへ反映される。state.CaptureGitSnapshotと同じgit command・同じhash
// 構成で計算するが、context取消でgitを中断できるようpackage内へ持つ。
type fingerprint struct {
	Head           string
	IndexDigest    string
	WorktreeDigest string
}

func computeFingerprint(ctx context.Context, repoRoot string) (fingerprint, error) {
	fp, err := captureFingerprint(ctx, repoRoot)
	if err != nil {
		return fingerprint{}, fmt.Errorf("repository状態のfingerprintを取得できません: %w", err)
	}
	return fp, nil
}

func captureRepositoryFingerprint(ctx context.Context, repoRoot string) (fingerprint, error) {
	head, err := fingerprintHead(ctx, repoRoot)
	if err != nil {
		return fingerprint{}, err
	}
	indexDigest, err := fingerprintIndexDigest(ctx, repoRoot)
	if err != nil {
		return fingerprint{}, err
	}
	worktreeDigest, err := fingerprintWorktreeDigest(ctx, repoRoot)
	if err != nil {
		return fingerprint{}, err
	}
	return fingerprint{Head: head, IndexDigest: indexDigest, WorktreeDigest: worktreeDigest}, nil
}

// commitが無いrepoではHeadを空文字とし、index/worktree digestで状態を識別する。
func fingerprintHead(ctx context.Context, repoRoot string) (string, error) {
	output, err := gitOutput(ctx, repoRoot, "rev-parse", "HEAD")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// git ls-files -sは<path>毎に<mode> <sha> <stage>をpath順で出力するため、出力全体の
// sha256がindex同一性を決定論的に表す。
func fingerprintIndexDigest(ctx context.Context, repoRoot string) (string, error) {
	output, err := gitOutput(ctx, repoRoot, "ls-files", "-s")
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(output)
	return hex.EncodeToString(sum[:]), nil
}

func fingerprintWorktreeDigest(ctx context.Context, repoRoot string) (string, error) {
	diffOutput, err := gitOutput(ctx, repoRoot, "diff", "--binary", "--no-ext-diff")
	if err != nil {
		return "", err
	}
	untrackedOutput, err := gitOutput(ctx, repoRoot, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return "", err
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
			return "", fmt.Errorf("untracked file %sをstatできません: %w", absPath, err)
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

// symlinkはtarget文字列だけをhashし、指す先がrepo外・巨大fileでも内容を読まない。
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
