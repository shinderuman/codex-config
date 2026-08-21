package workflow

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// report-only再出力経路のproduction因果test。scripted packetの終端だけでなく、
// dispatch→checkpoint capability→worker実行→終了後snapshot比較→review遷移を
// 実git repository・実CaptureGitSnapshotで検証する。

func newReportOnlyWorkflow(t *testing.T, repoRoot string, steps []runnerStep, mutate func(string) error) (*Workflow, *mutatingRunner, *bytes.Buffer) {
	t.Helper()
	w, r, out := newMutationWorkflow(t, repoRoot, steps, mutate)
	r.mutatePhase = "worker-report-only-1"
	return w, r, out
}

func requireReportOnlyFailClosed(t *testing.T, w *Workflow, out *bytes.Buffer, wantAxis string) {
	t.Helper()
	if w.state.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q want waiting-sol-review", w.state.TaskStatus())
	}
	pkt := lastPacketFromOutput(t, out.String())
	if pkt.Status != "NEEDS_SOL_REVIEW" || pkt.Risk != "HIGH" {
		t.Fatalf("packet = %s/%s want NEEDS_SOL_REVIEW/HIGH", pkt.Status, pkt.Risk)
	}
	if !strings.Contains(out.String(), "report-only worker開始前から終了後までの間にrepository状態が変化") &&
		!strings.Contains(out.String(), "report-only開始前snapshot") {
		t.Fatalf("report-only不変性確認失敗の原因が出力されていません: %q", out.String())
	}
	if !strings.Contains(out.String(), "report-only PACKET再出力worker") {
		t.Fatalf("reviewer用snapshot fail closed packetと区別されていません: %q", out.String())
	}
	if _, err := w.state.LoadResumeCheckpoint(); err == nil {
		t.Fatal("fail closed後はresume checkpointを残さない")
	}
	var event state.ModelCallLog
	found := false
	for _, l := range taskLogs(t, w.state) {
		if strings.HasSuffix(l.Phase, "report-only-end-snapshot-check") || strings.HasSuffix(l.Phase, "report-only-start-snapshot-check") {
			event = l
			found = true
		}
	}
	if !found {
		t.Fatalf("report-only snapshot eventがありません: %+v", phasesOf(taskLogs(t, w.state)))
	}
	if event.Role != state.WorkerRole {
		t.Fatalf("report-only不変性eventの主体はworkerであるべき: %s", event.Role)
	}
	if wantAxis != "" {
		if event.Outcome != "snapshot_mismatch" || !strings.Contains(event.Snapshot.MismatchAxis, wantAxis) {
			t.Fatalf("mismatch event = outcome %s axis %q want %s", event.Outcome, event.Snapshot.MismatchAxis, wantAxis)
		}
		if stats := currentStats(t, w.state); stats.SnapshotMismatchByAxis[wantAxis] != 1 {
			t.Fatalf("%s軸集計 = %+v", wantAxis, stats.SnapshotMismatchByAxis)
		}
	}
}

// TARGETS: PACKET dispatchはReadOnly capabilityでworkerを呼び、開始前baselineをworker実行前に保存する。
func TestReportOnlyDispatchCapabilityAndBaselineOrdering(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithRisk("high risk work", "HIGH")},
		{output: fixRequiredPacketWithTargets("PACKET")},
		{output: implementedPacketWithRisk("report re-emitted", "HIGH")},
		{output: needsSolReviewPacket()},
	}}
	r.onRun = func() {
		if len(r.prompts) != 3 {
			return
		}
		loaded, err := st.LoadReportOnlyStartSnapshot()
		if err != nil {
			t.Errorf("report-only worker実行前に開始前snapshotが保存されているべき: %v", err)
			return
		}
		if !state.EqualGitSnapshot(loaded, fixedSnapshot) {
			t.Errorf("開始前snapshot = %#v", loaded)
		}
	}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if got, want := len(r.readOnlyCalls), 4; got != want {
		t.Fatalf("呼出回数 = %d want %d", got, want)
	}
	if r.readOnlyCalls[0] {
		t.Fatal("通常workerはwrite capabilityを保持すべき")
	}
	if !r.readOnlyCalls[2] {
		t.Fatal("report-only workerはReadOnly capabilityでdispatchされるべき")
	}
	var reportOnlyReadOnly bool
	seen := false
	for _, l := range taskLogs(t, st) {
		if l.Phase == "worker-report-only-1" {
			reportOnlyReadOnly = l.ReadOnly
			seen = true
		}
	}
	if !seen {
		t.Fatal("telemetryへreport-only task呼出がありません")
	}
	if !reportOnlyReadOnly {
		t.Fatalf("report-only呼出telemetryのReadOnly = %v want true", reportOnlyReadOnly)
	}
}

