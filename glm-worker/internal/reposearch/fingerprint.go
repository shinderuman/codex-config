package reposearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"sort"
)

// captureFingerprintは検索中のstate変化をtestで再現するため差し替え可能にした
// fingerprint取得関数。本番はcaptureRepositoryFingerprintそのものを使う。
var captureFingerprint = captureRepositoryFingerprint

// fingerprintはfreshness判定の実体。index(tracked検索対象のmode・blob sha・path)と
// worktree(tracked変更diffとuntracked検索対象の読み取り結果)の2軸digestでrepository
// 状態を識別する。freshnessの対象はenumerateFilesと共有のcorpus policy(検索対象mode・
// 除外directory・nested repo・symlink・巨大file・binary)に限定し、検索対象外の状態は
// 無制限に読まない。.gitignore変更はuntracked列挙変化経由でworktree digestへ反映される。
type fingerprint struct {
	IndexDigest    string
	WorktreeDigest string
}

func computeFingerprint(ctx context.Context, repoRoot string, excludeDirs map[string]bool) (fingerprint, error) {
	fp, err := captureFingerprint(ctx, repoRoot, excludeDirs)
	if err != nil {
		return fingerprint{}, fmt.Errorf("repository状態のfingerprintを取得できません: %w", err)
	}
	return fp, nil
}

func captureRepositoryFingerprint(ctx context.Context, repoRoot string, excludeDirs map[string]bool) (fingerprint, error) {
	indexDigest, err := fingerprintIndexDigest(ctx, repoRoot, excludeDirs)
	if err != nil {
		return fingerprint{}, err
	}
	worktreeDigest, err := fingerprintWorktreeDigest(ctx, repoRoot, excludeDirs)
	if err != nil {
		return fingerprint{}, err
	}
	return fingerprint{IndexDigest: indexDigest, WorktreeDigest: worktreeDigest}, nil
}

// fingerprintIndexDigestはindex登録のうち検索対象corpusと同じmode・除外policyのentry
// だけをmode・blob sha・pathごとhashする。staged変更はblob sha経由で必ず反映される。
// symlink(120000)・submodule gitlink(160000)・除外directory配下のindex登録は検索対象外
// のため含めない。
func fingerprintIndexDigest(ctx context.Context, repoRoot string, excludeDirs map[string]bool) (string, error) {
	entries, err := trackedFileEntries(ctx, repoRoot)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	for _, entry := range entries {
		if excludedPath(entry.path, excludeDirs) {
			continue
		}
		hasher.Write([]byte(entry.mode))
		hasher.Write([]byte{0})
		hasher.Write([]byte(entry.sha))
		hasher.Write([]byte{0})
		hasher.Write([]byte(entry.path))
		hasher.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// fingerprintWorktreeDigestはtracked変更とuntracked corpusでworktree状態をhashする。
// tracked diffは`--binary`を付けずbinary内容を読ませず、除外directory直下をliteral
// pathspecで落とす(`name/`形式は同名の通常fileを除外しない)。除外directoryより深い
// segmentや検索対象外modeのtracked変更はdiffに残るが、これは再構築を余分に誘発する
// だけでstale結果は生まない。textconv等の設定差異へ依存しないよう`--no-textconv`も
// 指定する。untrackedは列挙と同じpath集合・readSearchableFileと同じ読み取り投影で
// hashするため、読み込みは常に検索対象と同じ上限内に収まる。
func fingerprintWorktreeDigest(ctx context.Context, repoRoot string, excludeDirs map[string]bool) (string, error) {
	args := append([]string{"diff", "--no-ext-diff", "--no-textconv", "--", "."}, excludePathspecs(excludeDirs)...)
	diffOutput, err := gitOutput(ctx, repoRoot, args...)
	if err != nil {
		return "", err
	}
	untracked, err := untrackedFilePaths(ctx, repoRoot)
	if err != nil {
		return "", err
	}
	return buildWorktreeDigest(diffOutput, untracked, repoRoot, excludeDirs)
}

// excludePathspecsは除外directory直下をdiff対象から外すpathspec列。`literal` magicで
// glob解釈を無効化し、`/`付きでdirectoryだけに当てるため除外名と同名の通常fileや
// 深い位置の同名directoryは検索対象のままdiffに残る。
func excludePathspecs(excludeDirs map[string]bool) []string {
	names := sortedExcludeDirs(excludeDirs)
	pathspecs := make([]string, 0, len(names))
	for _, name := range names {
		pathspecs = append(pathspecs, ":(exclude,literal)"+name+"/")
	}
	return pathspecs
}

// buildWorktreeDigestはtracked diff出力とuntracked corpus投影を1つのdigestへ混ぜる。
// untracked path列挙はuntrackedFilePathsと同じ実装(nested repoの`dir/` entry除去を
// 含む)を使い、列挙後に消失したpathを空扱いすると別状態を同一視するため消失は取得
// 失敗にする。除外directory配下はcorpus外のため読まずhashにも入れない。
// readSkipped同士(symlink・巨大file・binary間の遷移を含む)は同じ投影なので区別せず、
// 検索対象への出入りだけがdigestを変える。
func buildWorktreeDigest(diffOutput []byte, untrackedPaths []string, repoRoot string, excludeDirs map[string]bool) (string, error) {
	hasher := sha256.New()
	hasher.Write([]byte("diff\n"))
	hasher.Write(diffOutput)
	hasher.Write([]byte("\nuntracked\n"))

	sort.Strings(untrackedPaths)
	for _, path := range untrackedPaths {
		if path == "" || excludedPath(path, excludeDirs) {
			continue
		}
		absPath, err := joinWithinRoot(repoRoot, path)
		if err != nil {
			return "", fmt.Errorf("untracked %s: %w", path, err)
		}
		hasher.Write([]byte(path))
		hasher.Write([]byte{0})
		if err := hashUntrackedProjection(hasher, absPath); err != nil {
			return "", err
		}
		hasher.Write([]byte("\n"))
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func hashUntrackedProjection(hasher hash.Hash, absPath string) error {
	content, outcome, err := readSearchableFile(absPath)
	if err != nil {
		return err
	}
	switch outcome {
	case readMissing:
		return fmt.Errorf("untracked file %sが列挙後に消失しました", absPath)
	case readSkipped:
		hasher.Write([]byte("skipped\x00"))
	case readIndexed:
		sum := sha256.Sum256(content)
		hasher.Write([]byte("indexed\x00"))
		hasher.Write([]byte(hex.EncodeToString(sum[:])))
	}
	return nil
}

// fingerprintUnchangedは検索開始時点のfingerprintと現在状態を比較し、検索中の
// corpus変化(race)を検出する。
func fingerprintUnchanged(ctx context.Context, root string, excludeDirs map[string]bool, before fingerprint) (bool, error) {
	after, err := computeFingerprint(ctx, root, excludeDirs)
	if err != nil {
		return false, err
	}
	return after != before, nil
}
