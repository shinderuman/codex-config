package state

import (
	"testing"
	"time"
)

func timelineBaseTime() time.Time {
	return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
}

// TestCallsFromTaskEventsAggregatesPerCallはcall_id単位の集計でrole/phase/session・
// session内call番号・結果観測・tool種別別の測定済みdurationと未測定数を出すことを検証する。
func TestCallsFromTaskEventsAggregatesPerCall(t *testing.T) {
	base := timelineBaseTime()
	records := []TaskEventRecord{
		{
			TaskID: "task-1", CallID: "call-1", SessionID: "sess-a", Role: "worker", Phase: "worker-new",
			ModelAlias: "opus", Seq: 1, Timestamp: base, Kind: "system", Subtype: "init", MessageModel: "glm-5.3",
		},
		{
			TaskID: "task-1", CallID: "call-1", SessionID: "sess-a", Role: "worker", Phase: "worker-new",
			ModelAlias: "opus", Seq: 2, Timestamp: base.Add(2 * time.Second), Kind: "assistant", MessageModel: "glm-5.3",
			Blocks: []TaskBlockSummary{
				{Type: "tool_use", Name: "Bash", ToolID: "t1", Bytes: 80},
				{Type: "tool_use", Name: "Bash", ToolID: "t2", Bytes: 81},
				{Type: "tool_use", Name: "Read", ToolID: "t3", Bytes: 60},
			},
		},
		{
			TaskID: "task-1", CallID: "call-1", SessionID: "sess-a", Role: "worker", Phase: "worker-new",
			ModelAlias: "opus", Seq: 3, Timestamp: base.Add(3 * time.Second), Kind: "user",
			Blocks: []TaskBlockSummary{
				{Type: "tool_result", Name: "Bash", ToolID: "t1", Bytes: 100, DurationMS: 500},
				{Type: "tool_result", Name: "Bash", ToolID: "t2", Bytes: 101, DurationMS: 700, IsError: true},
				{Type: "tool_result", ToolID: "t4", Bytes: 102},
			},
		},
		{
			TaskID: "task-1", CallID: "call-1", SessionID: "sess-a", Role: "worker", Phase: "worker-new",
			ModelAlias: "opus", Seq: 4, Timestamp: base.Add(9 * time.Second), Kind: "result", Subtype: "success",
			DurationMS: 9000, DurationAPIMS: 8000, NumTurns: 4, TotalCostUSD: 0.5,
			Usage: &TaskEventUsage{InputTokens: 100, CacheReadInputTokens: 20, OutputTokens: 30},
		},
		{
			TaskID: "task-1", CallID: "call-2", SessionID: "sess-a", Role: "worker", Phase: "worker-resume",
			ModelAlias: "opus", Resumed: true, Seq: 1, Timestamp: base.Add(60 * time.Second), Kind: "assistant",
			MessageModel: "glm-5.3",
		},
		{
			TaskID: "task-1", CallID: "call-3", SessionID: "sess-b", Role: "reviewer", Phase: "reviewer-1",
			ModelAlias: "sonnet", Seq: 1, Timestamp: base.Add(120 * time.Second), Kind: "assistant",
		},
	}

	entries := CallsFromTaskEvents(records)
	if len(entries) != 3 {
		t.Fatalf("call数 = %d: %+v", len(entries), entries)
	}

	first := entries[0]
	if first.CallID != "call-1" || first.Role != "worker" || first.Phase != "worker-new" ||
		first.SessionID != "sess-a" || first.ModelAlias != "opus" || first.MessageModel != "glm-5.3" {
		t.Fatalf("call-1識別 = %+v", first)
	}
	if first.SessionCallIndex != 1 || first.SessionCalls != 2 {
		t.Fatalf("call-1 session内番号 = %d/%d", first.SessionCallIndex, first.SessionCalls)
	}
	if first.FirstAt != base || first.LastAt != base.Add(9*time.Second) || first.Events != 4 {
		t.Fatalf("call-1観測窓 = %v..%v events=%d", first.FirstAt, first.LastAt, first.Events)
	}
	if !first.ResultObserved || first.ResultSubtype != "success" || first.DurationMS != 9000 ||
		first.DurationAPIMS != 8000 || first.NumTurns != 4 || first.TotalCostUSD != 0.5 || first.Usage == nil {
		t.Fatalf("call-1結果観測 = %+v", first)
	}
	wantTools := []CallTimelineTool{
		{Name: "Bash", Uses: 2, Results: 2, Measured: 2, MeasuredSumMS: 1200, MeasuredMaxMS: 700, Errors: 1},
		{Name: "Read", Uses: 1},
		{Name: "unknown", Results: 1, Unmeasured: 1},
	}
	assertTimelineTools(t, first.Tools, wantTools)

	second := entries[1]
	if second.CallID != "call-2" || !second.Resumed || second.SessionCallIndex != 2 || second.SessionCalls != 2 {
		t.Fatalf("call-2 = %+v", second)
	}
	if second.ResultObserved || second.DurationMS != 0 || second.Usage != nil {
		t.Fatalf("result未観測callの結果field = %+v", second)
	}
	third := entries[2]
	if third.CallID != "call-3" || third.Role != "reviewer" || third.SessionCallIndex != 1 || third.SessionCalls != 1 {
		t.Fatalf("call-3 = %+v", third)
	}
}

