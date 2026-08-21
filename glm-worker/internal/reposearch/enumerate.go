package reposearch

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// enumerationVersionは対象file列挙・除外規約の変更をcache無効化へ反映するための版。
const enumerationVersion = 1

const (
	trackedModeRegular    = "100644"
	trackedModeExecutable = "100755"
)

// defaultExcludeDirsは削除できない既定除外directory名。VCS metadataと、git管理下に
// 入りうる依存・生成物directoryを対象とする。
var defaultExcludeDirs = []string{".git", ".hg", ".svn", "node_modules", "vendor", "dist", "build", "target", "__pycache__"}

// resolveExcludeDirsは既定へ追加directory名を合わせた集合を作る。追加名は空・`.`・
// `..`・`/`含有・絶対pathを拒否する。
func resolveExcludeDirs(extra []string) (map[string]bool, error) {
	dirs := make(map[string]bool, len(defaultExcludeDirs)+len(extra))
	for _, name := range defaultExcludeDirs {
		dirs[name] = true
	}
	for _, name := range extra {
		if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') || filepath.IsAbs(name) {
			return nil, fmt.Errorf("%w: ExcludeDirsの不正なdirectory名 %q", ErrInvalidOptions, name)
		}
		dirs[name] = true
	}
	return dirs, nil
}

// excludedPathはslash正規化したrepo相対pathのdirectory区間だけへ除外集合を適用する。
// 最終区間はfile名のため、除外名と同名の通常fileは除外しない。
func excludedPath(rel string, excludeDirs map[string]bool) bool {
	segments := strings.Split(filepath.ToSlash(rel), "/")
	for _, segment := range segments[:len(segments)-1] {
		if excludeDirs[segment] {
			return true
		}
	}
	return false
}

// enumerateFilesはworking tree基準の検索対象path一覧をrepo root相対で返す。trackedは
// `git ls-files -s`のindex登録から通常file(mode 100644/100755)だけを取り、symlink
// (120000)とsubmodule gitlink(160000)を除外する。untrackedは`--exclude-standard`で
// .gitignore・info/excludeをgit自身の規則で適用し、nested repoは`dir/`形式のentryと
// して現れるため末端`/`で除外する。既定・追加の除外directory配下もここで落とす。
// deleted tracked file・binary・巨大file・特殊fileの除外はrebuildIndexの読み込み段階が担う。
// fingerprintのfreshness判定もtrackedFileEntries・untrackedFilePaths・excludedPathの
// 同一実装でこのcorpus policyを共有する。
func enumerateFiles(ctx context.Context, repoRoot string, excludeDirs map[string]bool) ([]string, error) {
	entries, err := trackedFileEntries(ctx, repoRoot)
	if err != nil {
		return nil, err
	}
	untracked, err := untrackedFilePaths(ctx, repoRoot)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(entries)+len(untracked))
	paths := make([]string, 0, len(entries)+len(untracked))
	for _, entry := range entries {
		if entry.path == "" || seen[entry.path] || excludedPath(entry.path, excludeDirs) {
			continue
		}
		seen[entry.path] = true
		paths = append(paths, entry.path)
	}
	for _, path := range untracked {
		if path == "" || seen[path] || excludedPath(path, excludeDirs) {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// trackedEntryは検索対象modeのindex登録1件分。fingerprintのindex digestも列挙と
// 同一のmode filter・path集合で状態識別するため、mode・blob shaをpathとともに返す。
type trackedEntry struct {
	mode string
	sha  string
	path string
}

func trackedFileEntries(ctx context.Context, repoRoot string) ([]trackedEntry, error) {
	output, err := gitOutput(ctx, repoRoot, "ls-files", "-z", "-s", "--cached")
	if err != nil {
		return nil, fmt.Errorf("git ls-files --cached: %w", err)
	}
	seen := map[string]bool{}
	var entries []trackedEntry
	for _, entry := range strings.Split(string(output), "\x00") {
		if entry == "" {
			continue
		}
		mode, sha, path, ok := parseLsFilesStage(entry)
		if !ok {
			return nil, fmt.Errorf("git ls-files --cachedのentryを解析できません: %q", entry)
		}
		if mode != trackedModeRegular && mode != trackedModeExecutable {
			continue
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		entries = append(entries, trackedEntry{mode: mode, sha: sha, path: path})
	}
	return entries, nil
}

func untrackedFilePaths(ctx context.Context, repoRoot string) ([]string, error) {
	output, err := gitOutput(ctx, repoRoot, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("git ls-files --others: %w", err)
	}
	var paths []string
	for _, path := range strings.Split(string(output), "\x00") {
		if path == "" || strings.HasSuffix(path, "/") {
			continue
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func parseLsFilesStage(entry string) (string, string, string, bool) {
	tab := strings.IndexByte(entry, '\t')
	if tab < 0 {
		return "", "", "", false
	}
	header := strings.Fields(entry[:tab])
	if len(header) != 3 {
		return "", "", "", false
	}
	return header[0], header[1], entry[tab+1:], true
}

// joinWithinRootはroot配下へpathを結合し、絶対path・`..`越え等repository境界を越える
// path文字列を拒否する。cache由来やgit由来のpath検証にも使うため symlink解決ではなく
// 文字列判定で行う。
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
