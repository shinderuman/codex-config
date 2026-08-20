package workflow

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/runner"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

func gitIn(t *testing.T, repoRoot string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func initMutationRepo(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	gitIn(t, repoRoot, "init", "-q")
	gitIn(t, repoRoot, "config", "user.email", "test@example.com")
	gitIn(t, repoRoot, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(repoRoot, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repoRoot, "add", ".")
	gitIn(t, repoRoot, "commit", "-q", "-m", "base")
	return repoRoot
}

// mutatingRunnerはEdit/Write禁止のreviewerがBash相当でrepositoryを変更する事象を再現するfake runner。
type mutatingRunner struct {
	steps    []runnerStep
	prompts  []string
	models   []string
	repoRoot string
	mutate   func(repoRoot string) error
}

func (r *mutatingRunner) Run(
	role state.SessionRole,
	_ string,
	model string,
	readOnly bool,
	effort string,
	prompt string,
	outputPath string,
) (runner.RunResult, error) {
	r.prompts = append(r.prompts, prompt)
	r.models = append(r.models, model)
	step := r.steps[len(r.prompts)-1]
	if step.output != "" {
		if err := os.WriteFile(outputPath, []byte(step.output), 0o600); err != nil {
			return runner.RunResult{}, err
		}
	}
	result := step.result
	if result.SessionID == "" {
		result.SessionID = "test-session"
	}
	if result.Response == "" {
		result.Response = step.output
	}
	if role == state.ReviewerRole && step.runErr == nil && r.mutate != nil {
		if err := r.mutate(r.repoRoot); err != nil {
			return runner.RunResult{}, err
		}
	}
	return result, step.runErr
}

func (r *mutatingRunner) Probe(model string) (runner.ProbeResult, error) {
	return runner.ProbeResult{
		Response: runner.ProbeSentinel,
		Usage:    runner.TokenUsage{InputTokens: 1, OutputTokens: 1},
	}, nil
}

func newMutationWorkflow(t *testing.T, repoRoot string, steps []runnerStep, mutate func(string) error) (*Workflow, *mutatingRunner, *bytes.Buffer) {
	t.Helper()
	st := newStateStoreT(t)
	r := &mutatingRunner{repoRoot: repoRoot, steps: steps, mutate: mutate}
	out := &bytes.Buffer{}
	w := newMutationWorkflowShell(t, st)
	w.runner = r
	w.output = out
	w.config.RepoRoot = repoRoot
	w.captureSnapshot = state.CaptureGitSnapshot
	return w, r, out
}

// runner/output/RepoRoot/captureSnapshotは呼出側で実repoへ向ける。
func newMutationWorkflowShell(t *testing.T, st *state.StateStore) *Workflow {
	t.Helper()
	return newWorkflowT(t, st, &scriptedRunner{})
}

func requireReviewEndFailClosed(t *testing.T, w *Workflow, r *mutatingRunner, out *bytes.Buffer) {
	t.Helper()
	if w.state.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q want waiting-sol-review", w.state.TaskStatus())
	}
	pkt := lastPacketFromOutput(t, out.String())
	if pkt.Status() != "NEEDS_SOL_REVIEW" || pkt.Risk() != "HIGH" {
		t.Fatalf("packet = %s/%s want NEEDS_SOL_REVIEW/HIGH", pkt.Status(), pkt.Risk())
	}
	if !strings.Contains(out.String(), "reviewer実行中にrepository状態が変化") {
		t.Fatalf("review-end mismatch原因が出力されていません: %q", out.String())
	}
	if len(r.prompts) != 2 {
		t.Fatalf("mutation後はreview結果を採用せず追加model呼出しなし: calls=%d", len(r.prompts))
	}
	if _, err := w.state.LoadResumeCheckpoint(); err == nil {
		t.Fatal("fail closed後はresume checkpointを残さない")
	}
}

func mismatchEvent(t *testing.T, st *state.StateStore) state.ModelCallLog {
	t.Helper()
	for _, l := range taskLogs(t, st) {
		if l.Outcome == "snapshot_mismatch" && strings.HasSuffix(l.Phase, "review-end-snapshot-check") {
			return l
		}
	}
	t.Fatalf("review-endのsnapshot_mismatch eventがありません: %+v", phasesOf(taskLogs(t, st)))
	return state.ModelCallLog{}
}

// reviewerがtracked fileを変更した場合、PASSを採用せずfail closedし変更は保持される。
func TestReviewEndWorktreeMutationRejectsPass(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, r, out := newMutationWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}, func(root string) error {
		return os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("mutated\n"), 0o644)
	})

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	requireReviewEndFailClosed(t, w, r, out)
	content, err := os.ReadFile(filepath.Join(repoRoot, "tracked.txt"))
	if err != nil || string(content) != "mutated\n" {
		t.Fatalf("reviewer変更がrollbackまたは黙認されています: %q %v", content, err)
	}
	event := mismatchEvent(t, w.state)
	if event.Snapshot.Stage != string(state.SnapshotStageReviewEnd) || event.Snapshot.MismatchAxis != "worktree" {
		t.Fatalf("review-end mismatch記録 = %+v", event.Snapshot)
	}
	if stats := currentStats(t, w.state); stats.SnapshotMismatchByAxis["worktree"] != 1 {
		t.Fatalf("worktree軸集計 = %+v", stats.SnapshotMismatchByAxis)
	}
}