// 実装修正auto-fixはwrite capabilityと既存worker-end基準flowを維持し、開始前snapshotを作らない。
func TestImplementationAutoFixKeepsWriteCapabilityAndBaselineFlow(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithRisk("high risk work", "HIGH")},
		{output: fixRequiredPacketWithTargets("glm-worker/internal/state/store.go:Read")},
		{output: implementedPacketWithRisk("fixed implementation", "HIGH")},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if len(r.readOnlyCalls) != 4 || r.readOnlyCalls[2] {
		t.Fatalf("implementation auto-fixのreadOnly = %#v want [false,true,false,true]", r.readOnlyCalls)
	}
	if _, err := st.LoadReportOnlyStartSnapshot(); err == nil {
		t.Fatal("implementation auto-fixでreport-only開始前snapshotを作らない")
	}
	workerEnd, err := st.LoadWorkerEndSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !state.EqualGitSnapshot(workerEnd, fixedSnapshot) {
		t.Fatalf("auto-fix後のworker-end snapshot = %#v", workerEnd)
	}
}

// report-only workerがtracked fileを変更した場合、reviewer-2を呼ばずfail closedし変更は保持される。
func TestReportOnlyWorktreeMutationFailsClosedBeforeReview(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, r, out := newReportOnlyWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacketWithRisk("high risk work", "HIGH")},
		{output: fixRequiredPacketWithTargets("PACKET")},
		{output: implementedPacketWithRisk("report re-emitted", "HIGH")},
		{output: needsSolReviewPacket()},
	}, func(root string) error {
		return os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("mutated\n"), 0o644)
	})

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	requireReportOnlyFailClosed(t, w, out, "worktree")
	if len(r.prompts) != 3 {
		t.Fatalf("report-only mutation後はreviewer-2を呼ばない: calls=%d", len(r.prompts))
	}
	content, err := os.ReadFile(filepath.Join(repoRoot, "tracked.txt"))
	if err != nil || string(content) != "mutated\n" {
		t.Fatalf("report-only worker変更がrollbackまたは黙認されています: %q %v", content, err)
	}
	start, err := w.state.LoadReportOnlyStartSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	after, err := state.CaptureGitSnapshot(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if state.EqualGitSnapshot(start, after) {
		t.Fatal("不一致検出対象のsnapshotが一致しています")
	}
	// fail closedはresume checkpointの再実行だけ止め、--fix recoveryでの調査に必要な
	// 開始前snapshot・comparison・telemetry上のworker session診断を残す。
	if comparison, err := w.state.LoadSnapshotComparison(); err != nil || comparison.Stage != state.SnapshotStageReportOnlyEnd || comparison.Matched {
		t.Fatalf("report-only-end comparison証拠が保持されていません: %#v err=%v", comparison, err)
	}
	var reportOnlyCall state.ModelCallLog
	for _, l := range taskLogs(t, w.state) {
		if l.Phase == "worker-report-only-1" && l.Role == state.WorkerRole {
			reportOnlyCall = l
		}
	}
	if reportOnlyCall.SessionID != "test-session" {
		t.Fatalf("report-only worker呼出のsession診断が保持されていません: %+v", reportOnlyCall)
	}
}

// report-only workerがindexへstageした場合もfail closedする。
func TestReportOnlyIndexMutationFailsClosedBeforeReview(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, r, out := newReportOnlyWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacketWithRisk("high risk work", "HIGH")},
		{output: fixRequiredPacketWithTargets("PACKET")},
		{output: implementedPacketWithRisk("report re-emitted", "HIGH")},
		{output: needsSolReviewPacket()},
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
	requireReportOnlyFailClosed(t, w, out, "index")
	if len(r.prompts) != 3 {
		t.Fatalf("reviewer-2を呼ばない: calls=%d", len(r.prompts))
	}
	if gitIn(t, repoRoot, "diff", "--cached", "--name-only") != "tracked.txt" {
		t.Fatal("staged変更が保持されていません")
	}
}

// report-only workerがcommitした場合もhead軸でfail closedする。
func TestReportOnlyHeadMutationFailsClosedBeforeReview(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, _, out := newReportOnlyWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacketWithRisk("high risk work", "HIGH")},
		{output: fixRequiredPacketWithTargets("PACKET")},
		{output: implementedPacketWithRisk("report re-emitted", "HIGH")},
		{output: needsSolReviewPacket()},
	}, func(root string) error {
		if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("committed\n"), 0o644); err != nil {
			return err
		}
		gitIn(t, root, "add", "tracked.txt")
		gitIn(t, root, "commit", "-q", "-m", "report-only commit")
		return nil
	})

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	requireReportOnlyFailClosed(t, w, out, "head")
	if !strings.Contains(gitIn(t, repoRoot, "log", "-1", "--pretty=%s"), "report-only commit") {
		t.Fatal("commitがrollbackされています")
	}
}

