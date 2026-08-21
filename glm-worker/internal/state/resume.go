package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/packet"
)

const (
	resumeStateFile    = "resume-state.json"
	resumeStateVersion = 3
)

type ResumeStage string

const (
	ResumeStageWorker  ResumeStage = "worker"
	ResumeStageReview  ResumeStage = "reviewer"
	ResumeStageAutoFix ResumeStage = "auto-fix"
)

// ResumeCheckpointはZ.ai 5h上限中断時に次回再開へ引き継ぐ状態。
type ResumeCheckpoint struct {
	Version        int         `json:"version"`
	Stage          ResumeStage `json:"stage"`
	Phase          string      `json:"phase"`
	Role           SessionRole `json:"role"`
	Model          string      `json:"model"`
	ReadOnly       bool        `json:"read_only"`
	Effort         string      `json:"effort,omitempty"`
	Prompt         string      `json:"prompt"`
	OriginalPrompt string      `json:"original_prompt,omitempty"`
	Request        string      `json:"request"`
	Decision       string      `json:"decision,omitempty"`
	// WorkerResultはworker工程のtyped結果。v2までは表示行list(WorkerPacket)で保持し、
	// 読込時にpacket.FromDisplayLinesで等価なtyped結果へ変換する。
	WorkerResult   *packet.Result `json:"worker_result,omitempty"`
	ReviewNumber   int            `json:"review_number,omitempty"`
	AutoFixes      int            `json:"auto_fixes,omitempty"`
	RateLimited    bool           `json:"rate_limited"`
	ResetAtCST     string         `json:"reset_at_cst,omitempty"`
	ResetAtRFC3339 string         `json:"reset_at_rfc3339,omitempty"`
	// ResultCorrectionは意味検証不合格後の修正再依頼を同一sessionで1回だけ実行する工程を表す。
	// v2のPacketCompacted(構造欠陥再圧縮)を読込時に同じ1回制限へ読み替える。
	ResultCorrection bool `json:"result_correction,omitempty"`
	// RiskFloorReemitは同一reviewer sessionへNEEDS_SOL_REVIEW/HIGH再出力を依頼中の工程を表す。
	RiskFloorReemit bool `json:"risk_floor_reemit,omitempty"`
	// ReportOnlyはTARGETS: PACKETの報告再出力専用工程であることを表す。ReadOnly capabilityで
	// 実行し、resume後もsnapshot-report-only-start.jsonを再撮影せず同じ基準として使う。
	ReportOnly bool `json:"report_only,omitempty"`
	// EffectiveRiskはwrapperがworker原文riskと区別して決定した実効risk("HIGH"/"LOW")。
	// 空文字は旧checkpointなど未計算を表し、resume時に現在stateから安全側へ決定論的に再構成する。
	EffectiveRisk       string `json:"effective_risk,omitempty"`
	EffectiveRiskSource string `json:"effective_risk_source,omitempty"`
	// ProviderUnavailableは一時provider障害の回復がprobe上限・deadlineに到達し、
	// WORKER_ERROR/RATE_LIMITEDとは独立した再開可能停止状態になったことを表す。
	// role/phase/model/session/promptはそのまま保持し、--resumeで同一session/checkpointから再試行する。
	ProviderUnavailable               bool      `json:"provider_unavailable,omitempty"`
	ProviderUnavailableClassification string    `json:"provider_unavailable_classification,omitempty"`
	ProviderUnavailableProbes         int       `json:"provider_unavailable_probes,omitempty"`
	ProviderUnavailableStartedAt      time.Time `json:"provider_unavailable_started_at,omitempty"`
	// StopParentFilesは停止保存時点の親管理2file状態。review resumeのsnapshot例外が保存値と
	// 現在値の差分だけをwrapper停止期間中の親Codex更新として承認する。旧binaryのcheckpointは
	// このfieldを持たず、nilのときは停止期間の変化を機械識別できないためfail closedする。
	StopParentFiles *ParentFileStates `json:"stop_parent_files,omitempty"`
}

func (s *StateStore) SaveResumeCheckpoint(checkpoint ResumeCheckpoint) error {
	if checkpoint.Model == "" {
		return fmt.Errorf("resume state model is required")
	}
	checkpoint.Version = resumeStateVersion
	data, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return fmt.Errorf("resume stateをJSON化できません: %w", err)
	}

	if err := writeFileAtomic(s.Path(resumeStateFile), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("resume stateを書き込めません: %w", err)
	}
	return nil
}

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
	switch checkpoint.Version {
	case resumeStateVersion:
	case resumeStateVersion - 1:
		// v2のworker_packet表示行をtyped結果へ読み替える。変換できないcheckpointは
		// resume継続の入力にならないため、ここで失敗させる(fail closed)。
		converted, convertErr := convertLegacyCheckpoint(data)
		if convertErr != nil {
			return ResumeCheckpoint{}, convertErr
		}
		checkpoint = converted
	default:
		return ResumeCheckpoint{}, fmt.Errorf("unsupported resume state version: %d", checkpoint.Version)
	}
	if checkpoint.Model == "" {
		return ResumeCheckpoint{}, fmt.Errorf("resume state model is required")
	}
	return checkpoint, nil
}

// convertLegacyCheckpointはv2 checkpoint(worker_packet表示行・packet_compacted)を
// v3表現(worker_result・result_correction)へ等価変換する。表示行はKEY: value形式で
// typed結果へ復元でき、field順は表示契約上固定のため一意に戻せる。
func convertLegacyCheckpoint(data []byte) (ResumeCheckpoint, error) {
	var legacy struct {
		WorkerPacket    []string `json:"worker_packet"`
		PacketCompacted bool     `json:"packet_compacted"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return ResumeCheckpoint{}, fmt.Errorf("resume state v2を読めません: %w", err)
	}
	checkpoint := ResumeCheckpoint{}
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return ResumeCheckpoint{}, fmt.Errorf("resume stateを読めません: %w", err)
	}
	if len(legacy.WorkerPacket) > 0 {
		workerResult, err := packet.FromDisplayLines(legacy.WorkerPacket)
		if err != nil {
			return ResumeCheckpoint{}, fmt.Errorf("resume state v2のworker_packetを変換できません: %w", err)
		}
		checkpoint.WorkerResult = &workerResult
	}
	checkpoint.ResultCorrection = legacy.PacketCompacted
	checkpoint.Version = resumeStateVersion
	return checkpoint, nil
}

func (s *StateStore) ClearResumeCheckpoint() error {
	return s.Remove(resumeStateFile)
}
