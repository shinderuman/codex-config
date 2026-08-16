package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