// repo不変のreport-only再出力は既存のreview再開・risk floor routingを維持する。
func TestReportOnlyNoMutationMaintainsReviewRestartAndRiskFloor(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, r, out := newReportOnlyWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacketWithRisk("high risk work", "HIGH")},
		{output: fixRequiredPacketWithTargets("PACKET")},
		{output: implementedPacketWithRisk("report re-emitted", "HIGH")},
		{output: needsSolReviewPacket()},
	}, nil)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if w.state.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q want waiting-sol-review", w.state.TaskStatus())
	}
	if len(r.prompts) != 4 {
		t.Fatalf("worker+reviewer-1+report-only+reviewer-2が呼ばれるべき: calls=%d", len(r.prompts))
	}
	if got, want := strings.Join(r.models, ","), "opus,sonnet,opus,sonnet"; got != want {
		t.Fatalf("risk floor model routing = %q want %q", got, want)
	}
	if !strings.Contains(r.prompts[3], "現在のworking treeを実際に独立確認して判定してください") {
		t.Fatalf("reviewer-2は通常review promptであるべき: %s", r.prompts[3])
	}
	if strings.Contains(r.prompts[3], "実装・working tree変更・追加調査・test/lint/build/self-reviewをやり直さず") {
		t.Fatalf("reviewer-2へreport-only promptが使われています: %s", r.prompts[3])
	}
	pkt := lastPacketFromOutput(t, out.String())
	if pkt.Status != "NEEDS_SOL_REVIEW" || !strings.Contains(pkt.Summary, "review") {
		t.Fatalf("reviewer-2結果が採用されるべき: %q", out.String())
	}
	start, err := w.state.LoadReportOnlyStartSnapshot()
	if err != nil {
		t.Fatalf("report-only開始前snapshotが保存されていません: %v", err)
	}
	after, err := state.CaptureGitSnapshot(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !state.EqualGitSnapshot(start, after) {
		t.Fatalf("reviewer-2へ進んだ状態は開始前snapshotと同一のべき: %#v vs %#v", start, after)
	}
	for _, l := range taskLogs(t, w.state) {
		if l.Outcome == "snapshot_mismatch" {
			t.Fatalf("不変性が保たれているのにmismatch eventが記録されています: %+v", l)
		}
	}
}

// 開始前baselineを確保できないときはreport-only workerを実行せずfail closedする。
func TestReportOnlyStartSnapshotCaptureFailureStopsBeforeWorkerRun(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, r, out := newReportOnlyWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacketWithRisk("high risk work", "HIGH")},
		{output: fixRequiredPacketWithTargets("PACKET")},
		{output: implementedPacketWithRisk("report re-emitted", "HIGH")},
		{output: needsSolReviewPacket()},
	}, nil)
	realCapture := w.captureSnapshot
	calls := 0
	w.captureSnapshot = func(root string) (state.GitSnapshot, error) {
		calls++
		if calls == 5 {
			return state.GitSnapshot{}, errors.New("report-only start capture unavailable")
		}
		return realCapture(root)
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if w.state.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", w.state.TaskStatus())
	}
	if len(r.prompts) != 2 {
		t.Fatalf("baseline無しではreport-only workerを実行しない: calls=%d", len(r.prompts))
	}
	if !strings.Contains(out.String(), "report-only開始前snapshot取得失敗") {
		t.Fatalf("取得失敗原因が出力されていません: %q", out.String())
	}
}

func TestReportOnlyStartSnapshotSaveFailureStopsBeforeWorkerRun(t *testing.T) {
	repoRoot := initMutationRepo(t)
	st := newStateStoreT(t)
	blockerDir := st.Path("snapshot-report-only-start.json")
	if err := os.MkdirAll(blockerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockerDir, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &mutatingRunner{repoRoot: repoRoot}
	out := &bytes.Buffer{}
	w := newMutationWorkflowShell(t, st)
	w.runner = r
	w.output = out
	w.config.RepoRoot = repoRoot
	w.captureSnapshot = state.CaptureGitSnapshot
	reviewPacket := resultFromLines(
		"STATUS: FIX_REQUIRED",
		"RISK: HIGH",
		"SUMMARY: fix",
		"REQUIREMENT_COVERAGE: covered",
		"INVARIANTS: preserved",
		"TEST_EVIDENCE: ev",
		"ISSUES: i",
		"RESIDUAL_RISK: r",
		"TARGETS: PACKET",
		"ARTIFACTS: none",
	)

	if err := w.handleReviewResult("request", resultFromLines(workerPacketLines()...), reviewPacket, 1, 0); err != nil {
		t.Fatal(err)
	}
	if w.state.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", w.state.TaskStatus())
	}
	if len(r.prompts) != 0 {
		t.Fatalf("baseline保存失敗時はreport-only workerを実行しない: calls=%d", len(r.prompts))
	}
	if !strings.Contains(out.String(), "report-only開始前snapshot保存失敗") {
		t.Fatalf("保存失敗原因が出力されていません: %q", out.String())
	}
}

