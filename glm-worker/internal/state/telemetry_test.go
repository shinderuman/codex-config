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
		TopLevelUsage: TokenUsage{
			InputTokens:          1,
			CacheReadInputTokens: 2,
			OutputTokens:         3,
		},
		ResolvedModelUsage: map[string]ResolvedModelUsage{
			"glm-5.2": {InputTokens: 10, CacheReadInputTokens: 30, OutputTokens: 40},
			"glm-4.7": {InputTokens: 5, CacheReadInputTokens: 7, OutputTokens: 8},
		},
		TopLevelTurns: 2,
	})

	logs, err := st.ReadModelCallLogs(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].CallID == "" || logs[0].Prompt != "instruction" {
		t.Fatalf("telemetry = %#v", logs)
	}
	if logs[0].TopLevelUsage.InputTokens != 1 || logs[0].TreeUsage.InputTokens != 15 || logs[0].TreeUsage.OutputTokens != 48 {
		t.Fatalf("top-level/tree usage = %#v", logs[0])
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
	if stats.InputTokensByAlias["opus"] != 15 || stats.CacheReadInputTokensByAlias["opus"] != 37 || stats.TopLevelTurnsByAlias["opus"] != 2 {
		t.Fatalf("alias usage = %#v", stats)
	}
	if stats.OutputTokensByResolvedModel["glm-5.2"] != 40 || stats.CallTreesByResolvedModel["glm-5.2"] != 1 {
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
		TaskID:        taskID,
		ModelAlias:    "haiku",
		TopLevelUsage: TokenUsage{OutputTokens: 7},
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

func TestReadModelCallLogsSkipsVersion1(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.appendModelCallLog(ModelCallLog{Version: 1, TaskID: taskID, CallID: "legacy"}); err != nil {
		t.Fatal(err)
	}
	st.RecordModelCallLog(ModelCallLog{TaskID: taskID, CallID: "current"})

	logs, err := st.ReadModelCallLogs(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Version != modelCallLogVersion || logs[0].CallID != "current" {
		t.Fatalf("version 1を除外したtelemetry = %#v", logs)
	}
}
