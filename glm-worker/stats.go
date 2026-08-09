package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const currentStatsFile = "task-stats.json"

// statsWarnOutはtask statsのbest-effort警告の出力先。
// task-stats.jsonは観測用mirrorであり、task.statusが正規状態のため、
// mirrorの失敗はworkflowを止めずこのwriterへwarningを出す。
var statsWarnOut io.Writer = os.Stderr

type taskStats struct {
	Version                 int        `json:"version"`
	TaskID                  string     `json:"task_id"`
	StartedAt               time.Time  `json:"started_at"`
	ArchivedAt              *time.Time `json:"archived_at,omitempty"`
	Status                  taskStatus `json:"status"`
	ModelCalls              int        `json:"model_calls"`
	WorkerCalls             int        `json:"worker_calls"`
	ReviewerCalls           int        `json:"reviewer_calls"`
	DecisionCommands        int        `json:"decision_commands"`
	FixCommands             int        `json:"fix_commands"`
	ResumeCommands          int        `json:"resume_commands"`
	AutoFixRounds           int        `json:"auto_fix_rounds"`
	NeedsSolDecisionPackets int        `json:"needs_sol_decision_packets"`
	NeedsSolReviewPackets   int        `json:"needs_sol_review_packets"`
	PassPackets             int        `json:"pass_packets"`
	RateLimits              int        `json:"rate_limits"`
	PacketCompactions       int        `json:"packet_compactions"`
	SolPacketBytes          int        `json:"sol_packet_bytes"`
}

func warnStatsFailure(operation string, err error) {
	fmt.Fprintf(statsWarnOut, "WARNING: task statsの%sに失敗しました（観測用mirrorのため続行します）: %v\n", operation, err)
}

// InitializeTaskStatsは新規taskの観測用mirrorを初期化する。
// 失敗してもtask.statusなど正規状態へ影響しないためwarningだけ出す。
func (s *stateStore) InitializeTaskStats(taskID string) {
	stats := taskStats{
		Version:   1,
		TaskID:    taskID,
		StartedAt: time.Now().UTC(),
		Status:    taskStatusActive,
	}
	if err := s.writeTaskStats(stats); err != nil {
		warnStatsFailure("初期化", err)
	}
}

// UpdateTaskStatsは観測用mirrorを読み込んで更新する。
// task.idが無い場合は何もしない。corruptionやI/O失敗は正規状態へ影響させないため
// warningを出し、読み込み不能ならtask.idからmirrorを再構築する。
func (s *stateStore) UpdateTaskStats(update func(*taskStats)) {
	stats, err := s.loadTaskStats()
	if err != nil {
		stats, err = s.recoverTaskStats(err)
		if err != nil {
			return
		}
	}
	update(&stats)
	if err := s.writeTaskStats(stats); err != nil {
		warnStatsFailure("更新", err)
	}
}

// recoverTaskStatsはloadTaskStatsの失敗からmirrorを復旧する。
// ファイル不在でtask.idも無い場合は記録対象がないので何もしない。
// corruption・I/O失敗の場合はtask.idから再構築し、内容は失われるがmirrorは継続利用できる。
func (s *stateStore) recoverTaskStats(loadErr error) (taskStats, error) {
	if errors.Is(loadErr, os.ErrNotExist) {
		return s.bootstrapTaskStats()
	}
	warnStatsFailure("読み込み", loadErr)
	return s.bootstrapTaskStats()
}

func (s *stateStore) bootstrapTaskStats() (taskStats, error) {
	taskID, taskErr := s.Read("task.id")
	if taskErr != nil || taskID == "" {
		return taskStats{}, fmt.Errorf("task.idがありません")
	}
	return taskStats{
		Version:   1,
		TaskID:    taskID,
		StartedAt: time.Now().UTC(),
		Status:    s.TaskStatus(),
	}, nil
}

// ArchiveCurrentStatsは現在taskのmirrorをstats履歴へ移動する。
// 読み込み不能なmirrorを履歴へ持ち込まないよう、corruption時は移動をskipする。
// すべての失敗はbest-effortでwarningだけ出し、新規task開始や--resetを止めない。
func (s *stateStore) ArchiveCurrentStats() {
	stats, err := s.loadTaskStats()
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		warnStatsFailure("archive読み込み", err)
		return
	}

	now := time.Now().UTC()
	stats.ArchivedAt = &now
	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		warnStatsFailure("archive JSON化", err)
		return
	}
	historyPath := filepath.Join(s.dir, "stats", stats.TaskID+".json")
	if err := writeFileAtomic(historyPath, append(data, '\n'), 0o600); err != nil {
		warnStatsFailure("archive書き込み", err)
		return
	}
	if err := s.Remove(currentStatsFile); err != nil {
		warnStatsFailure("archive後削除", err)
	}
}

