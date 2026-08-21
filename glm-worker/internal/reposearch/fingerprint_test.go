package reposearch

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// freshnessCaseはproduction Search()経由のcache freshness境界1件分。初回Searchで
// cacheを書き、状態変化後に再Searchする。freshnessはcache結果と DisableCacheの
// 新規Search結果の完全一致(変化の過不足ない反映)と、変化の種類に応じたCacheStatusの
// 両方で判定する。fingerprintがcorpus軸を見落とすと結果不一致として、corpus外まで
// 見ると不必要なrebuiltとして直接検出される。
type freshnessCase struct {
	name string
	// setupは初回Search前のrepo準備。
	setup func(t *testing.T, dir string)
	// mutateは初回Search後の状態変化。
	mutate func(t *testing.T, dir string)
	// expectRebuiltは corpus が変化する変化か。trueはrebuilt必須、falseはhit必須、
	// nilはgit diff由来の安全な過剰再構築(tracked binary変更等)を許し結果一致だけ見る。
	expectRebuilt *bool
	// wantPathsは変化後の結果path一覧。
	wantPaths []string
}

func rebuiltExpected() *bool {
	rebuilt := true
	return &rebuilt
}

func hitExpected() *bool {
	rebuilt := false
	return &rebuilt
}

func TestSearchFreshnessTracksCorpusBoundaries(t *testing.T) {
	cases := []freshnessCase{
		{
			name: "tracked searchable content change rebuilds",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				commitAll(t, dir, "init")
			},
			mutate: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle changed\n")
			},
			expectRebuilt: rebuiltExpected(),
			wantPaths:     []string{"a.txt"},
		},
		{
			name: "tracked searchable deletion rebuilds",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				writeTestFile(t, filepath.Join(dir, "b.txt"), "needle two\n")
				commitAll(t, dir, "init")
			},
			mutate: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
					t.Fatal(err)
				}
			},
			expectRebuilt: rebuiltExpected(),
			wantPaths:     []string{"b.txt"},
		},
		{
			name: "tracked file named like exclude dir stays in corpus",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "vendor"), "needle regular file\n")
				commitAll(t, dir, "init")
			},
			mutate: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "vendor"), "needle changed\n")
			},
			expectRebuilt: rebuiltExpected(),
			wantPaths:     []string{"vendor"},
		},
		{
			name: "tracked change under default excluded dir keeps cache",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "real.txt"), "needle\n")
				writeTestFile(t, filepath.Join(dir, "vendor", "lib.go"), "needle vendored\n")
				commitAll(t, dir, "init")
			},
			mutate: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "vendor", "lib.go"), "needle vendored changed\n")
			},
			expectRebuilt: hitExpected(),
			wantPaths:     []string{"real.txt"},
		},
		{
			name: "tracked change under nested excluded dir never returns stale",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "real.txt"), "needle\n")
				writeTestFile(t, filepath.Join(dir, "sub", "node_modules", "x.js"), "needle\n")
				commitAll(t, dir, "init")
			},
			mutate: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "sub", "node_modules", "x.js"), "needle changed\n")
			},
			expectRebuilt: nil,
			wantPaths:     []string{"real.txt"},
		},
		{
			name: "tracked binary change never returns stale",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "text.txt"), "needle\n")
				writeTestFile(t, filepath.Join(dir, "data.bin"), "needle\x00payload one\n")
				commitAll(t, dir, "init")
			},
			mutate: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "data.bin"), "needle\x00payload two\n")
			},
			expectRebuilt: nil,
			wantPaths:     []string{"text.txt"},
		},
		{
			name: "untracked searchable add rebuilds",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				commitAll(t, dir, "init")
			},
			mutate: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "u.txt"), "needle untracked\n")
			},
			expectRebuilt: rebuiltExpected(),
			wantPaths:     []string{"a.txt", "u.txt"},
		},
		{
			name: "untracked searchable content change rebuilds",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				writeTestFile(t, filepath.Join(dir, "u.txt"), "needle untracked\n")
				commitAll(t, dir, "init")
			},
			mutate: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "u.txt"), "needle changed\n")
			},
			expectRebuilt: rebuiltExpected(),
			wantPaths:     []string{"a.txt", "u.txt"},
		},
		{
			name: "untracked searchable removal rebuilds",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				writeTestFile(t, filepath.Join(dir, "u.txt"), "needle untracked\n")
				commitAll(t, dir, "init")
			},
			mutate: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "u.txt")); err != nil {
					t.Fatal(err)
				}
			},
			expectRebuilt: rebuiltExpected(),
			wantPaths:     []string{"a.txt"},
		},
		{
			name: "untracked binary rewrite keeps cache",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				commitAll(t, dir, "init")
				writeTestFile(t, filepath.Join(dir, "u.bin"), "needle\x00payload one\n")
			},
			mutate: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "u.bin"), "needle\x00payload two\n")
			},
			expectRebuilt: hitExpected(),
			wantPaths:     []string{"a.txt"},
		},
		{
			name: "untracked binary to text rebuilds",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				commitAll(t, dir, "init")
				writeTestFile(t, filepath.Join(dir, "u.bin"), "needle\x00payload one\n")
			},
			mutate: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "u.bin"), "needle now text\n")
			},
			expectRebuilt: rebuiltExpected(),
			wantPaths:     []string{"a.txt", "u.bin"},
		},
		{
			name: "untracked oversize rewrite keeps cache",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				commitAll(t, dir, "init")
				writeTestFile(t, filepath.Join(dir, "u.log"), "needle "+strings.Repeat("x", maxFileBytes)+"\n")
			},
			mutate: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "u.log"), "needle "+strings.Repeat("y", maxFileBytes)+"\n")
			},
			expectRebuilt: hitExpected(),
			wantPaths:     []string{"a.txt"},
		},
		{
			name: "untracked oversize to searchable rebuilds",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				commitAll(t, dir, "init")
				writeTestFile(t, filepath.Join(dir, "u.log"), "needle "+strings.Repeat("x", maxFileBytes)+"\n")
			},
			mutate: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "u.log"), "needle small now\n")
			},
			expectRebuilt: rebuiltExpected(),
			wantPaths:     []string{"a.txt", "u.log"},
		},
		{
			name: "untracked symlink retarget keeps cache",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				writeTestFile(t, filepath.Join(dir, "target.txt"), "x\n")
				commitAll(t, dir, "init")
				if err := os.Symlink("target.txt", filepath.Join(dir, "link.txt")); err != nil {
					t.Fatal(err)
				}
			},
			mutate: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "link.txt")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("a.txt", filepath.Join(dir, "link.txt")); err != nil {
					t.Fatal(err)
				}
			},
			expectRebuilt: hitExpected(),
			wantPaths:     []string{"a.txt"},
		},
		{
			name: "untracked symlink to regular rebuilds",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				commitAll(t, dir, "init")
				if err := os.Symlink("a.txt", filepath.Join(dir, "link.txt")); err != nil {
					t.Fatal(err)
				}
			},
			mutate: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "link.txt")); err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, filepath.Join(dir, "link.txt"), "needle regular now\n")
			},
			expectRebuilt: rebuiltExpected(),
			wantPaths:     []string{"a.txt", "link.txt"},
		},
		{
			name: "untracked file under default excluded dir keeps cache",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				commitAll(t, dir, "init")
			},
			mutate: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "node_modules", "u.js"), "needle ignored corpus\n")
			},
			expectRebuilt: hitExpected(),
			wantPaths:     []string{"a.txt"},
		},
		{
			name: "nested untracked repo appears without error and keeps cache",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				commitAll(t, dir, "init")
			},
			mutate: func(t *testing.T, dir string) {
				nested := filepath.Join(dir, "inner")
				if err := os.MkdirAll(nested, 0o755); err != nil {
					t.Fatal(err)
				}
				gitRun(t, "", "init", "--quiet", nested)
				writeTestFile(t, filepath.Join(nested, "b.txt"), "needle nested\n")
			},
			expectRebuilt: hitExpected(),
			wantPaths:     []string{"a.txt"},
		},
		{
			name: "nested untracked repo removal keeps cache",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				commitAll(t, dir, "init")
				nested := filepath.Join(dir, "inner")
				if err := os.MkdirAll(nested, 0o755); err != nil {
					t.Fatal(err)
				}
				gitRun(t, "", "init", "--quiet", nested)
				writeTestFile(t, filepath.Join(nested, "b.txt"), "needle nested\n")
			},
			mutate: func(t *testing.T, dir string) {
				if err := os.RemoveAll(filepath.Join(dir, "inner")); err != nil {
					t.Fatal(err)
				}
			},
			expectRebuilt: hitExpected(),
			wantPaths:     []string{"a.txt"},
		},
		{
			name: "nested untracked repo content change keeps cache",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				commitAll(t, dir, "init")
				nested := filepath.Join(dir, "inner")
				if err := os.MkdirAll(nested, 0o755); err != nil {
					t.Fatal(err)
				}
				gitRun(t, "", "init", "--quiet", nested)
				writeTestFile(t, filepath.Join(nested, "b.txt"), "needle nested\n")
			},
			mutate: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "inner", "b.txt"), "needle nested changed\n")
				writeTestFile(t, filepath.Join(dir, "inner", "c.txt"), "needle nested added\n")
			},
			expectRebuilt: hitExpected(),
			wantPaths:     []string{"a.txt"},
		},
		{
			name: "gitignore hiding untracked searchable file rebuilds",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
				commitAll(t, dir, "init")
				writeTestFile(t, filepath.Join(dir, "u.txt"), "needle untracked\n")
			},
			mutate: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, ".gitignore"), "u.txt\n")
			},
			expectRebuilt: rebuiltExpected(),
			wantPaths:     []string{"a.txt"},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			dir := initRepo(t)
			tt.setup(t, dir)
			opts := Options{CacheRoot: t.TempDir()}

			if first := searchNeedle(t, dir, opts); first.CacheStatus != CacheStatusRebuilt {
				t.Fatalf("初回status = %q want rebuilt", first.CacheStatus)
			}
			tt.mutate(t, dir)
			report := searchNeedle(t, dir, opts)
			fresh := searchNeedle(t, dir, Options{DisableCache: true})

			if !reflect.DeepEqual(report.Results, fresh.Results) {
				t.Fatalf("cache結果と新規検索が不一致(staleまたは誤反映):\ncache: %v\nfresh: %v", report.Results, fresh.Results)
			}
			if tt.expectRebuilt != nil {
				wantStatus := CacheStatusHit
				if *tt.expectRebuilt {
					wantStatus = CacheStatusRebuilt
				}
				if report.CacheStatus != wantStatus {
					t.Fatalf("status = %q want %q", report.CacheStatus, wantStatus)
				}
			}
			if got := resultPaths(report); !reflect.DeepEqual(got, tt.wantPaths) {
				t.Fatalf("results = %v want %v", got, tt.wantPaths)
			}
		})
	}
}

