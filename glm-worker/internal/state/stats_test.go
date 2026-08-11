package state

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestAllTaskStatsSkipsVersion1(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	currentTask, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(st.dir, "stats", "legacy.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"version":1,"task_id":"legacy","model_calls":99,"input_tokens_by_alias":{"opus":999}}`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	all, err := st.AllTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].TaskID != currentTask || all[0].Version != taskStatsVersion {
		t.Fatalf("version 1を除外したstats = %#v", all)
	}
}

func TestUpdateTaskStatsRebuildsVersion1Mirror(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	legacy := `{"version":1,"task_id":"` + taskID + `","model_calls":99,"input_tokens_by_alias":{"opus":999}}`
	if err := os.WriteFile(st.Path(currentStatsFile), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	warnings, restore := captureStatsWarnings(t)
	defer restore()

	st.RecordModelCall(WorkerRole, "opus")

	stats, err := st.loadTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Version != taskStatsVersion || stats.ModelCalls != 1 || stats.InputTokensByAlias["opus"] != 0 {
		t.Fatalf("version 1から再構築したstats = %#v", stats)
	}
	if warnings.Len() == 0 {
		t.Fatal("version 1を破棄したwarningがありません")
	}
}
