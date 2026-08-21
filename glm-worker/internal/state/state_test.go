package state

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/packet"
)

func TestNewStateStoreInitializesRepositoryState(t *testing.T) {
	base := t.TempDir()
	st, err := NewStateStore(config.AppConfig{
		StateBase: base,
		RepoHash:  "repository-hash",
		RepoRoot:  "/tmp/repository",
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.Path("task.id") != filepath.Join(base, "repository-hash", "task.id") {
		t.Fatalf("state path = %q", st.Path("task.id"))
	}
	if st.LockPath() != filepath.Join(base, "repository-hash", "lock") {
		t.Fatalf("lock path = %q", st.LockPath())
	}
	if root := st.ReadOr("repo-root", "missing"); root != "/tmp/repository" {
		t.Fatalf("repo-root = %q", root)
	}
}

func TestSessionIDPersists(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}

	first, ready, err := st.SessionID(WorkerRole)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("new session should not be ready")
	}

	if err := st.MarkReady(WorkerRole); err != nil {
		t.Fatal(err)
	}

	second, ready, err := st.SessionID(WorkerRole)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("session should be ready")
	}
	if first != second {
		t.Fatalf("session changed: %s -> %s", first, second)
	}

	if filepath.Base(st.Path("worker.id")) != "worker.id" {
		t.Fatal("unexpected state path")
	}
}

func TestStartNewTaskRotatesSessions(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}

	firstTask, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	firstWorker, _, err := st.SessionID(WorkerRole)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkReady(WorkerRole); err != nil {
		t.Fatal(err)
	}

	secondTask, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	secondWorker, ready, err := st.SessionID(WorkerRole)
	if err != nil {
		t.Fatal(err)
	}

	if firstTask == secondTask {
		t.Fatal("task ID was not rotated")
	}
	if firstWorker == secondWorker {
		t.Fatal("worker session was not rotated")
	}
	if ready {
		t.Fatal("new task worker session must start unready")
	}
	if st.TaskStatus() != TaskStatusActive {
		t.Fatalf("task status = %q", st.TaskStatus())
	}

	all, err := st.AllTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("task stats count = %d, want 2", len(all))
	}
	statsByTask := make(map[string]TaskStats, len(all))
	for _, stats := range all {
		statsByTask[stats.TaskID] = stats
	}
	if statsByTask[firstTask].ArchivedAt == nil {
		t.Fatalf("first task stats were not archived: %#v", statsByTask[firstTask])
	}
	if statsByTask[secondTask].ArchivedAt != nil {
		t.Fatalf("second task stats are invalid: %#v", statsByTask[secondTask])
	}
}

func TestTaskStatsRecordCounters(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	st.RecordModelCall(WorkerRole, "opus")
	st.RecordModelCall(ReviewerRole, "haiku")
	st.RecordModelDuration("opus", 1500*time.Millisecond)
	st.RecordDecision()
	st.RecordFix()
	st.RecordResume()
	st.RecordAutoFix()
	st.RecordRateLimit("haiku")
	st.RecordResultCorrection()
	st.RecordStructuredRetryExhausted()
	st.RecordSolResult(packet.Result{Status: packet.StatusPass, Risk: packet.RiskLow})
	st.RecordSolResult(packet.Result{Status: packet.StatusNeedsSolDecision, Risk: packet.RiskHigh})
	st.RecordSolResult(packet.Result{Status: packet.StatusNeedsSolReview, Risk: packet.RiskHigh})

	stats, err := st.loadTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.ModelCalls != 2 || stats.WorkerCalls != 1 || stats.ReviewerCalls != 1 {
		t.Fatalf("model counters = %#v", stats)
	}
	if stats.ModelCallsByAlias["opus"] != 1 || stats.ModelCallsByAlias["haiku"] != 1 || stats.ModelDurationMSByAlias["opus"] != 1500 || stats.RateLimitsByAlias["haiku"] != 1 {
		t.Fatalf("model alias counters = %#v", stats)
	}
	if stats.ResultCorrections != 1 || stats.StructuredRetryExhausted != 1 || stats.PassPackets != 1 || stats.NeedsSolDecisionPackets != 1 || stats.NeedsSolReviewPackets != 1 || stats.SolPacketBytes == 0 {
		t.Fatalf("packet counters = %#v", stats)
	}
	if stats.DecisionCommands != 1 || stats.FixCommands != 1 || stats.ResumeCommands != 1 || stats.AutoFixRounds != 1 || stats.RateLimits != 1 {
		t.Fatalf("workflow counters = %#v", stats)
	}
}