// report-only検証のcomparison保存失敗も状態確認済み一致と同じくfail closedへ停止する。
func TestReportOnlyComparisonSaveFailureFailsClosed(t *testing.T) {
	repoRoot := initMutationRepo(t)
	st := newStateStoreT(t)
	blockerDir := st.Path("snapshot-comparison.json")
	if err := os.MkdirAll(blockerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockerDir, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &mutatingRunner{repoRoot: repoRoot, steps: []runnerStep{
		{output: implementedPacketWithRisk("report re-emitted", "HIGH")},
	}}
	out := &bytes.Buffer{}
	w := newMutationWorkflowShell(t, st)
	w.runner = r
	w.output = out
	w.config.RepoRoot = repoRoot
	w.temp = t.TempDir()
	w.captureSnapshot = state.CaptureGitSnapshot
	reviewPacket := resultFromLines(
		"STATUS: FIX_REQUIRED",
		"RISK: HIGH",
		"SUMMARY: fix",
		"REQUIREMENT_COVERAGE: covered",
		"INVARIANTS: preserved",
		"TEST_EVIDENCE: ev",
		"ISSUES: i",
		"RESIDUAL_RISK: r",
		"TARGETS: PACKET",
		"ARTIFACTS: none",
	)

	if err := w.handleReviewResult("request", resultFromLines(workerPacketLines()...), reviewPacket, 1, 0); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if len(r.prompts) != 1 {
		t.Fatalf("report-only worker実行後、reviewerへは進まない: calls=%d", len(r.prompts))
	}
	if !strings.Contains(out.String(), "snapshot comparison保存失敗") {
		t.Fatalf("comparison保存失敗の原因が出力されていません: %q", out.String())
	}
	for _, l := range taskLogs(t, st) {
		if strings.HasSuffix(l.Phase, "report-only-end-snapshot-check") && l.Outcome == "snapshot_save_failed" {
			return
		}
	}
	t.Fatalf("snapshot_save_failed eventがありません: %+v", phasesOf(taskLogs(t, st)))
}

// 終了後snapshot取得失敗は比較未実施としてfail closedし、mismatch軸集計へ混ぜない。
func TestReportOnlyEndSnapshotCaptureFailureFailsClosedNotMismatch(t *testing.T) {
	repoRoot := initMutationRepo(t)
	st := newStateStoreT(t)
	r := &mutatingRunner{repoRoot: repoRoot, steps: []runnerStep{
		{output: implementedPacketWithRisk("report re-emitted", "HIGH")},
	}}
	out := &bytes.Buffer{}
	w := newMutationWorkflowShell(t, st)
	w.runner = r
	w.output = out
	w.config.RepoRoot = repoRoot
	w.temp = t.TempDir()
	realCapture := state.CaptureGitSnapshot
	calls := 0
	w.captureSnapshot = func(root string) (state.GitSnapshot, error) {
		calls++
		if calls == 2 {
			return state.GitSnapshot{}, errors.New("report-only end capture unavailable")
		}
		return realCapture(root)
	}
	reviewPacket := resultFromLines(
		"STATUS: FIX_REQUIRED",
		"RISK: HIGH",
		"SUMMARY: fix",
		"REQUIREMENT_COVERAGE: covered",
		"INVARIANTS: preserved",
		"TEST_EVIDENCE: ev",
		"ISSUES: i",
		"RESIDUAL_RISK: r",
		"TARGETS: PACKET",
		"ARTIFACTS: none",
	)

	if err := w.handleReviewResult("request", resultFromLines(workerPacketLines()...), reviewPacket, 1, 0); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if len(r.prompts) != 1 {
		t.Fatalf("report-only workerは実行済みでreviewerへは進まない: calls=%d", len(r.prompts))
	}
	if !strings.Contains(out.String(), "report-only終了後snapshot取得失敗") {
		t.Fatalf("終了後取得失敗の原因が出力されていません: %q", out.String())
	}
	var unavailable state.ModelCallLog
	found := false
	for _, l := range taskLogs(t, st) {
		if l.Outcome == "snapshot_unavailable" && strings.HasSuffix(l.Phase, "report-only-end-snapshot-check") {
			unavailable = l
			found = true
		}
	}
	if !found {
		t.Fatalf("report-only-endのsnapshot_unavailable eventがありません: %+v", phasesOf(taskLogs(t, st)))
	}
	if unavailable.Snapshot.Stage != string(state.SnapshotStageReportOnlyEnd) {
		t.Fatalf("report-only-end取得失敗記録 = %+v", unavailable.Snapshot)
	}
	if stats := currentStats(t, st); stats.SnapshotMismatches != 0 || len(stats.SnapshotMismatchByAxis) != 0 {
		t.Fatalf("取得失敗がmismatch集計へ混ざっている: %+v", stats.SnapshotMismatchByAxis)
	}
}

// 5h上限で停止しても開始前snapshotを基準に保持し、停止期間中の変化をresume検証から隠さない。
func TestReportOnlyRateLimitResumeVerifiesAgainstSameStartSnapshot(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, _, _ := newReportOnlyWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacketWithRisk("high risk work", "HIGH")},
		{output: fixRequiredPacketWithTargets("PACKET")},
		{output: zaiFiveHourLog, runErr: errors.New("exit status 1")},
	}, nil)

	if err := w.ExecuteNewTask("request"); err == nil || !strings.Contains(err.Error(), "STATUS: RATE_LIMITED") {
		t.Fatalf("rate limit errorを期待: %v", err)
	}
	cp, err := w.state.LoadResumeCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if !cp.RateLimited || !cp.ReportOnly || !cp.ReadOnly || cp.Phase != "worker-report-only-1" {
		t.Fatalf("rate-limited checkpoint = %#v", cp)
	}
	startBeforeStop, err := w.state.LoadReportOnlyStartSnapshot()
	if err != nil {
		t.Fatalf("停止時点で開始前snapshotが保存されていません: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repoRoot, "tracked.txt"), []byte("drifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resumeRunner := &mutatingRunner{repoRoot: repoRoot, steps: []runnerStep{
		{output: implementedPacketWithRisk("report re-emitted", "HIGH")},
		{output: needsSolReviewPacket()},
	}}
	resumeRunner.mutatePhase = "worker-report-only-1"
	out := &bytes.Buffer{}
	rw := newMutationWorkflowShell(t, w.state)
	rw.runner = resumeRunner
	rw.output = out
	rw.config.RepoRoot = repoRoot
	realCapture := state.CaptureGitSnapshot
	captures := 0
	rw.captureSnapshot = func(root string) (state.GitSnapshot, error) {
		captures++
		return realCapture(root)
	}

	if err := rw.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	requireReportOnlyFailClosed(t, rw, out, "worktree")
	if len(resumeRunner.prompts) != 1 {
		t.Fatalf("resume検証失敗後はreviewerを呼ばない: calls=%d", len(resumeRunner.prompts))
	}
	if captures != 1 {
		t.Fatalf("resumeは検証のための1回だけsnapshot取得しbaselineを取り直さない: captures=%d", captures)
	}
	startAfterResume, err := rw.state.LoadReportOnlyStartSnapshot()
	if err != nil || !state.EqualGitSnapshot(startBeforeStop, startAfterResume) {
		t.Fatalf("resumeが開始前snapshotを再撮影しています: %#v err=%v", startAfterResume, err)
	}
	content, err := os.ReadFile(filepath.Join(repoRoot, "tracked.txt"))
	if err != nil || string(content) != "drifted\n" {
		t.Fatalf("停止期間中の変化がrollbackまたは黙認されています: %q %v", content, err)
	}
}

// 停止期間中に変化がなければresumeはreport-only再実行から通常reviewへ復帰する。
func TestReportOnlyRateLimitResumeWithoutDriftProceedsToReview(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, _, _ := newReportOnlyWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacketWithRisk("high risk work", "HIGH")},
		{output: fixRequiredPacketWithTargets("PACKET")},
		{output: zaiFiveHourLog, runErr: errors.New("exit status 1")},
	}, nil)

	if err := w.ExecuteNewTask("request"); err == nil {
		t.Fatal("rate limit errorを期待")
	}
	resumeRunner := &mutatingRunner{repoRoot: repoRoot, steps: []runnerStep{
		{output: implementedPacketWithRisk("report re-emitted", "HIGH")},
		{output: needsSolReviewPacket()},
	}}
	resumeRunner.mutatePhase = "worker-report-only-1"
	out := &bytes.Buffer{}
	rw := newMutationWorkflowShell(t, w.state)
	rw.runner = resumeRunner
	rw.output = out
	rw.config.RepoRoot = repoRoot
	rw.captureSnapshot = state.CaptureGitSnapshot

	if err := rw.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if rw.state.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q want waiting-sol-review", rw.state.TaskStatus())
	}
	if len(resumeRunner.prompts) != 2 {
		t.Fatalf("report-only再実行+reviewer-2が呼ばれるべき: calls=%d", len(resumeRunner.prompts))
	}
	if !resumeRunner.readOnlyCalls[0] {
		t.Fatal("resumeしたreport-only呼出もReadOnly capabilityであるべき")
	}
	pkt := lastPacketFromOutput(t, out.String())
	if pkt.Status != "NEEDS_SOL_REVIEW" || !strings.Contains(pkt.Summary, "review") {
		t.Fatalf("reviewer-2結果が採用されるべき: %q", out.String())
	}
	for _, l := range taskLogs(t, rw.state) {
		if l.Outcome == "snapshot_mismatch" {
			t.Fatalf("変化していないのにmismatch eventが記録されています: %+v", l)
		}
	}
}

