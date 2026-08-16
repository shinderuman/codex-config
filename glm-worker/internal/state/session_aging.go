package state

import (
	"slices"
	"time"
)

// SessionAgingは1 session(同一worker/reviewer sessionの連続呼出)の経時観測要約。
// 既存telemetryのTask Work Call記録だけから導出し、追加のmodel call・推測補完は行わない。
// CallLatencyMSは呼出順のwall timeで、位置がsession内call indexに対応する。
type SessionAging struct {
	SessionID              string
	Role                   SessionRole
	Models                 []string
	Calls                  int
	ResumedCalls           int
	CumulativeTurns        int
	CumulativeInputTokens  int64
	CumulativeOutputTokens int64
	CallLatencyMS          []int64
	FirstCallAt            time.Time
	LastCallAt             time.Time
}

// AgingFromModelCallLogsは呼出記録をsession単位へ集計する。session順は最初の呼出
// 記録順、呼出順はtelemetry記録順で安定させる。role/modelは実際の記録値だけを残す。
func AgingFromModelCallLogs(logs []ModelCallLog) []SessionAging {
	order := make([]string, 0)
	bySession := make(map[string]*SessionAging)
	for _, log := range logs {
		if log.CallType != CallTypeTask {
			continue
		}
		aging, ok := bySession[log.SessionID]
		if !ok {
			aging = &SessionAging{SessionID: log.SessionID, Role: log.Role, FirstCallAt: log.StartedAt}
			bySession[log.SessionID] = aging
			order = append(order, log.SessionID)
		}
		aging.Calls++
		if log.Resumed {
			aging.ResumedCalls++
		}
		aging.CumulativeTurns += log.TopLevelTurns
		aging.CumulativeInputTokens += log.TreeUsage.InputTokens +
			log.TreeUsage.CacheCreationInputTokens +
			log.TreeUsage.CacheReadInputTokens
		aging.CumulativeOutputTokens += log.TreeUsage.OutputTokens
		aging.CallLatencyMS = append(aging.CallLatencyMS, log.WallDurationMS)
		if log.StartedAt.After(aging.LastCallAt) {
			aging.LastCallAt = log.StartedAt
		}
		if !slices.Contains(aging.Models, log.ModelAlias) {
			aging.Models = append(aging.Models, log.ModelAlias)
		}
	}
	result := make([]SessionAging, 0, len(order))
	for _, sessionID := range order {
		result = append(result, *bySession[sessionID])
	}
	return result
}