// 追加除外directoryも既定除外と同じくcorpus外であり、配下のtracked変更はcacheを
// 無効化しない。除外policy違いのcache再利用判定は別testで固定済み。
func TestSearchFreshnessIgnoresUserExcludedDirContent(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "real.txt"), "needle\n")
	writeTestFile(t, filepath.Join(dir, "generated", "g.txt"), "needle generated\n")
	commitAll(t, dir, "init")
	opts := Options{CacheRoot: t.TempDir(), ExcludeDirs: []string{"generated"}}

	if first := searchNeedle(t, dir, opts); first.CacheStatus != CacheStatusRebuilt {
		t.Fatalf("初回status = %q want rebuilt", first.CacheStatus)
	}
	writeTestFile(t, filepath.Join(dir, "generated", "g.txt"), "needle generated changed\n")
	report := searchNeedle(t, dir, opts)
	if report.CacheStatus != CacheStatusHit {
		t.Fatalf("追加除外directory配下の変更後status = %q want hit", report.CacheStatus)
	}
	if got := resultPaths(report); !reflect.DeepEqual(got, []string{"real.txt"}) {
		t.Fatalf("results = %v want [real.txt]", got)
	}
}

// submodule配下は検索対象外。pointer移動(内部commit)も検索corpusを変えないため
// cacheを再利用し、submodule内部の文書は結果へ出ない。
func TestSearchTreatsSubmoduleOutsideCorpus(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "real.txt"), "needle\n")
	commitAll(t, dir, "init")
	subSource := t.TempDir()
	gitRun(t, "", "init", "--quiet", "--initial-branch=main", subSource)
	writeTestFile(t, filepath.Join(subSource, "s.txt"), "needle inside submodule\n")
	commitAll(t, subSource, "sub init")
	// local pathのsubmodule登録はgit 2.38.1以降既定でfile transportを拒否するため
	// このtest実行に限り許可する。
	gitRun(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", "--quiet", subSource, "deps/subm")
	commitAll(t, dir, "add submodule")
	opts := Options{CacheRoot: t.TempDir()}

	first := searchNeedle(t, dir, opts)
	if got := resultPaths(first); !reflect.DeepEqual(got, []string{"real.txt"}) {
		t.Fatalf("results = %v want submodule配下を除く [real.txt]", got)
	}

	writeTestFile(t, filepath.Join(dir, "deps", "subm", "s2.txt"), "needle second\n")
	gitRun(t, filepath.Join(dir, "deps", "subm"), "add", "-A")
	gitRun(t, filepath.Join(dir, "deps", "subm"), "commit", "--quiet", "-m", "s2")
	gitRun(t, dir, "add", "deps/subm")

	report := searchNeedle(t, dir, opts)
	fresh := searchNeedle(t, dir, Options{DisableCache: true})
	if !reflect.DeepEqual(report.Results, fresh.Results) {
		t.Fatalf("cache結果と新規検索が不一致:\ncache: %v\nfresh: %v", report.Results, fresh.Results)
	}
	if got := resultPaths(report); !reflect.DeepEqual(got, []string{"real.txt"}) {
		t.Fatalf("results = %v want submodule内部を含まない [real.txt]", got)
	}
}
