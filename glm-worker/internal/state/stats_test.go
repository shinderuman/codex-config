package state

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

func writeCorruptedTaskStats(t *testing.T, st *StateStore) {
	t.Helper()
	if err := os.WriteFile(st.Path(currentStatsFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStartNewTaskContinuesWithCorruptedStats(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	first, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	writeCorruptedTaskStats(t, st)

	warn, restore := captureStatsWarnings(t)
	defer restore()

	second, err := st.StartNewTask()
	if err != nil {
		t.Fatalf("StartNewTaskが破損mirrorで停止しました: %v", err)
	}
	if first == second {
		t.Fatal("taskがrotateしませんでした")
	}
	if st.TaskStatus() != TaskStatusActive {
		t.Fatalf("task.status = %q", st.TaskStatus())
	}
	if !strings.Contains(warn.String(), "WARNING") {
		t.Fatalf("破損mirrorの警告が出ませんでした: %q", warn.String())
	}
}

func TestSetTaskStatusContinuesWithCorruptedStats(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	writeCorruptedTaskStats(t, st)

	warn, restore := captureStatsWarnings(t)
	defer restore()

	if err := st.SetTaskStatus(TaskStatusComplete); err != nil {
		t.Fatalf("SetTaskStatusが破損mirrorで停止しました: %v", err)
	}
	if st.TaskStatus() != TaskStatusComplete {
		t.Fatalf("正規状態 task.status = %q", st.TaskStatus())
	}
	if !strings.Contains(warn.String(), "WARNING") {
		t.Fatalf("破損mirrorの警告が出ませんでした: %q", warn.String())
	}
}

func TestResetContinuesWithCorruptedStats(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	writeCorruptedTaskStats(t, st)

	warn, restore := captureStatsWarnings(t)
	defer restore()

	if err := st.Reset(); err != nil {
		t.Fatalf("Resetが破損mirrorで停止しました: %v", err)
	}
	if !strings.Contains(warn.String(), "WARNING") {
		t.Fatalf("破損mirrorの警告が出ませんでした: %q", warn.String())
	}
	if st.TaskStatus() != TaskStatus("none") {
		t.Fatalf("reset後の task.status = %q", st.TaskStatus())
	}
}

func TestRecordModelCallContinuesWithCorruptedStats(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	writeCorruptedTaskStats(t, st)

	warn, restore := captureStatsWarnings(t)
	defer restore()

	st.RecordModelCall(WorkerRole, "opus")

	if !strings.Contains(warn.String(), "WARNING") {
		t.Fatalf("破損mirrorの警告が出ませんでした: %q", warn.String())
	}
	if st.TaskStatus() != TaskStatusActive {
		t.Fatalf("正規状態 task.status = %q", st.TaskStatus())
	}
}

func TestStartNewTaskContinuesWhenArchiveWriteFails(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	first, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	statsDir := filepath.Join(st.dir, "stats")
	if err := os.MkdirAll(statsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(statsDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(statsDir, 0o700)

	warn, restore := captureStatsWarnings(t)
	defer restore()

	second, err := st.StartNewTask()
	if err != nil {
		t.Fatalf("StartNewTaskがarchive書き込み失敗で停止しました: %v", err)
	}
	if first == second {
		t.Fatal("taskがrotateしませんでした")
	}
	if st.TaskStatus() != TaskStatusActive {
		t.Fatalf("task.status = %q", st.TaskStatus())
	}
	if !strings.Contains(warn.String(), "WARNING") {
		t.Fatalf("archive書き込み失敗の警告が出ませんでした: %q", warn.String())
	}
}

func TestUpdateTaskStatsToleratesWriteFailure(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(st.dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(st.dir, 0o700)

	warn, restore := captureStatsWarnings(t)
	defer restore()

	st.RecordModelCall(WorkerRole, "opus")

	if !strings.Contains(warn.String(), "WARNING") {
		t.Fatalf("書き込み失敗の警告が出ませんでした: %q", warn.String())
	}
	if st.TaskStatus() != TaskStatusActive {
		t.Fatalf("正規状態 task.status = %q", st.TaskStatus())
	}
}

func TestAllTaskStatsSurfacesCorruptedMirror(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	writeCorruptedTaskStats(t, st)

	if _, err := st.AllTaskStats(); err == nil {
		t.Fatal("明示 --stats は破損mirrorをエラーとして返す必要があります")
	}
}
