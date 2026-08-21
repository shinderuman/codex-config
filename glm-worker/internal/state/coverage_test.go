package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func currentTaskIDForCoverage(t *testing.T, st *StateStore) string {
	t.Helper()
	taskID := st.ReadOr("task.id", "")
	if taskID == "" {
		t.Fatal("task.idがありません")
	}
	return taskID
}

func recordTaskCallForCoverage(st *StateStore, taskID string) {
	st.RecordModelCallLog(ModelCallLog{
		TaskID:     taskID,
		CallType:   CallTypeTask,
		ModelAlias: "opus",
	})
}

func writeTelemetryLinesForCoverage(t *testing.T, st *StateStore, taskID string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(st.ModelCallLogPath(taskID)), 0o700); err != nil {
		t.Fatal(err)
	}
	data := ""
	for _, line := range lines {
		data += line + "\n"
	}
	if err := os.WriteFile(st.ModelCallLogPath(taskID), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func archivedStatsForCoverage(taskID string, modelCalls int) TaskStats {
	archivedAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	return TaskStats{
		Version:    taskStatsVersion,
		TaskID:     taskID,
		StartedAt:  archivedAt,
		ArchivedAt: &archivedAt,
		Status:     TaskStatusActive,
		ModelCalls: modelCalls,
	}
}

func findCoverageEntry(t *testing.T, coverage TelemetryCoverage, taskID string) TaskCallCoverage {
	t.Helper()
	for _, entry := range coverage.Tasks {
		if entry.TaskID == taskID {
			return entry
		}
	}
	t.Fatalf("coverageにtask %sがありません", taskID)
	return TaskCallCoverage{}
}

func TestComputeTelemetryCoverageCompleteMatchesRawRecords(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	taskID := currentTaskIDForCoverage(t, st)
	st.RecordModelCall(WorkerRole, "opus")
	st.RecordModelCall(ReviewerRole, "sonnet")
	recordTaskCallForCoverage(st, taskID)
	recordTaskCallForCoverage(st, taskID)
	st.RecordModelCallLog(ModelCallLog{TaskID: taskID, CallType: CallTypeProbe})
	st.RecordModelCallLog(ModelCallLog{TaskID: taskID, CallType: CallTypeEvent})

	all, err := st.AllTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	coverage := st.ComputeTelemetryCoverage(all)
	if coverage.Status != CoverageComplete || !coverage.UsageKnown {
		t.Fatalf("coverage = %s usageKnown=%v、completeではありません: %+v", coverage.Status, coverage.UsageKnown, coverage)
	}
	if coverage.StatsCalls != 2 || coverage.RawRecords != 2 {
		t.Fatalf("stats=%d raw=%d、2対2が期待されます", coverage.StatsCalls, coverage.RawRecords)
	}
	if coverage.MissingCalls != 0 || coverage.ExcessRecords != 0 || coverage.OrphanFiles != 0 {
		t.Fatalf("complete時に欠損/過剰/orphanがあります: %+v", coverage)
	}
	entry := findCoverageEntry(t, coverage, taskID)
	if entry.Classification() != CoverageComplete {
		t.Fatalf("分類 = %s", entry.Classification())
	}
}

func TestComputeTelemetryCoverageZeroCallsWithoutTelemetryFile(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}

	all, err := st.AllTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	coverage := st.ComputeTelemetryCoverage(all)
	if coverage.Status != CoverageComplete || !coverage.UsageKnown {
		t.Fatalf("0件taskはcompleteであるべきです: %+v", coverage)
	}
	if coverage.StatsCalls != 0 || coverage.RawRecords != 0 {
		t.Fatalf("0対0が期待されます: %+v", coverage)
	}
}

func TestComputeTelemetryCoverageCurrentTaskRawShortageIsIncomplete(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	taskID := currentTaskIDForCoverage(t, st)
	st.RecordModelCall(WorkerRole, "opus")
	st.RecordModelCall(WorkerRole, "opus")
	st.RecordModelCall(ReviewerRole, "sonnet")
	recordTaskCallForCoverage(st, taskID)

	all, err := st.AllTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	coverage := st.ComputeTelemetryCoverage(all)
	if coverage.Status != CoverageIncomplete || coverage.UsageKnown {
		t.Fatalf("現在taskのraw不足はincomplete/usage不明です: %+v", coverage)
	}
	if coverage.MissingCalls != 2 {
		t.Fatalf("欠損2が期待されます: %d", coverage.MissingCalls)
	}
	entry := findCoverageEntry(t, coverage, taskID)
	if entry.Classification() != CoverageIncomplete {
		t.Fatalf("現在taskの不足はhistorical gapに分類しない: %s", entry.Classification())
	}
}

func TestComputeTelemetryCoverageArchivedStatsOnlyTaskIsHistoricalGap(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	gapTask := "ccc205d1-1111-4222-8333-444444444444"

	// 既知gapはtelemetry file自体が存在しない(statsだけに1 callが残る)。
	all := []TaskStats{archivedStatsForCoverage(gapTask, 1)}
	coverage := st.ComputeTelemetryCoverage(all)
	if coverage.Status != CoverageIncomplete || coverage.UsageKnown {
		t.Fatalf("historical gapありの集計はincomplete/usage不明です: %+v", coverage)
	}
	if coverage.MissingCalls != 1 || coverage.RawRecords != 0 {
		t.Fatalf("stats 1対raw 0の差が1であるべきです: %+v", coverage)
	}
	entry := findCoverageEntry(t, coverage, gapTask)
	if entry.Classification() != CoverageHistoricalGap {
		t.Fatalf("archive済みstats専有taskはhistorical gap: %s", entry.Classification())
	}
}

