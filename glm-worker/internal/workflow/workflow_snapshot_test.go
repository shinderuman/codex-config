package workflow

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

type captureResult struct {
	snap state.GitSnapshot
	err  error
}

type queueCapturer struct {
	results []captureResult
	calls   int
}

func (c *queueCapturer) capture(string) (state.GitSnapshot, error) {
	if c.calls >= len(c.results) {
		c.calls++
		return state.GitSnapshot{}, errors.New("snapshot queue exhausted")
	}
	result := c.results[c.calls]
	c.calls++
	return result.snap, result.err
}

func newSnapshotWorkflow(st *state.StateStore, r *scriptedRunner, out io.Writer) *Workflow {
	return NewWorkflow(config.AppConfig{
		WorkerModel:           "opus",
		ReviewerModel:         "haiku",
		HighRiskReviewerModel: "sonnet",
		RoutineEffort:         "high",
		MaxAutoFixRounds:      2,
	}, st, r, out)
}

func workerPacketLines() []string {
	return []string{
		"STATUS: IMPLEMENTED",
		"RISK: LOW",
		"SUMMARY: done",
		"REQUIREMENT_COVERAGE: covered",
		"TESTS: pass",
		"UNVERIFIED: none",
		"ARTIFACTS: none",
	}
}

func TestSnapshotCaptureFailureFailsClosedBeforeReviewer(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	var out bytes.Buffer
	w := newSnapshotWorkflow(st, r, &out)
	w.captureSnapshot = func(string) (state.GitSnapshot, error) {
		return state.GitSnapshot{}, errors.New("snapshot unavailable")
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if len(r.prompts) != 1 {
		t.Fatalf("reviewerを呼ばず停止すべき: calls=%d", len(r.prompts))
	}
	if !strings.Contains(out.String(), "STATUS: NEEDS_SOL_REVIEW") || !strings.Contains(out.String(), "RISK: HIGH") {
		t.Fatalf("fail closed packetが出力されていません: %q", out.String())
	}
	if !strings.Contains(out.String(), "worker-end snapshot取得失敗") {
		t.Fatalf("取得失敗の原因が記録されていません: %q", out.String())
	}
	if _, err := st.LoadResumeCheckpoint(); err == nil {
		t.Fatal("fail closed後はresume checkpointを残さない")
	}
}

func TestSnapshotReviewStartCaptureFailureFailsClosed(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	var out bytes.Buffer
	w := newSnapshotWorkflow(st, r, &out)
	queue := &queueCapturer{results: []captureResult{
		{snap: fixedSnapshot},
		{err: errors.New("review-start capture unavailable")},
	}}
	w.captureSnapshot = queue.capture

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if len(r.prompts) != 1 {
		t.Fatalf("reviewerを呼ばず停止すべき: calls=%d", len(r.prompts))
	}
	if !strings.Contains(out.String(), "review-start snapshot取得失敗") {
		t.Fatalf("review-start取得失敗の原因が記録されていません: %q", out.String())
	}
}

func TestSnapshotWorkerEndReviewStartMismatchFailsClosed(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	var out bytes.Buffer
	w := newSnapshotWorkflow(st, r, &out)
	workerEnd := state.GitSnapshot{Head: "a", IndexDigest: "a", WorktreeDigest: "a"}
	reviewStart := state.GitSnapshot{Head: "b", IndexDigest: "b", WorktreeDigest: "b"}
	queue := &queueCapturer{results: []captureResult{
		{snap: workerEnd},
		{snap: reviewStart},
	}}
	w.captureSnapshot = queue.capture

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if len(r.prompts) != 1 {
		t.Fatalf("reviewerを呼ばず停止すべき: calls=%d", len(r.prompts))
	}
	loadedWorker, err := st.LoadWorkerEndSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	loadedReview, err := st.LoadReviewStartSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if loadedWorker.Head != "a" || loadedReview.Head != "b" {
		t.Fatalf("worker-end/review-startが区別保存されていません: worker=%#v review=%#v", loadedWorker, loadedReview)
	}
	comparison, err := st.LoadSnapshotComparison()
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Matched || comparison.HeadMatch {
		t.Fatalf("不一致comparisonが記録されていません: %#v", comparison)
	}
	if !strings.Contains(out.String(), "一致しません") {
		t.Fatalf("不一致原因がpacketへ出力されていません: %q", out.String())
	}
}

func TestSnapshotComparisonSaveFailureFailsClosed(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.SaveWorkerEndSnapshot(fixedSnapshot); err != nil {
		t.Fatal(err)
	}
	// comparison file pathへ非空dirを置きSaveSnapshotComparisonを失敗させる。
	blockerDir := st.Path("snapshot-comparison.json")
	if err := os.MkdirAll(blockerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockerDir, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w := newSnapshotWorkflow(st, &scriptedRunner{}, &out)
	w.captureSnapshot = func(string) (state.GitSnapshot, error) {
		return fixedSnapshot, nil
	}

	stopped, err := w.verifyReviewStartSnapshot()
	if err != nil {
		t.Fatalf("state errorではなくfail closed停止すべき: %v", err)
	}
	if !stopped {
		t.Fatal("comparison保存失敗時はfail closedへ停止する必要があります")
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if !strings.Contains(out.String(), "snapshot comparison保存失敗") {
		t.Fatalf("comparison保存失敗の原因が記録されていません: %q", out.String())
	}
}

func TestSnapshotMatchReachesReviewer(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if len(r.prompts) != 2 {
		t.Fatalf("worker+reviewerが呼ばれるべき: calls=%d", len(r.prompts))
	}
	loadedWorker, err := st.LoadWorkerEndSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !state.EqualGitSnapshot(loadedWorker, fixedSnapshot) {
		t.Fatalf("worker-end snapshot = %#v", loadedWorker)
	}
	comparison, err := st.LoadSnapshotComparison()
	if err != nil {
		t.Fatal(err)
	}
	if !comparison.Matched {
		t.Fatalf("一致比較が記録されるべき: %#v", comparison)
	}
}

func TestSnapshotReviewResumeDriftFailsClosed(t *testing.T) {
	st := newStateStoreT(t)
	seeded := state.GitSnapshot{Head: "a", IndexDigest: "a", WorktreeDigest: "a"}
	if err := st.SaveReviewStartSnapshot(seeded); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:          state.ResumeStageReview,
		Phase:          "reviewer-1",
		Role:           state.ReviewerRole,
		Model:          "sonnet",
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
	r := &scriptedRunner{steps: []runnerStep{{output: passPacket()}}}
	var out bytes.Buffer
	w := newSnapshotWorkflow(st, r, &out)
	w.captureSnapshot = func(string) (state.GitSnapshot, error) {
		return state.GitSnapshot{Head: "b", IndexDigest: "b", WorktreeDigest: "b"}, nil
	}

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if len(r.prompts) != 0 {
		t.Fatalf("reviewerをresumeせず停止すべき: calls=%d", len(r.prompts))
	}
	if !strings.Contains(out.String(), "review開始時から状態が変化") {
		t.Fatalf("resume drift原因がpacketへ出力されていません: %q", out.String())
	}
	if _, err := st.LoadResumeCheckpoint(); err == nil {
		t.Fatal("resume drift fail closed後はcheckpointを残さない")
	}
}

func TestSnapshotReviewResumeMatchResumesReviewer(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.SaveReviewStartSnapshot(fixedSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:          state.ResumeStageReview,
		Phase:          "reviewer-1",
		Role:           state.ReviewerRole,
		Model:          "sonnet",
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
	r := &scriptedRunner{steps: []runnerStep{{output: passPacket()}}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if len(r.prompts) != 1 || r.models[0] != "sonnet" {
		t.Fatalf("reviewer resumeが1回だけ呼ばれるべき: prompts=%d models=%#v", len(r.prompts), r.models)
	}
	comparison, err := st.LoadSnapshotComparison()
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Stage != state.SnapshotStageReviewResume || !comparison.Matched {
		t.Fatalf("resume一致比較が記録されるべき: %#v", comparison)
	}
}

func TestSnapshotCapturedOnDecisionPath(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.Write("last-request", "request"); err != nil {
		t.Fatal(err)
	}
	if err := st.Touch("pending-decision"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusWaitingDecision); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithRisk("decision applied", "HIGH")},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteDecision("A案で進める"); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadWorkerEndSnapshot()
	if err != nil {
		t.Fatalf("decision経路でworker-end snapshotが保存されていません: %v", err)
	}
	if !state.EqualGitSnapshot(loaded, fixedSnapshot) {
		t.Fatalf("worker-end snapshot = %#v", loaded)
	}
	if _, err := st.LoadReviewStartSnapshot(); err != nil {
		t.Fatalf("decision経路でreview-start snapshotが保存されていません: %v", err)
	}
}

func TestSnapshotCapturedOnAutoFixPath(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: fixRequiredPacket()},
		{output: implementedPacket("fixed")},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadWorkerEndSnapshot()
	if err != nil {
		t.Fatalf("auto-fix経路でworker-end snapshotが保存されていません: %v", err)
	}
	if !state.EqualGitSnapshot(loaded, fixedSnapshot) {
		t.Fatalf("worker-end snapshot = %#v", loaded)
	}
}

func TestSnapshotCapturedOnExplicitFixPath(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.Write("last-request", "request"); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("last-review", "previous review"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusWaitingSolReview); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithRisk("explicit fix", "HIGH")},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteExplicitFix("境界値を修正する"); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadWorkerEndSnapshot()
	if err != nil {
		t.Fatalf("explicit fix経路でworker-end snapshotが保存されていません: %v", err)
	}
	if !state.EqualGitSnapshot(loaded, fixedSnapshot) {
		t.Fatalf("worker-end snapshot = %#v", loaded)
	}
}

func TestSnapshotCapturedOnWorkerResumePath(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:          state.ResumeStageWorker,
		Phase:          "worker-new",
		Role:           state.WorkerRole,
		Model:          "opus",
		Effort:         "high",
		Prompt:         "p",
		OriginalPrompt: "p",
		Request:        "req",
		RateLimited:    true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("resumed")},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if _, err := st.LoadWorkerEndSnapshot(); err != nil {
		t.Fatalf("worker resume経路でworker-end snapshotが保存されていません: %v", err)
	}
}
