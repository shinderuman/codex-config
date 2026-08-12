package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/packet"
)

const (
	currentStatsFile = "task-stats.json"
	taskStatsVersion = 2
)

var errUnsupportedTaskStatsVersion = errors.New("unsupported task stats version")

// statsWarnOutはtask statsのbest-effort警告の出力先。
// task-stats.jsonは観測用mirrorであり、task.statusが正規状態のため、
// mirrorの失敗はworkflowを止めずこのwriterへwarningを出す。
var statsWarnOut io.Writer = os.Stderr

// TaskStatsは観測用のタスク統計mirror。
type TaskStats struct {
	Version                                 int              `json:"version"`
	TaskID                                  string           `json:"task_id"`
	StartedAt                               time.Time        `json:"started_at"`
	ArchivedAt                              *time.Time       `json:"archived_at,omitempty"`
	Status                                  TaskStatus       `json:"status"`
	ModelCalls                              int              `json:"model_calls"`
	ModelCallsByAlias                       map[string]int   `json:"model_calls_by_alias,omitempty"`
	ModelDurationMSByAlias                  map[string]int64 `json:"model_duration_ms_by_alias,omitempty"`
	RateLimitsByAlias                       map[string]int   `json:"rate_limits_by_alias,omitempty"`
	InputTokensByAlias                      map[string]int64 `json:"input_tokens_by_alias,omitempty"`
	CacheCreationInputTokensByAlias         map[string]int64 `json:"cache_creation_input_tokens_by_alias,omitempty"`
	CacheReadInputTokensByAlias             map[string]int64 `json:"cache_read_input_tokens_by_alias,omitempty"`
	OutputTokensByAlias                     map[string]int64 `json:"output_tokens_by_alias,omitempty"`
	TopLevelTurnsByAlias                    map[string]int   `json:"top_level_turns_by_alias,omitempty"`
	CallTreesByResolvedModel                map[string]int   `json:"call_trees_by_resolved_model,omitempty"`
	InputTokensByResolvedModel              map[string]int64 `json:"input_tokens_by_resolved_model,omitempty"`
	CacheCreationInputTokensByResolvedModel map[string]int64 `json:"cache_creation_input_tokens_by_resolved_model,omitempty"`
	CacheReadInputTokensByResolvedModel     map[string]int64 `json:"cache_read_input_tokens_by_resolved_model,omitempty"`
	OutputTokensByResolvedModel             map[string]int64 `json:"output_tokens_by_resolved_model,omitempty"`
	WorkerCalls                             int              `json:"worker_calls"`
	ReviewerCalls                           int              `json:"reviewer_calls"`
	DecisionCommands                        int              `json:"decision_commands"`
	FixCommands                             int              `json:"fix_commands"`
	ResumeCommands                          int              `json:"resume_commands"`
	AutoFixRounds                           int              `json:"auto_fix_rounds"`
	NeedsSolDecisionPackets                 int              `json:"needs_sol_decision_packets"`
	NeedsSolReviewPackets                   int              `json:"needs_sol_review_packets"`
	PassPackets                             int              `json:"pass_packets"`
	RateLimits                              int              `json:"rate_limits"`
	PacketCompactions                       int              `json:"packet_compactions"`
	SolPacketBytes                          int              `json:"sol_packet_bytes"`
}

func warnStatsFailure(operation string, err error) {
	fmt.Fprintf(statsWarnOut, "WARNING: task statsの%sに失敗しました（観測用mirrorのため続行します）: %v\n", operation, err)
}

// InitializeTaskStatsは新規taskの観測用mirrorを初期化する。
// 失敗してもtask.statusなど正規状態へ影響しないためwarningだけ出す。
func (s *StateStore) InitializeTaskStats(taskID string) {
	stats := TaskStats{
		Version:   taskStatsVersion,
		TaskID:    taskID,
		StartedAt: time.Now().UTC(),
		Status:    TaskStatusActive,
	}
	if err := s.writeTaskStats(stats); err != nil {
		warnStatsFailure("初期化", err)
	}
}

