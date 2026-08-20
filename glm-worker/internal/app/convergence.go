package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// convergenceDeltaVerificationOnlyはsame-snapshot roundのうち、当該roundを生成した
// worker呼出がevent logでtool利用を観測され・file変更toolを一度も使っていないもの。
// treeはsnapshotで同一と確定しているため再review対象状態は同一のまま扱う。
const convergenceDeltaVerificationOnly = "verification-only"

// convergenceMutatingToolsはworktree内容を変更しうるtool名。これらが観測された
// same-snapshot roundはverification-onlyへ細分化しない(編集後に戻した可能性)。
var convergenceMutatingTools = map[string]bool{
	"Edit":         true,
	"Write":        true,
	"NotebookEdit": true,
}

var convergenceReviewerPhase = regexp.MustCompile(`^reviewer-(\d+)(-risk-floor)?$`)

// convergenceRoundは表示1行分のround観測。recordはround log、reviewer/workerは時間窓で
// 対応付けたtelemetry呼出。gapはreviewer番号不一致かseq不連続でrecord欠落が疑われるround。
type convergenceRound struct {
	record   state.RoundRecord
	delta    state.RoundDelta
	gap      bool
	mismatch bool
	reviewer []state.ModelCallLog
	worker   []state.ModelCallLog
}

// printConvergenceはround log・telemetry・event logだけからreview/fix convergenceを
// 表示する。state書換・repo lock・AI call・provider/workerへの問い合わせを行わない。
// telemetryの呼出結果・token・durationとround logのsnapshot・path分類をrecordの
// CapturedAt時間窓とWorkerPhaseで対応付け、対応付け不能な値はunknownとして推測しない。
// taskIDArgが空なら現在task、指定されればそのtaskの保存済みlogを読む。task ID検証は
// --timelineと同じUUID v4境界を使う。明示指定taskのround log不在・読込失敗はerrorと
// し、現在taskの不在は正常終了する。
func printConvergence(st *state.StateStore, taskIDArg string, stdout io.Writer) error {
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

	records, skipped, recordsErr := readRoundRecords(st, taskID)
	switch {
	case errors.Is(recordsErr, os.ErrNotExist):
		if explicit {
			return fmt.Errorf("task %sのround logがありません: %w", taskID, recordsErr)
		}
		fmt.Fprintln(stdout, "ROUNDS_LOG: none")
		return nil
	case recordsErr != nil:
		if explicit {
			return fmt.Errorf("task %sのround logを読めません: %w", taskID, recordsErr)
		}
		fmt.Fprintln(stdout, "ROUNDS_LOG: unreadable")
		return nil
	}
	fmt.Fprintf(stdout, "ROUNDS_LOG: %s\n", st.RoundLogPath(taskID))
	if skipped > 0 {
		fmt.Fprintf(stdout, "SKIPPED_ROUNDS: %d\n", skipped)
	}

	logs, logErr := readStatusTelemetry(st, taskID)
	printConvergenceTelemetryState(logErr, stdout)
	events := readConvergenceEvents(st, taskID, stdout)

	rounds, baseline := buildConvergenceRounds(records, logs)
	refineConvergenceDeltas(rounds, events)
	printConvergenceRounds(stdout, baseline, rounds)
	printConvergenceSummary(stdout, rounds)
	return nil
}

// refineConvergenceDeltasはsame-snapshot roundをevent logのtool観測で
// verification-onlyへ細分化する。DELTA行とSUMMARY行が同じ分類を共有するため
// ここでdelta.Class自体を書き替える。
func refineConvergenceDeltas(rounds []convergenceRound, events []state.TaskEventRecord) {
	for i := range rounds {
		if rounds[i].delta.Class != state.RoundDeltaSameSnapshot {
			continue
		}
		uses, mutating := convergenceWorkerToolUse(events, rounds[i].record.WorkerPhase)
		if uses > 0 && !mutating {
			rounds[i].delta.Class = convergenceDeltaVerificationOnly
		}
	}
}

