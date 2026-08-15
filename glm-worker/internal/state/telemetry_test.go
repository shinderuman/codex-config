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
		CallType:    CallTypeTask,
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
			"glm-5.3": {InputTokens: 10, CacheReadInputTokens: 30, OutputTokens: 40},
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
	if stats.OutputTokensByResolvedModel["glm-5.3"] != 40 || stats.CallTreesByResolvedModel["glm-5.3"] != 1 {
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
		CallType:      CallTypeTask,
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

// v2 recordはcall_typeを持たずtask/probeが区別できないため、v3集計へ混在させない。
func TestReadModelCallLogsSkipsVersion2(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.appendModelCallLog(ModelCallLog{Version: 2, TaskID: taskID, CallID: "v2", CallType: CallTypeProbe}); err != nil {
		t.Fatal(err)
	}
	st.RecordModelCallLog(ModelCallLog{TaskID: taskID, CallID: "v3", CallType: CallTypeTask})

	logs, err := st.ReadModelCallLogs(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Version != 3 || logs[0].CallID != "v3" {
		t.Fatalf("version 2を除外したtelemetry = %#v", logs)
	}
}

// probe recordはJSONLへtoken/cost/resolved modelを残すが、task集計mirrorへは混ぜない。
func TestRecordProbeCallLogExcludedFromTaskAggregates(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	st.RecordModelCallLog(ModelCallLog{
		TaskID:     taskID,
		CallType:   CallTypeProbe,
		ModelAlias: "opus",
		Outcome:    "probe_success",
		TopLevelUsage: TokenUsage{
			InputTokens:  5,
			OutputTokens: 7,
		},
		ResolvedModelUsage: map[string]ResolvedModelUsage{
			"glm-5.3": {InputTokens: 5, OutputTokens: 7, CostUSD: 0.01},
		},
		TotalCostUSD: 0.01,
	})

	logs, err := st.ReadModelCallLogs(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].CallType != CallTypeProbe {
		t.Fatalf("probe record = %#v", logs)
	}
	if logs[0].TotalCostUSD != 0.01 || logs[0].ResolvedModelUsage["glm-5.3"].OutputTokens != 7 {
		t.Fatalf("probe cost/resolved modelがtelemetryへ記録されていない: %#v", logs[0])
	}
	stats, err := st.loadTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.ModelCalls != 0 || stats.InputTokensByAlias["opus"] != 0 ||
		stats.OutputTokensByResolvedModel["glm-5.3"] != 0 || stats.CallTreesByResolvedModel["glm-5.3"] != 0 {
		t.Fatalf("probeがtask集計へ混ざっている: %#v", stats)
	}
}

// 旧record(診断field欠落)と新record(診断field付き)が同一v2 JSONLへ混在しても、
// 既存token集計は両方から維持され、診断値はcaptured recordだけに現れる(not capturedと区別)。
func TestReadModelCallLogsMixedSchemaPreservesTokensAndDiagnostics(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	legacy := ModelCallLog{
		Version:    modelCallLogVersion,
		CallID:     "legacy",
		CallType:   CallTypeTask,
		TaskID:     taskID,
		Phase:      "worker-new",
		ModelAlias: "opus",
		Outcome:    "success",
		TopLevelUsage: TokenUsage{
			InputTokens:  100,
			OutputTokens: 50,
		},
	}
	if err := st.appendModelCallLog(legacy); err != nil {
		t.Fatal(err)
	}
	st.RecordModelCallLog(ModelCallLog{
		CallID:             "current",
		CallType:           CallTypeTask,
		TaskID:             taskID,
		Phase:              "reviewer-1",
		ModelAlias:         "haiku",
		Outcome:            "success",
		WorkerReportedRisk: "HIGH",
		EffectiveRisk:      "HIGH",
		RiskFloorCategory:  "worker-declared",
		TopLevelUsage:      TokenUsage{InputTokens: 20, OutputTokens: 10},
	})

	logs, err := st.ReadModelCallLogs(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("両recordが読めるべき: %#v", logs)
	}
	var legacyRec, currentRec ModelCallLog
	for _, l := range logs {
		switch l.CallID {
		case "legacy":
			legacyRec = l
		case "current":
			currentRec = l
		}
	}
	if legacyRec.WorkerReportedRisk != "" || legacyRec.EffectiveRisk != "" || legacyRec.RiskFloorCategory != "" {
		t.Fatalf("旧recordの診断fieldは未観測(空)のべき: %+v", legacyRec)
	}
	if legacyRec.TopLevelUsage.InputTokens != 100 {
		t.Fatalf("旧recordのtoken集計が失われた: %+v", legacyRec)
	}
	if currentRec.WorkerReportedRisk != "HIGH" || currentRec.EffectiveRisk != "HIGH" || currentRec.RiskFloorCategory != "worker-declared" {
		t.Fatalf("新recordの診断fieldが失われた: %+v", currentRec)
	}
	if currentRec.TopLevelUsage.InputTokens != 20 {
		t.Fatalf("新recordのtoken集計が失われた: %+v", currentRec)
	}

	// legacyはappendModelCallLogで直接書かれたためstats mirrorへは含まれず、current recordの
	// tokenだけがstatsへ集計される(旧recordのtokenはJSONL読込側で維持される)。
	stats, err := st.loadTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.InputTokensByAlias["haiku"] != 20 || stats.OutputTokensByAlias["haiku"] != 10 {
		t.Fatalf("current recordのtoken集計 = %+v", stats.InputTokensByAlias)
	}
	if stats.InputTokensByAlias["opus"] != 0 {
		t.Fatalf("legacyはstats未集計のべき: %+v", stats.InputTokensByAlias)
	}
}