// provider一時障害から同じsession/checkpointで回復した後も終了後同一性検証は効く。
func TestReportOnlyTransientRecoveryStillEnforcesInvariant(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, r, out := newReportOnlyWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacketWithRisk("high risk work", "HIGH")},
		{output: fixRequiredPacketWithTargets("PACKET")},
		{output: "API Error: 503 Service Unavailable", runErr: errors.New("exit status 1")},
		{output: implementedPacketWithRisk("report re-emitted", "HIGH")},
		{output: needsSolReviewPacket()},
	}, func(root string) error {
		return os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("mutated\n"), 0o644)
	})

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	requireReportOnlyFailClosed(t, w, out, "worktree")
	if len(r.prompts) != 4 {
		t.Fatalf("transient失敗+resumed呼出後に検証で停止すべき: calls=%d", len(r.prompts))
	}
	if r.phases[2] != "worker-report-only-1" || r.phases[3] != "worker-report-only-1" {
		t.Fatalf("同一phaseのtransient再実行であるべき: %#v", r.phases)
	}
}

// provider-unavailable停止からのresumeもprobe gate通過後に同じ開始前snapshot基準で検証する。
func TestReportOnlyProviderUnavailableResumeVerifiesAgainstStartSnapshot(t *testing.T) {
	repoRoot := initMutationRepo(t)
	st := newStateStoreT(t)
	baseline, err := state.CaptureGitSnapshot(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveReportOnlyStartSnapshot(baseline); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:                             state.ResumeStageAutoFix,
		Phase:                             "worker-report-only-1",
		Role:                              state.WorkerRole,
		Model:                             "opus",
		ReadOnly:                          true,
		ReportOnly:                        true,
		Effort:                            "high",
		Prompt:                            "report only",
		OriginalPrompt:                    "report only",
		Request:                           "req",
		ReviewNumber:                      1,
		AutoFixes:                         1,
		ProviderUnavailable:               true,
		ProviderUnavailableClassification: "http-503",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusProviderUnavailable); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "tracked.txt"), []byte("drifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &mutatingRunner{repoRoot: repoRoot, steps: []runnerStep{
		{output: implementedPacketWithRisk("report re-emitted", "HIGH")},
		{output: needsSolReviewPacket()},
	}}
	out := &bytes.Buffer{}
	w := newMutationWorkflowShell(t, st)
	w.runner = r
	w.output = out
	w.config.RepoRoot = repoRoot
	realCapture := state.CaptureGitSnapshot
	captures := 0
	w.captureSnapshot = func(root string) (state.GitSnapshot, error) {
		captures++
		return realCapture(root)
	}

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	requireReportOnlyFailClosed(t, w, out, "worktree")
	if len(r.probes) != 1 {
		t.Fatalf("provider resumeは本task再開前にprobe 1回でgateされるべき: %v", r.probes)
	}
	if len(r.prompts) != 1 {
		t.Fatalf("resume検証失敗後はreviewerを呼ばない: calls=%d", len(r.prompts))
	}
	if captures != 1 {
		t.Fatalf("resumeは検証のための1回だけsnapshot取得しbaselineを取り直さない: captures=%d", captures)
	}
}

