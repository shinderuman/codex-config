package workflow

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// readRoundLogTは現在taskのround logを読むtest helper。
func readRoundLogT(t *testing.T, st *state.StateStore) []state.RoundRecord {
	t.Helper()
	records, err := st.ReadRoundRecords(st.ReadOr("task.id", ""))
	if err != nil {
		t.Fatalf("round log読み取り: %v", err)
	}
	return records
}

// TestConvergenceRecordsBaselineAndReviewRoundsはnew task実行がbaseline recordと
// round 1 record(WorkerPhase・ReviewNumber・snapshot)を書くことを検証する。
func TestConvergenceRecordsBaselineAndReviewRounds(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	records := readRoundLogT(t, st)
	if len(records) != 2 {
		t.Fatalf("record数 = %d want 2: %+v", len(records), records)
	}
	baseline := records[0]
	if baseline.WorkerPhase != state.RoundWorkerPhaseBaseline || baseline.ReviewNumber != 0 {
		t.Fatalf("baseline record = %+v", baseline)
	}
	if baseline.Snapshot != (state.SnapshotDigest{Head: "test-head", IndexDigest: "test-index", WorktreeDigest: "test-worktree"}) {
		t.Fatalf("baseline snapshot = %+v", baseline.Snapshot)
	}
	round := records[1]
	if round.Seq != 2 || round.ReviewNumber != 1 || round.AutoFixes != 0 || round.WorkerPhase != "worker-new" {
		t.Fatalf("round 1 record = %+v", round)
	}
	if round.Snapshot != baseline.Snapshot {
		t.Fatalf("round 1 snapshot = %+v", round.Snapshot)
	}
}

// TestConvergenceRecordsAutoFixRoundsはauto-fix経路のround recordがreview番号と
// worker-auto-fix phaseを正しく持つことを検証する。
func TestConvergenceRecordsAutoFixRounds(t *testing.T) {
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
	records := readRoundLogT(t, st)
	if len(records) != 3 {
		t.Fatalf("record数 = %d want 3: %+v", len(records), records)
	}
	if records[1].ReviewNumber != 1 || records[1].WorkerPhase != "worker-new" || records[1].AutoFixes != 0 {
		t.Fatalf("round 1 record = %+v", records[1])
	}
	if records[2].ReviewNumber != 2 || records[2].WorkerPhase != "worker-auto-fix-1" || records[2].AutoFixes != 1 {
		t.Fatalf("round 2 record = %+v", records[2])
	}
}

// TestConvergenceRecordsDecisionRoundはSol decision実行のround recordが
// worker-decision phaseを持つことを検証する。
func TestConvergenceRecordsDecisionRound(t *testing.T) {
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
	records := readRoundLogT(t, st)
	if len(records) != 1 {
		t.Fatalf("decision経路のrecord数 = %d want 1: %+v", len(records), records)
	}
	if records[0].WorkerPhase != "worker-decision" || records[0].ReviewNumber != 1 {
		t.Fatalf("decision round record = %+v", records[0])
	}
}

// TestConvergenceCaptureErrorRecordedNotFatalはpath観測失敗がCaptureErrorへ記録
// されるだけでreview flowを crashingさせないことを検証する。観測失敗は
// self-protection HIGHへ倒されるためrisk floor reemitまで含む経路になる。
func TestConvergenceCaptureErrorRecordedNotFatal(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.collectChangedPaths = func(string, string) ([]string, error) {
		return nil, errors.New("changed paths unavailable")
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("観測失敗でreview flowが止まっています: %s", st.TaskStatus())
	}
	records := readRoundLogT(t, st)
	if len(records) != 2 {
		t.Fatalf("record数 = %d want 2", len(records))
	}
	if records[0].CaptureError == "" || records[1].CaptureError == "" {
		t.Fatalf("CaptureErrorが記録されていません: %+v %+v", records[0], records[1])
	}
	if records[1].Paths != nil {
		t.Fatalf("観測失敗時にPathsが残っています: %+v", records[1].Paths)
	}
}

// TestConvergenceRecordAppendFailureDoesNotAffectTaskはround log追記失敗がtask
// 完了へ影響しないことを検証する。
func TestConvergenceRecordAppendFailureDoesNotAffectTask(t *testing.T) {
	st := newStateStoreT(t)
	taskID := st.ReadOr("task.id", "")
	if err := os.MkdirAll(st.RoundLogPath(taskID), 0o700); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("round log失敗でtaskが完了していません: %s", st.TaskStatus())
	}
}

// TestConvergencePathObservationUsesWorktreeはround recordのpath観測がrepo
// worktreeの実内容(digest・削除)を読むことを検証する。
func TestConvergencePathObservationUsesWorktree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.config.RepoRoot = root
	w.collectChangedPaths = func(string, string) ([]string, error) {
		return []string{"main.go", "gone.go"}, nil
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	records := readRoundLogT(t, st)
	round := records[1]
	if len(round.Paths) != 2 {
		t.Fatalf("path観測数 = %d want 2: %+v", len(round.Paths), round.Paths)
	}
	if round.Paths[0].Path != "gone.go" || !round.Paths[0].Deleted {
		t.Fatalf("削除path観測 = %+v", round.Paths[0])
	}
	if round.Paths[1].Path != "main.go" || round.Paths[1].FullDigest == "" || round.Paths[1].SemanticDigest == "" {
		t.Fatalf("通常path観測 = %+v", round.Paths[1])
	}
}
