package app

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

func printStatus(st *state.StateStore, stdout io.Writer) error {
	fmt.Fprintf(stdout, "REPO: %s\n", st.ReadOr("repo-root", "unknown"))
	taskID := st.ReadOr("task.id", "none")
	fmt.Fprintf(stdout, "TASK_ID: %s\n", taskID)
	if taskID != "none" {
		fmt.Fprintf(stdout, "ARTIFACT_DIR: %s\n", st.ArtifactDir(taskID))
	} else {
		fmt.Fprintln(stdout, "ARTIFACT_DIR: none")
	}
	fmt.Fprintf(stdout, "TASK_STATUS: %s\n", st.TaskStatus())
	fmt.Fprintf(stdout, "WORKER_SESSION: %s\n", st.ReadOr("worker.id", "none"))
	fmt.Fprintf(stdout, "REVIEWER_SESSION: %s\n", st.ReadOr("reviewer.id", "none"))
	if st.Exists("pending-decision") {
		fmt.Fprintln(stdout, "PENDING_DECISION: yes")
	} else {
		fmt.Fprintln(stdout, "PENDING_DECISION: no")
	}

	checkpoint, err := st.LoadResumeCheckpoint()
	rateLimited := err == nil && checkpoint.RateLimited
	providerUnavailable := err == nil && checkpoint.ProviderUnavailable

	if rateLimited {
		fmt.Fprintln(stdout, "RATE_LIMITED: yes")
		fmt.Fprintf(stdout, "RATE_LIMIT_PHASE: %s\n", checkpoint.Phase)
		fmt.Fprintf(stdout, "RESET_AT_CST: %s\n", checkpoint.ResetAtCST)
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
	} else {
		fmt.Fprintln(stdout, "PROVIDER_UNAVAILABLE: no")
	}

	if rateLimited || providerUnavailable {
		fmt.Fprintln(stdout, "RESUME_AVAILABLE: yes")
	} else {
		fmt.Fprintln(stdout, "RESUME_AVAILABLE: no")
	}
	return nil
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
	}

	fmt.Fprintf(stdout, "TASKS: %d\n", len(all))
	fmt.Fprintf(stdout, "MODEL_CALLS: %d\n", aggregate.ModelCalls)
	fmt.Fprintf(stdout, "MODEL_CALLS_BY_ALIAS: %s\n", formatIntMap(aggregate.ModelCallsByAlias))
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
	fmt.Fprintf(stdout, "AUTO_FIX_ROUNDS: %d\n", aggregate.AutoFixRounds)
	fmt.Fprintf(stdout, "NEEDS_SOL_DECISION_PACKETS: %d\n", aggregate.NeedsSolDecisionPackets)
	fmt.Fprintf(stdout, "NEEDS_SOL_REVIEW_PACKETS: %d\n", aggregate.NeedsSolReviewPackets)
	fmt.Fprintf(stdout, "PASS_PACKETS: %d\n", aggregate.PassPackets)
	fmt.Fprintf(stdout, "RATE_LIMITS: %d\n", aggregate.RateLimits)
	fmt.Fprintf(stdout, "RATE_LIMITS_BY_ALIAS: %s\n", formatIntMap(aggregate.RateLimitsByAlias))
	fmt.Fprintf(stdout, "PROVIDER_UNAVAILABLE: %d\n", aggregate.ProviderUnavailable)
	fmt.Fprintf(stdout, "PROVIDER_UNAVAILABLE_BY_ALIAS: %s\n", formatIntMap(aggregate.ProviderUnavailableByAlias))
	fmt.Fprintf(stdout, "PACKET_COMPACTIONS: %d\n", aggregate.PacketCompactions)
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

func resetState(st *state.StateStore, stdout io.Writer) error {
	if err := st.Reset(); err != nil {
		return err
	}

	fmt.Fprintln(stdout, "STATUS: RESET")
	fmt.Fprintf(stdout, "REPO: %s\n", st.ReadOr("repo-root", "unknown"))
	return nil
}
