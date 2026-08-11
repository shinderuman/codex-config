package state

import (
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
	st.RecordPacketCompaction()
	st.RecordSolPacket(packet.FromLines([]string{
		"STATUS: PASS",
		"RISK: LOW",
	}))
	st.RecordSolPacket(packet.FromLines([]string{
		"STATUS: NEEDS_SOL_DECISION",
		"RISK: HIGH",
	}))
	st.RecordSolPacket(packet.FromLines([]string{
		"STATUS: NEEDS_SOL_REVIEW",
		"RISK: HIGH",
	}))

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
	if stats.PacketCompactions != 1 || stats.PassPackets != 1 || stats.NeedsSolDecisionPackets != 1 || stats.NeedsSolReviewPackets != 1 || stats.SolPacketBytes == 0 {
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
