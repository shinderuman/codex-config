package abeval

import (
	"fmt"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// GLMUsageFromTaskStatsは既存task stats 1件からGLM usageとproxy指標を導出する。
// tokenとmodel_callsはTask Work Callだけを数える既存集計(alias別mapの総和)を使い、
// probe呼出を混ぜない。usage測定のための追加AI promptは発生しない。
func GLMUsageFromTaskStats(stats state.TaskStats) (GLMUsage, ProxyMetrics) {
	usage := GLMUsage{
		Source:                   GLMUsageSourceTaskStats,
		TaskID:                   stats.TaskID,
		InputTokens:              sumInt64Map(stats.InputTokensByAlias),
		CacheCreationInputTokens: sumInt64Map(stats.CacheCreationInputTokensByAlias),
		CacheReadInputTokens:     sumInt64Map(stats.CacheReadInputTokensByAlias),
		OutputTokens:             sumInt64Map(stats.OutputTokensByAlias),
		ModelCalls:               stats.ModelCalls,
	}
	proxy := ProxyMetrics{
		SolPacketBytes:      stats.SolPacketBytes,
		SolDecisionCommands: stats.DecisionCommands,
		SolFixCommands:      stats.FixCommands,
		AutoFixRounds:       stats.AutoFixRounds,
	}
	return usage, proxy
}

// ResolveFromTaskStatsはglm_usage.sourceがglm-worker-task-statsのrecordのGLM usageと
// proxy指標を既存stats履歴から解決する。task_idが空・該当taskが見つからないときは
// 零値へ黙らずerrorを返す(取得漏れを零値・unknownと混同させない)。
func ResolveFromTaskStats(record RunRecord, all []state.TaskStats) (RunRecord, error) {
	if record.GLMUsage.TaskID == "" {
		return record, fmt.Errorf("glm_usageを解決できません: task_idが空です")
	}
	for _, stats := range all {
		if stats.TaskID != record.GLMUsage.TaskID {
			continue
		}
		usage, proxy := GLMUsageFromTaskStats(stats)
		record.GLMUsage = usage
		record.Proxy = proxy
		return record, nil
	}
	return record, fmt.Errorf("glm_usageを解決できません: task %sのstatsが見つかりません", record.GLMUsage.TaskID)
}

func sumInt64Map(values map[string]int64) int64 {
	var total int64
	for _, value := range values {
		total += value
	}
	return total
}
