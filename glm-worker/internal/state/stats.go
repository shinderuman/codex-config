package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/packet"
)

const (
	currentStatsFile = "task-stats.json"
	// taskStatsVersionはTaskStats JSONのschema version。既存fieldの意味/JSON名を変更するときだけ
	// bumpし(旧archiveはAllTaskStats/decodeTaskStatsが読み飛ばす)、新集計fieldのomitempty追加は
	// 後方互換(旧archiveは該当map欠落=0寄与)のためbump不要。
	// v3はmodel_calls/実行時間/token集計をTask Work Call専用(probeを含まない)へ変更したためbumpし、
	// probeをmodel_callsへ混ぜていたv2以前のarchiveを集計へ混在させない。
	taskStatsVersion = 3
)

var errUnsupportedTaskStatsVersion = errors.New("unsupported task stats version")

// statsWarnOutはtask statsのbest-effort警告の出力先。
// task-stats.jsonは観測用mirrorであり、task.statusが正規状態のため、
// mirrorの失敗はworkflowを止めずこのwriterへwarningを出す。
var statsWarnOut io.Writer = os.Stderr

// TaskStatsは観測用のタスク統計mirror。
type TaskStats struct {
	Version    int        `json:"version"`
	TaskID     string     `json:"task_id"`
	StartedAt  time.Time  `json:"started_at"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	Status     TaskStatus `json:"status"`
	// ModelCalls/ModelCallsByAlias/各token mapはTask Work Call(worker/reviewerの本task呼出、
	// transient再試行を含む)だけを数える。probe呼出はProbeOutcomeへ、tokenはJSONL telemetryへ
	// 記録され、これらの集計へは混ぜない。
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
	ProviderUnavailable                     int              `json:"provider_unavailable"`
	ProviderUnavailableByAlias              map[string]int   `json:"provider_unavailable_by_alias,omitempty"`
	// 診断集計(v2のままomitempty追加)。旧archiveは該当map欠落=未観測(0寄与)で、
	// --stats集計でcaptured record/archiveだけが計上される。比率(分母)は算出しない。
	RiskFloorByCategory    map[string]int `json:"risk_floor_by_category,omitempty"`
	SnapshotMismatches     int            `json:"snapshot_mismatches,omitempty"`
	SnapshotMismatchByAxis map[string]int `json:"snapshot_mismatch_by_axis,omitempty"`
	PacketRejectByCategory map[string]int `json:"packet_reject_by_category,omitempty"`
	ProbeOutcome           map[string]int `json:"probe_outcome,omitempty"`
	// TransientRetriesはtransient障害後にprobe成功を経て再実行した本task呼出回数。
	// ModelCallsは初回呼出とこの再試行の両方を含む(重複なく数えるため)。
	TransientRetries int `json:"transient_retries,omitempty"`
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

// CurrentTaskStatsは現在taskの観測用mirrorを読み込む。表示専用参照向けで、
// file不在・corruptionはerrorとして呼出元へ返す。
func (s *StateStore) CurrentTaskStats() (TaskStats, error) {
	return s.loadTaskStats()
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

// RecordModelCallはTask Work Call(本taskのworker/reviewer呼出)をrole/alias別に数える。
// probe呼出はここへ入れない。
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

// RecordTransientRetryはtransient障害からの本task再実行1回を数える。
func (s *StateStore) RecordTransientRetry() {
	s.UpdateTaskStats(func(stats *TaskStats) {
		stats.TransientRetries++
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

func (s *StateStore) RecordProviderUnavailable(model string) {
	s.UpdateTaskStats(func(stats *TaskStats) {
		stats.ProviderUnavailable++
		if stats.ProviderUnavailableByAlias == nil {
			stats.ProviderUnavailableByAlias = make(map[string]int)
		}
		stats.ProviderUnavailableByAlias[model]++
	})
}

func (s *StateStore) RecordPacketCompaction() {
	s.UpdateTaskStats(func(stats *TaskStats) {
		stats.PacketCompactions++
	})
}

// RecordRiskFloorはrisk floorがactive(HIGH)だったreview roundをcategory別に集計する。
// categoryはrisk_floor_sourceから正規化した安定bucketで、self-protectionのpath詳細は含まない。
func (s *StateStore) RecordRiskFloor(category string) {
	if category == "" {
		return
	}
	s.UpdateTaskStats(func(stats *TaskStats) {
		addInt(&stats.RiskFloorByCategory, category, 1)
	})
}

// RecordSnapshotMismatchはdigest不一致eventsを総数と軸別に集計する。axisは"head,index,worktree"の
// 複数可で、各軸を個別に加算する。取得失敗(比較未実施)は呼び出し側で除外し、真の不一致だけ計上する。
func (s *StateStore) RecordSnapshotMismatch(axis string) {
	if axis == "" {
		return
	}
	s.UpdateTaskStats(func(stats *TaskStats) {
		stats.SnapshotMismatches++
		for _, a := range strings.Split(axis, ",") {
			addInt(&stats.SnapshotMismatchByAxis, a, 1)
		}
	})
}

// RecordPacketRejectはpacket検証不合格をcoarse category別に集計する。
func (s *StateStore) RecordPacketReject(category string) {
	if category == "" {
		return
	}
	s.UpdateTaskStats(func(stats *TaskStats) {
		addInt(&stats.PacketRejectByCategory, category, 1)
	})
}

// RecordProbeOutcomeはprobe結果(probe_success/probe_failure)を集計する。
func (s *StateStore) RecordProbeOutcome(outcome string) {
	if outcome == "" {
		return
	}
	s.UpdateTaskStats(func(stats *TaskStats) {
		addInt(&stats.ProbeOutcome, outcome, 1)
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