// 開始前snapshotが欠損したresumeはworker/provider呼出前に比較不能としてfail closedし、
// reviewerへ進めない。新baselineを撮って欠損を隠さない。
func TestReportOnlyResumeWithoutStartSnapshotFailsClosedBeforeCalls(t *testing.T) {
	repoRoot := initMutationRepo(t)
	st := newStateStoreT(t)
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("worker.id", "worker-session-evidence"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:          state.ResumeStageAutoFix,
		Phase:          "worker-report-only-1",
		Role:           state.WorkerRole,
		Model:          "opus",
		ReadOnly:       true,
		ReportOnly:     true,
		Effort:         "high",
		Prompt:         "report only",
		OriginalPrompt: "report only",
		Request:        "req",
		ReviewNumber:   1,
		AutoFixes:      1,
		RateLimited:    true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}
	r := &mutatingRunner{repoRoot: repoRoot, steps: []runnerStep{
		{output: implementedPacketWithRisk("report re-emitted", "HIGH")},
		{output: needsSolReviewPacket()},
	}}
	out := &bytes.Buffer{}
	w := newMutationWorkflowShell(t, st)
	w.runner = r
	w.output = out
	w.config.RepoRoot = repoRoot
	realCapture := state.CaptureGitSnapshot
	captures := 0
	w.captureSnapshot = func(root string) (state.GitSnapshot, error) {
		captures++
		return realCapture(root)
	}

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	requireReportOnlyFailClosed(t, w, out, "")
	if len(r.prompts) != 0 || len(r.probes) != 0 {
		t.Fatalf("基準snapshot無しのresumeはworker/probeを1件も呼ばない: prompts=%d probes=%d", len(r.prompts), len(r.probes))
	}
	if captures != 0 {
		t.Fatalf("基準snapshot無しのresumeは新baselineを撮影しない: captures=%d", captures)
	}
	if !strings.Contains(out.String(), "resume再開前にreport-only開始前snapshotが欠損") {
		t.Fatalf("欠損原因が出力されていません: %q", out.String())
	}
	if _, err := st.LoadReportOnlyStartSnapshot(); err == nil {
		t.Fatal("resumeが新baselineを作成しています")
	}
	// fail closedはresume再実行だけ止め、--fix recoveryでの調査に必要な診断証拠を残す。
	if got, err := st.Read("worker.id"); err != nil || got != "worker-session-evidence" {
		t.Fatalf("worker session ID証拠が保持されていません: %q %v", got, err)
	}
	var gateEvent *state.ModelCallLog
	for _, l := range taskLogs(t, st) {
		if l.Outcome == "snapshot_unavailable" && l.Phase == "report-only-start-snapshot-check" {
			gateEvent = &l
			break
		}
	}
	if gateEvent == nil {
		t.Fatalf("resume前gateのsnapshot_unavailable eventがありません: %+v", phasesOf(taskLogs(t, st)))
	}
	if gateEvent.Role != state.WorkerRole {
		t.Fatalf("resume前gate eventの主体 = %s want worker", gateEvent.Role)
	}
}

