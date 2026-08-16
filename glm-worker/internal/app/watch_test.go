package app

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

func writeTaskEventLines(t *testing.T, st *state.StateStore, taskID string, records ...state.TaskEventRecord) {
	t.Helper()
	for _, record := range records {
		if err := st.AppendTaskEvent(record); err != nil {
			t.Fatal(err)
		}
	}
}

func watchTestStore(t *testing.T) (*state.StateStore, config.AppConfig) {
	t.Helper()
	cfg := config.AppConfig{
		StateBase: t.TempDir(),
		RepoHash:  "watchhash",
		RepoRoot:  "/repo",
	}
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Write("task.id", "12345678-aaaa-bbbb-cccc-dddddddddddd"); err != nil {
		t.Fatal(err)
	}
	return st, cfg
}

// TestWatchRendersSavedEventsWithoutSideEffectsは保存済みevent logだけを読んで表示し、
// state書換・repo lockを行わないことを検証する。
func TestWatchRendersSavedEventsWithoutSideEffects(t *testing.T) {
	st, _ := watchTestStore(t)
	taskID := "12345678-aaaa-bbbb-cccc-dddddddddddd"
	writeTaskEventLines(t, st, taskID,
		state.TaskEventRecord{TaskID: taskID, CallID: "call-1", Role: "worker", Phase: "worker-new", Kind: "system", Subtype: "init", MessageModel: "glm-5.3"},
		state.TaskEventRecord{TaskID: taskID, CallID: "call-1", Role: "worker", Phase: "worker-new", Kind: "assistant", MessageModel: "glm-5.3", Blocks: []state.TaskBlockSummary{{Type: "thinking", Bytes: 456}, {Type: "tool_use", Name: "Bash", ToolID: "toolu_1", Bytes: 88}}, Usage: &state.TaskEventUsage{InputTokens: 100, OutputTokens: 7}},
		state.TaskEventRecord{TaskID: taskID, CallID: "call-2", Role: "reviewer", Phase: "reviewer-1", Resumed: true, Kind: "result", Subtype: "success", NumTurns: 3, TotalCostUSD: 0.25, DurationMS: 1500, Usage: &state.TaskEventUsage{OutputTokens: 20}},
		state.TaskEventRecord{TaskID: taskID, CallID: "call-2", Role: "reviewer", Phase: "reviewer-1", Kind: "user", Blocks: []state.TaskBlockSummary{{Type: "tool_result", Name: "Read", ToolID: "toolu_1", Bytes: 814, DurationMS: 456}}},
	)

	entriesBefore, err := os.ReadDir(st.Path("."))
	if err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	close(stop)
	out := &bytes.Buffer{}
	if err := printWatch(st, out, time.Millisecond, stop); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{
		"TASK_ID: " + taskID,
		"EVENT_LOG: " + st.TaskEventLogPath(taskID),
		"worker-new worker system init model=glm-5.3",
		"thinking:456b",
		"tool_use(Bash):88b",
		"in=100 out=7",
		"reviewer-1 reviewer resumed result success",
		"turns=3",
		"cost=0.2500",
		"dur=1500ms",
		"tool_result(Read):814b/456ms",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("watch表示に%qがありません: %s", want, rendered)
		}
	}
	if strings.Contains(rendered, "GLM_WORKER_PROBE_OK") || strings.Count(rendered, "\n") != 7 {
		t.Fatalf("watch表示 = %q", rendered)
	}

	entriesAfter, err := os.ReadDir(st.Path("."))
	if err != nil {
		t.Fatal(err)
	}
	if len(entriesBefore) != len(entriesAfter) {
		t.Fatalf("watchがstate dirを変更しました: %d -> %d", len(entriesBefore), len(entriesAfter))
	}
	if st.Exists("lock") {
		t.Fatal("watchがrepo lockを作成しました")
	}
}

