package state

import (
	"os"
	"testing"
	"time"
)

func TestRecordModelCallLogPersistsPrivateJSONL(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	st.RecordModelCallLog(ModelCallLog{
		TaskID:      taskID,
		SessionID:   "session",
		StartedAt:   now,
		CompletedAt: now.Add(time.Second),
		Phase:       "worker-new",
		Role:        WorkerRole,
		ModelAlias:  "opus",
		Outcome:     "success",
		Prompt:      "instruction",
		Response:    "packet",
		Usage: TokenUsage{
			InputTokens:              10,
			CacheCreationInputTokens: 20,
			CacheReadInputTokens:     30,
			OutputTokens:             40,
		},
		ResolvedModelUsage: map[string]ResolvedModelUsage{
			"glm-5.2": {InputTokens: 10, CacheReadInputTokens: 30, OutputTokens: 40},
		},
		NumTurns: 2,
	})

	logs, err := st.ReadModelCallLogs(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].CallID == "" || logs[0].Prompt != "instruction" {
		t.Fatalf("telemetry = %#v", logs)
	}
	info, err := os.Stat(st.ModelCallLogPath(taskID))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("telemetry mode = %o", info.Mode().Perm())
	}
	stats, err := st.loadTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.InputTokensByAlias["opus"] != 10 || stats.CacheReadInputTokensByAlias["opus"] != 30 || stats.NumTurnsByAlias["opus"] != 2 {
		t.Fatalf("alias usage = %#v", stats)
	}
	if stats.OutputTokensByResolvedModel["glm-5.2"] != 40 || stats.ModelCallsByResolvedModel["glm-5.2"] != 1 {
		t.Fatalf("resolved usage = %#v", stats)
	}
}

func TestRecordModelCallLogFailureDoesNotBlockStats(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(st.ModelCallLogPath(taskID), 0o700); err != nil {
		t.Fatal(err)
	}
	warnings, restore := captureStatsWarnings(t)
	defer restore()

	st.RecordModelCallLog(ModelCallLog{
		TaskID:     taskID,
		ModelAlias: "haiku",
		Usage:      TokenUsage{OutputTokens: 7},
	})

	if warnings.Len() == 0 {
		t.Fatal("telemetry失敗warningがありません")
	}
	stats, err := st.loadTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.OutputTokensByAlias["haiku"] != 7 {
		t.Fatalf("telemetry失敗後のstats = %#v", stats)
	}
}