// UpdateTaskStatsは観測用mirrorを読み込んで更新する。
// task.idが無い場合は何もしない。corruptionやI/O失敗は正規状態へ影響させないため
// warningを出し、読み込み不能ならtask.idからmirrorを再構築する。
func (s *StateStore) UpdateTaskStats(update func(*TaskStats)) {
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
func (s *StateStore) recoverTaskStats(loadErr error) (TaskStats, error) {
	if errors.Is(loadErr, os.ErrNotExist) {
		return s.bootstrapTaskStats()
	}
	warnStatsFailure("読み込み", loadErr)
	return s.bootstrapTaskStats()
}

func (s *StateStore) bootstrapTaskStats() (TaskStats, error) {
	taskID, taskErr := s.Read("task.id")
	if taskErr != nil || taskID == "" {
		return TaskStats{}, fmt.Errorf("task.idがありません")
	}
	return TaskStats{
		Version:   taskStatsVersion,
		TaskID:    taskID,
		StartedAt: time.Now().UTC(),
		Status:    s.TaskStatus(),
	}, nil
}

// ArchiveCurrentStatsは現在taskのmirrorをstats履歴へ移動する。
// 読み込み不能なmirrorを履歴へ持ち込まないよう、corruption時は移動をskipする。
// すべての失敗はbest-effortでwarningだけ出し、新規task開始やresetを止めない。
func (s *StateStore) ArchiveCurrentStats() {
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

func (s *StateStore) loadTaskStats() (TaskStats, error) {
	data, err := os.ReadFile(s.Path(currentStatsFile))
	if err != nil {
		return TaskStats{}, err
	}
	return decodeTaskStats(data)
}

func decodeTaskStats(data []byte) (TaskStats, error) {
	var stats TaskStats
	if err := json.Unmarshal(data, &stats); err != nil {
		return TaskStats{}, fmt.Errorf("task statsを読めません: %w", err)
	}
	if stats.Version != taskStatsVersion {
		return TaskStats{}, fmt.Errorf("%w: %d", errUnsupportedTaskStatsVersion, stats.Version)
	}
	return stats, nil
}

func (s *StateStore) writeTaskStats(stats TaskStats) error {
	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return fmt.Errorf("task statsをJSON化できません: %w", err)
	}
	return writeFileAtomic(s.Path(currentStatsFile), append(data, '\n'), 0o600)
}

// AllTaskStatsは--stats専用に履歴と現在のmirrorを全件読み込む。
// 明示操作のため、読み込み失敗はエラーとして呼び出し元へ返す。
func (s *StateStore) AllTaskStats() ([]TaskStats, error) {
	result := make([]TaskStats, 0)
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
		stats, err := decodeTaskStats(data)
		if errors.Is(err, errUnsupportedTaskStatsVersion) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("task stats historyを読めません: %w", err)
		}
		result = append(result, stats)
	}
	current, err := s.loadTaskStats()
	if err == nil {
		result = append(result, current)
	} else if errors.Is(err, errUnsupportedTaskStatsVersion) {
		return result, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return result, nil
}

func (s *StateStore) RecordModelCall(role SessionRole, model string) {
	s.UpdateTaskStats(func(stats *TaskStats) {
		stats.ModelCalls++
		if stats.ModelCallsByAlias == nil {
			stats.ModelCallsByAlias = make(map[string]int)
		}
		stats.ModelCallsByAlias[model]++
		if role == ReviewerRole {
			stats.ReviewerCalls++
		} else {
			stats.WorkerCalls++
		}
	})
}

func (s *StateStore) RecordModelDuration(model string, duration time.Duration) {
	s.UpdateTaskStats(func(stats *TaskStats) {
		if stats.ModelDurationMSByAlias == nil {
			stats.ModelDurationMSByAlias = make(map[string]int64)
		}
		stats.ModelDurationMSByAlias[model] += duration.Milliseconds()
	})
}

func (s *StateStore) RecordDecision() {
	s.UpdateTaskStats(func(stats *TaskStats) {
		stats.DecisionCommands++
	})
}

func (s *StateStore) RecordFix() {
	s.UpdateTaskStats(func(stats *TaskStats) {
		stats.FixCommands++
	})
}

func (s *StateStore) RecordResume() {
	s.UpdateTaskStats(func(stats *TaskStats) {
		stats.ResumeCommands++
	})
}

func (s *StateStore) RecordAutoFix() {
	s.UpdateTaskStats(func(stats *TaskStats) {
		stats.AutoFixRounds++
	})
}

func (s *StateStore) RecordRateLimit(model string) {
	s.UpdateTaskStats(func(stats *TaskStats) {
		stats.RateLimits++
		if stats.RateLimitsByAlias == nil {
			stats.RateLimitsByAlias = make(map[string]int)
		}
		stats.RateLimitsByAlias[model]++
	})
}

func (s *StateStore) RecordPacketCompaction() {
	s.UpdateTaskStats(func(stats *TaskStats) {
		stats.PacketCompactions++
	})
}

func (s *StateStore) RecordSolPacket(value packet.Packet) {
	s.UpdateTaskStats(func(stats *TaskStats) {
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

// Resetは現在タスクの状態・session・baseline・resume checkpoint・snapshotをクリアし、
// 現在mirrorをstats履歴へアーカイブする。出力は呼び出し側(app)の責務。
func (s *StateStore) Reset() error {
	s.ArchiveCurrentStats()
	return s.Remove(taskStateFileNames()...)
}
