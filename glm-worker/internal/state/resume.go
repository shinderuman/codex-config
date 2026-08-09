package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

const resumeStateFile = "resume-state.json"

// ResumeStageは中断再開の復元段階を表す。
type ResumeStage string

const (
	ResumeStageWorker  ResumeStage = "worker"
	ResumeStageReview  ResumeStage = "reviewer"
	ResumeStageAutoFix ResumeStage = "auto-fix"
)

// ResumeCheckpointはZ.ai 5h上限中断時に次回再開へ引き継ぐ状態。
type ResumeCheckpoint struct {
	Version         int         `json:"version"`
	Stage           ResumeStage `json:"stage"`
	Phase           string      `json:"phase"`
	Role            SessionRole `json:"role"`
	Model           string      `json:"model,omitempty"`
	ReadOnly        bool        `json:"read_only"`
	Effort          string      `json:"effort,omitempty"`
	Prompt          string      `json:"prompt"`
	OriginalPrompt  string      `json:"original_prompt,omitempty"`
	Request         string      `json:"request"`
	Decision        string      `json:"decision,omitempty"`
	WorkerPacket    []string    `json:"worker_packet,omitempty"`
	ReviewNumber    int         `json:"review_number,omitempty"`
	AutoFixes       int         `json:"auto_fixes,omitempty"`
	RateLimited     bool        `json:"rate_limited"`
	ResetAtCST      string      `json:"reset_at_cst,omitempty"`
	ResetAtRFC3339  string      `json:"reset_at_rfc3339,omitempty"`
	PacketCompacted bool        `json:"packet_compacted,omitempty"`
}

// SaveResumeCheckpointは中断再開用状態を原子的に書き込む。
func (s *StateStore) SaveResumeCheckpoint(checkpoint ResumeCheckpoint) error {
	checkpoint.Version = 1
	data, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return fmt.Errorf("resume stateをJSON化できません: %w", err)
	}

	if err := writeFileAtomic(s.Path(resumeStateFile), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("resume stateを書き込めません: %w", err)
	}
	return nil
}

// LoadResumeCheckpointは中断再開用状態を読み込む。
func (s *StateStore) LoadResumeCheckpoint() (ResumeCheckpoint, error) {
	data, err := os.ReadFile(s.Path(resumeStateFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ResumeCheckpoint{}, fmt.Errorf("STATUS: WORKER_ERROR\nERROR: resumable task is not available")
		}
		return ResumeCheckpoint{}, err
	}

	var checkpoint ResumeCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return ResumeCheckpoint{}, fmt.Errorf("resume stateを読めません: %w", err)
	}
	if checkpoint.Version != 1 {
		return ResumeCheckpoint{}, fmt.Errorf("unsupported resume state version: %d", checkpoint.Version)
	}
	return checkpoint, nil
}

// ClearResumeCheckpointは中断再開用状態を削除する。
func (s *StateStore) ClearResumeCheckpoint() error {
	return s.Remove(resumeStateFile)
}
