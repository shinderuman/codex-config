package state

import (
	"os"
	"os/exec"
	"path/filepath"
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

func initCommittedRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, "", "init", "--quiet", dir)
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "tracked.txt")
	gitRun(t, dir, "commit", "--quiet", "-m", "init")
	return dir
}

func TestCaptureGitSnapshotReflectsStateAxes(t *testing.T) {
	tests := []struct {
		name             string
		mutate           func(t *testing.T, dir string)
		wantHeadChange   bool
		wantIndexChanged bool
		wantWorktree     bool
	}{
		{
			name:         "unstaged tracked change",
			mutate:       mutateUnstagedTracked,
			wantWorktree: true,
		},
		{
			name:             "staged new file only",
			mutate:           mutateStagedNewFile,
			wantIndexChanged: true,
		},
		{
			name:         "untracked file added",
			mutate:       mutateUntrackedAdded,
			wantWorktree: true,
		},
		{
			name:           "head moved via empty commit",
			mutate:         mutateEmptyCommit,
			wantHeadChange: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := initCommittedRepo(t)
			base, err := CaptureGitSnapshot(dir)
			if err != nil {
				t.Fatal(err)
			}
			tt.mutate(t, dir)
			after, err := CaptureGitSnapshot(dir)
			if err != nil {
				t.Fatal(err)
			}
			if headChanged := base.Head != after.Head; headChanged != tt.wantHeadChange {
				t.Errorf("head change=%v want %v (base=%q after=%q)", headChanged, tt.wantHeadChange, base.Head, after.Head)
			}
			if indexChanged := base.IndexDigest != after.IndexDigest; indexChanged != tt.wantIndexChanged {
				t.Errorf("index change=%v want %v", indexChanged, tt.wantIndexChanged)
			}
			if worktreeChanged := base.WorktreeDigest != after.WorktreeDigest; worktreeChanged != tt.wantWorktree {
				t.Errorf("worktree change=%v want %v", worktreeChanged, tt.wantWorktree)
			}
		})
	}
}

func mutateUnstagedTracked(t *testing.T, dir string) {
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("modified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mutateStagedNewFile(t *testing.T, dir string) {
	if err := os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "staged.txt")
}

func mutateUntrackedAdded(t *testing.T, dir string) {
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mutateEmptyCommit(t *testing.T, dir string) {
	gitRun(t, dir, "commit", "--quiet", "--allow-empty", "-m", "empty")
}

func TestCaptureGitSnapshotDetectsUntrackedContentChange(t *testing.T) {
	dir := initCommittedRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := CaptureGitSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := CaptureGitSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if base.Head != after.Head || base.IndexDigest != after.IndexDigest {
		t.Fatalf("head/indexが変化しています: base=%#v after=%#v", base, after)
	}
	if base.WorktreeDigest == after.WorktreeDigest {
		t.Fatalf("untracked内容変更へworktree digestが反応しません: base=%#v after=%#v", base, after)
	}
}

func TestCaptureGitSnapshotFailsOutsideRepo(t *testing.T) {
	if _, err := CaptureGitSnapshot(t.TempDir()); err == nil {
		t.Fatal("git repo外でのsnapshot取得は失敗する必要があります")
	}
}

func TestCaptureGitSnapshotHandlesNoCommitRepo(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, "", "init", "--quiet", dir)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := CaptureGitSnapshot(dir)
	if err != nil {
		t.Fatalf("commit未作成repoではsnapshot取得できる必要があります: %v", err)
	}
	if snap.Head != "" {
		t.Fatalf("commit未作成repoのHeadは空文字の想定: %q", snap.Head)
	}
	if snap.IndexDigest == "" || snap.WorktreeDigest == "" {
		t.Fatalf("commit未作成repoでもindex/worktree digestが取得できる必要があります: %#v", snap)
	}
}

