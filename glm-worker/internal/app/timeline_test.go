package app

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

func timelineBaseTime() time.Time {
	return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
}

// TestTimelineRendersCallsToolsGraphAndAgingは保存済みevent logとtelemetryだけから
// call単位のrole/phase/session番号・観測窓・結果観測・tool種別別測定済みduration・
// 相対graph・session agingを表示することを検証する。
func TestTimelineRendersCallsToolsGraphAndAging(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	base := timelineBaseTime()
	writeTaskEventLines(t, st, taskID,
		state.TaskEventRecord{TaskID: taskID, CallID: "call-1", SessionID: "sess-a", Role: "worker", Phase: "worker-new", ModelAlias: "opus", Seq: 1, Timestamp: base, Kind: "system", Subtype: "init", MessageModel: "glm-5.3"},
		state.TaskEventRecord{TaskID: taskID, CallID: "call-1", SessionID: "sess-a", Role: "worker", Phase: "worker-new", ModelAlias: "opus", Seq: 2, Timestamp: base.Add(2 * time.Second), Kind: "assistant", MessageModel: "glm-5.3", Blocks: []state.TaskBlockSummary{
			{Type: "tool_use", Name: "Bash", ToolID: "t1", Bytes: 80},
			{Type: "tool_use", Name: "Bash", ToolID: "t2", Bytes: 81},
			{Type: "tool_use", Name: "Read", ToolID: "t3", Bytes: 60},
		}},
		state.TaskEventRecord{TaskID: taskID, CallID: "call-1", SessionID: "sess-a", Role: "worker", Phase: "worker-new", ModelAlias: "opus", Seq: 3, Timestamp: base.Add(3 * time.Second), Kind: "user", Blocks: []state.TaskBlockSummary{
			{Type: "tool_result", Name: "Bash", ToolID: "t1", Bytes: 100, DurationMS: 500},
			{Type: "tool_result", Name: "Bash", ToolID: "t2", Bytes: 101, DurationMS: 700, IsError: true},
			{Type: "tool_result", ToolID: "t4", Bytes: 102},
		}},
		state.TaskEventRecord{TaskID: taskID, CallID: "call-1", SessionID: "sess-a", Role: "worker", Phase: "worker-new", ModelAlias: "opus", Seq: 4, Timestamp: base.Add(9 * time.Second), Kind: "result", Subtype: "success", DurationMS: 9000, DurationAPIMS: 8000, NumTurns: 4, TotalCostUSD: 0.5, Usage: &state.TaskEventUsage{InputTokens: 100, CacheReadInputTokens: 20, OutputTokens: 30}},
		state.TaskEventRecord{TaskID: taskID, CallID: "call-2", SessionID: "sess-b", Role: "reviewer", Phase: "reviewer-1", ModelAlias: "sonnet", Resumed: true, Seq: 1, Timestamp: base.Add(5 * time.Minute), Kind: "assistant", MessageModel: "glm-4.7"},
		state.TaskEventRecord{TaskID: taskID, CallID: "call-2", SessionID: "sess-b", Role: "reviewer", Phase: "reviewer-1", ModelAlias: "sonnet", Resumed: true, Seq: 2, Timestamp: base.Add(5*time.Minute + 3*time.Second), Kind: "assistant", MessageModel: "glm-4.7"},
	)
	st.RecordModelCallLog(state.ModelCallLog{
		TaskID: taskID, CallType: state.CallTypeTask, SessionID: "sess-a", Role: state.WorkerRole,
		ModelAlias: "opus", StartedAt: base, CompletedAt: base.Add(9 * time.Second),
		TreeUsage:      state.TokenUsage{InputTokens: 100, CacheReadInputTokens: 20, OutputTokens: 30},
		WallDurationMS: 9000, TopLevelTurns: 4,
	})
	st.RecordModelCallLog(state.ModelCallLog{
		TaskID: taskID, CallType: state.CallTypeTask, SessionID: "sess-b", Role: state.ReviewerRole,
		ModelAlias: "sonnet", StartedAt: base.Add(5 * time.Minute), CompletedAt: base.Add(5*time.Minute + 3*time.Second),
		TreeUsage: state.TokenUsage{InputTokens: 50, OutputTokens: 5}, WallDurationMS: 3000,
	})

	var out bytes.Buffer
	if err := printTimeline(st, "", &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{
		"TASK_ID: " + taskID,
		"TASK_STATUS: active",
		"EVENT_LOG: " + st.TaskEventLogPath(taskID),
		"CALLS: 2",
		"CALL #1/2 role=worker phase=worker-new session=sess-a#1 model=opus(glm-5.3)",
		"CALL #1/2 WINDOW start=2026-08-16T12:00:00Z end=2026-08-16T12:00:09Z span=9000ms events=4",
		"CALL #1/2 RESULT status=success dur=9000ms api=8000ms turns=4 in=120 out=30 cost=0.5000",
		"CALL #1/2 TOOLS Bash uses=2 results=2 measured=2 sum=1200ms max=700ms errors=1; Read uses=1; unknown uses=0 results=1 unmeasured=1",
		"CALL #2/2 role=reviewer phase=reviewer-1 session=sess-b#1 resumed=true model=sonnet(glm-4.7)",
		"CALL #2/2 WINDOW start=2026-08-16T12:05:00Z end=2026-08-16T12:05:03Z span=3000ms events=2",
		"CALL #2/2 RESULT none",
		"CALL #2/2 TOOLS none",
		"TOOL_TOTALS: Bash uses=2 results=2 measured=2 sum=1200ms max=700ms errors=1; Read uses=1; unknown uses=0 results=1 unmeasured=1",
		"GRAPH_SPAN_MAX: 9000ms",
		"GRAPH #1 span=9000ms [" + strings.Repeat("#", 40) + "]",
		"GRAPH #2 span=3000ms [" + strings.Repeat("#", 13) + "]",
		"SESSION_AGING: role=worker model=opus id=sess-a calls=1 resumed=0 turns=4 in=120 out=30 lat_ms=9000",
		"SESSION_AGING: role=reviewer model=sonnet id=sess-b calls=1 resumed=0 turns=0 in=50 out=5 lat_ms=3000",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("timeline表示に%qがありません:\n%s", want, body)
		}
	}
}

