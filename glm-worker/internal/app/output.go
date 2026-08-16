package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/abeval"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/autoresume"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

func printStatus(st *state.StateStore, stdout io.Writer) error {
	fmt.Fprintf(stdout, "REPO: %s\n", st.ReadOr("repo-root", "unknown"))
	probe := ProbeRepoLock(st.LockPath())
	fmt.Fprintf(stdout, "REPOSITORY_LOCK: %s\n", probe.State)
	fmt.Fprintf(stdout, "LOCK_PID: %s\n", probe.PID)
	taskID := st.ReadOr("task.id", "none")
	fmt.Fprintf(stdout, "TASK_ID: %s\n", taskID)
	if taskID != "none" {
		fmt.Fprintf(stdout, "ARTIFACT_DIR: %s\n", st.ArtifactDir(taskID))
	} else {
		fmt.Fprintln(stdout, "ARTIFACT_DIR: none")
	}
	fmt.Fprintf(stdout, "TASK_STATUS: %s\n", st.TaskStatus())
	if st.TaskStatus() == state.TaskStatusActive {
		fmt.Fprintf(stdout, "TASK_LIVENESS: %s\n", taskLiveness(probe))
	}
	fmt.Fprintf(stdout, "WORKER_SESSION: %s\n", st.ReadOr("worker.id", "none"))
	fmt.Fprintf(stdout, "REVIEWER_SESSION: %s\n", st.ReadOr("reviewer.id", "none"))
	if st.Exists("pending-decision") {
		fmt.Fprintln(stdout, "PENDING_DECISION: yes")
	} else {
		fmt.Fprintln(stdout, "PENDING_DECISION: no")
	}

	logs, logErr := readStatusTelemetry(st, taskID)
	printTaskDetail(st, taskID, stdout)

	checkpoint, err := st.LoadResumeCheckpoint()
	rateLimited := err == nil && checkpoint.RateLimited
	providerUnavailable := err == nil && checkpoint.ProviderUnavailable

	if rateLimited {
		fmt.Fprintln(stdout, "RATE_LIMITED: yes")
		fmt.Fprintf(stdout, "RATE_LIMIT_PHASE: %s\n", checkpoint.Phase)
		fmt.Fprintf(stdout, "RESET_AT_CST: %s\n", checkpoint.ResetAtCST)
		if checkpoint.ResetAtRFC3339 != "" {
			fmt.Fprintf(stdout, "RESET_AT_RFC3339: %s\n", checkpoint.ResetAtRFC3339)
		}
		fmt.Fprintln(stdout, "RESET_TIMEZONE: CST (China Standard Time, UTC+8)")
	} else {
		fmt.Fprintln(stdout, "RATE_LIMITED: no")
	}

	if providerUnavailable {
		fmt.Fprintln(stdout, "PROVIDER_UNAVAILABLE: yes")
		fmt.Fprintf(stdout, "PROVIDER_PHASE: %s\n", checkpoint.Phase)
		classification := checkpoint.ProviderUnavailableClassification
		if classification == "" {
			classification = "unknown"
		}
		fmt.Fprintf(stdout, "PROVIDER_CLASSIFICATION: %s\n", classification)
		fmt.Fprintf(stdout, "PROVIDER_PROBES: %d\n", checkpoint.ProviderUnavailableProbes)
		if !checkpoint.ProviderUnavailableStartedAt.IsZero() {
			fmt.Fprintf(stdout, "PROVIDER_ELAPSED: %s\n", time.Since(checkpoint.ProviderUnavailableStartedAt).Truncate(time.Second))
		} else {
			fmt.Fprintln(stdout, "PROVIDER_ELAPSED: unknown")
		}
		fmt.Fprintln(stdout, "PROVIDER_RESUME_PLAN: --resume re-probes the provider before continuing this phase")
	} else {
		fmt.Fprintln(stdout, "PROVIDER_UNAVAILABLE: no")
	}

	printProbeDetail(logs, stdout)

	if rateLimited || providerUnavailable {
		fmt.Fprintln(stdout, "RESUME_AVAILABLE: yes")
	} else {
		fmt.Fprintln(stdout, "RESUME_AVAILABLE: no")
	}

	printSessionAging(taskID, logErr, logs, stdout)
	return nil
}