func TestComputeTelemetryCoverageExcessRecords(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	taskID := "excess0001-1111-4222-8333-444444444444"
	recordTaskCallForCoverage(st, taskID)
	recordTaskCallForCoverage(st, taskID)
	recordTaskCallForCoverage(st, taskID)

	current := archivedStatsForCoverage(taskID, 1)
	current.ArchivedAt = nil
	coverage := st.ComputeTelemetryCoverage([]TaskStats{current})
	if coverage.Status != CoverageIncomplete || coverage.UsageKnown {
		t.Fatalf("過剰recordだけでもusage総量の信頼はなくincompleteです: %+v", coverage)
	}
	if coverage.ExcessRecords != 2 || coverage.MissingCalls != 0 {
		t.Fatalf("過剰2/欠損0が期待されます: %+v", coverage)
	}
	entry := findCoverageEntry(t, coverage, taskID)
	if entry.Classification() != CoverageIncomplete {
		t.Fatalf("過剰recordの分類 = %s", entry.Classification())
	}

	archived := archivedStatsForCoverage(taskID, 1)
	coverage = st.ComputeTelemetryCoverage([]TaskStats{archived})
	entry = findCoverageEntry(t, coverage, archived.TaskID)
	if entry.Classification() != CoverageIncomplete {
		t.Fatalf("archive済みでも過剰recordはhistorical gapにしない: %s", entry.Classification())
	}
}

func TestComputeTelemetryCoverageSkipsOldVersionRecords(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	taskID := "oldrecord1-1111-4222-8333-444444444444"
	oldRecord, err := json.Marshal(map[string]any{
		"version":   modelCallLogVersion - 1,
		"call_type": CallTypeTask,
		"task_id":   taskID,
	})
	if err != nil {
		t.Fatal(err)
	}
	currentRecord, err := json.Marshal(ModelCallLog{Version: modelCallLogVersion, CallType: CallTypeTask, TaskID: taskID})
	if err != nil {
		t.Fatal(err)
	}
	noCallType, err := json.Marshal(ModelCallLog{Version: modelCallLogVersion, TaskID: taskID})
	if err != nil {
		t.Fatal(err)
	}
	writeTelemetryLinesForCoverage(t, st, taskID, []string{
		string(oldRecord),
		string(currentRecord),
		string(noCallType),
	})

	coverage := st.ComputeTelemetryCoverage([]TaskStats{{Version: taskStatsVersion, TaskID: taskID, ModelCalls: 1}})
	if coverage.RawRecords != 1 {
		t.Fatalf("現行versionのtask recordだけを数える: %d", coverage.RawRecords)
	}
	if coverage.Status != CoverageComplete {
		t.Fatalf("旧recordは集計外です: %+v", coverage)
	}
}

func TestComputeTelemetryCoverageUnreadableTelemetryDoesNotInventCounts(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	taskID := "broken0001-1111-4222-8333-444444444444"
	writeTelemetryLinesForCoverage(t, st, taskID, []string{"{not json"})

	coverage := st.ComputeTelemetryCoverage([]TaskStats{{Version: taskStatsVersion, TaskID: taskID, ModelCalls: 2}})
	if coverage.Status != CoverageUnreadable || coverage.UsageKnown {
		t.Fatalf("読み取り不能fileはunreadable/usage不明です: %+v", coverage)
	}
	if coverage.MissingCalls != 0 || coverage.ExcessRecords != 0 {
		t.Fatalf("record数不明のtaskで欠損/過剰を算出しません: %+v", coverage)
	}
	entry := findCoverageEntry(t, coverage, taskID)
	if entry.Classification() != CoverageUnreadable {
		t.Fatalf("分類 = %s", entry.Classification())
	}
}

func TestComputeTelemetryCoverageOrphanTelemetryFile(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	orphanTask := "orphan0001-1111-4222-8333-444444444444"
	recordTaskCallForCoverage(st, orphanTask)

	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	all, err := st.AllTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	coverage := st.ComputeTelemetryCoverage(all)
	if coverage.OrphanFiles != 1 {
		t.Fatalf("集計対象外telemetry fileを1件報告すべきです: %+v", coverage)
	}
	if coverage.Status != CoverageIncomplete || coverage.UsageKnown {
		t.Fatalf("orphan fileがある以上usage総量の完全性は証明できずincompleteです: %+v", coverage)
	}
	if coverage.MissingCalls != 0 || coverage.ExcessRecords != 0 {
		t.Fatalf("集計task自体の欠損/過剰はありません: %+v", coverage)
	}
	if findCoverageEntry(t, coverage, currentTaskIDForCoverage(t, st)).Classification() != CoverageComplete {
		t.Fatal("集計対象task自身の対応はcompleteのままです")
	}
}

func TestComputeTelemetryCoverageArchivedFlowHistoricalGap(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	first, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	st.RecordModelCall(WorkerRole, "opus")
	second, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("taskがrotateしませんでした")
	}
	st.RecordModelCall(WorkerRole, "opus")
	recordTaskCallForCoverage(st, second)

	all, err := st.AllTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	coverage := st.ComputeTelemetryCoverage(all)
	if coverage.MissingCalls != 1 || coverage.StatsCalls != 2 || coverage.RawRecords != 1 {
		t.Fatalf("archive済みgap 1件と現在task完全一致: %+v", coverage)
	}
	if findCoverageEntry(t, coverage, first).Classification() != CoverageHistoricalGap {
		t.Fatal("archive済みのrecord無し呼出はhistorical gapです")
	}
	if findCoverageEntry(t, coverage, second).Classification() != CoverageComplete {
		t.Fatal("現在taskは完全一致です")
	}
}
