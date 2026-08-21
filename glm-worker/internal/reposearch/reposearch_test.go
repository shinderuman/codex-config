package reposearch

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, "", "init", "--quiet", dir)
	return dir
}

func commitAll(t *testing.T, dir string, message string) {
	t.Helper()
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "--quiet", "-m", message)
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func resultPaths(report Report) []string {
	paths := make([]string, 0, len(report.Results))
	for _, result := range report.Results {
		paths = append(paths, result.Path)
	}
	return paths
}

func TestSearchRanksContentAndPathSeparately(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "alpha", "doc.txt"), "first line\nneedle needle again\nlast line\n")
	writeTestFile(t, filepath.Join(dir, "beta", "needle.txt"), "unrelated content\n")
	writeTestFile(t, filepath.Join(dir, "gamma", "other.txt"), "nothing here\n")
	commitAll(t, dir, "init")

	report, err := Search(context.Background(), dir, "needle", Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	got := resultPaths(report)
	if len(got) != 2 {
		t.Fatalf("results = %v want alpha/doc.txt と beta/needle.txt の2件", got)
	}
	byPath := map[string]Result{}
	for _, result := range report.Results {
		byPath[result.Path] = result
	}
	doc := byPath["alpha/doc.txt"]
	if doc.ContentScore <= 0 || doc.PathScore != 0 {
		t.Fatalf("alpha/doc.txt content=%v path=%v want content>0 path=0", doc.ContentScore, doc.PathScore)
	}
	if doc.Line != 2 || doc.Snippet != "needle needle again" {
		t.Fatalf("snippet line=%d %q want line=2 %q", doc.Line, doc.Snippet, "needle needle again")
	}
	needle := byPath["beta/needle.txt"]
	if needle.PathScore <= 0 || needle.ContentScore != 0 {
		t.Fatalf("beta/needle.txt content=%v path=%v want content=0 path>0", needle.ContentScore, needle.PathScore)
	}
	if needle.Line != 0 || needle.Snippet != "" {
		t.Fatalf("path一致のみのLine/Snippet = %d/%q want 0/空", needle.Line, needle.Snippet)
	}
	if doc.Score != doc.ContentScore+doc.PathScore || needle.Score != needle.ContentScore+needle.PathScore {
		t.Fatal("ScoreはContentScore+PathScoreであるべきです")
	}
	if report.IndexedFiles != 3 || report.SkippedFiles != 0 {
		t.Fatalf("indexed=%d skipped=%d want 3/0", report.IndexedFiles, report.SkippedFiles)
	}
}

func TestSearchIsDeterministicWithTieOrderByPath(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "zzz.txt"), "same text\n")
	writeTestFile(t, filepath.Join(dir, "aaa.txt"), "same text\n")
	writeTestFile(t, filepath.Join(dir, "mmm.txt"), "same text\n")
	commitAll(t, dir, "init")

	opts := Options{DisableCache: true}
	first, err := Search(context.Background(), dir, "same", opts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Search(context.Background(), dir, "same", opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Results, second.Results) {
		t.Fatalf("同一入力の結果が不一致:\n%v\n%v", first.Results, second.Results)
	}
	got := resultPaths(first)
	want := []string{"aaa.txt", "mmm.txt", "zzz.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("同点順序 = %v want path昇順 %v", got, want)
	}
}

func TestSearchEmptyQueryIsTypedError(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "a\n")
	commitAll(t, dir, "init")
	for _, query := range []string{"", "   ", "。。。", "//._-"} {
		if _, err := Search(context.Background(), dir, query, Options{DisableCache: true}); !errors.Is(err, ErrEmptyQuery) {
			t.Fatalf("query %q のerror = %v want ErrEmptyQuery", query, err)
		}
	}
}

func TestSearchReflectsWorkingTreeState(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "gone.txt"), "needle here\n")
	writeTestFile(t, filepath.Join(dir, "keep.txt"), "plain\n")
	commitAll(t, dir, "init")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "keep.txt"), "needle now present\n")
	writeTestFile(t, filepath.Join(dir, "untracked.txt"), "needle untracked\n")

	report, err := Search(context.Background(), dir, "needle", Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	got := resultPaths(report)
	want := []string{"untracked.txt", "keep.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("results = %v want %v (deletedは検索しない)", got, want)
	}
	if report.SkippedFiles != 1 {
		t.Fatalf("skipped = %d want 1 (deleted gone.txt)", report.SkippedFiles)
	}
}