// TestTimelineSkipsCorruptLinesはevent logの部分破損行をskip件数として報告し、
// 以後のrecord表示へ波及させない。
func TestTimelineSkipsCorruptLines(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	base := timelineBaseTime()
	writeTaskEventLines(t, st, taskID,
		state.TaskEventRecord{TaskID: taskID, CallID: "call-1", Role: "worker", Phase: "worker-new", Timestamp: base, Kind: "assistant"},
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
		state.TaskEventRecord{TaskID: taskID, CallID: "call-1", Role: "worker", Phase: "worker-new", Timestamp: base.Add(time.Second), Kind: "result", Subtype: "success", DurationMS: 1000},
	)

	var out bytes.Buffer
	if err := printTimeline(st, "", &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, "SKIPPED_EVENTS: 1") {
		t.Fatalf("skip件数表示がありません:\n%s", body)
	}
	if !strings.Contains(body, "CALL #1/1 RESULT status=success dur=1000ms") {
		t.Fatalf("破損行以後の表示がありません:\n%s", body)
	}
}

// TestTimelineCurrentTaskWithoutEventsはevent logがまだない現在taskを正常終了し、
// telemetry由来のsession agingだけでも表示する。
func TestTimelineCurrentTaskWithoutEvents(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	st.RecordModelCallLog(state.ModelCallLog{
		TaskID: taskID, CallType: state.CallTypeTask, SessionID: "sess-a", Role: state.WorkerRole,
		ModelAlias: "opus", StartedAt: timelineBaseTime(), CompletedAt: timelineBaseTime().Add(time.Second),
		WallDurationMS: 1000,
	})

	var out bytes.Buffer
	if err := printTimeline(st, "", &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{
		"TASK_ID: " + taskID,
		"EVENT_LOG: none",
		"SESSION_AGING: role=worker model=opus id=sess-a calls=1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("無event log表示に%qがありません:\n%s", want, body)
		}
	}
	if strings.Contains(body, "CALL #") || strings.Contains(body, "TOOL_TOTALS") {
		t.Fatalf("event logがないのにcall表示が出ています:\n%s", body)
	}
}

// TestTimelineExplicitTaskは明示指定task IDで現在task以外の保存済みevent log・stats
// 履歴status・telemetry agingを表示し、存在しないtask IDはerrorにする。
func TestTimelineExplicitTask(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	oldTaskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	base := timelineBaseTime()
	writeTaskEventLines(t, st, oldTaskID,
		state.TaskEventRecord{TaskID: oldTaskID, CallID: "call-1", SessionID: "sess-old", Role: "worker", Phase: "worker-new", ModelAlias: "opus", Timestamp: base, Kind: "result", Subtype: "success", DurationMS: 4000},
	)
	st.RecordModelCallLog(state.ModelCallLog{
		TaskID: oldTaskID, CallType: state.CallTypeTask, SessionID: "sess-old", Role: state.WorkerRole,
		ModelAlias: "opus", StartedAt: base, CompletedAt: base.Add(4 * time.Second), WallDurationMS: 4000,
	})
	if err := st.SetTaskStatus(state.TaskStatusComplete); err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := printTimeline(st, oldTaskID, &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{
		"TASK_ID: " + oldTaskID,
		"TASK_STATUS: complete",
		"CALL #1/1 role=worker phase=worker-new session=sess-old#1",
		"CALL #1/1 RESULT status=success dur=4000ms",
		"SESSION_AGING: role=worker model=opus id=sess-old calls=1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("明示task表示に%qがありません:\n%s", want, body)
		}
	}

	out.Reset()
	if err := printTimeline(st, "12345678-1234-4234-8123-123456789abc", &out); err == nil {
		t.Fatalf("存在しないtask IDがerrorになりません: %s", out.String())
	}
}