// TestWatchSkipsCorruptLinesはevent logの部分破損行をskipして以後の行を表示する。
func TestWatchSkipsCorruptLines(t *testing.T) {
	st, _ := watchTestStore(t)
	taskID := "12345678-aaaa-bbbb-cccc-dddddddddddd"
	writeTaskEventLines(t, st, taskID,
		state.TaskEventRecord{TaskID: taskID, CallID: "call-1", Role: "worker", Phase: "worker-new", Kind: "assistant"},
	)
	path := st.TaskEventLogPath(taskID)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{\"version\":1,\"kind\":\"brokencorrupt\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	writeTaskEventLines(t, st, taskID,
		state.TaskEventRecord{TaskID: taskID, CallID: "call-1", Role: "worker", Phase: "worker-new", Seq: 2, Kind: "result", Subtype: "success"},
	)

	stop := make(chan struct{})
	close(stop)
	out := &bytes.Buffer{}
	if err := printWatch(st, out, time.Millisecond, stop); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "EVENT_SKIPPED") || strings.Count(rendered, "EVENT_SKIPPED") != 1 {
		t.Fatalf("破損行skip表示 = %q", rendered)
	}
	if !strings.Contains(rendered, "result success") {
		t.Fatalf("破損行以後の表示がありません: %q", rendered)
	}
}

// TestWatchFollowsAppendedEventsはfollow中の追記を表示する。
func TestWatchFollowsAppendedEvents(t *testing.T) {
	st, _ := watchTestStore(t)
	taskID := "12345678-aaaa-bbbb-cccc-dddddddddddd"
	writeTaskEventLines(t, st, taskID,
		state.TaskEventRecord{TaskID: taskID, CallID: "call-1", Role: "worker", Phase: "worker-new", Kind: "system", Subtype: "init"},
	)

	stop := make(chan struct{})
	out := &bytes.Buffer{}
	rendered := make(chan string, 1)
	go func() {
		err := printWatch(st, out, 5*time.Millisecond, stop)
		if err != nil {
			t.Error(err)
		}
		rendered <- out.String()
	}()

	time.Sleep(20 * time.Millisecond)
	writeTaskEventLines(t, st, taskID,
		state.TaskEventRecord{TaskID: taskID, CallID: "call-1", Role: "worker", Phase: "worker-new", Seq: 2, Kind: "result", Subtype: "success", NumTurns: 2},
	)
	time.Sleep(30 * time.Millisecond)
	close(stop)
	followOut := <-rendered

	if !strings.Contains(followOut, "system init") {
		t.Fatalf("既存行表示がありません: %q", followOut)
	}
	if !strings.Contains(followOut, "result success turns=2") {
		t.Fatalf("追記行のfollow表示がありません: %q", followOut)
	}
}

// TestWatchWithoutTaskOrLogはtask不在・event log不在で即座に終了する。
func TestWatchWithoutTaskOrLog(t *testing.T) {
	cfg := config.AppConfig{StateBase: t.TempDir(), RepoHash: "watchhash", RepoRoot: "/repo"}

	stop := make(chan struct{})
	close(stop)
	out := &bytes.Buffer{}
	if err := printWatch(state.AttachStateStore(cfg), out, time.Millisecond, stop); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "EVENT_LOG: none") {
		t.Fatalf("task不在表示 = %q", out.String())
	}

	st, _ := watchTestStore(t)
	out = &bytes.Buffer{}
	if err := printWatch(st, out, time.Millisecond, stop); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "EVENT_LOG_STATUS: empty") {
		t.Fatalf("log不在表示 = %q", out.String())
	}
}

// TestExecuteWatchDoesNotCreateStateは--watch実行がstate dirを一切作成・書換しない。
func TestExecuteWatchDoesNotCreateState(t *testing.T) {
	base := t.TempDir()
	cfg := config.AppConfig{StateBase: base, RepoHash: "watchhash", RepoRoot: "/repo"}
	cmd, err := ParseCommand([]string{"--watch"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Mode != ModeWatch {
		t.Fatalf("mode = %v", cmd.Mode)
	}
	out := &bytes.Buffer{}
	if err := Execute(cmd, cfg, nil, out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "EVENT_LOG: none") {
		t.Fatalf("watch出力 = %q", out.String())
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("--watchがstate dirを作成しました: %v", entries)
	}
}

func TestParseCommandWatchRejectsExtraArgs(t *testing.T) {
	if _, err := ParseCommand([]string{"--watch", "extra"}); err == nil {
		t.Fatal("余分な引数が受け入れられています")
	}
}