func TestTaskStatusDoesNotInferMissingCanonicalState(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if err := st.Write("task.id", "task-without-status"); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("last-review", "STATUS: NEEDS_SOL_REVIEW\nRISK: HIGH"); err != nil {
		t.Fatal(err)
	}
	if err := st.Touch("pending-decision"); err != nil {
		t.Fatal(err)
	}
	if status := st.TaskStatus(); status != TaskStatus("none") {
		t.Fatalf("task.statusなしで状態を推定しました: %q", status)
	}
}

func TestTaskStatsRebuildsMissingMirrorForCurrentTask(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if err := st.Write("task.id", "current-task"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(TaskStatusActive); err != nil {
		t.Fatal(err)
	}
	st.RecordModelCall(WorkerRole, "opus")

	stats, err := st.loadTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TaskID != "current-task" || stats.ModelCalls != 1 {
		t.Fatalf("recovered task stats = %#v", stats)
	}
}

func TestRemoveUnreadySessionOnlyRemovesUnreadyID(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if _, _, err := st.SessionID(WorkerRole); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveUnreadySession(WorkerRole); err != nil {
		t.Fatal(err)
	}
	if st.Exists("worker.id") {
		t.Fatal("unready session IDが残っています")
	}

	if _, _, err := st.SessionID(ReviewerRole); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkReady(ReviewerRole); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveUnreadySession(ReviewerRole); err != nil {
		t.Fatal(err)
	}
	if !st.Exists("reviewer.id") {
		t.Fatal("ready session IDを削除しました")
	}
}

func TestIsolationPolicyRoundTripAndDefaultEmpty(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if got := st.IsolationPolicy(); got != "" {
		t.Fatalf("default policy = %q, want empty", got)
	}
	if err := st.SetIsolationPolicy("claude-isolation-1"); err != nil {
		t.Fatal(err)
	}
	if got := st.IsolationPolicy(); got != "claude-isolation-1" {
		t.Fatalf("policy = %q", got)
	}
}

func TestResetSessionsForPolicyIsNoOpWhenCurrent(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if err := st.Write("worker.id", "worker-1"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkReady(WorkerRole); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("reviewer.id", "reviewer-1"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkReady(ReviewerRole); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsolationPolicy("claude-isolation-1"); err != nil {
		t.Fatal(err)
	}

	if err := st.ResetSessionsForPolicy("claude-isolation-1"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"worker.id", "worker.ready", "reviewer.id", "reviewer.ready"} {
		if !st.Exists(name) {
			t.Fatalf("policy一致時は%sを保持する必要があります", name)
		}
	}
}

func TestResetSessionsForPolicyClearsBothRolesOnStalePolicy(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if err := st.Write("worker.id", "worker-1"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkReady(WorkerRole); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("reviewer.id", "reviewer-1"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkReady(ReviewerRole); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsolationPolicy("claude-isolation-stale"); err != nil {
		t.Fatal(err)
	}

	if err := st.ResetSessionsForPolicy("claude-isolation-1"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"worker.id", "worker.ready", "reviewer.id", "reviewer.ready"} {
		if st.Exists(name) {
			t.Fatalf("policy不一致時は%sを破棄する必要があります", name)
		}
	}
}

func TestResetSessionsForPolicyClearsBothRolesOnMissingMarker(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if err := st.Write("worker.id", "worker-1"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkReady(WorkerRole); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("reviewer.id", "reviewer-1"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkReady(ReviewerRole); err != nil {
		t.Fatal(err)
	}

	if err := st.ResetSessionsForPolicy("claude-isolation-1"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"worker.id", "worker.ready", "reviewer.id", "reviewer.ready"} {
		if st.Exists(name) {
			t.Fatalf("marker欠落時は%sを破棄する必要があります", name)
		}
	}
}

func TestStartNewTaskClearsIsolationPolicy(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsolationPolicy("claude-isolation-stale"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	if got := st.IsolationPolicy(); got != "" {
		t.Fatalf("StartNewTask後のpolicy = %q, want empty", got)
	}
}

func TestCaptureGitBaselineAndDescription(t *testing.T) {
	repository := t.TempDir()
	command := exec.Command("git", "init", "--quiet", repository)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(repository, "untracked.txt"), []byte("content\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st := &StateStore{dir: t.TempDir()}
	if err := CaptureGitBaseline(config.AppConfig{RepoRoot: repository}, st); err != nil {
		t.Fatal(err)
	}
	status, err := st.Read("baseline-status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "untracked.txt") {
		t.Fatalf("baseline status = %q", status)
	}
	description := st.BaselineDescription()
	for _, name := range []string{"baseline-status", "baseline-worktree.patch", "baseline-index.patch"} {
		if !strings.Contains(description, st.Path(name)) {
			t.Fatalf("baseline descriptionに%sがありません: %s", name, description)
		}
	}
}

func TestCaptureGitBaselineClearsStaleFilesWhenGitFails(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	for _, name := range []string{"baseline-status", "baseline-worktree.patch", "baseline-index.patch"} {
		if err := st.Write(name, "stale"); err != nil {
			t.Fatal(err)
		}
	}

	if err := CaptureGitBaseline(config.AppConfig{RepoRoot: filepath.Join(t.TempDir(), "missing")}, st); err != nil {
		t.Fatal(err)
	}
	if st.BaselineDescription() != "none" {
		t.Fatalf("stale baselineが残っています: %s", st.BaselineDescription())
	}
}

func TestCaptureGitBaselineRecordsHeadWithCommit(t *testing.T) {
	repository := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "tester"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repository
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repository, "file.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	addCmd := exec.Command("git", "add", "file.txt")
	addCmd.Dir = repository
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	commitCmd := exec.Command("git", "commit", "--quiet", "-m", "base")
	commitCmd.Dir = repository
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = repository
	head, err := headCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	wantHead := strings.TrimSpace(string(head))

	st := &StateStore{dir: t.TempDir()}
	if err := CaptureGitBaseline(config.AppConfig{RepoRoot: repository}, st); err != nil {
		t.Fatal(err)
	}
	gotHead, err := st.Read("baseline-head")
	if err != nil {
		t.Fatalf("baseline-headが記録されていません: %v", err)
	}
	if gotHead != wantHead {
		t.Fatalf("baseline-head=%q want %q", gotHead, wantHead)
	}
}

func TestCaptureGitBaselineOmitsHeadOnNoCommitRepo(t *testing.T) {
	repository := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", repository)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	st := &StateStore{dir: t.TempDir()}
	if err := CaptureGitBaseline(config.AppConfig{RepoRoot: repository}, st); err != nil {
		t.Fatal(err)
	}
	if st.Exists("baseline-head") {
		head, _ := st.Read("baseline-head")
		t.Fatalf("commit無しrepoでbaseline-headが存在します: %q", head)
	}
}

func TestResolveRepoHeadDistinguishesUnbornFromCorrupt(t *testing.T) {
	t.Run("non-git directory is corrupt not unborn", func(t *testing.T) {
		_, unborn, err := resolveRepoHead(t.TempDir())
		if err == nil {
			t.Fatal("非git directoryはerrorになるべき")
		}
		if unborn {
			t.Fatal("非git directoryをunborn扱いしてはいけない")
		}
	})
	t.Run("unborn branch repo reports unborn without error", func(t *testing.T) {
		repository := t.TempDir()
		if out, err := exec.Command("git", "init", "--quiet", repository).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
		head, unborn, err := resolveRepoHead(repository)
		if err != nil {
			t.Fatalf("unborn repoでerror: %v", err)
		}
		if !unborn || head != "" {
			t.Fatalf("unborn repo=(%q,%v,nil) want (\"\",true,nil)", head, unborn)
		}
	})
	t.Run("committed repo resolves head", func(t *testing.T) {
		repository := t.TempDir()
		for _, args := range [][]string{
			{"init", "--quiet"},
			{"config", "user.email", "t@example.com"},
			{"config", "user.name", "tester"},
		} {
			cmd := exec.Command("git", args...)
			cmd.Dir = repository
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
		if err := os.WriteFile(filepath.Join(repository, "f.txt"), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "f.txt"}, {"commit", "--quiet", "-m", "x"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = repository
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
		head, unborn, err := resolveRepoHead(repository)
		if err != nil || unborn || head == "" {
			t.Fatalf("committed repo=(%q,%v,%v) want (sha,false,nil)", head, unborn, err)
		}
	})
	t.Run("detached HEAD with missing commit is error not unborn", func(t *testing.T) {
		repository := t.TempDir()
		for _, args := range [][]string{
			{"init", "--quiet"},
			{"config", "user.email", "t@example.com"},
			{"config", "user.name", "tester"},
		} {
			cmd := exec.Command("git", args...)
			cmd.Dir = repository
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
		if err := os.WriteFile(filepath.Join(repository, ".git", "HEAD"), []byte("deadbeefdeadbeefdeadbeefdeadbeefdeadbeef\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, unborn, err := resolveRepoHead(repository)
		if err == nil {
			t.Fatal("detached HEADが存在しないcommitを指す場合、errorになるべき")
		}
		if unborn {
			t.Fatal("detached HEADをunborn扱いしてはいけない")
		}
	})
	t.Run("corrupt symbolic HEAD is error not unborn", func(t *testing.T) {
		repository := t.TempDir()
		for _, args := range [][]string{
			{"init", "--quiet"},
			{"config", "user.email", "t@example.com"},
			{"config", "user.name", "tester"},
		} {
			cmd := exec.Command("git", args...)
			cmd.Dir = repository
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
		if err := os.WriteFile(filepath.Join(repository, ".git", "HEAD"), []byte("ref: not-under-refs-heads\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, unborn, err := resolveRepoHead(repository)
		if err == nil {
			t.Fatal("HEADがrefs/heads外のsymbolic targetを指す場合、errorになるべき")
		}
		if unborn {
			t.Fatal("refs/heads外のsymbolic HEADをunborn扱いしてはいけない")
		}
	})
	t.Run("detached HEAD pointing at tree object is error not head", func(t *testing.T) {
		repository := t.TempDir()
		for _, args := range [][]string{
			{"init", "--quiet"},
			{"config", "user.email", "t@example.com"},
			{"config", "user.name", "tester"},
			{"add", "."},
		} {
			cmd := exec.Command("git", args...)
			cmd.Dir = repository
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
		if err := os.WriteFile(filepath.Join(repository, "f.txt"), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "f.txt"}, {"commit", "--quiet", "-m", "x"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = repository
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
		treeCmd := exec.Command("git", "rev-parse", "HEAD^{tree}")
		treeCmd.Dir = repository
		tree, err := treeCmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repository, ".git", "HEAD"), bytes.TrimSpace(tree), 0o644); err != nil {
			t.Fatal(err)
		}
		_, unborn, err := resolveRepoHead(repository)
		if err == nil {
			t.Fatal("tree objectを指すdetached HEADはcommitへpeelできずerrorになるべき")
		}
		if unborn {
			t.Fatal("tree objectを指すdetached HEADをunborn扱いしてはいけない")
		}
	})
	t.Run("symbolic HEAD to loose ref with missing object is error not unborn", func(t *testing.T) {
		repository := t.TempDir()
		for _, args := range [][]string{
			{"init", "--quiet"},
			{"config", "user.email", "t@example.com"},
			{"config", "user.name", "tester"},
		} {
			cmd := exec.Command("git", args...)
			cmd.Dir = repository
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
		refsHeads := filepath.Join(repository, ".git", "refs", "heads")
		if err := os.MkdirAll(refsHeads, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(refsHeads, "broken"), []byte("feedfacefeedfacefeedfacefeedfacefeedface\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repository, ".git", "HEAD"), []byte("ref: refs/heads/broken\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, unborn, err := resolveRepoHead(repository)
		if err == nil {
			t.Fatal("missing objectを指すloose refへのsymbolic HEADはerrorになるべき")
		}
		if unborn {
			t.Fatal("missing objectのloose refを正当unborn扱いしてはいけない")
		}
	})
	t.Run("symbolic HEAD to empty loose ref is error not unborn", func(t *testing.T) {
		repository := t.TempDir()
		for _, args := range [][]string{
			{"init", "--quiet"},
			{"config", "user.email", "t@example.com"},
			{"config", "user.name", "tester"},
		} {
			cmd := exec.Command("git", args...)
			cmd.Dir = repository
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
		refsHeads := filepath.Join(repository, ".git", "refs", "heads")
		if err := os.MkdirAll(refsHeads, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(refsHeads, "empty"), []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repository, ".git", "HEAD"), []byte("ref: refs/heads/empty\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, unborn, err := resolveRepoHead(repository)
		if err == nil {
			t.Fatal("空loose refへのsymbolic HEADはerrorになるべき")
		}
		if unborn {
			t.Fatal("空loose refを正当unborn扱いしてはいけない")
		}
	})
}

func TestStartNewTaskClearsBaselineHead(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if err := st.Write("baseline-head", "previous-task-head"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	if st.Exists("baseline-head") {
		head, _ := st.Read("baseline-head")
		t.Fatalf("前taskのbaseline-headが残留しています: %q", head)
	}
}

func TestResetClearsBaselineHead(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if err := st.Write("baseline-head", "leftover-head"); err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(); err != nil {
		t.Fatal(err)
	}
	if st.Exists("baseline-head") {
		head, _ := st.Read("baseline-head")
		t.Fatalf("Reset後もbaseline-headが残留しています: %q", head)
	}
}