// reviewerがuntracked fileを作成した場合もworktree軸で拒否される。
func TestReviewEndUntrackedMutationRejectsPass(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, r, out := newMutationWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}, func(root string) error {
		return os.WriteFile(filepath.Join(root, "generated.go"), []byte("package x\n"), 0o644)
	})

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	requireReviewEndFailClosed(t, w, r, out)
	if _, err := os.Stat(filepath.Join(repoRoot, "generated.go")); err != nil {
		t.Fatalf("untracked変更が保持されていません: %v", err)
	}
	if event := mismatchEvent(t, w.state); event.Snapshot.MismatchAxis != "worktree" {
		t.Fatalf("mismatch axis = %q want worktree", event.Snapshot.MismatchAxis)
	}
}

// reviewerがindexへstageした場合、index軸で検出される。
func TestReviewEndIndexMutationRejectsPass(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, r, out := newMutationWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}, func(root string) error {
		if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("staged\n"), 0o644); err != nil {
			return err
		}
		gitIn(t, root, "add", "tracked.txt")
		return nil
	})

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	requireReviewEndFailClosed(t, w, r, out)
	event := mismatchEvent(t, w.state)
	if !strings.Contains(event.Snapshot.MismatchAxis, "index") {
		t.Fatalf("mismatch axis = %q want index含む", event.Snapshot.MismatchAxis)
	}
	if gitIn(t, repoRoot, "diff", "--cached", "--name-only") != "tracked.txt" {
		t.Fatal("staged変更が保持されていません")
	}
}

// reviewerがcommitした場合、head軸で検出される。
func TestReviewEndHeadMutationRejectsPass(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, r, out := newMutationWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}, func(root string) error {
		if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("committed\n"), 0o644); err != nil {
			return err
		}
		gitIn(t, root, "add", "tracked.txt")
		gitIn(t, root, "commit", "-q", "-m", "reviewer commit")
		return nil
	})

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	requireReviewEndFailClosed(t, w, r, out)
	if event := mismatchEvent(t, w.state); !strings.Contains(event.Snapshot.MismatchAxis, "head") {
		t.Fatalf("mismatch axis = %q want head含む", event.Snapshot.MismatchAxis)
	}
	if !strings.Contains(gitIn(t, repoRoot, "log", "-1", "--pretty=%s"), "reviewer commit") {
		t.Fatal("commitがrollbackされています")
	}
}

// FIX_REQUIREDも採用せずauto-fixを起こさない。
func TestReviewEndMutationRejectsFixRequired(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, r, out := newMutationWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: fixRequiredPacket()},
		{output: implementedPacket("auto fixed")},
	}, func(root string) error {
		return os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("mutated\n"), 0o644)
	})

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	requireReviewEndFailClosed(t, w, r, out)
	if stats := currentStats(t, w.state); stats.AutoFixRounds != 0 {
		t.Fatalf("auto-fixが発生しています: %d", stats.AutoFixRounds)
	}
}

// reviewer自身のNEEDS_SOL_REVIEW packetも採用せずwrapperのfail closed packetへ差し替える。
func TestReviewEndMutationRejectsNeedsSolReview(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, r, out := newMutationWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: needsSolReviewPacket()},
	}, func(root string) error {
		return os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("mutated\n"), 0o644)
	})

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	requireReviewEndFailClosed(t, w, r, out)
	if strings.Contains(out.String(), "STATUS: NEEDS_SOL_REVIEW\nRISK: HIGH\nSUMMARY: review\n") {
		t.Fatalf("reviewer自身のNEEDS_SOL_REVIEW packetがそのまま出力されています: %q", out.String())
	}
}

// mutation無しの通常reviewはPASS採用が維持され、review-end一致comparisonが残る。
func TestReviewEndMatchProceedsToPass(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, _, out := newMutationWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}, nil)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if w.state.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("status = %q", w.state.TaskStatus())
	}
	if lastPacketFromOutput(t, out.String()).Status() != "PASS" {
		t.Fatalf("PASSが採用されていません: %q", out.String())
	}
	comparison, err := w.state.LoadSnapshotComparison()
	if err != nil || comparison.Stage != state.SnapshotStageReviewEnd || !comparison.Matched {
		t.Fatalf("review-end一致comparison = %#v err=%v", comparison, err)
	}
	for _, l := range taskLogs(t, w.state) {
		if l.Outcome == "snapshot_mismatch" {
			t.Fatalf("一致時にmismatch eventが記録されています: %+v", l)
		}
	}
}

