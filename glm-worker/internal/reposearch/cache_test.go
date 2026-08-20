package reposearch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func searchNeedle(t *testing.T, dir string, opts Options) Report {
	t.Helper()
	report, err := Search(context.Background(), dir, "needle", opts)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// canonicalCachePathはSearch内部と同じcanonical root評価を経由したcache file pathを
// 返す。macOSのTempDir symlink(/var -> /private/var)でも実pathと一致させるため。
func canonicalCachePath(t *testing.T, cacheRoot, dir string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cachePathFor(cacheRoot, canonical)
}

func TestCacheRebuiltThenHitReturnsIdenticalResults(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	commitAll(t, dir, "init")
	cacheRoot := t.TempDir()
	opts := Options{CacheRoot: cacheRoot}

	first := searchNeedle(t, dir, opts)
	if first.CacheStatus != CacheStatusRebuilt {
		t.Fatalf("初回status = %q want rebuilt", first.CacheStatus)
	}
	second := searchNeedle(t, dir, opts)
	if second.CacheStatus != CacheStatusHit {
		t.Fatalf("2回目status = %q want hit", second.CacheStatus)
	}
	first.CacheStatus = second.CacheStatus
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("cache hit結果がrebuild結果と不一致:\n%+v\n%+v", first, second)
	}

	cachePath := canonicalCachePath(t, cacheRoot, dir)
	info, err := os.Stat(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("cache file permission = %v want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(cachePath))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("cache dir permission = %v want 0700", dirInfo.Mode().Perm())
	}
}

func TestCacheRebuildsOnWorkingTreeChange(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	commitAll(t, dir, "init")
	opts := Options{CacheRoot: t.TempDir()}

	searchNeedle(t, dir, opts)
	writeTestFile(t, filepath.Join(dir, "b.txt"), "needle two\n")
	report := searchNeedle(t, dir, opts)
	if report.CacheStatus != CacheStatusRebuilt {
		t.Fatalf("変更後status = %q want rebuilt", report.CacheStatus)
	}
	if !reflect.DeepEqual(resultPaths(report), []string{"a.txt", "b.txt"}) {
		t.Fatalf("results = %v want 変更反映済みの同点path昇順 [a.txt b.txt]", resultPaths(report))
	}
}

func TestCacheRebuildsOnCorruptionAndStaleSchema(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	commitAll(t, dir, "init")
	cacheRoot := t.TempDir()
	opts := Options{CacheRoot: cacheRoot}
	searchNeedle(t, dir, opts)
	cachePath := canonicalCachePath(t, cacheRoot, dir)

	valid, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	corruptions := map[string]func(data []byte) []byte{
		"garbage":         func([]byte) []byte { return []byte("not json {") },
		"partial write":   func(data []byte) []byte { return data[:len(data)/2] },
		"empty":           func([]byte) []byte { return nil },
		"truncated bytes": func(data []byte) []byte { return data[:len(data)-10] },
		"embedded null":   func(data []byte) []byte { return append(data, 0, 0, 0) },
	}
	for name, corrupt := range corruptions {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(cachePath, corrupt(valid), 0o600); err != nil {
				t.Fatal(err)
			}
			report := searchNeedle(t, dir, opts)
			if report.CacheStatus != CacheStatusRebuilt {
				t.Fatalf("status = %q want rebuilt", report.CacheStatus)
			}
			if !reflect.DeepEqual(resultPaths(report), []string{"a.txt"}) {
				t.Fatalf("results = %v want [a.txt]", resultPaths(report))
			}
		})
	}
}

func TestCacheRebuildsOnVersionAndRepoMismatch(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	writeTestFile(t, filepath.Join(dir, "b.txt"), "needle two\n")
	commitAll(t, dir, "init")
	cacheRoot := t.TempDir()
	opts := Options{CacheRoot: cacheRoot}
	searchNeedle(t, dir, opts)
	cachePath := canonicalCachePath(t, cacheRoot, dir)

	mutations := map[string]func(data *cacheData){
		"schema version":     func(data *cacheData) { data.SchemaVersion = cacheSchemaVersion + 1 },
		"tokenizer policy":   func(data *cacheData) { data.TokenizerVersion = tokenizerVersion + 1 },
		"enumeration policy": func(data *cacheData) { data.EnumerationVersion = enumerationVersion + 1 },
		"exclude policy":     func(data *cacheData) { data.ExcludeDirs = append(data.ExcludeDirs, "tampered") },
		"repo root":          func(data *cacheData) { data.RepoRoot = "/other/repo" },
		"digest":             func(data *cacheData) { data.WorktreeDigest = strings.Repeat("0", 64) },
		"docs path escape":   func(data *cacheData) { data.Docs[0].Path = "../escape" },
		"unsorted docs":      func(data *cacheData) { data.Docs[0].Path = "zzz.txt" },
		"count mismatch":     func(data *cacheData) { data.IndexedFiles = data.IndexedFiles + 1 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(cachePath)
			if err != nil {
				t.Fatal(err)
			}
			var cached cacheData
			if err := json.Unmarshal(data, &cached); err != nil {
				t.Fatal(err)
			}
			mutate(&cached)
			rewritten, err := json.Marshal(cached)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cachePath, rewritten, 0o600); err != nil {
				t.Fatal(err)
			}
			report := searchNeedle(t, dir, opts)
			if report.CacheStatus != CacheStatusRebuilt {
				t.Fatalf("status = %q want rebuilt", report.CacheStatus)
			}
			if !reflect.DeepEqual(resultPaths(report), []string{"a.txt", "b.txt"}) {
				t.Fatalf("results = %v want [a.txt b.txt]", resultPaths(report))
			}
		})
	}
}