// readStatusTelemetryは現在taskのtelemetry呼出記録を読む。file不在は空扱いとし、
// corruption等の読み取り失敗は表示側でunreadable表示へ使う(status自体は失敗させない)。
func readStatusTelemetry(st *state.StateStore, taskID string) ([]state.ModelCallLog, error) {
	if taskID == "none" {
		return nil, nil
	}
	logs, err := st.ReadModelCallLogs(taskID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return logs, nil
}

// printTaskDetailは既存stateだけから現在taskの実行観測(current phase/role/model・開始
// 経過時間・最終event)を表示する。AI call・provider requestは行わず、読み取れた値だけを
// 出し、取得できない項目はunknown/noneとする(推測補完はしない)。
func printTaskDetail(st *state.StateStore, taskID string, stdout io.Writer) {
	if stats, err := st.CurrentTaskStats(); err != nil || stats.StartedAt.IsZero() {
		fmt.Fprintln(stdout, "TASK_STARTED_AT: unknown")
		fmt.Fprintln(stdout, "TASK_ELAPSED: unknown")
	} else {
		fmt.Fprintf(stdout, "TASK_STARTED_AT: %s\n", stats.StartedAt.Format(time.RFC3339))
		fmt.Fprintf(stdout, "TASK_ELAPSED: %s\n", time.Since(stats.StartedAt).Truncate(time.Second))
	}

	current := currentCallView{}
	if last, ok := lastTaskEvent(st, taskID); ok {
		current = currentCallView{phase: last.Phase, role: last.Role, model: last.ModelAlias}
		if current.model == "" {
			current.model = last.MessageModel
		}
		fmt.Fprintln(stdout, "LAST_EVENT: "+formatTaskEvent(last))
		if last.Timestamp.IsZero() {
			fmt.Fprintln(stdout, "LAST_EVENT_AGE: unknown")
		} else {
			fmt.Fprintf(stdout, "LAST_EVENT_AGE: %s\n", time.Since(last.Timestamp).Truncate(time.Second))
		}
	} else {
		fmt.Fprintln(stdout, "LAST_EVENT: none")
		fmt.Fprintln(stdout, "LAST_EVENT_AGE: unknown")
		if checkpoint, err := st.LoadResumeCheckpoint(); err == nil {
			current = currentCallView{phase: checkpoint.Phase, role: string(checkpoint.Role), model: checkpoint.Model}
		}
	}
	fmt.Fprintf(stdout, "CURRENT_PHASE: %s\n", orUnknown(current.phase))
	fmt.Fprintf(stdout, "CURRENT_ROLE: %s\n", orUnknown(current.role))
	fmt.Fprintf(stdout, "CURRENT_MODEL: %s\n", orUnknown(current.model))
}

// printProbeDetailはprovider probe呼出の観測(実行回数と最終probe)をtelemetryだけから
// 表示する。in-process backoff中の状況観測に使い、probe記録がないときは何も出さない。
func printProbeDetail(logs []state.ModelCallLog, stdout io.Writer) {
	probes := make([]state.ModelCallLog, 0)
	for _, log := range logs {
		if log.CallType == state.CallTypeProbe {
			probes = append(probes, log)
		}
	}
	if len(probes) == 0 {
		return
	}
	last := probes[len(probes)-1]
	fmt.Fprintf(stdout, "PROBES: %d\n", len(probes))
	fmt.Fprintf(stdout, "PROBE_LAST_AT: %s\n", last.CompletedAt.Format(time.RFC3339))
	fmt.Fprintf(stdout, "PROBE_LAST_AGE: %s\n", time.Since(last.CompletedAt).Truncate(time.Second))
	fmt.Fprintf(stdout, "PROBE_LAST_OUTCOME: %s\n", orUnknown(last.Outcome))
	fmt.Fprintf(stdout, "PROBE_LAST_ATTEMPT: %d\n", last.ProbeAttempt)
}

// printSessionAgingは現在taskのsession別aging要約(role/model・session内call index相当の
// latency列・累積turn/token)を既存telemetryだけから表示する。
func printSessionAging(taskID string, logErr error, logs []state.ModelCallLog, stdout io.Writer) {
	if taskID == "none" {
		fmt.Fprintln(stdout, "SESSION_AGING: none")
		return
	}
	if logErr != nil {
		fmt.Fprintln(stdout, "SESSION_AGING: unreadable")
		return
	}
	sessions := state.AgingFromModelCallLogs(logs)
	if len(sessions) == 0 {
		fmt.Fprintln(stdout, "SESSION_AGING: none")
		return
	}
	for _, session := range sessions {
		latencies := make([]string, 0, len(session.CallLatencyMS))
		for _, latency := range session.CallLatencyMS {
			latencies = append(latencies, fmt.Sprintf("%d", latency))
		}
		fmt.Fprintf(
			stdout,
			"SESSION_AGING: role=%s model=%s id=%s calls=%d resumed=%d turns=%d in=%d out=%d lat_ms=%s\n",
			session.Role,
			strings.Join(session.Models, "+"),
			session.SessionID,
			session.Calls,
			session.ResumedCalls,
			session.CumulativeTurns,
			session.CumulativeInputTokens,
			session.CumulativeOutputTokens,
			strings.Join(latencies, ","),
		)
	}
}

// currentCallViewは--status表示用の現在呼出識別。event log最終recordを優先し、
// eventがないときはresume checkpointから補う。
type currentCallView struct {
	phase string
	role  string
	model string
}

// lastTaskEventはtask event logの最終parse可能recordを返す。書き込み途中の末尾部分行は
// parse失敗として無視される。
func lastTaskEvent(st *state.StateStore, taskID string) (state.TaskEventRecord, bool) {
	if taskID == "none" {
		return state.TaskEventRecord{}, false
	}
	file, err := os.Open(st.TaskEventLogPath(taskID))
	if err != nil {
		return state.TaskEventRecord{}, false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var last state.TaskEventRecord
	found := false
	for scanner.Scan() {
		record, err := state.ParseTaskEventLine(scanner.Bytes())
		if err != nil {
			continue
		}
		last = record
		found = true
	}
	return last, found
}

func orUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

// taskLivenessはTASK_STATUS=active時のrepo lock実保持による生存表示。
// lock heldなら現在のglm-worker processが同一repo taskを実行中、freeはstale候補、
// unknownは判定不能。lock file内PIDは権威にせずstale PID・PID reuseでrunning扱いしない。
func taskLiveness(probe LockProbe) string {
	switch probe.State {
	case LockHeld:
		return "running"
	case LockFree:
		return "stale"
	default:
		return "unknown"
	}
}

func printStats(st *state.StateStore, stdout io.Writer) error {
	all, err := st.AllTaskStats()
	if err != nil {
		return err
	}

	aggregate := state.TaskStats{}
	for _, stats := range all {
		aggregate.ModelCalls += stats.ModelCalls
		mergeIntMap(&aggregate.ModelCallsByAlias, stats.ModelCallsByAlias)
		mergeInt64Map(&aggregate.ModelDurationMSByAlias, stats.ModelDurationMSByAlias)
		mergeIntMap(&aggregate.RateLimitsByAlias, stats.RateLimitsByAlias)
		mergeInt64Map(&aggregate.InputTokensByAlias, stats.InputTokensByAlias)
		mergeInt64Map(&aggregate.CacheCreationInputTokensByAlias, stats.CacheCreationInputTokensByAlias)
		mergeInt64Map(&aggregate.CacheReadInputTokensByAlias, stats.CacheReadInputTokensByAlias)
		mergeInt64Map(&aggregate.OutputTokensByAlias, stats.OutputTokensByAlias)
		mergeIntMap(&aggregate.TopLevelTurnsByAlias, stats.TopLevelTurnsByAlias)
		mergeIntMap(&aggregate.CallTreesByResolvedModel, stats.CallTreesByResolvedModel)
		mergeInt64Map(&aggregate.InputTokensByResolvedModel, stats.InputTokensByResolvedModel)
		mergeInt64Map(&aggregate.CacheCreationInputTokensByResolvedModel, stats.CacheCreationInputTokensByResolvedModel)
		mergeInt64Map(&aggregate.CacheReadInputTokensByResolvedModel, stats.CacheReadInputTokensByResolvedModel)
		mergeInt64Map(&aggregate.OutputTokensByResolvedModel, stats.OutputTokensByResolvedModel)
		aggregate.WorkerCalls += stats.WorkerCalls
		aggregate.ReviewerCalls += stats.ReviewerCalls
		aggregate.DecisionCommands += stats.DecisionCommands
		aggregate.FixCommands += stats.FixCommands
		aggregate.ResumeCommands += stats.ResumeCommands
		aggregate.AutoFixRounds += stats.AutoFixRounds
		aggregate.NeedsSolDecisionPackets += stats.NeedsSolDecisionPackets
		aggregate.NeedsSolReviewPackets += stats.NeedsSolReviewPackets
		aggregate.PassPackets += stats.PassPackets
		aggregate.RateLimits += stats.RateLimits
		aggregate.PacketCompactions += stats.PacketCompactions
		aggregate.SolPacketBytes += stats.SolPacketBytes
		aggregate.ProviderUnavailable += stats.ProviderUnavailable
		mergeIntMap(&aggregate.ProviderUnavailableByAlias, stats.ProviderUnavailableByAlias)
		mergeIntMap(&aggregate.RiskFloorByCategory, stats.RiskFloorByCategory)
		aggregate.SnapshotMismatches += stats.SnapshotMismatches
		mergeIntMap(&aggregate.SnapshotMismatchByAxis, stats.SnapshotMismatchByAxis)
		mergeIntMap(&aggregate.PacketRejectByCategory, stats.PacketRejectByCategory)
		mergeIntMap(&aggregate.ProbeOutcome, stats.ProbeOutcome)
		aggregate.TransientRetries += stats.TransientRetries
	}

	probeCalls := 0
	for _, count := range aggregate.ProbeOutcome {
		probeCalls += count
	}

	fmt.Fprintf(stdout, "TASKS: %d\n", len(all))
	// MODEL_CALLSはTask Work Callのみ。probeはPROBE_CALLS(probe_outcome総計)へ別計上し、
	// TOTAL_AI_CALLS = MODEL_CALLS + PROBE_CALLS で重複・欠落なく導出できる。
	fmt.Fprintf(stdout, "MODEL_CALLS: %d\n", aggregate.ModelCalls)
	fmt.Fprintf(stdout, "MODEL_CALLS_BY_ALIAS: %s\n", formatIntMap(aggregate.ModelCallsByAlias))
	fmt.Fprintf(stdout, "PROBE_CALLS: %d\n", probeCalls)
	fmt.Fprintf(stdout, "TOTAL_AI_CALLS: %d\n", aggregate.ModelCalls+probeCalls)
	fmt.Fprintf(stdout, "MODEL_DURATION_MS_BY_ALIAS: %s\n", formatInt64Map(aggregate.ModelDurationMSByAlias))
	fmt.Fprintf(stdout, "INPUT_TOKENS_BY_ALIAS: %s\n", formatInt64Map(aggregate.InputTokensByAlias))
	fmt.Fprintf(stdout, "CACHE_CREATION_INPUT_TOKENS_BY_ALIAS: %s\n", formatInt64Map(aggregate.CacheCreationInputTokensByAlias))
	fmt.Fprintf(stdout, "CACHE_READ_INPUT_TOKENS_BY_ALIAS: %s\n", formatInt64Map(aggregate.CacheReadInputTokensByAlias))
	fmt.Fprintf(stdout, "TOTAL_PROMPT_TOKENS_BY_ALIAS: %s\n", formatInt64Map(sumInt64Maps(
		aggregate.InputTokensByAlias,
		aggregate.CacheCreationInputTokensByAlias,
		aggregate.CacheReadInputTokensByAlias,
	)))
	fmt.Fprintf(stdout, "OUTPUT_TOKENS_BY_ALIAS: %s\n", formatInt64Map(aggregate.OutputTokensByAlias))
	fmt.Fprintf(stdout, "TOP_LEVEL_TURNS_BY_ALIAS: %s\n", formatIntMap(aggregate.TopLevelTurnsByAlias))
	fmt.Fprintf(stdout, "CALL_TREES_BY_RESOLVED_MODEL: %s\n", formatIntMap(aggregate.CallTreesByResolvedModel))
	fmt.Fprintf(stdout, "INPUT_TOKENS_BY_RESOLVED_MODEL: %s\n", formatInt64Map(aggregate.InputTokensByResolvedModel))
	fmt.Fprintf(stdout, "CACHE_CREATION_INPUT_TOKENS_BY_RESOLVED_MODEL: %s\n", formatInt64Map(aggregate.CacheCreationInputTokensByResolvedModel))
	fmt.Fprintf(stdout, "CACHE_READ_INPUT_TOKENS_BY_RESOLVED_MODEL: %s\n", formatInt64Map(aggregate.CacheReadInputTokensByResolvedModel))
	fmt.Fprintf(stdout, "OUTPUT_TOKENS_BY_RESOLVED_MODEL: %s\n", formatInt64Map(aggregate.OutputTokensByResolvedModel))
	fmt.Fprintf(stdout, "WORKER_CALLS: %d\n", aggregate.WorkerCalls)
	fmt.Fprintf(stdout, "REVIEWER_CALLS: %d\n", aggregate.ReviewerCalls)
	fmt.Fprintf(stdout, "DECISION_COMMANDS: %d\n", aggregate.DecisionCommands)
	fmt.Fprintf(stdout, "FIX_COMMANDS: %d\n", aggregate.FixCommands)
	fmt.Fprintf(stdout, "RESUME_COMMANDS: %d\n", aggregate.ResumeCommands)
	fmt.Fprintf(stdout, "TRANSIENT_RETRIES: %d\n", aggregate.TransientRetries)
	fmt.Fprintf(stdout, "AUTO_FIX_ROUNDS: %d\n", aggregate.AutoFixRounds)
	fmt.Fprintf(stdout, "NEEDS_SOL_DECISION_PACKETS: %d\n", aggregate.NeedsSolDecisionPackets)
	fmt.Fprintf(stdout, "NEEDS_SOL_REVIEW_PACKETS: %d\n", aggregate.NeedsSolReviewPackets)
	fmt.Fprintf(stdout, "PASS_PACKETS: %d\n", aggregate.PassPackets)
	fmt.Fprintf(stdout, "RATE_LIMITS: %d\n", aggregate.RateLimits)
	fmt.Fprintf(stdout, "RATE_LIMITS_BY_ALIAS: %s\n", formatIntMap(aggregate.RateLimitsByAlias))
	fmt.Fprintf(stdout, "PROVIDER_UNAVAILABLE: %d\n", aggregate.ProviderUnavailable)
	fmt.Fprintf(stdout, "PROVIDER_UNAVAILABLE_BY_ALIAS: %s\n", formatIntMap(aggregate.ProviderUnavailableByAlias))
	fmt.Fprintf(stdout, "PACKET_COMPACTIONS: %d\n", aggregate.PacketCompactions)
	fmt.Fprintf(stdout, "RISK_FLOOR_BY_CATEGORY: %s\n", formatIntMap(aggregate.RiskFloorByCategory))
	fmt.Fprintf(stdout, "SNAPSHOT_MISMATCHES: %d\n", aggregate.SnapshotMismatches)
	fmt.Fprintf(stdout, "SNAPSHOT_MISMATCH_BY_AXIS: %s\n", formatIntMap(aggregate.SnapshotMismatchByAxis))
	fmt.Fprintf(stdout, "PACKET_REJECT_BY_CATEGORY: %s\n", formatIntMap(aggregate.PacketRejectByCategory))
	fmt.Fprintf(stdout, "PROBE_OUTCOME: %s\n", formatIntMap(aggregate.ProbeOutcome))
	fmt.Fprintf(stdout, "SOL_PACKET_BYTES: %d\n", aggregate.SolPacketBytes)
	fmt.Fprintf(stdout, "TELEMETRY_DIR: %s\n", st.Path("telemetry"))
	fmt.Fprintf(stdout, "CURRENT_TASK_ID: %s\n", st.ReadOr("task.id", "none"))
	fmt.Fprintf(stdout, "CURRENT_TASK_STATUS: %s\n", st.TaskStatus())
	currentTaskID := st.ReadOr("task.id", "none")
	if currentTaskID != "none" {
		fmt.Fprintf(stdout, "CURRENT_ARTIFACT_DIR: %s\n", st.ArtifactDir(currentTaskID))
	} else {
		fmt.Fprintln(stdout, "CURRENT_ARTIFACT_DIR: none")
	}
	return nil
}

func mergeIntMap(target *map[string]int, source map[string]int) {
	if *target == nil {
		*target = make(map[string]int)
	}
	for key, value := range source {
		(*target)[key] += value
	}
}

func mergeInt64Map(target *map[string]int64, source map[string]int64) {
	if *target == nil {
		*target = make(map[string]int64)
	}
	for key, value := range source {
		(*target)[key] += value
	}
}

func formatIntMap(values map[string]int) string {
	items := make([]string, 0, len(values))
	for key, value := range values {
		items = append(items, fmt.Sprintf("%s=%d", key, value))
	}
	sort.Strings(items)
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ",")
}

func formatInt64Map(values map[string]int64) string {
	items := make([]string, 0, len(values))
	for key, value := range values {
		items = append(items, fmt.Sprintf("%s=%d", key, value))
	}
	sort.Strings(items)
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ",")
}

