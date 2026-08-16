package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// timelineGraphMaxWidthはcall観測窓の相対barの最大文字数。
const timelineGraphMaxWidth = 40

// printTimelineは保存済みevent logとtelemetryだけからtask/call単位のtimelineとgraphを
// 表示する。state書換・repo lock・AI call・provider/workerへの問い合わせを行わない。
// 対応付けできなかったtool duration・観測されていない結果値はunknown/未測定として
// 表示し、推測で埋めない。taskIDArgが空なら現在task、指定されればそのtaskの保存済み
// logを読む。event log・telemetry pathの構築に使うtask ID(明示引数・現在task両方)は
// 先に生成形式(UUID v4)へ検証し、不正値はfilesystemへ触れずerrorとする。
// event logのない現在taskはその旨を表示して正常終了し、明示指定taskの
// log不在・読込失敗は明示commandのerrorとして返す。
func printTimeline(st *state.StateStore, taskIDArg string, stdout io.Writer) error {
	explicit := taskIDArg != ""
	taskID := taskIDArg
	if taskID == "" {
		taskID = st.ReadOr("task.id", "none")
	}
	if !validTimelineTaskID(taskID, explicit) {
		return fmt.Errorf("task IDが生成されるUUID v4形式と一致しません: %q", taskID)
	}
	fmt.Fprintf(stdout, "TASK_ID: %s\n", taskID)
	fmt.Fprintf(stdout, "TASK_STATUS: %s\n", timelineTaskStatus(st, taskID, explicit))

	records, skipped, err := readTaskEventRecords(st, taskID)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if explicit {
			return fmt.Errorf("task %sのevent logがありません: %w", taskID, err)
		}
		fmt.Fprintln(stdout, "EVENT_LOG: none")
	case err != nil:
		if explicit {
			return fmt.Errorf("task %sのevent logを読めません: %w", taskID, err)
		}
		fmt.Fprintln(stdout, "EVENT_LOG: unreadable")
	default:
		printTimelineCalls(stdout, st.TaskEventLogPath(taskID), records, skipped)
	}

	logs, logErr := readStatusTelemetry(st, taskID)
	printSessionAging(taskID, logErr, logs, stdout)
	return nil
}

// validTimelineTaskIDはevent log・telemetry path構築へ使うtask IDの境界を判定する。
// 明示指定は生成形式UUID v4のみを受け付け、現在taskは無task sentinel "none"かUUID v4
// のみを受け付ける(破損・改変されたtask.idでstate root外へ出ないようにする)。
func validTimelineTaskID(taskID string, explicit bool) bool {
	if taskID == "none" && !explicit {
		return true
	}
	return state.ValidGeneratedUUID(taskID)
}

// timelineTaskStatusは現在taskは正規task.statusを、明示指定taskはstats履歴のarchive値を
// 返す。履歴にも無いtask IDはunknownとする(推測しない)。
func timelineTaskStatus(st *state.StateStore, taskID string, explicit bool) string {
	if !explicit {
		return string(st.TaskStatus())
	}
	if taskID == st.ReadOr("task.id", "none") {
		return string(st.TaskStatus())
	}
	all, err := st.AllTaskStats()
	if err != nil {
		return "unknown"
	}
	for _, stats := range all {
		if stats.TaskID == taskID {
			return string(stats.Status)
		}
	}
	return "unknown"
}