func printConvergenceTelemetryState(logErr error, stdout io.Writer) {
	if logErr != nil {
		fmt.Fprintln(stdout, "TELEMETRY: unreadable")
		return
	}
	fmt.Fprintln(stdout, "TELEMETRY: ok")
}

// readConvergenceEventsはverification-only細分化用のevent logを読む。event logは
// best-effort観測のため、不在はnone・読込失敗はunreadableとして扱いerrorにしない。
func readConvergenceEvents(st *state.StateStore, taskID string, stdout io.Writer) []state.TaskEventRecord {
	if taskID == "none" {
		fmt.Fprintln(stdout, "EVENT_LOG: none")
		return nil
	}
	records, _, err := readTaskEventRecords(st, taskID)
	switch {
	case errors.Is(err, os.ErrNotExist):
		fmt.Fprintln(stdout, "EVENT_LOG: none")
	case err != nil:
		fmt.Fprintln(stdout, "EVENT_LOG: unreadable")
	default:
		fmt.Fprintln(stdout, "EVENT_LOG: ok")
		return records
	}
	return nil
}

// readRoundRecordsはround logを行ごとに読む。破損行・旧version行はskipしてその件数を
// 返し、log全体の読込失敗(不在含む)だけをerrorとして返す。
func readRoundRecords(st *state.StateStore, taskID string) ([]state.RoundRecord, int, error) {
	file, err := os.Open(st.RoundLogPath(taskID))
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var records []state.RoundRecord
	skipped := 0
	for scanner.Scan() {
		record, err := state.ParseRoundLine(scanner.Bytes())
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

// buildConvergenceRoundsはround record列を表示単位へ組み立てる。baseline recordは
// 先頭に高々1つだけ分離し、以降をreview roundとして前recordとの差分を分類する。
// telemetry呼出は各recordのCapturedAtを境界とする時間窓へ配る。round iのreviewer呼出は
// [CapturedAt_i, CapturedAt_{i+1})へ、round iを生成したworker呼出は直前の境界からの
// 窓でWorkerPhaseと一致するものを対応付ける。reviewer phase番号がrecordのReviewNumber
// と一致しない呼出が同じ窓に現れたとき、record欠落が疑われるため次roundの分類をunknown
// へ倒す。
func buildConvergenceRounds(records []state.RoundRecord, logs []state.ModelCallLog) ([]convergenceRound, *state.RoundRecord) {
	if len(records) == 0 {
		return nil, nil
	}
	buckets := bucketTaskCallsByRound(records, logs)

	var baseline *state.RoundRecord
	rest := records
	if records[0].WorkerPhase == state.RoundWorkerPhaseBaseline {
		b := records[0]
		baseline = &b
		rest = records[1:]
	}
	rounds := make([]convergenceRound, 0, len(rest))
	for index, record := range rest {
		round := convergenceRound{record: record}
		recordIndex := index + len(records) - len(rest)
		round.reviewer = reviewerCallsInBucket(buckets[recordIndex], record.ReviewNumber, &round.mismatch)
		round.worker = producingWorkerCalls(buckets[recordIndex-1], record.WorkerPhase)
		round.gap = roundHasRecordGap(records, recordIndex)
		round.delta = state.CompareRoundRecords(prevRoundRecord(records, recordIndex), &rest[index])
		rounds = append(rounds, round)
	}
	for i := 1; i < len(rounds); i++ {
		if rounds[i-1].mismatch {
			rounds[i].gap = true
		}
	}
	for i := range rounds {
		if rounds[i].gap {
			rounds[i].delta = state.RoundDelta{Class: state.RoundDeltaUnknown}
		}
	}
	return rounds, baseline
}

// bucketTaskCallsByRoundはtask呼出をrecord境界の時間窓へ割り当てる。窓kは
// [records[k].CapturedAt, records[k+1].CapturedAt)で、最後の窓は以降全て。最初の境界
// より前の呼出は窓-1へ置き、先頭recordがround 1のときその生成worker呼出として扱える
// ようにする(先頭がbaselineのとき通常は存在しない)。
func bucketTaskCallsByRound(records []state.RoundRecord, logs []state.ModelCallLog) map[int][]state.ModelCallLog {
	buckets := make(map[int][]state.ModelCallLog, len(records)+1)
	boundaryIndex := func(startedAt time.Time) int {
		index := -1
		for i := len(records) - 1; i >= 0; i-- {
			if !records[i].CapturedAt.After(startedAt) {
				index = i
				break
			}
		}
		return index
	}
	for _, entry := range logs {
		if entry.CallType != state.CallTypeTask {
			continue
		}
		index := boundaryIndex(entry.StartedAt)
		buckets[index] = append(buckets[index], entry)
	}
	return buckets
}

func reviewerCallsInBucket(entries []state.ModelCallLog, reviewNumber int, mismatch *bool) []state.ModelCallLog {
	result := make([]state.ModelCallLog, 0, len(entries))
	for _, entry := range entries {
		if entry.Role != state.ReviewerRole {
			continue
		}
		match := convergenceReviewerPhase.FindStringSubmatch(entry.Phase)
		if match == nil || match[1] != fmt.Sprint(reviewNumber) {
			*mismatch = true
			continue
		}
		result = append(result, entry)
	}
	return result
}

// producingWorkerCallsはround生成呼出のtelemetry記録を取り出す。WorkerPhaseと一致
// (packet圧縮のsuffix付きを含む)するworker呼出のうち、IMPLEMENTED以外のpacketを
// 返した呼出(Sol decisionへ向かった試行)は対象から外す。packet空欄の呼出は
// transient再試行・invalid packetなど当該呼出の消費なので残す。
func producingWorkerCalls(entries []state.ModelCallLog, workerPhase string) []state.ModelCallLog {
	result := make([]state.ModelCallLog, 0, len(entries))
	for _, entry := range entries {
		if entry.Role != state.WorkerRole {
			continue
		}
		if entry.Phase != workerPhase && entry.Phase != workerPhase+"-packet-compact" {
			continue
		}
		if entry.PacketStatus != "" && entry.PacketStatus != "IMPLEMENTED" {
			continue
		}
		result = append(result, entry)
	}
	return result
}

// roundHasRecordGapは直前recordとのseq不連続を検出する。先頭round(またはbaseline直後)
// は比較対象が無いためfalse。
func roundHasRecordGap(records []state.RoundRecord, recordIndex int) bool {
	if recordIndex <= 0 {
		return false
	}
	return records[recordIndex].Seq != records[recordIndex-1].Seq+1
}

func prevRoundRecord(records []state.RoundRecord, recordIndex int) *state.RoundRecord {
	if recordIndex <= 0 {
		return nil
	}
	prev := records[recordIndex-1]
	return &prev
}

func printConvergenceRounds(stdout io.Writer, baseline *state.RoundRecord, rounds []convergenceRound) {
	if baseline != nil {
		fmt.Fprintf(
			stdout,
			"BASELINE: captured=%s paths=%d snapshot=%s\n",
			convergenceTime(baseline.CapturedAt),
			len(baseline.Paths),
			convergenceSnapshotState(baseline),
		)
		if baseline.CaptureError != "" {
			fmt.Fprintf(stdout, "BASELINE_ERROR: %s\n", baseline.CaptureError)
		}
	} else {
		fmt.Fprintln(stdout, "BASELINE: none")
	}
	fmt.Fprintf(stdout, "ROUNDS: %d\n", len(rounds))
	for index, round := range rounds {
		printConvergenceRound(stdout, index+1, round)
	}
}

func printConvergenceRound(stdout io.Writer, number int, round convergenceRound) {
	record := round.record
	fmt.Fprintf(
		stdout,
		"ROUND #%d seq=%d review=%d autofixes=%d worker=%s\n",
		number, record.Seq, record.ReviewNumber, record.AutoFixes, record.WorkerPhase,
	)
	deltaClass := round.delta.Class
	notes := []string{}
	if round.gap {
		notes = append(notes, "gap=yes")
	}
	if round.mismatch {
		notes = append(notes, "mismatched_reviewer=yes")
	}
	if record.CaptureError != "" {
		notes = append(notes, "capture_error="+convergenceNoteText(record.CaptureError))
	}
	note := ""
	if len(notes) > 0 {
		note = " " + strings.Join(notes, " ")
	}
	fmt.Fprintf(
		stdout,
		"ROUND #%d DELTA: class=%s changed=%d nonsemantic=%d%s\n",
		number, deltaClass, round.delta.ChangedPaths, round.delta.ChangedPaths-round.delta.SemanticPaths, note,
	)
	snapshot := record.Snapshot
	fmt.Fprintf(
		stdout,
		"ROUND #%d SNAPSHOT: head=%s index=%s worktree=%s\n",
		number, shortDigest(snapshot.Head), shortDigest(snapshot.IndexDigest), shortDigest(snapshot.WorktreeDigest),
	)
	printConvergenceReview(stdout, number, round)
	printConvergenceCost(stdout, "REVIEWER", number, round.reviewer)
	printConvergenceCost(stdout, "WORKER", number, round.worker)
}

// convergenceWorkerToolUseは当該phaseのworker eventからtool_use観測数とfile変更tool
// の有無を数える。event recordが無いときは0観測として細分化しない。
func convergenceWorkerToolUse(events []state.TaskEventRecord, workerPhase string) (int, bool) {
	uses := 0
	mutating := false
	for _, event := range events {
		if event.Role != string(state.WorkerRole) || event.Phase != workerPhase {
			continue
		}
		for _, block := range event.Blocks {
			if block.Type != "tool_use" {
				continue
			}
			uses++
			if convergenceMutatingTools[block.Name] {
				mutating = true
			}
		}
	}
	return uses, mutating
}

func printConvergenceReview(stdout io.Writer, number int, round convergenceRound) {
	outcome := "none"
	risk := "unknown"
	reported := "unknown"
	reemit := "no"
	unresolved := "no"
	snapshot := "unknown"
	for _, entry := range round.reviewer {
		if strings.HasSuffix(entry.Phase, "-risk-floor") {
			reemit = "yes"
		}
		if entry.PacketStatus != "" {
			outcome = entry.PacketStatus
		}
		if entry.EffectiveRisk != "" {
			risk = entry.EffectiveRisk
		}
		if entry.ReviewerReportedRisk != "" {
			reported = entry.ReviewerReportedRisk
		}
		if entry.Snapshot != nil && entry.Snapshot.Matched != nil && *entry.Snapshot.Matched {
			snapshot = "matched"
		}
	}
	if outcome == "FIX_REQUIRED" {
		unresolved = "yes"
	}
	fmt.Fprintf(
		stdout,
		"ROUND #%d REVIEW: calls=%d outcome=%s risk=%s reported=%s reemit=%s unresolved=%s snapshot=%s\n",
		number, len(round.reviewer), outcome, risk, reported, reemit, unresolved, snapshot,
	)
}

func printConvergenceCost(stdout io.Writer, label string, number int, entries []state.ModelCallLog) {
	if len(entries) == 0 {
		fmt.Fprintf(stdout, "ROUND #%d %s_COST: none\n", number, label)
		return
	}
	var usage state.TokenUsage
	turns := 0
	durationMS := int64(0)
	costUSD := 0.0
	for _, entry := range entries {
		usage.InputTokens += entry.TreeUsage.InputTokens
		usage.CacheCreationInputTokens += entry.TreeUsage.CacheCreationInputTokens
		usage.CacheReadInputTokens += entry.TreeUsage.CacheReadInputTokens
		usage.OutputTokens += entry.TreeUsage.OutputTokens
		turns += entry.TopLevelTurns
		durationMS += entry.WallDurationMS
		costUSD += entry.TotalCostUSD
	}
	line := fmt.Sprintf(
		"ROUND #%d %s_COST: calls=%d in=%d out=%d turns=%d dur=%dms",
		number, label, len(entries),
		usage.InputTokens+usage.CacheCreationInputTokens+usage.CacheReadInputTokens,
		usage.OutputTokens, turns, durationMS,
	)
	if costUSD != 0 {
		line += fmt.Sprintf(" cost=%.4f", costUSD)
	}
	fmt.Fprintln(stdout, line)
}

type convergenceSummary struct {
	rounds        int
	reviewerCalls int
	reviewerIn    int64
	reviewerOut   int64
	reviewerDurMS int64
}

func printConvergenceSummary(stdout io.Writer, rounds []convergenceRound) {
	byClass := make(map[string]*convergenceSummary)
	unresolved := 0
	high := 0
	for _, round := range rounds {
		class := round.delta.Class
		if _, ok := byClass[class]; !ok {
			byClass[class] = &convergenceSummary{}
		}
		summary := byClass[class]
		summary.rounds++
		summary.reviewerCalls += len(round.reviewer)
		for _, entry := range round.reviewer {
			summary.reviewerIn += entry.TreeUsage.InputTokens +
				entry.TreeUsage.CacheCreationInputTokens +
				entry.TreeUsage.CacheReadInputTokens
			summary.reviewerOut += entry.TreeUsage.OutputTokens
			summary.reviewerDurMS += entry.WallDurationMS
		}
		if roundOutcomeUnresolved(round) {
			unresolved++
		}
		if roundRiskHigh(round) {
			high++
		}
	}
	for _, class := range orderedSummaryClasses(byClass) {
		summary := byClass[class]
		fmt.Fprintf(
			stdout,
			"SUMMARY delta=%s rounds=%d reviewer_calls=%d reviewer_in=%d reviewer_out=%d reviewer_dur_ms=%d\n",
			class, summary.rounds, summary.reviewerCalls, summary.reviewerIn, summary.reviewerOut, summary.reviewerDurMS,
		)
	}
	fmt.Fprintf(stdout, "UNRESOLVED_ISSUE_ROUNDS: %d\n", unresolved)
	fmt.Fprintf(stdout, "HIGH_ROUNDS: %d\n", high)
}

func orderedSummaryClasses(byClass map[string]*convergenceSummary) []string {
	order := []string{
		state.RoundDeltaSameSnapshot,
		convergenceDeltaVerificationOnly,
		state.RoundDeltaCommentDocFormat,
		state.RoundDeltaSemantic,
		state.RoundDeltaUnknown,
		state.RoundDeltaInitial,
	}
	classes := make([]string, 0, len(byClass))
	for _, class := range order {
		if _, ok := byClass[class]; ok {
			classes = append(classes, class)
		}
	}
	extra := make([]string, 0)
	for class := range byClass {
		if !containsString(classes, class) {
			extra = append(extra, class)
		}
	}
	sort.Strings(extra)
	return append(classes, extra...)
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func roundOutcomeUnresolved(round convergenceRound) bool {
	for _, entry := range round.reviewer {
		if entry.PacketStatus == "FIX_REQUIRED" {
			return true
		}
	}
	return false
}

func roundRiskHigh(round convergenceRound) bool {
	for _, entry := range round.reviewer {
		if entry.EffectiveRisk == "HIGH" || entry.ReviewerReportedRisk == "HIGH" {
			return true
		}
	}
	return false
}

// convergenceNoteTextはnote欄へ埋め込むerror文字列を1行へ押し潰す。
func convergenceNoteText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func convergenceTime(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	return value.UTC().Format(time.RFC3339)
}

func convergenceSnapshotState(record *state.RoundRecord) string {
	if record.Snapshot.IndexDigest == "" || record.Snapshot.WorktreeDigest == "" {
		return "unknown"
	}
	return "ok"
}

func shortDigest(value string) string {
	if value == "" {
		return "none"
	}
	if len(value) > 8 {
		return value[:8]
	}
	return value
}