// 除外policy違いのcacheを再利用すると収集対象の差が結果へ漏れるため、
// 実効除外集合が完全一致する時だけhitする。
func TestCacheRebuildsWhenExcludeDirsDiffer(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	writeTestFile(t, filepath.Join(dir, "generated", "g.txt"), "needle generated\n")
	commitAll(t, dir, "init")
	cacheRoot := t.TempDir()
	excluding := Options{CacheRoot: cacheRoot, ExcludeDirs: []string{"generated"}}

	if report := searchNeedle(t, dir, excluding); report.CacheStatus != CacheStatusRebuilt {
		t.Fatalf("初回status = %q want rebuilt", report.CacheStatus)
	}
	if report := searchNeedle(t, dir, excluding); report.CacheStatus != CacheStatusHit {
		t.Fatalf("同一除外集合のstatus = %q want hit", report.CacheStatus)
	}

	report := searchNeedle(t, dir, Options{CacheRoot: cacheRoot})
	if report.CacheStatus != CacheStatusRebuilt {
		t.Fatalf("除外なし呼出のstatus = %q want rebuilt (除外ありcacheをhitしてはならない)", report.CacheStatus)
	}
	if want := []string{"a.txt", "generated/g.txt"}; !reflect.DeepEqual(resultPaths(report), want) {
		t.Fatalf("results = %v want 除外なしの全件 %v", resultPaths(report), want)
	}
}

func TestCacheRebuildsWhenCopiedFromOtherRepo(t *testing.T) {
	first := initRepo(t)
	writeTestFile(t, filepath.Join(first, "a.txt"), "needle one\n")
	commitAll(t, first, "init")
	second := initRepo(t)
	writeTestFile(t, filepath.Join(second, "b.txt"), "needle two\n")
	commitAll(t, second, "init")
	cacheRoot := t.TempDir()

	searchNeedle(t, first, Options{CacheRoot: cacheRoot})
	otherCache, err := os.ReadFile(canonicalCachePath(t, cacheRoot, first))
	if err != nil {
		t.Fatal(err)
	}
	secondPath := canonicalCachePath(t, cacheRoot, second)
	if err := os.MkdirAll(filepath.Dir(secondPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, otherCache, 0o600); err != nil {
		t.Fatal(err)
	}

	report := searchNeedle(t, second, Options{CacheRoot: cacheRoot})
	if report.CacheStatus != CacheStatusRebuilt {
		t.Fatalf("status = %q want rebuilt (別repoのcacheは使わない)", report.CacheStatus)
	}
	if !reflect.DeepEqual(resultPaths(report), []string{"b.txt"}) {
		t.Fatalf("results = %v want [b.txt]", resultPaths(report))
	}
}

func TestCacheWriteFailureReturnsResultsAsWarning(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	commitAll(t, dir, "init")
	cacheRoot := t.TempDir()
	opts := Options{CacheRoot: cacheRoot}
	searchNeedle(t, dir, opts)
	cachePath := canonicalCachePath(t, cacheRoot, dir)
	before, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}

	cacheDir := filepath.Dir(cachePath)
	if err := os.Chmod(cacheDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(cacheDir, 0o700) })
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle changed\n")
	report := searchNeedle(t, dir, opts)

	if report.CacheStatus != CacheStatusWriteWarning {
		t.Fatalf("status = %q want write-warning", report.CacheStatus)
	}
	if len(report.Warnings) == 0 || !strings.Contains(report.Warnings[0], "cacheを書き込めません") {
		t.Fatalf("warnings = %v want 書込み失敗warning", report.Warnings)
	}
	if !reflect.DeepEqual(resultPaths(report), []string{"a.txt"}) {
		t.Fatalf("results = %v write失敗時も結果を返すべきです", resultPaths(report))
	}
	after, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("書込み失敗で既存cacheが破壊されています")
	}
}

func TestCacheKeepsNoRawSource(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "sentinelword1 sentinelword2\nother line with sentinelword3\n")
	commitAll(t, dir, "init")
	cacheRoot := t.TempDir()

	searchNeedle(t, dir, Options{CacheRoot: cacheRoot, MaxResults: 1})
	data, err := os.ReadFile(canonicalCachePath(t, cacheRoot, dir))
	if err != nil {
		t.Fatal(err)
	}
	cacheText := string(data)
	for _, forbidden := range []string{
		"sentinelword1 sentinelword2",
		"other line with",
		"\nother",
	} {
		if strings.Contains(cacheText, forbidden) {
			t.Fatalf("cacheにraw source %qが残っています", forbidden)
		}
	}
}