// TestCallsFromTaskEventsOutOfOrderTimestampsはfile順と時刻順が一致しないrecordでも
// 観測窓を実際のtimestamp最小最大から算出する。
func TestCallsFromTaskEventsOutOfOrderTimestamps(t *testing.T) {
	base := timelineBaseTime()
	records := []TaskEventRecord{
		{CallID: "call-1", Role: "worker", Timestamp: base.Add(10 * time.Second), Kind: "user"},
		{CallID: "call-1", Role: "worker", Timestamp: base, Kind: "system"},
	}
	entries := CallsFromTaskEvents(records)
	if len(entries) != 1 {
		t.Fatalf("call数 = %d", len(entries))
	}
	if entries[0].FirstAt != base || entries[0].LastAt != base.Add(10*time.Second) {
		t.Fatalf("観測窓 = %v..%v", entries[0].FirstAt, entries[0].LastAt)
	}
}

// TestCallsFromTaskEventsLastResultWinsは同一call内の複数result recordで最後のものを
// 採用する(streamの最終result eventが観測値)。
func TestCallsFromTaskEventsLastResultWins(t *testing.T) {
	base := timelineBaseTime()
	records := []TaskEventRecord{
		{CallID: "call-1", Role: "worker", Timestamp: base, Kind: "result", Subtype: "error_during_execution", IsError: true},
		{CallID: "call-1", Role: "worker", Timestamp: base.Add(time.Second), Kind: "result", Subtype: "success", DurationMS: 1000},
	}
	entries := CallsFromTaskEvents(records)
	if entries[0].ResultSubtype != "success" || entries[0].IsError || entries[0].DurationMS != 1000 {
		t.Fatalf("最終result = %+v", entries[0])
	}
}

// TestSumCallTimelineToolsはcall横断のtool合算で件数・合計時間・最大時間を統合する。
func TestSumCallTimelineTools(t *testing.T) {
	entries := []CallTimelineEntry{
		{Tools: []CallTimelineTool{
			{Name: "Bash", Uses: 2, Results: 2, Measured: 2, MeasuredSumMS: 300, MeasuredMaxMS: 200},
			{Name: "Read", Uses: 1, Results: 1, Unmeasured: 1},
		}},
		{Tools: []CallTimelineTool{
			{Name: "Bash", Uses: 1, Results: 1, Measured: 1, MeasuredSumMS: 900, MeasuredMaxMS: 900, Errors: 1},
		}},
	}
	totals := SumCallTimelineTools(entries)
	assertTimelineTools(t, totals, []CallTimelineTool{
		{Name: "Bash", Uses: 3, Results: 3, Measured: 3, MeasuredSumMS: 1200, MeasuredMaxMS: 900, Errors: 1},
		{Name: "Read", Uses: 1, Results: 1, Unmeasured: 1},
	})
}

func assertTimelineTools(t *testing.T, got []CallTimelineTool, want []CallTimelineTool) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("tool数 = %d: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tool[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