// report-only推定は厳格な生成形式"worker-report-only-<十進数>"(packet圧縮suffix付きを
// 含む)の全体一致のみを認め、類似phase・前後追加・異種工程へ誤適用しない。
func TestReportOnlyFromPhaseStrict(t *testing.T) {
	for _, phase := range []string{
		"worker-report-only-1",
		"worker-report-only-2",
		"worker-report-only-10",
		"worker-report-only-1-packet-compact",
		"worker-report-only-10-packet-compact",
	} {
		if !reportOnlyFromPhase(phase) {
			t.Fatalf("phase %q はreport-onlyとして推定されるべき", phase)
		}
	}
	for _, phase := range []string{
		"",
		"worker-report-only-",
		"worker-report-only-x",
		"worker-report-only-1x",
		"worker-report-only-1-extra",
		"worker-report-only-1.5",
		"xworker-report-only-1",
		"WORKER-REPORT-ONLY-1",
		"worker-auto-fix-1",
		"worker-auto-fix-1-packet-compact",
		"worker-report-only--packet-compact",
		"worker-report-only-packet-compact",
		"worker-report-only-1-packet-compact-x",
		"worker-report-only-1-packet-compacted",
		"worker-report-only-1-packet-compact-packet-compact",
		"worker-new",
		"worker-decision",
		"worker-explicit-fix",
		"reviewer-1",
	} {
		if reportOnlyFromPhase(phase) {
			t.Fatalf("phase %q をreport-onlyへ誤適用しています", phase)
		}
	}
}

// 旧binary保存のreport-only checkpoint(ReportOnly field無し)を厳格phaseから推定し、
// 基準snapshot欠損時はrate-limit resumeでもworker/probe呼出前にfail closedする。
// packet圧縮で末尾へ"-packet-compact"が付いた保存形式も同じ推定へ含める。
func TestReportOnlyRateLimitResumeLegacyCheckpointWithoutSnapshotStopsBeforeCalls(t *testing.T) {
	for _, phase := range []string{"worker-report-only-1", "worker-report-only-1-packet-compact"} {
		t.Run(phase, func(t *testing.T) {
			repoRoot := initMutationRepo(t)
			st := newStateStoreT(t)
			if err := st.Write("last-request", "req"); err != nil {
				t.Fatal(err)
			}
			if err := st.Write("worker.id", "legacy-worker-session"); err != nil {
				t.Fatal(err)
			}
			if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
				Stage:          state.ResumeStageAutoFix,
				Phase:          phase,
				Role:           state.WorkerRole,
				Model:          "opus",
				ReadOnly:       true,
				Effort:         "high",
				Prompt:         "report only",
				OriginalPrompt: "report only",
				Request:        "req",
				ReviewNumber:   1,
				AutoFixes:      1,
				RateLimited:    true,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
				t.Fatal(err)
			}
			r := &mutatingRunner{repoRoot: repoRoot}
			out := &bytes.Buffer{}
			w := newMutationWorkflowShell(t, st)
			w.runner = r
			w.output = out
			w.config.RepoRoot = repoRoot
			w.captureSnapshot = state.CaptureGitSnapshot

			if err := w.ExecuteResume(); err != nil {
				t.Fatal(err)
			}
			requireReportOnlyFailClosed(t, w, out, "")
			if len(r.prompts) != 0 || len(r.probes) != 0 {
				t.Fatalf("旧checkpoint欠損時はworker/probeを1件も呼ばない: prompts=%d probes=%d", len(r.prompts), len(r.probes))
			}
			if !strings.Contains(out.String(), "resume再開前にreport-only開始前snapshotが欠損") {
				t.Fatalf("旧checkpoint欠損の原因が出力されていません: %q", out.String())
			}
			if _, err := st.LoadReportOnlyStartSnapshot(); err == nil {
				t.Fatal("resumeが旧checkpointへ新baselineを作成しています")
			}
			if got, err := st.Read("worker.id"); err != nil || got != "legacy-worker-session" {
				t.Fatalf("診断証拠のworker session IDが保持されていません: %q %v", got, err)
			}
		})
	}
}