// writeTimelineSentinelはstate root外へ置いた読まれてはならないevent logのsentinelを
// 書く。path traversal可能な実装なら ../../evil は <StateBase>/evil.jsonl へ解決される。
func writeTimelineSentinel(t *testing.T, cfg config.AppConfig) {
	t.Helper()
	sentinel := state.TaskEventRecord{
		Version: 1, TaskID: "evil", CallID: "sentinel-call", Role: "sentinel-role",
		Phase: "sentinel-phase", Timestamp: timelineBaseTime(), Kind: "assistant",
	}
	data, err := json.Marshal(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg.StateBase, "evil.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestTimelineRejectsTaskIDOutsideGeneratedFormは明示task IDの生成形式検証が
// filesystemへのprobe/readより先に働き、state root外のsentinelを読まないことを検証する。
func TestTimelineRejectsTaskIDOutsideGeneratedForm(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeTimelineSentinel(t, cfg)

	for _, taskID := range []string{
		"../../evil",
		"../../../sessions/other/events/x",
		"/etc/hostname",
		"12345678-1234-1234-8123-123456789abc",
		"12345678-1234-4234-c123-123456789abc",
		"12345678-1234-4234-8123-123456789ABC",
		"none",
	} {
		out := &bytes.Buffer{}
		if err := printTimeline(st, taskID, out); err == nil {
			t.Fatalf("不正task ID %qがerrorになりません: %s", taskID, out.String())
		}
		if body := out.String(); body != "" {
			t.Fatalf("不正task ID %qが出力しました: %s", taskID, body)
		}
		if strings.Contains(out.String(), "sentinel-role") {
			t.Fatalf("state root外のsentinelが読まれました: %s", out.String())
		}
	}
}

// TestTimelineRejectsTamperedCurrentTaskIDは現在taskのtask.idが破損・改変されていても
// state root外へ出ないことを検証する。
func TestTimelineRejectsTamperedCurrentTaskID(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeTimelineSentinel(t, cfg)
	if err := st.Write("task.id", "../../evil"); err != nil {
		t.Fatal(err)
	}

	out := &bytes.Buffer{}
	if err := printTimeline(st, "", out); err == nil {
		t.Fatalf("改変task.idがerrorになりません: %s", out.String())
	}
	if body := out.String(); body != "" || strings.Contains(body, "sentinel-role") {
		t.Fatalf("改変task.idで出力またはsentinel読取がありました: %q", body)
	}
}

// TestExecuteTimelineRejectsTraversalはproduction Execute経路でも生成形式検証が
// state root外のsentinel読取を防ぐことを検証する。
func TestExecuteTimelineRejectsTraversal(t *testing.T) {
	cfg := newAppConfig(t)
	writeTimelineSentinel(t, cfg)
	cmd, err := ParseCommand([]string{"--timeline", "../../evil"})
	if err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	if err := Execute(cmd, cfg, nil, out, io.Discard); err == nil {
		t.Fatalf("traversal task IDがerrorになりません: %s", out.String())
	}
	if body := out.String(); body != "" || strings.Contains(body, "sentinel-role") {
		t.Fatalf("traversal task IDで出力またはsentinel読取がありました: %q", body)
	}
}

// TestExecuteTimelineDoesNotCreateStateは--timeline実行がstate dirを一切作成・書換しない。
func TestExecuteTimelineDoesNotCreateState(t *testing.T) {
	base := t.TempDir()
	cfg := config.AppConfig{StateBase: base, RepoHash: "timelinehash", RepoRoot: "/repo"}
	cmd, err := ParseCommand([]string{"--timeline"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Mode != ModeTimeline || cmd.Payload != "" {
		t.Fatalf("command = %+v", cmd)
	}
	out := &bytes.Buffer{}
	if err := Execute(cmd, cfg, nil, out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "TASK_ID: none") || !strings.Contains(out.String(), "EVENT_LOG: none") {
		t.Fatalf("timeline出力 = %q", out.String())
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("--timelineがstate dirを作成しました: %v", entries)
	}
}

func TestParseCommandTimeline(t *testing.T) {
	cmd, err := ParseCommand([]string{"--timeline", "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Mode != ModeTimeline || cmd.Payload != "task-1" {
		t.Fatalf("command = %+v", cmd)
	}
	if _, err := ParseCommand([]string{"--timeline", "task-1", "extra"}); err == nil {
		t.Fatal("余分な引数が受け入れられています")
	}
}