// readTaskEventRecordsはtask event logを行ごとに読む。破損行・旧version行はskipして
// その件数を返し、log全体の読込失敗だけをerrorとする。
func readTaskEventRecords(st *state.StateStore, taskID string) ([]state.TaskEventRecord, int, error) {
	file, err := os.Open(st.TaskEventLogPath(taskID))
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var records []state.TaskEventRecord
	skipped := 0
	for scanner.Scan() {
		record, err := state.ParseTaskEventLine(scanner.Bytes())
		if err != nil {
			skipped++
			continue
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return records, skipped, err
	}
	return records, skipped, nil
}

func printTimelineCalls(stdout io.Writer, logPath string, records []state.TaskEventRecord, skipped int) {
	fmt.Fprintf(stdout, "EVENT_LOG: %s\n", logPath)
	if skipped > 0 {
		fmt.Fprintf(stdout, "SKIPPED_EVENTS: %d\n", skipped)
	}
	entries := state.CallsFromTaskEvents(records)
	fmt.Fprintf(stdout, "CALLS: %d\n", len(entries))
	for index, entry := range entries {
		printTimelineCall(stdout, index+1, len(entries), entry)
	}
	printTimelineTools(stdout, "TOOL_TOTALS:", state.SumCallTimelineTools(entries))
	printTimelineGraph(stdout, entries)
}

func printTimelineCall(stdout io.Writer, index int, total int, entry state.CallTimelineEntry) {
	label := fmt.Sprintf("CALL #%d/%d", index, total)
	session := entry.SessionID
	if session == "" {
		session = "unknown"
	}
	resumed := ""
	if entry.Resumed {
		resumed = " resumed=true"
	}
	fmt.Fprintf(
		stdout,
		"%s role=%s phase=%s session=%s#%d%s model=%s\n",
		label, orUnknown(entry.Role), orUnknown(entry.Phase), session, entry.SessionCallIndex, resumed,
		timelineModel(entry),
	)
	fmt.Fprintf(stdout, "%s WINDOW %s\n", label, timelineWindow(entry))
	fmt.Fprintf(stdout, "%s RESULT %s\n", label, timelineResult(entry))
	printTimelineTools(stdout, label+" TOOLS", entry.Tools)
}

// timelineModelはaliasと実modelの両方が観測できているときだけ両方を出す。
func timelineModel(entry state.CallTimelineEntry) string {
	model := orUnknown(entry.ModelAlias)
	if entry.MessageModel != "" && entry.MessageModel != entry.ModelAlias {
		model += "(" + entry.MessageModel + ")"
	}
	return model
}

func timelineWindow(entry state.CallTimelineEntry) string {
	if entry.FirstAt.IsZero() || entry.LastAt.IsZero() {
		return fmt.Sprintf("start=unknown end=unknown span=unknown events=%d", entry.Events)
	}
	return fmt.Sprintf(
		"start=%s end=%s span=%dms events=%d",
		entry.FirstAt.UTC().Format(time.RFC3339),
		entry.LastAt.UTC().Format(time.RFC3339),
		entry.LastAt.Sub(entry.FirstAt).Milliseconds(),
		entry.Events,
	)
}

// timelineResultはresult eventが観測されたcallだけ結果値を出す。duration 0は
// 未測定としてunknown表示し、tokenはresult usageが観測できたときだけ出す。
func timelineResult(entry state.CallTimelineEntry) string {
	if !entry.ResultObserved {
		return "none"
	}
	parts := []string{fmt.Sprintf("status=%s", orUnknown(entry.ResultSubtype))}
	if entry.DurationMS > 0 {
		parts = append(parts, fmt.Sprintf("dur=%dms", entry.DurationMS))
	} else {
		parts = append(parts, "dur=unknown")
	}
	if entry.DurationAPIMS > 0 {
		parts = append(parts, fmt.Sprintf("api=%dms", entry.DurationAPIMS))
	}
	if entry.NumTurns > 0 {
		parts = append(parts, fmt.Sprintf("turns=%d", entry.NumTurns))
	}
	if entry.Usage != nil {
		promptTokens := entry.Usage.InputTokens +
			entry.Usage.CacheCreationInputTokens +
			entry.Usage.CacheReadInputTokens
		parts = append(parts, fmt.Sprintf("in=%d out=%d", promptTokens, entry.Usage.OutputTokens))
	}
	if entry.TotalCostUSD != 0 {
		parts = append(parts, fmt.Sprintf("cost=%.4f", entry.TotalCostUSD))
	}
	if entry.IsError {
		parts = append(parts, "is_error=true")
	}
	return strings.Join(parts, " ")
}

// printTimelineToolsはtool種別別の観測を1行へ出す。件数0のfieldは出さず、
// 測定済みdurationの合計・最大だけを出す(未測定は推測しない)。
func printTimelineTools(stdout io.Writer, label string, tools []state.CallTimelineTool) {
	if len(tools) == 0 {
		fmt.Fprintf(stdout, "%s none\n", label)
		return
	}
	rendered := make([]string, 0, len(tools))
	for _, tool := range tools {
		parts := []string{fmt.Sprintf("%s uses=%d", tool.Name, tool.Uses)}
		if tool.Results > 0 {
			parts = append(parts, fmt.Sprintf("results=%d", tool.Results))
		}
		if tool.Measured > 0 {
			parts = append(parts, fmt.Sprintf("measured=%d sum=%dms max=%dms", tool.Measured, tool.MeasuredSumMS, tool.MeasuredMaxMS))
		}
		if tool.Unmeasured > 0 {
			parts = append(parts, fmt.Sprintf("unmeasured=%d", tool.Unmeasured))
		}
		if tool.Errors > 0 {
			parts = append(parts, fmt.Sprintf("errors=%d", tool.Errors))
		}
		rendered = append(rendered, strings.Join(parts, " "))
	}
	fmt.Fprintf(stdout, "%s %s\n", label, strings.Join(rendered, "; "))
}

// printTimelineGraphはcall観測窓の相対barを出す。全callの窓が測定できないときは
// 何も出さない。
func printTimelineGraph(stdout io.Writer, entries []state.CallTimelineEntry) {
	var maxSpanMS int64
	spans := make([]int64, len(entries))
	for i, entry := range entries {
		if entry.FirstAt.IsZero() || entry.LastAt.IsZero() {
			continue
		}
		spans[i] = entry.LastAt.Sub(entry.FirstAt).Milliseconds()
		if spans[i] > maxSpanMS {
			maxSpanMS = spans[i]
		}
	}
	if maxSpanMS <= 0 {
		return
	}
	fmt.Fprintf(stdout, "GRAPH_SPAN_MAX: %dms\n", maxSpanMS)
	for i, span := range spans {
		fmt.Fprintf(stdout, "GRAPH #%d span=%dms [%s]\n", i+1, span, timelineBar(span, maxSpanMS))
	}
}

func timelineBar(value int64, max int64) string {
	if value <= 0 || max <= 0 {
		return ""
	}
	width := value * timelineGraphMaxWidth / max
	if width < 1 {
		width = 1
	}
	if width > timelineGraphMaxWidth {
		width = timelineGraphMaxWidth
	}
	return strings.Repeat("#", int(width))
}