func sumInt64Maps(values ...map[string]int64) map[string]int64 {
	result := make(map[string]int64)
	for _, items := range values {
		for key, value := range items {
			result[key] += value
		}
	}
	return result
}

// printEvalABはdirect/orchestrated A/B run dir(spec.json・direct.json・orchestrated.json)を
// 読み込み、glm_usage.sourceがglm-worker-task-statsの記録だけを既存stats履歴から解決し、
// 比較前提を検証してから結果を表示する。明示commandのため読み込み・解決・検証失敗は
// errorとして返す。AI呼出は行わない。
func printEvalAB(st *state.StateStore, dir string, stdout io.Writer) error {
	spec, direct, orchestrated, err := abeval.LoadPair(dir)
	if err != nil {
		return err
	}
	if orchestrated.GLMUsage.Source == abeval.GLMUsageSourceTaskStats {
		all, err := st.AllTaskStats()
		if err != nil {
			return err
		}
		orchestrated, err = abeval.ResolveFromTaskStats(orchestrated, all)
		if err != nil {
			return err
		}
	}
	if err := abeval.ValidatePair(spec, direct, orchestrated); err != nil {
		return err
	}
	fmt.Fprint(stdout, abeval.Format(abeval.Compare(spec, direct, orchestrated)))
	return nil
}