func TestSearchResolvesSubdirToToplevel(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "sub", "doc.txt"), "needle\n")
	commitAll(t, dir, "init")

	report, err := Search(context.Background(), filepath.Join(dir, "sub"), "needle", Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultPaths(report); !reflect.DeepEqual(got, []string{"sub/doc.txt"}) {
		t.Fatalf("results = %v want sub/doc.txt", got)
	}
}

func TestSearchNotARepositoryFails(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.txt"), "a\n")
	if _, err := Search(context.Background(), dir, "needle", Options{DisableCache: true}); err == nil {
		t.Fatal("git repository外の検索はerrorになるべきです")
	}
}

func TestSearchWithoutCommitCoversUntracked(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "u.txt"), "needle\n")

	report, err := Search(context.Background(), dir, "needle", Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultPaths(report); !reflect.DeepEqual(got, []string{"u.txt"}) {
		t.Fatalf("results = %v want [u.txt]", got)
	}
}

func TestSearchContextCancelAborts(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle\n")
	commitAll(t, dir, "init")

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Search(canceled, dir, "needle", Options{DisableCache: true}); err == nil {
		t.Fatal("取消済みcontextではerrorになるべきです")
	}

	ctx, cancelDuringSearch := context.WithCancel(context.Background())
	original := captureFingerprint
	captureFingerprint = func(ctx context.Context, repoRoot string, excludeDirs map[string]bool) (fingerprint, error) {
		cancelDuringSearch()
		return original(ctx, repoRoot, excludeDirs)
	}
	t.Cleanup(func() { captureFingerprint = original })
	if _, err := Search(ctx, dir, "needle", Options{DisableCache: true}); err == nil {
		t.Fatal("検索中のcontext取消でgit subprocessは中断されerrorになるべきです")
	}
}

func TestSearchLimitsResults(t *testing.T) {
	dir := initRepo(t)
	for i := 0; i < 25; i++ {
		writeTestFile(t, filepath.Join(dir, "f", "file"+string(rune('a'+i))+".txt"), "needle\n")
	}
	commitAll(t, dir, "init")

	defaultReport, err := Search(context.Background(), dir, "needle", Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultReport.Results) != defaultMaxResults {
		t.Fatalf("既定結果数 = %d want %d", len(defaultReport.Results), defaultMaxResults)
	}
	small, err := Search(context.Background(), dir, "needle", Options{DisableCache: true, MaxResults: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(small.Results) != 5 {
		t.Fatalf("MaxResults=5の結果数 = %d want 5", len(small.Results))
	}
}

func TestResolveBoundRejectsOutOfRange(t *testing.T) {
	tests := []struct {
		requested int
		want      int
		wantError bool
	}{
		{requested: -1, wantError: true},
		{requested: 0, want: defaultMaxResults},
		{requested: 5, want: 5},
		{requested: hardMaxResults, want: hardMaxResults},
		{requested: hardMaxResults + 1, wantError: true},
	}
	for _, tt := range tests {
		got, err := resolveBound(tt.requested, defaultMaxResults, hardMaxResults, "MaxResults")
		if tt.wantError {
			if !errors.Is(err, ErrInvalidOptions) {
				t.Fatalf("requested=%d はErrInvalidOptionsになるべきです: %v", tt.requested, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("requested=%d: %v", tt.requested, err)
		}
		if got != tt.want {
			t.Fatalf("resolveBound(%d) = %d want %d", tt.requested, got, tt.want)
		}
	}
	if _, err := resolveBound(-1, defaultMaxFiles, hardMaxFiles, "MaxFiles"); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("MaxFiles負数 = %v want ErrInvalidOptions", err)
	}
	if _, err := resolveBound(hardMaxTotalBytes+1, defaultMaxTotalBytes, hardMaxTotalBytes, "MaxTotalBytes"); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("MaxTotalBytes超 = %v want ErrInvalidOptions", err)
	}
}

func TestSearchMaxFilesLimitReturnsNoPartialResults(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle\n")
	writeTestFile(t, filepath.Join(dir, "b.txt"), "needle\n")
	writeTestFile(t, filepath.Join(dir, "c.txt"), "needle\n")
	commitAll(t, dir, "init")
	cacheRoot := t.TempDir()
	opts := Options{CacheRoot: cacheRoot, MaxFiles: 2}

	if _, err := Search(context.Background(), dir, "needle", opts); !errors.Is(err, ErrIndexLimit) {
		t.Fatalf("error = %v want ErrIndexLimit", err)
	}
	if _, err := os.Stat(canonicalCachePath(t, cacheRoot, dir)); !os.IsNotExist(err) {
		t.Fatal("上限超過時にcacheが書かれています")
	}

	opts.MaxFiles = 3
	report, err := Search(context.Background(), dir, "needle", opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resultPaths(report), []string{"a.txt", "b.txt", "c.txt"}) {
		t.Fatalf("results = %v", resultPaths(report))
	}
}

func TestSearchMaxTotalBytesLimitReturnsNoPartialResults(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	writeTestFile(t, filepath.Join(dir, "b.txt"), "needle two three four\n")
	commitAll(t, dir, "init")
	cacheRoot := t.TempDir()
	opts := Options{CacheRoot: cacheRoot, MaxTotalBytes: 20}

	if _, err := Search(context.Background(), dir, "needle", opts); !errors.Is(err, ErrIndexLimit) {
		t.Fatalf("error = %v want ErrIndexLimit", err)
	}
	if _, err := os.Stat(canonicalCachePath(t, cacheRoot, dir)); !os.IsNotExist(err) {
		t.Fatal("上限超過時にcacheが書かれています")
	}

	opts.MaxTotalBytes = 40
	if _, err := Search(context.Background(), dir, "needle", opts); err != nil {
		t.Fatalf("上限内でerror = %v", err)
	}
}

func TestAttachSnippetsWarnsAndKeepsResultOnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeTestFile(t, path, "needle one\n")
	// 読み取り不可にするとreadSearchableFileだけが失敗し、結果は残してwarning経路を通る。
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o644) })

	results := []Result{{Path: "a.txt", Score: 1, ContentScore: 1}}
	warnings := attachSnippets(dir, results, []string{"needle"})

	if len(warnings) != 1 || !strings.Contains(warnings[0], "snippet生成に失敗しました") {
		t.Fatalf("warnings = %v want snippet読み込み失敗warning 1件", warnings)
	}
	if results[0].Line != 0 || results[0].Snippet != "" {
		t.Fatalf("snippet失敗時 Line/Snippet = %d/%q want 0/空のまま", results[0].Line, results[0].Snippet)
	}
}