func TestSnapshotSaveLoadAndComparison(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	workerEnd := GitSnapshot{Head: "h1", IndexDigest: "i1", WorktreeDigest: "w1"}
	reviewStart := GitSnapshot{Head: "h1", IndexDigest: "i1", WorktreeDigest: "w1"}
	if err := st.SaveWorkerEndSnapshot(workerEnd); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveReviewStartSnapshot(reviewStart); err != nil {
		t.Fatal(err)
	}
	loadedWorker, err := st.LoadWorkerEndSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !EqualGitSnapshot(loadedWorker, workerEnd) {
		t.Fatalf("worker-end round-trip: %#v", loadedWorker)
	}
	loadedReview, err := st.LoadReviewStartSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !EqualGitSnapshot(loadedReview, reviewStart) {
		t.Fatalf("review-start round-trip: %#v", loadedReview)
	}

	matched := CompareGitSnapshot(workerEnd, reviewStart, SnapshotStageReviewStart, "")
	if !matched.Matched || !matched.HeadMatch || !matched.IndexMatch || !matched.WorktreeMatch {
		t.Fatalf("一致snapshotのcomparison = %#v", matched)
	}

	differ := reviewStart
	differ.Head = "h2"
	differed := CompareGitSnapshot(workerEnd, differ, SnapshotStageReviewStart, "head moved")
	if differed.Matched || differed.HeadMatch || !differed.IndexMatch || !differed.WorktreeMatch {
		t.Fatalf("不一致snapshotのcomparison = %#v", differed)
	}
	if differed.Reason != "head moved" {
		t.Fatalf("reason = %q", differed.Reason)
	}
	if err := st.SaveSnapshotComparison(differed); err != nil {
		t.Fatal(err)
	}
	loadedComparison, err := st.LoadSnapshotComparison()
	if err != nil {
		t.Fatal(err)
	}
	if loadedComparison.Matched || loadedComparison.Stage != SnapshotStageReviewStart {
		t.Fatalf("comparison round-trip = %#v", loadedComparison)
	}
}

func TestLoadMissingSnapshotsError(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if _, err := st.LoadWorkerEndSnapshot(); err == nil {
		t.Fatal("未保存worker-end snapshotの読込は失敗する必要があります")
	}
	if _, err := st.LoadReviewStartSnapshot(); err == nil {
		t.Fatal("未保存review-start snapshotの読込は失敗する必要があります")
	}
}

func makeSymlink(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureGitSnapshotSymlinkTargetChangeDetected(t *testing.T) {
	dir := initCommittedRepo(t)
	makeSymlink(t, filepath.Join(dir, "link.txt"), "target-a")
	base, err := CaptureGitSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	makeSymlink(t, filepath.Join(dir, "link.txt"), "target-b")
	after, err := CaptureGitSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if base.WorktreeDigest == after.WorktreeDigest {
		t.Fatalf("symlink target文字列変更へworktree digestが反応しません: base=%q after=%q", base.WorktreeDigest, after.WorktreeDigest)
	}
}

func TestCaptureGitSnapshotIgnoresExternalSymlinkContent(t *testing.T) {
	dir := initCommittedRepo(t)
	externalDir := t.TempDir()
	externalFile := filepath.Join(externalDir, "real.txt")
	if err := os.WriteFile(externalFile, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	makeSymlink(t, filepath.Join(dir, "link.txt"), externalFile)
	base, err := CaptureGitSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(externalFile, []byte("v2-different-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := CaptureGitSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if base.WorktreeDigest != after.WorktreeDigest {
		t.Fatalf("repo外target内容変更へdigestが非依存でない: base=%q after=%q", base.WorktreeDigest, after.WorktreeDigest)
	}
}

func TestBuildWorktreeDigestRejectsSpecialFile(t *testing.T) {
	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "fifo.txt")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Skipf("mkfifo不可: %v", err)
	}
	_, err := buildWorktreeDigest(nil, []byte("fifo.txt\x00"), dir)
	if err == nil {
		t.Fatal("FIFO等の特殊fileはsnapshot失敗にする必要があります")
	}
}

func TestBuildWorktreeDigestFailsOnEnumeratedFileDisappearance(t *testing.T) {
	dir := t.TempDir()
	_, err := buildWorktreeDigest(nil, []byte("gone.txt\x00"), dir)
	if err == nil {
		t.Fatal("列挙後に消失したpathは空内容ではなく取得失敗にする必要があります")
	}
}

func TestBuildWorktreeDigestRejectsPathEscape(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "inside.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(dir, filepath.Join(outsideDir, "secret.txt"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = buildWorktreeDigest(nil, []byte(rel+"\x00"), dir)
	if err == nil {
		t.Fatal("repo境界を越えるpathは拒否する必要があります")
	}
}
