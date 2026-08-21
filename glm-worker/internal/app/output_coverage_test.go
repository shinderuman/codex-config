package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// writeArchivedGapStatsは既知historical gap task(ccc205d1型: statsだけに呼出が残り
// raw JSONLが存在しない)のstats archive fileをstats履歴へ書き込む。
func writeArchivedGapStats(t *testing.T, st *state.StateStore, taskID string, modelCalls int) {
	t.Helper()
	archivedAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	stats := state.TaskStats{
		Version:    3,
		TaskID:     taskID,
		StartedAt:  archivedAt,
		ArchivedAt: &archivedAt,
		Status:     state.TaskStatusActive,
		ModelCalls: modelCalls,
	}
	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Path("stats"), taskID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func recordCoverageTaskCall(st *state.StateStore, taskID string) {
	st.RecordModelCallLog(state.ModelCallLog{
		TaskID:     taskID,
		CallType:   state.CallTypeTask,
		ModelAlias: "opus",
	})
}

func TestPrintStatsTelemetryCoverageHistoricalGapAndCurrentTask(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	gapTask := "ccc205d1-1111-4222-8333-444444444444"
	writeArchivedGapStats(t, st, gapTask, 1)

	current, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	st.RecordModelCall(state.WorkerRole, "opus")
	st.RecordModelCall(state.ReviewerRole, "sonnet")
	recordCoverageTaskCall(st, current)
	recordCoverageTaskCall(st, current)

	var out bytes.Buffer
	if err := printStats(st, &out); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"MODEL_CALLS: 3",
		"TELEMETRY_COVERAGE: incomplete",
		"TELEMETRY_COVERAGE_MODEL_CALLS: 3",
		"TELEMETRY_COVERAGE_RAW_RECORDS: 2",
		"TELEMETRY_COVERAGE_MISSING_CALLS: 1",
		"TELEMETRY_COVERAGE_EXCESS_RECORDS: 0",
		"USAGE_TOTALS_COVERAGE: unknown",
		"TELEMETRY_COVERAGE_HISTORICAL_GAP: task=" + gapTask + " stats_calls=1 raw_records=0 missing=1 usage=unknown",
	} {
		if !strings.Contains(out.String(), value+"\n") {
			t.Fatalf("coverage出力に%qがありません: %s", value, out.String())
		}
	}
	if strings.Contains(out.String(), "TELEMETRY_COVERAGE_INCOMPLETE_TASK") {
		t.Fatalf("完全一致の現在taskをincomplete行へ出力しません: %s", out.String())
	}
}

func TestPrintStatsTelemetryCoverageComplete(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	current, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	st.RecordModelCall(state.WorkerRole, "opus")
	recordCoverageTaskCall(st, current)

	var out bytes.Buffer
	if err := printStats(st, &out); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"TELEMETRY_COVERAGE: complete",
		"TELEMETRY_COVERAGE_MODEL_CALLS: 1",
		"TELEMETRY_COVERAGE_RAW_RECORDS: 1",
		"TELEMETRY_COVERAGE_MISSING_CALLS: 0",
		"USAGE_TOTALS_COVERAGE: complete",
	} {
		if !strings.Contains(out.String(), value+"\n") {
			t.Fatalf("complete時のcoverage出力に%qがありません: %s", value, out.String())
		}
	}
	for _, forbidden := range []string{
		"TELEMETRY_COVERAGE_HISTORICAL_GAP",
		"TELEMETRY_COVERAGE_INCOMPLETE_TASK",
		"TELEMETRY_COVERAGE_UNREADABLE_TASK",
	} {
		if strings.Contains(out.String(), forbidden) {
			t.Fatalf("complete時に%qを出力しません: %s", forbidden, out.String())
		}
	}
}

func TestPrintStatsTelemetryCoverageCurrentTaskShortageAndUnreadable(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	current, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	st.RecordModelCall(state.WorkerRole, "opus")
	st.RecordModelCall(state.WorkerRole, "opus")
	recordCoverageTaskCall(st, current)

	brokenTask := "broken0001-1111-4222-8333-444444444444"
	writeArchivedGapStats(t, st, brokenTask, 1)
	telemetryPath := st.ModelCallLogPath(brokenTask)
	if err := os.MkdirAll(filepath.Dir(telemetryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(telemetryPath, []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := printStats(st, &out); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"TELEMETRY_COVERAGE: unreadable",
		"TELEMETRY_COVERAGE_MISSING_CALLS: 1",
		"TELEMETRY_COVERAGE_INCOMPLETE_TASK: task=" + current + " stats_calls=2 raw_records=1 missing=1 excess=0 usage=unknown",
		"TELEMETRY_COVERAGE_UNREADABLE_TASK: task=" + brokenTask + " stats_calls=1",
		"USAGE_TOTALS_COVERAGE: unknown",
	} {
		if !strings.Contains(out.String(), value+"\n") {
			t.Fatalf("coverage出力に%qがありません: %s", value, out.String())
		}
	}
}

func TestPrintStatsTelemetryCoverageOrphanOnlyIsIncomplete(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	current, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	st.RecordModelCall(state.WorkerRole, "opus")
	recordCoverageTaskCall(st, current)

	orphanTask := "orphan0001-1111-4222-8333-444444444444"
	recordCoverageTaskCall(st, orphanTask)

	var out bytes.Buffer
	if err := printStats(st, &out); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"TELEMETRY_COVERAGE: incomplete",
		"TELEMETRY_COVERAGE_MODEL_CALLS: 1",
		"TELEMETRY_COVERAGE_RAW_RECORDS: 1",
		"TELEMETRY_COVERAGE_MISSING_CALLS: 0",
		"TELEMETRY_COVERAGE_EXCESS_RECORDS: 0",
		"TELEMETRY_COVERAGE_ORPHAN_FILES: 1",
		"USAGE_TOTALS_COVERAGE: unknown",
	} {
		if !strings.Contains(out.String(), value+"\n") {
			t.Fatalf("orphanのみ時のcoverage出力に%qがありません: %s", value, out.String())
		}
	}
	for _, forbidden := range []string{
		"TELEMETRY_COVERAGE_HISTORICAL_GAP",
		"TELEMETRY_COVERAGE_INCOMPLETE_TASK",
		"TELEMETRY_COVERAGE_UNREADABLE_TASK",
	} {
		if strings.Contains(out.String(), forbidden) {
			t.Fatalf("集計task自体が完全一致ならtask行は出しません(%q): %s", forbidden, out.String())
		}
	}
}

func TestPrintStatsTelemetryCoverageExcessOnlyTaskLineHasUsageUnknown(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	current, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	st.RecordModelCall(state.WorkerRole, "opus")
	recordCoverageTaskCall(st, current)
	recordCoverageTaskCall(st, current)

	var out bytes.Buffer
	if err := printStats(st, &out); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"TELEMETRY_COVERAGE: incomplete",
		"TELEMETRY_COVERAGE_MISSING_CALLS: 0",
		"TELEMETRY_COVERAGE_EXCESS_RECORDS: 1",
		"TELEMETRY_COVERAGE_INCOMPLETE_TASK: task=" + current + " stats_calls=1 raw_records=2 missing=0 excess=1 usage=unknown",
		"USAGE_TOTALS_COVERAGE: unknown",
	} {
		if !strings.Contains(out.String(), value+"\n") {
			t.Fatalf("過剰のみ時のcoverage出力に%qがありません: %s", value, out.String())
		}
	}
}