// auto-fix loopのrepeated reviewでもreview-end検証が通り、risk floor reemit結果が採用される。
func TestReviewEndMatchOnAutoFixLoop(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, _, out := newMutationWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: fixRequiredPacket()},
		{output: implementedPacket("fixed")},
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}, nil)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if w.state.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", w.state.TaskStatus())
	}
	pkt := lastPacketFromOutput(t, out.String())
	if pkt.Status() != "NEEDS_SOL_REVIEW" || strings.Contains(out.String(), "reviewer実行中にrepository状態が変化") {
		t.Fatalf("reemit結果が採用されるべき: %q", out.String())
	}
	for _, l := range taskLogs(t, w.state) {
		if l.Outcome == "snapshot_mismatch" {
			t.Fatalf("変化していないのにmismatch eventが記録されています: %+v", l)
		}
	}
}

// rate-limit resumeでreviewerが正常終了した直後のmutationをreview-end検出する。
// resume前のreview-start照合は壊さない。
func TestReviewEndMutationAfterRateLimitResumeRejectsPass(t *testing.T) {
	repoRoot := initMutationRepo(t)
	st := newStateStoreT(t)
	reviewStart, err := state.CaptureGitSnapshot(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveReviewStartSnapshot(reviewStart); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:          state.ResumeStageReview,
		Phase:          "reviewer-1",
		Role:           state.ReviewerRole,
		Model:          "haiku",
		ReadOnly:       true,
		Effort:         "high",
		Prompt:         "review",
		OriginalPrompt: "review",
		Request:        "request",
		WorkerPacket:   workerPacketLines(),
		ReviewNumber:   1,
		RateLimited:    true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}
	r := &mutatingRunner{repoRoot: repoRoot, steps: []runnerStep{{output: passPacket()}}, mutate: func(root string) error {
		return os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("mutated\n"), 0o644)
	}}
	out := &bytes.Buffer{}
	w := newMutationWorkflowShell(t, st)
	w.runner = r
	w.output = out
	w.config.RepoRoot = repoRoot
	w.captureSnapshot = state.CaptureGitSnapshot

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if w.state.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", w.state.TaskStatus())
	}
	if lastPacketFromOutput(t, out.String()).Status() != "NEEDS_SOL_REVIEW" {
		t.Fatalf("resume後PASSが採用されています: %q", out.String())
	}
	if event := mismatchEvent(t, st); event.Snapshot.Stage != string(state.SnapshotStageReviewEnd) {
		t.Fatalf("resume後のreview-end mismatch記録 = %+v", event.Snapshot)
	}
}

// risk floor reemit呼出中のmutationも採用前に検出する。reemit自身のNEEDS_SOL_REVIEWは出さない。
func TestReviewEndMutationOnRiskFloorReemitRejects(t *testing.T) {
	repoRoot := initMutationRepo(t)
	reviewerCalls := 0
	r := &mutatingRunner{
		repoRoot: repoRoot,
		steps: []runnerStep{
			{output: implementedPacketWithRisk("high risk work", "HIGH")},
			{output: passPacket()},
			{output: needsSolReviewPacket()},
		},
	}
	r.mutate = func(root string) error {
		reviewerCalls++
		if reviewerCalls < 2 {
			return nil
		}
		return os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("mutated\n"), 0o644)
	}
	st := newStateStoreT(t)
	out := &bytes.Buffer{}
	w := newMutationWorkflowShell(t, st)
	w.runner = r
	w.output = out
	w.config.RepoRoot = repoRoot
	w.captureSnapshot = state.CaptureGitSnapshot

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if w.state.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", w.state.TaskStatus())
	}
	if len(r.prompts) != 3 {
		t.Fatalf("reemit呼出で停止すべき: calls=%d", len(r.prompts))
	}
	pkt := lastPacketFromOutput(t, out.String())
	if pkt.Status() != "NEEDS_SOL_REVIEW" || !strings.Contains(out.String(), "reviewer実行中にrepository状態が変化") {
		t.Fatalf("reemit結果ではなくfail closed packetであるべき: %q", out.String())
	}
}

// review-end時点のsnapshot取得失敗は比較未実施としてmismatch軸集計へ混ぜない。
func TestReviewEndCaptureFailureFailsClosedNotMismatch(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	var out bytes.Buffer
	w := newSnapshotWorkflow(st, r, &out)
	w.captureSnapshot = func(string) (state.GitSnapshot, error) {
		return fixedSnapshot, nil
	}
	calls := 0
	realCapture := w.captureSnapshot
	w.captureSnapshot = func(root string) (state.GitSnapshot, error) {
		calls++
		if calls > 3 {
			return state.GitSnapshot{}, errors.New("review-end capture unavailable")
		}
		return realCapture(root)
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	var unavailable state.ModelCallLog
	found := false
	for _, l := range taskLogs(t, st) {
		if l.Outcome == "snapshot_unavailable" {
			unavailable = l
			found = true
		}
	}
	if !found {
		t.Fatal("snapshot_unavailable eventがありません")
	}
	if unavailable.Snapshot.Stage != string(state.SnapshotStageReviewEnd) {
		t.Fatalf("review-end取得失敗記録 = %+v", unavailable.Snapshot)
	}
	stats := currentStats(t, st)
	if stats.SnapshotMismatches != 0 || len(stats.SnapshotMismatchByAxis) != 0 {
		t.Fatalf("取得失敗がmismatch集計へ混ざっている: %+v", stats.SnapshotMismatchByAxis)
	}
}
