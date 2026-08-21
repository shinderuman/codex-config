package state

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// coverage状態label。TaskStats model_callsとraw JSONL task record数の対応が
// 取れているかを表示するための契約値で、usage推測補完の有無を表すものではない。
const (
	CoverageComplete      = "complete"
	CoverageIncomplete    = "incomplete"
	CoverageUnreadable    = "unreadable"
	CoverageHistoricalGap = "historical-gap"
)

// TaskCallCoverageは1 task分のTaskStats model_callsとraw JSONL task record数の対応。
// Archivedはstats archive移行済み(以後recordが追記されない恒久状態)かどうか。
type TaskCallCoverage struct {
	TaskID     string
	Archived   bool
	StatsCalls int
	RawRecords int
	Unreadable bool
}

// MissingCallsはstatsに呼出が記録されているのにraw recordが存在しない差分。
// 欠けた呼出のusageを推測しないため、この差分は数として出すだけにする。
// Unreadable時はrecord数が不明であり、欠損数も算出しない。
func (c TaskCallCoverage) MissingCalls() int {
	if c.Unreadable || c.StatsCalls <= c.RawRecords {
		return 0
	}
	return c.StatsCalls - c.RawRecords
}

// ExcessRecordsはstats呼出数を超えて重複等で余分に存在するraw record数。
func (c TaskCallCoverage) ExcessRecords() int {
	if c.Unreadable || c.RawRecords <= c.StatsCalls {
		return 0
	}
	return c.RawRecords - c.StatsCalls
}

// Classificationはtask単位のcoverage分類。archive済みtaskのrecord欠損は今後埋まる
// 可能性がない恒久gap(wrapper孤児化時にstatsだけが先行加算された既知`ccc205d1`型)で
// historical-gap、現在taskの欠損は呼出中または消失のどちらかであるため区別しない。
func (c TaskCallCoverage) Classification() string {
	switch {
	case c.Unreadable:
		return CoverageUnreadable
	case c.MissingCalls() > 0:
		if c.Archived {
			return CoverageHistoricalGap
		}
		return CoverageIncomplete
	case c.ExcessRecords() > 0:
		return CoverageIncomplete
	}
	return CoverageComplete
}

// TelemetryCoverageは集計対象task全体のcoverage。Missing/Excessはtask別差分の
// 加算で、全体を1本の差へ相殺しない。OrphanFilesは集計対象taskに属さない
// telemetry file数(旧version stats archive等、raw側だけ残ったdata)。対応する
// statsが集計に存在しないraw dataが残る以上usage総量の完全性は証明できないため、
// 1件でもあればStatusをincompleteへ落とす。
type TelemetryCoverage struct {
	Tasks         []TaskCallCoverage
	StatsCalls    int
	RawRecords    int
	MissingCalls  int
	ExcessRecords int
	OrphanFiles   int
	Status        string
	UsageKnown    bool
}

// ComputeTelemetryCoverageは--stats集計taskのstats model_callsとraw JSONL task
// record数の対応を集計する。record読み取り不能なtaskはStatusをunreadableへ fail
// visibleにし、record数の不足を補完・相殺しない。
func (s *StateStore) ComputeTelemetryCoverage(tasks []TaskStats) TelemetryCoverage {
	coverage := TelemetryCoverage{
		Tasks:  make([]TaskCallCoverage, 0, len(tasks)),
		Status: CoverageComplete,
	}
	known := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		known[task.TaskID] = true
		entry := TaskCallCoverage{
			TaskID:     task.TaskID,
			Archived:   task.ArchivedAt != nil,
			StatsCalls: task.ModelCalls,
		}
		records, err := s.countTaskCallRecords(task.TaskID)
		if err != nil {
			entry.Unreadable = true
		} else {
			entry.RawRecords = records
		}
		coverage.Tasks = append(coverage.Tasks, entry)
		coverage.StatsCalls += entry.StatsCalls
		coverage.RawRecords += entry.RawRecords
		coverage.MissingCalls += entry.MissingCalls()
		coverage.ExcessRecords += entry.ExcessRecords()

		switch entry.Classification() {
		case CoverageUnreadable:
			coverage.Status = CoverageUnreadable
		case CoverageIncomplete, CoverageHistoricalGap:
			if coverage.Status == CoverageComplete {
				coverage.Status = CoverageIncomplete
			}
		}
	}
	coverage.OrphanFiles = s.countOrphanTelemetryFiles(known)
	if coverage.OrphanFiles > 0 && coverage.Status == CoverageComplete {
		coverage.Status = CoverageIncomplete
	}
	coverage.UsageKnown = coverage.Status == CoverageComplete
	return coverage
}

// countTaskCallRecordsはtelemetry JSONLから現行versionのTask Work Call record数だけを
// 数える。record本文(prompt/response)は読まず、旧version recordはReadModelCallLogsと
// 同じく読み飛ばす。file不在は記録0として扱う。
func (s *StateStore) countTaskCallRecords(taskID string) (int, error) {
	file, err := os.Open(s.ModelCallLogPath(taskID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var record struct {
			Version  int    `json:"version"`
			CallType string `json:"call_type"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return 0, fmt.Errorf("telemetryを読めません: %w", err)
		}
		if record.Version == modelCallLogVersion && record.CallType == CallTypeTask {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *StateStore) countOrphanTelemetryFiles(known map[string]bool) int {
	paths, err := filepath.Glob(filepath.Join(s.dir, "telemetry", "*.jsonl"))
	if err != nil {
		return 0
	}
	count := 0
	for _, path := range paths {
		if !known[strings.TrimSuffix(filepath.Base(path), ".jsonl")] {
			count++
		}
	}
	return count
}
