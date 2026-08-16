package state

import (
	"testing"
	"time"
)

func agingTestLog(sessionID string, role SessionRole, model string, resumed bool, turns int, tokens TokenUsage, wallMS int64, startedAt time.Time) ModelCallLog {
	return ModelCallLog{
		CallType:       CallTypeTask,
		SessionID:      sessionID,
		Role:           role,
		ModelAlias:     model,
		Resumed:        resumed,
		TopLevelTurns:  turns,
		TreeUsage:      tokens,
		WallDurationMS: wallMS,
		StartedAt:      startedAt,
	}
}

// TestAgingFromModelCallLogsAggregatesPerSessionはsession別のcall index相当latency列・
// 累積turn/token・role/model関連付けをtelemetry記録だけから組み立てることを検証する。
// probe/event recordは集計へ入れない。
func TestAgingFromModelCallLogsAggregatesPerSession(t *testing.T) {
	base := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	logs := []ModelCallLog{
		agingTestLog("sess-worker", WorkerRole, "opus", false, 10, TokenUsage{InputTokens: 100, CacheReadInputTokens: 50, OutputTokens: 20}, 8000, base),
		{CallType: CallTypeProbe, SessionID: "none", Role: WorkerRole, ModelAlias: "opus", StartedAt: base.Add(time.Minute)},
		agingTestLog("sess-reviewer", ReviewerRole, "haiku", false, 3, TokenUsage{InputTokens: 40, OutputTokens: 10}, 2000, base.Add(2*time.Minute)),
		agingTestLog("sess-worker", WorkerRole, "opus", true, 12, TokenUsage{InputTokens: 300, CacheCreationInputTokens: 25, OutputTokens: 30}, 9000, base.Add(3*time.Minute)),
		{CallType: CallTypeEvent, Role: WorkerRole, ModelAlias: "opus", Outcome: "provider_unavailable", StartedAt: base.Add(4 * time.Minute)},
	}

	sessions := AgingFromModelCallLogs(logs)
	if len(sessions) != 2 {
		t.Fatalf("session数 = %d: %#v", len(sessions), sessions)
	}

	worker := sessions[0]
	if worker.SessionID != "sess-worker" || worker.Role != WorkerRole || len(worker.Models) != 1 || worker.Models[0] != "opus" {
		t.Fatalf("worker session = %#v", worker)
	}
	if worker.Calls != 2 || worker.ResumedCalls != 1 {
		t.Fatalf("worker calls = %d resumed = %d", worker.Calls, worker.ResumedCalls)
	}
	if worker.CumulativeTurns != 22 {
		t.Fatalf("worker累積turns = %d", worker.CumulativeTurns)
	}
	if worker.CumulativeInputTokens != 475 || worker.CumulativeOutputTokens != 50 {
		t.Fatalf("worker累積token = %d/%d", worker.CumulativeInputTokens, worker.CumulativeOutputTokens)
	}
	if len(worker.CallLatencyMS) != 2 || worker.CallLatencyMS[0] != 8000 || worker.CallLatencyMS[1] != 9000 {
		t.Fatalf("worker latency列 = %v", worker.CallLatencyMS)
	}
	if !worker.FirstCallAt.Equal(base) || !worker.LastCallAt.Equal(base.Add(3*time.Minute)) {
		t.Fatalf("worker呼出時刻 = %s/%s", worker.FirstCallAt, worker.LastCallAt)
	}

	reviewer := sessions[1]
	if reviewer.SessionID != "sess-reviewer" || reviewer.Role != ReviewerRole || reviewer.Calls != 1 || reviewer.CumulativeTurns != 3 {
		t.Fatalf("reviewer session = %#v", reviewer)
	}
}

// TestAgingFromModelCallLogsTracksModelChangeは同一session内のmodel変更を記録順の
// model列として残し、値を推測しないことを検証する。
func TestAgingFromModelCallLogsTracksModelChange(t *testing.T) {
	base := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	logs := []ModelCallLog{
		agingTestLog("sess", WorkerRole, "opus", false, 1, TokenUsage{}, 1000, base),
		agingTestLog("sess", WorkerRole, "sonnet", true, 2, TokenUsage{}, 2000, base.Add(time.Minute)),
	}

	sessions := AgingFromModelCallLogs(logs)
	if len(sessions) != 1 || len(sessions[0].Models) != 2 || sessions[0].Models[0] != "opus" || sessions[0].Models[1] != "sonnet" {
		t.Fatalf("model列 = %#v", sessions)
	}
}

// TestAgingFromModelCallLogsEmptyはTask Work Call記録がないとき空要約を返す。
func TestAgingFromModelCallLogsEmpty(t *testing.T) {
	if sessions := AgingFromModelCallLogs(nil); len(sessions) != 0 {
		t.Fatalf("空記録のsession aging = %#v", sessions)
	}
}