func TestCacheRebuildsOnFirstSearchWithDefaultRoot(t *testing.T) {
	// CacheRoot空は既定root(GLM_WORKER_HOME/search)を意味するため、実homeへ
	// 書き込まないよう隔離する。
	t.Setenv("GLM_WORKER_HOME", t.TempDir())
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	commitAll(t, dir, "init")

	report := searchNeedle(t, dir, Options{})
	if report.CacheStatus != CacheStatusRebuilt {
		t.Fatalf("status = %q want rebuilt", report.CacheStatus)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("warnings = %v want なし", report.Warnings)
	}
}

func TestSearchUsesDefaultCacheRootWhenCacheRootEmpty(t *testing.T) {
	workerHome := t.TempDir()
	t.Setenv("GLM_WORKER_HOME", workerHome)
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	commitAll(t, dir, "init")

	first := searchNeedle(t, dir, Options{})
	if first.CacheStatus != CacheStatusRebuilt {
		t.Fatalf("初回status = %q want rebuilt", first.CacheStatus)
	}
	cacheRoot := filepath.Join(workerHome, "search")
	cachePath := canonicalCachePath(t, cacheRoot, dir)
	info, err := os.Stat(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("既定cache file permission = %v want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(cachePath))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("既定cache dir permission = %v want 0700", dirInfo.Mode().Perm())
	}
	if second := searchNeedle(t, dir, Options{}); second.CacheStatus != CacheStatusHit {
		t.Fatalf("2回目status = %q want hit", second.CacheStatus)
	}
}

func TestDisableCacheSkipsAllCacheIO(t *testing.T) {
	workerHome := t.TempDir()
	t.Setenv("GLM_WORKER_HOME", workerHome)
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	commitAll(t, dir, "init")

	report := searchNeedle(t, dir, Options{DisableCache: true})
	if report.CacheStatus != CacheStatusRebuilt || len(report.Warnings) != 0 {
		t.Fatalf("status=%q warnings=%v want rebuilt/なし", report.CacheStatus, report.Warnings)
	}
	if _, err := os.Stat(filepath.Join(workerHome, "search")); !os.IsNotExist(err) {
		t.Fatal("DisableCache時に既定cache rootが作成されています")
	}
}

func TestDisableCacheConflictsWithCacheRoot(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	commitAll(t, dir, "init")

	opts := Options{CacheRoot: t.TempDir(), DisableCache: true}
	if _, err := Search(context.Background(), dir, "needle", opts); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("error = %v want ErrInvalidOptions", err)
	}
}

func TestCacheRebuildsOnHeadAndIndexChange(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	commitAll(t, dir, "init")
	opts := Options{CacheRoot: t.TempDir()}

	searchNeedle(t, dir, opts)
	gitRun(t, dir, "commit", "--quiet", "--allow-empty", "-m", "second")
	if report := searchNeedle(t, dir, opts); report.CacheStatus != CacheStatusRebuilt {
		t.Fatalf("HEAD移動後status = %q want rebuilt", report.CacheStatus)
	}

	searchNeedle(t, dir, opts)
	writeTestFile(t, filepath.Join(dir, "b.txt"), "needle staged\n")
	gitRun(t, dir, "add", "b.txt")
	if report := searchNeedle(t, dir, opts); report.CacheStatus != CacheStatusRebuilt {
		t.Fatalf("index変更後status = %q want rebuilt", report.CacheStatus)
	}
}

func TestCacheSemanticCorruptionRebuilds(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	commitAll(t, dir, "init")
	cacheRoot := t.TempDir()
	opts := Options{CacheRoot: cacheRoot}
	searchNeedle(t, dir, opts)
	cachePath := canonicalCachePath(t, cacheRoot, dir)

	mutations := map[string]func(data *cacheData){
		"zero tf count":        func(data *cacheData) { data.Docs[0].ContentTF["needle"] = 0 },
		"tf sum mismatch":      func(data *cacheData) { data.Docs[0].ContentTF["needle"] = 5 },
		"length without tf":    func(data *cacheData) { data.Docs[0].PathLength = 0 },
		"path in excluded dir": func(data *cacheData) { data.Docs[0].Path = "node_modules/a.txt" },
		"bytes count negative": func(data *cacheData) { data.IndexedBytes = -1 },
		"files over limit":     func(data *cacheData) { data.Docs = append(data.Docs, data.Docs[0]) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(cachePath)
			if err != nil {
				t.Fatal(err)
			}
			var cached cacheData
			if err := json.Unmarshal(data, &cached); err != nil {
				t.Fatal(err)
			}
			mutate(&cached)
			rewritten, err := json.Marshal(cached)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cachePath, rewritten, 0o600); err != nil {
				t.Fatal(err)
			}
			report := searchNeedle(t, dir, opts)
			if report.CacheStatus != CacheStatusRebuilt {
				t.Fatalf("status = %q want rebuilt", report.CacheStatus)
			}
			if !reflect.DeepEqual(resultPaths(report), []string{"a.txt"}) {
				t.Fatalf("results = %v want [a.txt]", resultPaths(report))
			}
		})
	}
}