func resetState(st *state.StateStore, stdout io.Writer) error {
	if err := st.Reset(); err != nil {
		return err
	}

	fmt.Fprintln(stdout, "STATUS: RESET")
	fmt.Fprintf(stdout, "REPO: %s\n", st.ReadOr("repo-root", "unknown"))
	return nil
}

func printVerifyAutoResume(cmd Command, cfg config.AppConfig, stdout io.Writer) error {
	params := autoresume.Params{
		AutomationKey:    cmd.Verify.Key,
		ExpectedRFC3339:  cmd.Verify.RFC3339,
		ExpectedThreadID: cmd.Verify.ThreadID,
		AutomationsDir:   filepath.Join(cfg.CodexConfigDir, "automations"),
		DBPath:           filepath.Join(cfg.CodexConfigDir, "sqlite", "codex-dev.db"),
	}
	result := autoresume.Verify(params, autoresume.ReadDBRowSqlite3)
	autoresume.WriteResult(stdout, result)

	if result.Outcome == autoresume.Pass {
		return nil
	}
	return fmt.Errorf("verification %s: %s", outcomeLabel(result.Outcome), result.Reason)
}

func outcomeLabel(o autoresume.Outcome) string {
	switch o {
	case autoresume.Pass:
		return "pass"
	case autoresume.Fail:
		return "fail"
	default:
		return "unavailable"
	}
}
