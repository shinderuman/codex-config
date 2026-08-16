package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
)

func newEventTestStore(t *testing.T) *StateStore {
	t.Helper()
	st, err := NewStateStore(config.AppConfig{
		StateBase: t.TempDir(),
		RepoHash:  "eventhash",
		RepoRoot:  "/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestAppendTaskEventFillsVersionAndAppendsLines(t *testing.T) {
	st := newEventTestStore(t)
	base := TaskEventRecord{
		TaskID: "task-1",
		CallID: "call-1",
		Role:   "worker",
		Phase:  "worker-new",
		Kind:   "assistant",
	}
	if err := st.AppendTaskEvent(base); err != nil {
		t.Fatal(err)
	}
	sequel := base
	sequel.Kind = "result"
	if err := st.AppendTaskEvent(sequel); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(st.TaskEventLogPath("task-1"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("追記行数 = %d: %s", len(lines), data)
	}
	first, err := ParseTaskEventLine([]byte(lines[0]))
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != taskEventLogVersion || first.Seq != 0 || first.Timestamp.IsZero() {
		t.Fatalf("record = %#v", first)
	}
	info, err := os.Stat(st.TaskEventLogPath("task-1"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("event log権限 = %v", info.Mode().Perm())
	}
}

func TestParseTaskEventLineRejectsCorruptAndOldVersion(t *testing.T) {
	if _, err := ParseTaskEventLine([]byte("not json")); err == nil {
		t.Fatal("破損行がparseできています")
	}
	old, err := json.Marshal(TaskEventRecord{Version: taskEventLogVersion + 1, Kind: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseTaskEventLine(old); err == nil {
		t.Fatal("旧version recordが読み飛ばされていません")
	}
}

func TestAppendTaskEventIsolatedPerTask(t *testing.T) {
	st := newEventTestStore(t)
	for _, taskID := range []string{"task-a", "task-b"} {
		record := TaskEventRecord{TaskID: taskID, CallID: "c", Role: "reviewer", Phase: "reviewer-1", Kind: "result"}
		if err := st.AppendTaskEvent(record); err != nil {
			t.Fatal(err)
		}
	}
	for _, taskID := range []string{"task-a", "task-b"} {
		data, err := os.ReadFile(st.TaskEventLogPath(taskID))
		if err != nil {
			t.Fatal(err)
		}
		if len(strings.Split(strings.TrimSpace(string(data)), "\n")) != 1 {
			t.Fatalf("task %sのevent log = %s", taskID, data)
		}
	}
	if got := filepath.Base(filepath.Dir(st.TaskEventLogPath("task-a"))); got != "events" {
		t.Fatalf("event log配置dir = %q", got)
	}
}

// writeEventLogWithmtimeは旧taskのevent logを指定mtimeで作る。retentionの新旧判定は
// mtime順のため、作成順と異なる時刻を与えて順序依存を検出できるようにする。
func writeEventLogWithmtime(t *testing.T, st *StateStore, taskID string, mtime time.Time) {
	t.Helper()
	if err := st.AppendTaskEvent(TaskEventRecord{TaskID: taskID, CallID: "c", Role: "worker", Phase: "p", Kind: "result"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(st.TaskEventLogPath(taskID), mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestPruneTaskEventLogsKeepsNewestAndCurrent(t *testing.T) {
	st := newEventTestStore(t)
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for index, taskID := range []string{"old-a", "old-b", "old-c", "old-d"} {
		writeEventLogWithmtime(t, st, taskID, base.Add(time.Duration(index)*time.Hour))
	}
	writeEventLogWithmtime(t, st, "current", base.Add(-24*time.Hour))

	st.PruneTaskEventLogs(2, "current")

	for _, taskID := range []string{"old-a", "old-b"} {
		if _, err := os.Stat(st.TaskEventLogPath(taskID)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("古いlog %sが残っています: %v", taskID, err)
		}
	}
	for _, taskID := range []string{"old-c", "old-d", "current"} {
		if _, err := os.Stat(st.TaskEventLogPath(taskID)); err != nil {
			t.Fatalf("残すべきlog %sがありません: %v", taskID, err)
		}
	}
}

// TestPruneTaskEventLogsFailureStopsQuietlyは削除失敗(削除できないfile)がwarningだけに
// 留まり、他logの整理や呼出元へ影響しないことを検証する。
func TestPruneTaskEventLogsFailureStopsQuietly(t *testing.T) {
	st := newEventTestStore(t)
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	writeEventLogWithmtime(t, st, "survivor", base)
	unremovable := st.TaskEventLogPath("stuck")
	if err := os.MkdirAll(filepath.Join(unremovable, "inner"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(unremovable, base.Add(-time.Hour), base.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	warnings, restore := captureStatsWarnings(t)
	defer restore()
	st.PruneTaskEventLogs(1, "current")

	if _, err := os.Stat(st.TaskEventLogPath("survivor")); err != nil {
		t.Fatalf("削除失敗とは無関係のlogが削除されました: %v", err)
	}
	if !strings.Contains(warnings.String(), "retention整理に失敗") {
		t.Fatalf("整理失敗warning = %q", warnings.String())
	}
}

// TestStartNewTaskPrunesOldEventLogsは新規task開始が旧task event logのretention整理を
// best-effortで行い、新規taskのlogを作成済みstateへ影響させないことを検証する。
func TestStartNewTaskPrunesOldEventLogs(t *testing.T) {
	st := newEventTestStore(t)
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for index := 0; index <= retainedTaskEventLogs; index++ {
		writeEventLogWithmtime(t, st, fmt.Sprintf("history-%02d", index), base.Add(time.Duration(index)*time.Hour))
	}

	second, err := st.StartNewTask()
	if err != nil {
		t.Fatalf("retention整理で新規task開始が失敗しました: %v", err)
	}

	if _, err := os.Stat(st.TaskEventLogPath("history-00")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("最古の旧logが残っています: %v", err)
	}
	for index := 1; index <= retainedTaskEventLogs; index++ {
		if _, err := os.Stat(st.TaskEventLogPath(fmt.Sprintf("history-%02d", index))); err != nil {
			t.Fatalf("保持対象の旧log %02dがありません: %v", index, err)
		}
	}
	if second == "" {
		t.Fatal("新規task IDが採番されていません")
	}
}
