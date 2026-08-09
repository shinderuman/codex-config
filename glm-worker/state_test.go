package main

import (
	"path/filepath"
	"testing"
)

func TestSessionIDPersists(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}

	first, ready, err := state.SessionID(workerRole)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("new session should not be ready")
	}

	if err := state.MarkReady(workerRole); err != nil {
		t.Fatal(err)
	}

	second, ready, err := state.SessionID(workerRole)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("session should be ready")
	}
	if first != second {
		t.Fatalf("session changed: %s -> %s", first, second)
	}

	if filepath.Base(state.Path("worker.id")) != "worker.id" {
		t.Fatal("unexpected state path")
	}
}

func TestStartNewTaskRotatesSessions(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}

	firstTask, err := state.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	firstWorker, _, err := state.SessionID(workerRole)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkReady(workerRole); err != nil {
		t.Fatal(err)
	}

	secondTask, err := state.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	secondWorker, ready, err := state.SessionID(workerRole)
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
	if state.TaskStatus() != taskStatusActive {
		t.Fatalf("task status = %q", state.TaskStatus())
	}

	all, err := state.allTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("task stats count = %d, want 2", len(all))
	}
	statsByTask := make(map[string]taskStats, len(all))
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
	state := &stateStore{dir: t.TempDir()}
	if _, err := state.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordModelCall(workerRole); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordModelCall(reviewerRole); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordPacketCompaction(); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordSolPacket(packetFromLines([]string{
		"STATUS: PASS",
		"RISK: LOW",
	})); err != nil {
		t.Fatal(err)
	}

	stats, err := state.loadTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.ModelCalls != 2 || stats.WorkerCalls != 1 || stats.ReviewerCalls != 1 {
		t.Fatalf("model counters = %#v", stats)
	}
	if stats.PacketCompactions != 1 || stats.PassPackets != 1 || stats.SolPacketBytes == 0 {
		t.Fatalf("packet counters = %#v", stats)
	}
}

func TestTaskStatusInfersLegacyState(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}
	if err := state.Write("task.id", "legacy-task"); err != nil {
		t.Fatal(err)
	}
	if err := state.Write("last-review", "STATUS: NEEDS_SOL_REVIEW\nRISK: HIGH"); err != nil {
		t.Fatal(err)
	}
	if status := state.TaskStatus(); status != taskStatusWaitingSolReview {
		t.Fatalf("legacy task status = %q", status)
	}

	if err := state.Remove("last-review"); err != nil {
		t.Fatal(err)
	}
	if err := state.Touch("pending-decision"); err != nil {
		t.Fatal(err)
	}
	if status := state.TaskStatus(); status != taskStatusWaitingDecision {
		t.Fatalf("legacy pending decision status = %q", status)
	}
}

func TestTaskStatsLazilyInitializesLegacyTask(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}
	if err := state.Write("task.id", "legacy-task"); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordModelCall(workerRole); err != nil {
		t.Fatal(err)
	}

	stats, err := state.loadTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TaskID != "legacy-task" || stats.ModelCalls != 1 {
		t.Fatalf("legacy task stats = %#v", stats)
	}
}
