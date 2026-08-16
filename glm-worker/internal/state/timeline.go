package state

import (
	"sort"
	"time"
)

// CallTimelineToolはtool種別(tool名)別の呼出観測。Usesはtool_use block数、Resultsは
// tool_result block数。MeasuredはID対応付けできた結果だけの測定数で、対応付け不能な
// 結果はUnmeasuredへ数える(durationを推測して埋めない)。tool名を持たないblockは
// unknown tool種別へ集計する。
type CallTimelineTool struct {
	Name          string
	Uses          int
	Results       int
	Measured      int
	MeasuredSumMS int64
	MeasuredMaxMS int64
	Unmeasured    int
	Errors        int
}

// CallTimelineEntryはevent log 1 call分(call_id単位)の観測要約。ResultObservedは
// そのcall内にresult eventが存在したかで、falseのとき結果系fieldは未観測のまま。
// SessionCallIndex/SessionCallsは同一session内のevent log上のcall順(1始まり)。
type CallTimelineEntry struct {
	CallID           string
	Role             string
	Phase            string
	SessionID        string
	Resumed          bool
	ModelAlias       string
	MessageModel     string
	SessionCallIndex int
	SessionCalls     int
	FirstAt          time.Time
	LastAt           time.Time
	Events           int
	ResultObserved   bool
	ResultSubtype    string
	IsError          bool
	DurationMS       int64
	DurationAPIMS    int64
	NumTurns         int
	TotalCostUSD     float64
	Usage            *TaskEventUsage
	Tools            []CallTimelineTool
}

// CallsFromTaskEventsはevent log record列をcall単位へ集計する。call順はlog内での
// 初出順、tool順はtool名のsort順で安定させる。record列はfile順そのまま渡すこと。
func CallsFromTaskEvents(records []TaskEventRecord) []CallTimelineEntry {
	order := make([]string, 0)
	byCall := make(map[string]*CallTimelineEntry)
	toolsByCall := make(map[string]map[string]*CallTimelineTool)
	for _, record := range records {
		entry, ok := byCall[record.CallID]
		if !ok {
			entry = &CallTimelineEntry{CallID: record.CallID}
			byCall[record.CallID] = entry
			toolsByCall[record.CallID] = make(map[string]*CallTimelineTool)
			order = append(order, record.CallID)
		}
		absorbTaskEvent(entry, toolsByCall[record.CallID], record)
	}
	numberSessionCalls(byCall, order)
	result := make([]CallTimelineEntry, 0, len(order))
	for _, callID := range order {
		entry := byCall[callID]
		entry.Tools = sortedTimelineTools(toolsByCall[callID])
		result = append(result, *entry)
	}
	return result
}

func absorbTaskEvent(entry *CallTimelineEntry, tools map[string]*CallTimelineTool, record TaskEventRecord) {
	entry.Events++
	if entry.Role == "" {
		entry.Role = record.Role
	}
	if entry.Phase == "" {
		entry.Phase = record.Phase
	}
	if entry.SessionID == "" {
		entry.SessionID = record.SessionID
	}
	if entry.ModelAlias == "" {
		entry.ModelAlias = record.ModelAlias
	}
	entry.Resumed = entry.Resumed || record.Resumed
	if record.MessageModel != "" {
		entry.MessageModel = record.MessageModel
	}
	if record.Timestamp != (time.Time{}) {
		if entry.FirstAt == (time.Time{}) || record.Timestamp.Before(entry.FirstAt) {
			entry.FirstAt = record.Timestamp
		}
		if record.Timestamp.After(entry.LastAt) {
			entry.LastAt = record.Timestamp
		}
	}
	if record.Kind == "result" {
		entry.ResultObserved = true
		entry.ResultSubtype = record.Subtype
		entry.IsError = record.IsError
		entry.DurationMS = record.DurationMS
		entry.DurationAPIMS = record.DurationAPIMS
		entry.NumTurns = record.NumTurns
		entry.TotalCostUSD = record.TotalCostUSD
		entry.Usage = record.Usage
	}
	for _, block := range record.Blocks {
		absorbTaskBlock(tools, block)
	}
}

func absorbTaskBlock(tools map[string]*CallTimelineTool, block TaskBlockSummary) {
	switch block.Type {
	case "tool_use":
		timelineTool(tools, block.Name).Uses++
	case "tool_result":
		tool := timelineTool(tools, block.Name)
		tool.Results++
		if block.IsError {
			tool.Errors++
		}
		if block.DurationMS > 0 {
			tool.Measured++
			tool.MeasuredSumMS += block.DurationMS
			if block.DurationMS > tool.MeasuredMaxMS {
				tool.MeasuredMaxMS = block.DurationMS
			}
			return
		}
		tool.Unmeasured++
	}
}

func timelineTool(tools map[string]*CallTimelineTool, name string) *CallTimelineTool {
	if name == "" {
		name = "unknown"
	}
	tool, ok := tools[name]
	if !ok {
		tool = &CallTimelineTool{Name: name}
		tools[name] = tool
	}
	return tool
}

func numberSessionCalls(byCall map[string]*CallTimelineEntry, order []string) {
	counts := make(map[string]int)
	for _, callID := range order {
		entry := byCall[callID]
		counts[entry.SessionID]++
		entry.SessionCallIndex = counts[entry.SessionID]
	}
	for _, entry := range byCall {
		entry.SessionCalls = counts[entry.SessionID]
	}
}

func sortedTimelineTools(tools map[string]*CallTimelineTool) []CallTimelineTool {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]CallTimelineTool, 0, len(names))
	for _, name := range names {
		result = append(result, *tools[name])
	}
	return result
}

// SumCallTimelineToolsはentry群のtool観測をtool名単位へ合算する。同一tool名の
// 測定済み最大値は合算後の最大値として扱う。
func SumCallTimelineTools(entries []CallTimelineEntry) []CallTimelineTool {
	tools := make(map[string]*CallTimelineTool)
	for _, entry := range entries {
		for _, tool := range entry.Tools {
			total, ok := tools[tool.Name]
			if !ok {
				total = &CallTimelineTool{Name: tool.Name}
				tools[tool.Name] = total
			}
			total.Uses += tool.Uses
			total.Results += tool.Results
			total.Measured += tool.Measured
			total.MeasuredSumMS += tool.MeasuredSumMS
			if tool.MeasuredMaxMS > total.MeasuredMaxMS {
				total.MeasuredMaxMS = tool.MeasuredMaxMS
			}
			total.Unmeasured += tool.Unmeasured
			total.Errors += tool.Errors
		}
	}
	return sortedTimelineTools(tools)
}
