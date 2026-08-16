package abeval

import (
	"strings"
	"testing"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

func fakeTaskStats(taskID string) state.TaskStats {
	return state.TaskStats{
		Version:    3,
		TaskID:     taskID,
		Status:     state.TaskStatusComplete,
		ModelCalls: 5,
		InputTokensByAlias: map[string]int64{
			"opus":   600000,
			"sonnet": 250000,
		},
		CacheReadInputTokensByAlias: map[string]int64{
			"opus": 2100000,
		},
		OutputTokensByAlias: map[string]int64{
			"opus":   100000,
			"sonnet": 20000,
		},
		SolPacketBytes:   812,
		DecisionCommands: 1,
		FixCommands:      0,
		AutoFixRounds:    1,
	}
}

func TestResolveFromTaskStatsFillsGLMUsageAndProxy(t *testing.T) {
	spec := validSpec()
	record := validOrchestratedRecord(spec)
	record.GLMUsage = GLMUsage{Source: GLMUsageSourceTaskStats, TaskID: "task-1234"}

	resolved, err := ResolveFromTaskStats(record, []state.TaskStats{fakeTaskStats("other-task"), fakeTaskStats("task-1234")})
	if err != nil {
		t.Fatal(err)
	}
	usage := resolved.GLMUsage
	if usage.InputTokens != 850000 || usage.OutputTokens != 120000 || usage.CacheReadInputTokens != 2100000 {
		t.Fatalf("token導出 = %+v", usage)
	}
	if usage.ModelCalls != 5 {
		t.Fatalf("model_calls = %d want 5(Task Work Callのみ)", usage.ModelCalls)
	}
	if resolved.Proxy.SolPacketBytes != 812 || resolved.Proxy.SolDecisionCommands != 1 || resolved.Proxy.AutoFixRounds != 1 {
		t.Fatalf("proxy導出 = %+v", resolved.Proxy)
	}
}

func TestResolveFromTaskStatsFailsWhenTaskMissing(t *testing.T) {
	spec := validSpec()
	record := validOrchestratedRecord(spec)
	record.GLMUsage = GLMUsage{Source: GLMUsageSourceTaskStats, TaskID: "missing-task"}

	if _, err := ResolveFromTaskStats(record, []state.TaskStats{fakeTaskStats("other-task")}); err == nil {
		t.Fatal("stats不在taskが黙って零値化されました")
	} else if !strings.Contains(err.Error(), "missing-task") {
		t.Fatalf("err = %q", err.Error())
	}
}

func TestResolveFromTaskStatsRequiresTaskID(t *testing.T) {
	spec := validSpec()
	record := validOrchestratedRecord(spec)
	record.GLMUsage = GLMUsage{Source: GLMUsageSourceTaskStats}

	if _, err := ResolveFromTaskStats(record, nil); err == nil || !strings.Contains(err.Error(), "task_idが空") {
		t.Fatalf("空task_idが黙って解決されました: %v", err)
	}
}

func TestGLMUsageFromTaskStatsSumsAliasesPerKind(t *testing.T) {
	usage, _ := GLMUsageFromTaskStats(fakeTaskStats("task-1234"))
	if usage.InputTokens != 850000 {
		t.Fatalf("input = %d want alias総和850000", usage.InputTokens)
	}
	if usage.CacheCreationInputTokens != 0 {
		t.Fatalf("cache creation = %d want 0", usage.CacheCreationInputTokens)
	}
	if usage.Source != GLMUsageSourceTaskStats || usage.TaskID != "task-1234" {
		t.Fatalf("source/task = %s/%s", usage.Source, usage.TaskID)
	}
}