// provider-unavailable停止の旧checkpointも同じくprobeより前のgateで停止し、
// 基準snapshot欠損を新baseline撮影で隠さない。
func TestReportOnlyProviderUnavailableResumeLegacyCheckpointWithoutSnapshotStopsBeforeCalls(t *testing.T) {
	repoRoot := initMutationRepo(t)
	st := newStateStoreT(t)
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("worker.id", "legacy-worker-session"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:                             state.ResumeStageAutoFix,
		Phase:                             "worker-report-only-1",
		Role:                              state.WorkerRole,
		Model:                             "opus",
		ReadOnly:                          true,
		Effort:                            "high",
		Prompt:                            "report only",
		OriginalPrompt:                    "report only",
		Request:                           "req",
		ReviewNumber:                      1,
		AutoFixes:                         1,
		ProviderUnavailable:               true,
		ProviderUnavailableClassification: "http-503",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusProviderUnavailable); err != nil {
		t.Fatal(err)
	}
	r := &mutatingRunner{repoRoot: repoRoot}
	out := &bytes.Buffer{}
	w := newMutationWorkflowShell(t, st)
	w.runner = r
	w.output = out
	w.config.RepoRoot = repoRoot
	w.captureSnapshot = state.CaptureGitSnapshot

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	requireReportOnlyFailClosed(t, w, out, "")
	if len(r.prompts) != 0 || len(r.probes) != 0 {
		t.Fatalf("旧checkpoint欠損時はprobeより前に停止しworker/probeを呼ばない: prompts=%d probes=%d", len(r.prompts), len(r.probes))
	}
	if !strings.Contains(out.String(), "resume再開前にreport-only開始前snapshotが欠損") {
		t.Fatalf("旧checkpoint欠損の原因が出力されていません: %q", out.String())
	}
	if _, err := st.LoadReportOnlyStartSnapshot(); err == nil {
		t.Fatal("resumeが旧checkpointへ新baselineを作成しています")
	}
}

// TestReportOnlyPostconditionScenarioPinnedInEscapedCorpusはescaped corpusがreport-only
// repo不変postconditionのproduction検出scenarioを保持することを固定する。corpus契約testは
// 存在するscenarioの妥当性だけを検証するため、当該scenarioの削除は本pin検証だけが検知する。
func TestReportOnlyPostconditionScenarioPinnedInEscapedCorpus(t *testing.T) {
	sc, mf := loadCorpus(t)
	found := ""
	for _, s := range sc.Scenarios {
		if s.ReportOnlyMutatesWorktree {
			found = s.ID
			break
		}
	}
	if found == "" {
		t.Fatal("escaped corpusにreport-only不変性破壊scenarioがありません")
	}
	for _, path := range []string{"codex/glm-worker/prompts/WORKER.md", "codex/glm-worker/prompts/REVIEWER.md"} {
		listed := false
		for _, e := range mf.InstructionFiles {
			if e.Path != path {
				continue
			}
			for _, sid := range e.Scenarios {
				if sid == found {
					listed = true
				}
			}
		}
		if !listed {
			t.Fatalf("manifestの%sが%sをpinしていません", path, found)
		}
	}
}

// 通常implementation auto-fixのresumeはreport-only推定の対象外で、開始前snapshotが
// 無くても従来どおりworker再実行からreviewへ進む。
func TestAutoFixResumeWithoutReportOnlyPhaseKeepsLegacyFlow(t *testing.T) {
	repoRoot := initMutationRepo(t)
	st := newStateStoreT(t)
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:          state.ResumeStageAutoFix,
		Phase:          "worker-auto-fix-1",
		Role:           state.WorkerRole,
		Model:          "opus",
		Effort:         "high",
		Prompt:         "fix",
		OriginalPrompt: "fix",
		Request:        "req",
		ReviewNumber:   1,
		AutoFixes:      1,
		RateLimited:    true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}
	r := &mutatingRunner{repoRoot: repoRoot, steps: []runnerStep{
		{output: implementedPacketWithRisk("fixed implementation", "LOW")},
		{output: needsSolReviewPacket()},
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
	if len(r.prompts) != 2 || r.readOnlyCalls[0] {
		t.Fatalf("通常auto-fix resumeはwrite capabilityでworker再実行からreviewへ進むべき: calls=%d readOnly=%v", len(r.prompts), r.readOnlyCalls)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q want waiting-sol-review", st.TaskStatus())
	}
	if strings.Contains(out.String(), "report-only") {
		t.Fatalf("通常auto-fix resumeへreport-only推定が誤適用されています: %q", out.String())
	}
	if _, err := st.LoadReportOnlyStartSnapshot(); err == nil {
		t.Fatal("通常auto-fix resumeでreport-only開始前snapshotを作らない")
	}
}
