package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const resumeStateFile = "resume-state.json"

type resumeStage string

const (
	resumeStageWorker  resumeStage = "worker"
	resumeStageReview  resumeStage = "reviewer"
	resumeStageAutoFix resumeStage = "auto-fix"
)

type resumeCheckpoint struct {
	Version         int         `json:"version"`
	Stage           resumeStage `json:"stage"`
	Phase           string      `json:"phase"`
	Role            sessionRole `json:"role"`
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

func (s *stateStore) SaveResumeCheckpoint(checkpoint resumeCheckpoint) error {
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

func (s *stateStore) LoadResumeCheckpoint() (resumeCheckpoint, error) {
	data, err := os.ReadFile(s.Path(resumeStateFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return resumeCheckpoint{}, fmt.Errorf("STATUS: WORKER_ERROR\nERROR: resumable task is not available")
		}
		return resumeCheckpoint{}, err
	}

	var checkpoint resumeCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return resumeCheckpoint{}, fmt.Errorf("resume stateを読めません: %w", err)
	}
	if checkpoint.Version != 1 {
		return resumeCheckpoint{}, fmt.Errorf("unsupported resume state version: %d", checkpoint.Version)
	}
	return checkpoint, nil
}

func (s *stateStore) ClearResumeCheckpoint() error {
	return s.Remove(resumeStateFile)
}

func packetFromLines(lines []string) packet {
	fields := make(map[string]string)
	for _, line := range lines {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if _, exists := fields[key]; exists {
			continue
		}
		fields[key] = value
	}

	return packet{
		Lines:  append([]string(nil), lines...),
		Fields: fields,
	}
}

func resumePrompt(checkpoint resumeCheckpoint) string {
	originalPrompt := checkpoint.OriginalPrompt
	if originalPrompt == "" {
		originalPrompt = checkpoint.Prompt
	}

	return fmt.Sprintf(`前回のClaude Code呼び出しはZ.ai GLM Coding Planの5時間利用上限で中断しました。

同じタスク・同じsessionの中断箇所から作業を再開してください。
現在のworking treeには前回の途中変更が残っている可能性があります。破棄せず、session文脈と照合して続行してください。
最初から調査・実装をやり直さず、未完了部分だけを進めて所定のPACKETまで完了してください。

前回の指示:
%s
`, originalPrompt)
}