func TestAttachSnippetsWarnsOnPathOutsideRepository(t *testing.T) {
	results := []Result{{Path: "../escape.txt", Score: 1, PathScore: 1}}
	warnings := attachSnippets(t.TempDir(), results, []string{"escape"})

	if len(warnings) != 1 || !strings.Contains(warnings[0], "repository境界") {
		t.Fatalf("warnings = %v want 境界外path警告 1件", warnings)
	}
	if results[0].Line != 0 || results[0].Snippet != "" {
		t.Fatalf("境界外pathの Line/Snippet = %d/%q want 0/空", results[0].Line, results[0].Snippet)
	}
}

func TestSearchSnippetTruncatesLongLine(t *testing.T) {
	dir := initRepo(t)
	longLine := strings.Repeat("あ", maxSnippetRunes+50) + " needle"
	writeTestFile(t, filepath.Join(dir, "long.txt"), longLine+"\n")
	commitAll(t, dir, "init")

	report, err := Search(context.Background(), dir, "needle", Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("results = %v want long.txt", resultPaths(report))
	}
	snippet := report.Results[0].Snippet
	if got := len([]rune(snippet)); got != maxSnippetRunes {
		t.Fatalf("snippet rune数 = %d want %d", got, maxSnippetRunes)
	}
	if !strings.HasSuffix(snippet, "...") {
		t.Fatalf("snippet末尾 = %q want ...付き", snippet[len(snippet)-3:])
	}
}

func TestSearchSkipsBinaryLargeAndSymlinkFiles(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "text.txt"), "needle\n")
	commitAll(t, dir, "init")
	writeTestFile(t, filepath.Join(dir, "binary.bin"), "needle\x00needle\n")
	writeTestFile(t, filepath.Join(dir, "large.txt"), "needle "+strings.Repeat("x", maxFileBytes)+"\n")
	if err := os.Symlink(filepath.Join(dir, "text.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}

	report, err := Search(context.Background(), dir, "needle", Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultPaths(report); !reflect.DeepEqual(got, []string{"text.txt"}) {
		t.Fatalf("results = %v want [text.txt]", got)
	}
	if report.SkippedFiles != 3 {
		t.Fatalf("skipped = %d want 3 (binary・巨大・symlink)", report.SkippedFiles)
	}
}

func TestSearchIgnoresFilesInvisibleToGitEnumeration(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "text.txt"), "needle\n")
	commitAll(t, dir, "init")
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skipf("FIFOを作成できません: %v", err)
	}

	// gitはuntracked列挙にFIFOを出さないため、検索もfingerprintも影響を受けない。
	report, err := Search(context.Background(), dir, "needle", Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultPaths(report); !reflect.DeepEqual(got, []string{"text.txt"}) {
		t.Fatalf("results = %v want [text.txt]", got)
	}
	if report.IndexedFiles != 1 || report.SkippedFiles != 0 {
		t.Fatalf("indexed=%d skipped=%d want 1/0 (FIFOはgit列挙上不可視)", report.IndexedFiles, report.SkippedFiles)
	}
}

