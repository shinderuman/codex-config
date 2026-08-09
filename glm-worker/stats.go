package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const currentStatsFile = "task-stats.json"

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

func (s *stateStore) InitializeTaskStats(taskID string) error {
	stats := taskStats{
		Version:   1,
		TaskID:    taskID,
		StartedAt: time.Now().UTC(),
		Status:    taskStatusActive,
	}
	return s.writeTaskStats(stats)
}

func (s *stateStore) UpdateTaskStats(update func(*taskStats)) error {
	stats, err := s.loadTaskStats()
	if errors.Is(err, os.ErrNotExist) {
		taskID, taskErr := s.Read("task.id")
		if taskErr != nil || taskID == "" {
			return nil
		}
		stats = taskStats{
			Version:   1,
			TaskID:    taskID,
			StartedAt: time.Now().UTC(),
			Status:    s.TaskStatus(),
		}
		err = nil
	}
	if err != nil {
		return err
	}
	update(&stats)
	return s.writeTaskStats(stats)
}

func (s *stateStore) ArchiveCurrentStats() error {
	stats, err := s.loadTaskStats()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	stats.ArchivedAt = &now
	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return fmt.Errorf("task statsをJSON化できません: %w", err)
	}
	historyPath := filepath.Join(s.dir, "stats", stats.TaskID+".json")
	if err := writeFileAtomic(historyPath, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("task statsをarchiveできません: %w", err)
	}
	return s.Remove(currentStatsFile)
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

func (s *stateStore) RecordModelCall(role sessionRole) error {
	return s.UpdateTaskStats(func(stats *taskStats) {
		stats.ModelCalls++
		if role == reviewerRole {
			stats.ReviewerCalls++
		} else {
			stats.WorkerCalls++
		}
	})
}

func (s *stateStore) RecordCommand(mode commandMode) error {
	return s.UpdateTaskStats(func(stats *taskStats) {
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

func (s *stateStore) RecordAutoFix() error {
	return s.UpdateTaskStats(func(stats *taskStats) {
		stats.AutoFixRounds++
	})
}

func (s *stateStore) RecordRateLimit() error {
	return s.UpdateTaskStats(func(stats *taskStats) {
		stats.RateLimits++
	})
}

func (s *stateStore) RecordPacketCompaction() error {
	return s.UpdateTaskStats(func(stats *taskStats) {
		stats.PacketCompactions++
	})
}

func (s *stateStore) RecordSolPacket(value packet) error {
	return s.UpdateTaskStats(func(stats *taskStats) {
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
