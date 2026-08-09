package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStatsWarningsはstatsWarnOutを一時的に置き換え、警告文字列を返す。
func captureStatsWarnings(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	previous := statsWarnOut
	statsWarnOut = &buf
	return &buf, func() { statsWarnOut = previous }
}

func writeCorruptedTaskStats(t *testing.T, state *stateStore) {
	t.Helper()
	if err := os.WriteFile(state.Path(currentStatsFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStartNewTaskContinuesWithCorruptedStats(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}
	first, err := state.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	writeCorruptedTaskStats(t, state)

	warn, restore := captureStatsWarnings(t)
	defer restore()

	second, err := state.StartNewTask()
	if err != nil {
		t.Fatalf("StartNewTaskが破損mirrorで停止しました: %v", err)
	}
	if first == second {
		t.Fatal("taskがrotateしませんでした")
	}
	if state.TaskStatus() != taskStatusActive {
		t.Fatalf("task.status = %q", state.TaskStatus())
	}
	if !strings.Contains(warn.String(), "WARNING") {
		t.Fatalf("破損mirrorの警告が出ませんでした: %q", warn.String())
	}
}

func TestSetTaskStatusContinuesWithCorruptedStats(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}
	if _, err := state.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	writeCorruptedTaskStats(t, state)

	warn, restore := captureStatsWarnings(t)
	defer restore()

	if err := state.SetTaskStatus(taskStatusComplete); err != nil {
		t.Fatalf("SetTaskStatusが破損mirrorで停止しました: %v", err)
	}
	if state.TaskStatus() != taskStatusComplete {
		t.Fatalf("正規状態 task.status = %q", state.TaskStatus())
	}
	if !strings.Contains(warn.String(), "WARNING") {
		t.Fatalf("破損mirrorの警告が出ませんでした: %q", warn.String())
	}
}

func TestResetStateContinuesWithCorruptedStats(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}
	if _, err := state.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	writeCorruptedTaskStats(t, state)

	warn, restore := captureStatsWarnings(t)
	defer restore()

	if err := resetState(state); err != nil {
		t.Fatalf("resetStateが破損mirrorで停止しました: %v", err)
	}
	if !strings.Contains(warn.String(), "WARNING") {
		t.Fatalf("破損mirrorの警告が出ませんでした: %q", warn.String())
	}
	if state.TaskStatus() != taskStatus("none") {
		t.Fatalf("reset後の task.status = %q", state.TaskStatus())
	}
}

func TestRunModelContinuesWithCorruptedStats(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}
	if _, err := state.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	writeCorruptedTaskStats(t, state)

	warn, restore := captureStatsWarnings(t)
	defer restore()

	runner := &scriptedRunner{outputs: []string{
		"PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: LOW\nSUMMARY: ok\nREQUIREMENT_COVERAGE: covered\nTESTS: pass\nUNVERIFIED: none\nPACKET_END\n",
	}}
	w := newWorkflow(appConfig{RoutineEffort: "high"}, state, runner)
	w.temp = t.TempDir()

	result, err := w.runModel(resumeCheckpoint{
		Stage:   resumeStageWorker,
		Phase:   "worker-new",
		Role:    workerRole,
		Effort:  "high",
		Prompt:  "original",
		Request: "request",
	})
	if err != nil {
		t.Fatalf("runModelが破損mirrorで停止しました: %v", err)
	}
	if result.Status() != "IMPLEMENTED" {
		t.Fatalf("status = %q", result.Status())
	}
	if !strings.Contains(warn.String(), "WARNING") {
		t.Fatalf("破損mirrorの警告が出ませんでした: %q", warn.String())
	}
}

func TestStartNewTaskContinuesWhenArchiveWriteFails(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}
	first, err := state.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	statsDir := filepath.Join(state.dir, "stats")
	if err := os.MkdirAll(statsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(statsDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(statsDir, 0o700)

	warn, restore := captureStatsWarnings(t)
	defer restore()

	second, err := state.StartNewTask()
	if err != nil {
		t.Fatalf("StartNewTaskがarchive書き込み失敗で停止しました: %v", err)
	}
	if first == second {
		t.Fatal("taskがrotateしませんでした")
	}
	if state.TaskStatus() != taskStatusActive {
		t.Fatalf("task.status = %q", state.TaskStatus())
	}
	if !strings.Contains(warn.String(), "WARNING") {
		t.Fatalf("archive書き込み失敗の警告が出ませんでした: %q", warn.String())
	}
}

func TestUpdateTaskStatsToleratesWriteFailure(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}
	if _, err := state.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state.dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(state.dir, 0o700)

	warn, restore := captureStatsWarnings(t)
	defer restore()

	state.RecordModelCall(workerRole)

	if !strings.Contains(warn.String(), "WARNING") {
		t.Fatalf("書き込み失敗の警告が出ませんでした: %q", warn.String())
	}
	if state.TaskStatus() != taskStatusActive {
		t.Fatalf("正規状態 task.status = %q", state.TaskStatus())
	}
}

func TestStatsCommandSurfacesCorruptedMirror(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}
	if _, err := state.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	writeCorruptedTaskStats(t, state)

	if err := printStats(state); err == nil {
		t.Fatal("明示 --stats は破損mirrorをエラーとして返す必要があります")
	}
}