func TestSearchIgnoresGitignoredGeneratedFiles(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, ".gitignore"), "*.gen\n")
	writeTestFile(t, filepath.Join(dir, "real.txt"), "needle\n")
	commitAll(t, dir, "init")
	writeTestFile(t, filepath.Join(dir, "generated.gen"), "needle\n")

	report, err := Search(context.Background(), dir, "needle", Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultPaths(report); !reflect.DeepEqual(got, []string{"real.txt"}) {
		t.Fatalf("results = %v want .gitignore対象を除く [real.txt]", got)
	}
}

func TestSearchExcludesDefaultDirectoriesEvenWhenTracked(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "node_modules", "pkg.js"), "needle\n")
	writeTestFile(t, filepath.Join(dir, "build", "out.js"), "needle\n")
	writeTestFile(t, filepath.Join(dir, "vendor"), "needle as regular file\n")
	writeTestFile(t, filepath.Join(dir, "sub", "node_modules"), "needle as nested regular file\n")
	writeTestFile(t, filepath.Join(dir, "real.txt"), "needle\n")
	commitAll(t, dir, "init")

	report, err := Search(context.Background(), dir, "needle", Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"real.txt", "vendor", "sub/node_modules"}
	if got := resultPaths(report); !reflect.DeepEqual(got, want) {
		t.Fatalf("results = %v want 除外directory配下を除く %v", got, want)
	}
}

func TestSearchExcludeDirsOptionAddsDirectories(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "generated", "g.txt"), "needle\n")
	writeTestFile(t, filepath.Join(dir, "real.txt"), "needle\n")
	commitAll(t, dir, "init")

	report, err := Search(context.Background(), dir, "needle", Options{DisableCache: true, ExcludeDirs: []string{"generated"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultPaths(report); !reflect.DeepEqual(got, []string{"real.txt"}) {
		t.Fatalf("results = %v want 追加除外適用後 [real.txt]", got)
	}
	for _, invalid := range []string{"", ".", "..", "a/b", "/abs"} {
		opts := Options{DisableCache: true, ExcludeDirs: []string{"ok", invalid}}
		if _, err := Search(context.Background(), dir, "needle", opts); !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("ExcludeDirs %q = %v want ErrInvalidOptions", invalid, err)
		}
	}
}

func TestSearchPathWeightControlsPathRanking(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "doc.txt"), "needle content\n")
	writeTestFile(t, filepath.Join(dir, "needle.txt"), "filler\n")
	commitAll(t, dir, "init")

	zero := 0.0
	weighted, err := Search(context.Background(), dir, "needle", Options{DisableCache: true, PathWeight: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultPaths(weighted); !reflect.DeepEqual(got, []string{"doc.txt"}) {
		t.Fatalf("PathWeight=0のresults = %v want path一致を含まない [doc.txt]", got)
	}

	huge := 1e6
	flipped, err := Search(context.Background(), dir, "needle", Options{DisableCache: true, PathWeight: &huge})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultPaths(flipped); got[0] != "needle.txt" {
		t.Fatalf("PathWeight=1e6の先頭 = %v want needle.txt", got)
	}

	definition, err := Search(context.Background(), dir, "needle", Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range definition.Results {
		if result.Score != result.ContentScore+result.PathScore {
			t.Fatalf("Score=ContentScore+PathScoreが崩れています: %+v", result)
		}
	}
	byPath := map[string]Result{}
	for _, result := range definition.Results {
		byPath[result.Path] = result
	}
	if byPath["needle.txt"].PathScore <= 0 {
		t.Fatalf("既定PathWeight=0.5でpath寄与が消えています: %+v", byPath["needle.txt"])
	}

	for _, invalid := range []float64{-1, math.NaN(), math.Inf(1)} {
		weight := invalid
		if _, err := Search(context.Background(), dir, "needle", Options{DisableCache: true, PathWeight: &weight}); !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("PathWeight=%v = %v want ErrInvalidOptions", invalid, err)
		}
	}
}
