package app

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/shinderuman/codex-config/glm-worker/internal/state"
)

// printStatusは--statusの人間向けレポートを出力する。
func printStatus(st *state.StateStore, stdout io.Writer) error {
	fmt.Fprintf(stdout, "REPO: %s\n", st.ReadOr("repo-root", "unknown"))
	fmt.Fprintf(stdout, "TASK_ID: %s\n", st.ReadOr("task.id", "none"))
	fmt.Fprintf(stdout, "TASK_STATUS: %s\n", st.TaskStatus())
	fmt.Fprintf(stdout, "WORKER_SESSION: %s\n", st.ReadOr("worker.id", "none"))
	fmt.Fprintf(stdout, "REVIEWER_SESSION: %s\n", st.ReadOr("reviewer.id", "none"))
	if st.Exists("pending-decision") {
		fmt.Fprintln(stdout, "PENDING_DECISION: yes")
	} else {
		fmt.Fprintln(stdout, "PENDING_DECISION: no")
	}

	checkpoint, err := st.LoadResumeCheckpoint()
	if err == nil && checkpoint.RateLimited {
		fmt.Fprintln(stdout, "RATE_LIMITED: yes")
		fmt.Fprintf(stdout, "RATE_LIMIT_PHASE: %s\n", checkpoint.Phase)
		fmt.Fprintf(stdout, "RESET_AT_CST: %s\n", checkpoint.ResetAtCST)
		fmt.Fprintln(stdout, "RESET_TIMEZONE: CST (China Standard Time, UTC+8)")
		fmt.Fprintln(stdout, "RESUME_AVAILABLE: yes")
	} else {
		fmt.Fprintln(stdout, "RATE_LIMITED: no")
		fmt.Fprintln(stdout, "RESUME_AVAILABLE: no")
	}
	return nil
}

// printStatsは--statsの集計レポートを出力する。
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
	}

	fmt.Fprintf(stdout, "TASKS: %d\n", len(all))
	fmt.Fprintf(stdout, "MODEL_CALLS: %d\n", aggregate.ModelCalls)
	fmt.Fprintf(stdout, "MODEL_CALLS_BY_ALIAS: %s\n", formatIntMap(aggregate.ModelCallsByAlias))
	fmt.Fprintf(stdout, "MODEL_DURATION_MS_BY_ALIAS: %s\n", formatInt64Map(aggregate.ModelDurationMSByAlias))
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
	fmt.Fprintf(stdout, "PACKET_COMPACTIONS: %d\n", aggregate.PacketCompactions)
	fmt.Fprintf(stdout, "SOL_PACKET_BYTES: %d\n", aggregate.SolPacketBytes)
	fmt.Fprintf(stdout, "CURRENT_TASK_ID: %s\n", st.ReadOr("task.id", "none"))
	fmt.Fprintf(stdout, "CURRENT_TASK_STATUS: %s\n", st.TaskStatus())
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

// resetStateは状態をクリアし完了レポートを出力する。
func resetState(st *state.StateStore, stdout io.Writer) error {
	if err := st.Reset(); err != nil {
		return err
	}

	fmt.Fprintln(stdout, "STATUS: RESET")
	fmt.Fprintf(stdout, "REPO: %s\n", st.ReadOr("repo-root", "unknown"))
	return nil
}