func (s *stateStore) loadTaskStats() (taskStats, error) {
	data, err := os.ReadFile(s.Path(currentStatsFile))
	if err != nil {
		return taskStats{}, err
	}
	var stats taskStats
	if err := json.Unmarshal(data, &stats); err != nil {
		return taskStats{}, fmt.Errorf("task statsを読めません: %w", err)
	}
	return stats, nil
}

func (s *stateStore) writeTaskStats(stats taskStats) error {
	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return fmt.Errorf("task statsをJSON化できません: %w", err)
	}
	return writeFileAtomic(s.Path(currentStatsFile), append(data, '\n'), 0o600)
}

func (s *stateStore) RecordModelCall(role sessionRole) {
	s.UpdateTaskStats(func(stats *taskStats) {
		stats.ModelCalls++
		if role == reviewerRole {
			stats.ReviewerCalls++
		} else {
			stats.WorkerCalls++
		}
	})
}

func (s *stateStore) RecordCommand(mode commandMode) {
	s.UpdateTaskStats(func(stats *taskStats) {
		switch mode {
		case modeDecision:
			stats.DecisionCommands++
		case modeFix:
			stats.FixCommands++
		case modeResume:
			stats.ResumeCommands++
		}
	})
}

func (s *stateStore) RecordAutoFix() {
	s.UpdateTaskStats(func(stats *taskStats) {
		stats.AutoFixRounds++
	})
}

func (s *stateStore) RecordRateLimit() {
	s.UpdateTaskStats(func(stats *taskStats) {
		stats.RateLimits++
	})
}

func (s *stateStore) RecordPacketCompaction() {
	s.UpdateTaskStats(func(stats *taskStats) {
		stats.PacketCompactions++
	})
}

func (s *stateStore) RecordSolPacket(value packet) {
	s.UpdateTaskStats(func(stats *taskStats) {
		stats.SolPacketBytes += value.ByteSize()
		switch value.Status() {
		case "NEEDS_SOL_DECISION":
			stats.NeedsSolDecisionPackets++
		case "NEEDS_SOL_REVIEW":
			stats.NeedsSolReviewPackets++
		case "PASS":
			stats.PassPackets++
		}
	})
}

func printStats(state *stateStore) error {
	all, err := state.allTaskStats()
	if err != nil {
		return err
	}
	aggregate := taskStats{}
	for _, stats := range all {
		aggregate.ModelCalls += stats.ModelCalls
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

	fmt.Printf("TASKS: %d\n", len(all))
	fmt.Printf("MODEL_CALLS: %d\n", aggregate.ModelCalls)
	fmt.Printf("WORKER_CALLS: %d\n", aggregate.WorkerCalls)
	fmt.Printf("REVIEWER_CALLS: %d\n", aggregate.ReviewerCalls)
	fmt.Printf("DECISION_COMMANDS: %d\n", aggregate.DecisionCommands)
	fmt.Printf("FIX_COMMANDS: %d\n", aggregate.FixCommands)
	fmt.Printf("RESUME_COMMANDS: %d\n", aggregate.ResumeCommands)
	fmt.Printf("AUTO_FIX_ROUNDS: %d\n", aggregate.AutoFixRounds)
	fmt.Printf("NEEDS_SOL_DECISION_PACKETS: %d\n", aggregate.NeedsSolDecisionPackets)
	fmt.Printf("NEEDS_SOL_REVIEW_PACKETS: %d\n", aggregate.NeedsSolReviewPackets)
	fmt.Printf("PASS_PACKETS: %d\n", aggregate.PassPackets)
	fmt.Printf("RATE_LIMITS: %d\n", aggregate.RateLimits)
	fmt.Printf("PACKET_COMPACTIONS: %d\n", aggregate.PacketCompactions)
	fmt.Printf("SOL_PACKET_BYTES: %d\n", aggregate.SolPacketBytes)
	fmt.Printf("CURRENT_TASK_ID: %s\n", state.ReadOr("task.id", "none"))
	fmt.Printf("CURRENT_TASK_STATUS: %s\n", state.TaskStatus())
	return nil
}

// allTaskStatsは--stats専用の集計を行う。
// 明示操作のため、読み込み失敗はエラーとして呼び出し元へ返す。
func (s *stateStore) allTaskStats() ([]taskStats, error) {
	result := make([]taskStats, 0)
	historyPaths, err := filepath.Glob(filepath.Join(s.dir, "stats", "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(historyPaths)
	for _, path := range historyPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var stats taskStats
		if err := json.Unmarshal(data, &stats); err != nil {
			return nil, fmt.Errorf("task stats historyを読めません: %w", err)
		}
		result = append(result, stats)
	}
	current, err := s.loadTaskStats()
	if err == nil {
		result = append(result, current)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return result, nil
}
