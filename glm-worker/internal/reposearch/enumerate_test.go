package reposearch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func defaultTestExcludeDirs(t *testing.T) map[string]bool {
	t.Helper()
	dirs, err := resolveExcludeDirs(nil)
	if err != nil {
		t.Fatal(err)
	}
	return dirs
}

func TestEnumerateTracksWorkingTreeFiles(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "a\n")
	writeTestFile(t, filepath.Join(dir, ".gitignore"), "gen/\n")
	commitAll(t, dir, "init")
	writeTestFile(t, filepath.Join(dir, "new.txt"), "new\n")
	writeTestFile(t, filepath.Join(dir, "gen", "generated.txt"), "generated\n")

	paths, err := enumerateFiles(context.Background(), dir, defaultTestExcludeDirs(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".gitignore", "a.txt", "new.txt"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("enumerate = %v want %v", paths, want)
	}
}

func TestEnumerateExcludesTrackedSymlinkAndSubmoduleGitlink(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "a\n")
	if err := os.Symlink("a.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "a.txt", "link.txt")
	gitRun(t, dir, "update-index", "--add", "--cacheinfo", "160000", "0000000000000000000000000000000000000001", "vendored/sub")
	commitAll(t, dir, "init")

	paths, err := enumerateFiles(context.Background(), dir, defaultTestExcludeDirs(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.txt"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("enumerate = %v want %v", paths, want)
	}
}

func TestEnumerateExcludesNestedRepository(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "a\n")
	commitAll(t, dir, "init")
	nested := filepath.Join(dir, "inner")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, "", "init", "--quiet", nested)
	writeTestFile(t, filepath.Join(nested, "b.txt"), "b\n")

	paths, err := enumerateFiles(context.Background(), dir, defaultTestExcludeDirs(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.txt"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("enumerate = %v want %v", paths, want)
	}
}

func TestEnumerateListsIndexViewIncludingWorktreeDeleted(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "old.txt"), "old\n")
	writeTestFile(t, filepath.Join(dir, "keep.txt"), "keep\n")
	commitAll(t, dir, "init")
	if err := os.Remove(filepath.Join(dir, "old.txt")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "mv", "keep.txt", "renamed.txt")

	paths, err := enumerateFiles(context.Background(), dir, defaultTestExcludeDirs(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"old.txt", "renamed.txt"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("enumerate = %v want %v", paths, want)
	}
}

func TestEnumerateWithoutCommitListsUntracked(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "u.txt"), "u\n")

	paths, err := enumerateFiles(context.Background(), dir, defaultTestExcludeDirs(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"u.txt"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("enumerate = %v want %v", paths, want)
	}
}

func TestExcludedPathAppliesToDirectorySegmentsOnly(t *testing.T) {
	excludes := map[string]bool{"node_modules": true, ".git": true}
	allowed := []string{"node_modules", "sub/node_modules", "a/.git", "nodules/x.txt"}
	for _, rel := range allowed {
		if excludedPath(rel, excludes) {
			t.Fatalf("%sはfile名同名のため除外されるべきではありません", rel)
		}
	}
	blocked := []string{"node_modules/x.txt", "sub/node_modules/y.js", ".git/config", "a/.git/HEAD"}
	for _, rel := range blocked {
		if !excludedPath(rel, excludes) {
			t.Fatalf("%sはdirectory区間のため除外されるべきです", rel)
		}
	}
}

func TestResolveExcludeDirsUnionsWithDefaults(t *testing.T) {
	dirs, err := resolveExcludeDirs([]string{"generated", "generated"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range append(defaultExcludeDirs, "generated") {
		if !dirs[name] {
			t.Fatalf("除外集合に%sがありません", name)
		}
	}
}

func TestJoinWithinRootRejectsEscape(t *testing.T) {
	root := "/repo"
	for _, rel := range []string{"a.txt", "dir/b.txt"} {
		if _, err := joinWithinRoot(root, rel); err != nil {
			t.Fatalf("%sは許可されるべきです: %v", rel, err)
		}
	}
	for _, rel := range []string{"../escape", "dir/../../escape", "/absolute", "."} {
		if _, err := joinWithinRoot(root, rel); err == nil {
			t.Fatalf("%sは拒否されるべきです", rel)
		}
	}
}

func TestEnumerateContextCancelAborts(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "a\n")
	commitAll(t, dir, "init")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := enumerateFiles(ctx, dir, defaultTestExcludeDirs(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v want context.Canceled", err)
	}
}
