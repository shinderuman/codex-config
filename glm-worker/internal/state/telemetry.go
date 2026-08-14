package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// modelCallLogVersionはModelCallLog JSONのschema version。既存fieldの意味やJSON名を
// 変更するときだけbumpし、ReadModelCallLogsが旧version recordを読み飛ばす(fail-closed)。
// 新fieldのomitempty追加は後方互換(旧recordは新field欠落=未観測/not captured)のためbump不要。
const modelCallLogVersion = 2

type TokenUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

type ResolvedModelUsage struct {
	InputTokens              int64   `json:"input_tokens"`
	CacheCreationInputTokens int64   `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64   `json:"cache_read_input_tokens"`
	OutputTokens             int64   `json:"output_tokens"`
	CostUSD                  float64 `json:"cost_usd,omitempty"`
}

// ModelCallLogはCodexが後からモデル配分を評価するための呼出単位ログ。
type ModelCallLog struct {
	Version             int                           `json:"version"`
	CallID              string                        `json:"call_id"`
	TaskID              string                        `json:"task_id"`
	SessionID           string                        `json:"session_id"`
	StartedAt           time.Time                     `json:"started_at"`
	CompletedAt         time.Time                     `json:"completed_at"`
	Phase               string                        `json:"phase"`
	Role                SessionRole                   `json:"role"`
	ModelAlias          string                        `json:"model_alias"`
	ResolvedModelUsage  map[string]ResolvedModelUsage `json:"resolved_model_usage,omitempty"`
	Effort              string                        `json:"effort"`
	ReadOnly            bool                          `json:"read_only"`
	Resumed             bool                          `json:"resumed"`
	Outcome             string                        `json:"outcome"`
	PacketStatus        string                        `json:"packet_status,omitempty"`
	Prompt              string                        `json:"prompt,omitempty"`
	PromptBytes         int                           `json:"prompt_bytes"`
	PromptSHA256        string                        `json:"prompt_sha256"`
	SystemPromptBytes   int                           `json:"system_prompt_bytes"`
	SystemPromptSHA256  string                        `json:"system_prompt_sha256"`
	SystemPrompt        string                        `json:"system_prompt,omitempty"`
	Response            string                        `json:"response,omitempty"`
	ResponseBytes       int                           `json:"response_bytes"`
	ResponseSHA256      string                        `json:"response_sha256"`
	Error               string                        `json:"error,omitempty"`
	TopLevelUsage       TokenUsage                    `json:"top_level_usage"`
	TreeUsage           TokenUsage                    `json:"tree_usage"`
	WallDurationMS      int64                         `json:"wall_duration_ms"`
	ClaudeDurationMS    int64                         `json:"claude_duration_ms,omitempty"`
	ClaudeAPIDurationMS int64                         `json:"claude_api_duration_ms,omitempty"`
	TopLevelTurns       int                           `json:"top_level_turns,omitempty"`
	TotalCostUSD        float64                       `json:"total_cost_usd,omitempty"`
	// 診断field群(v2のままomitempty追加)。未設定は「このcallで観測されなかった/not captured」を表す。
	// enum系 risk/classification/resume_source は空文字=未観測(HIGH/LOW等の意味値と区別)、
	// probe_attempt/retry_elapsed_ms のint零値=未観測、snapshot は *struct nil=未観測で、
	// その内部 matched *bool も nil=未比較。旧recordはこれらが欠落=未観測として扱う。
	WorkerReportedRisk     string              `json:"worker_reported_risk,omitempty"`
	ReviewerReportedRisk   string              `json:"reviewer_reported_risk,omitempty"`
	EffectiveRisk          string              `json:"effective_risk,omitempty"`
	RiskFloorSource        string              `json:"risk_floor_source,omitempty"`
	RiskFloorCategory      string              `json:"risk_floor_category,omitempty"`
	PacketRejectReason     string              `json:"packet_reject_reason,omitempty"`
	ProviderClassification string              `json:"provider_classification,omitempty"`
	ProbeAttempt           int                 `json:"probe_attempt,omitempty"`
	RetryElapsedMS         int64               `json:"retry_elapsed_ms,omitempty"`
	ResumeSource           string              `json:"resume_source,omitempty"`
	Snapshot               *SnapshotDiagnostic `json:"snapshot,omitempty"`
}

// RecordModelCallLogは詳細ログを追記し、token集計をmirrorへ反映する。
// どちらかが失敗しても正規workflowを止めない。
func (s *StateStore) RecordModelCallLog(value ModelCallLog) {
	if value.Version == 0 {
		value.Version = modelCallLogVersion
	}
	if value.CallID == "" {
		callID, err := newUUID()
		if err != nil {
			warnStatsFailure("telemetry call ID生成", err)
		} else {
			value.CallID = callID
		}
	}
	value.TreeUsage = modelCallTreeUsage(value)
	if err := s.appendModelCallLog(value); err != nil {
		warnStatsFailure("telemetry追記", err)
	}
	s.recordTokenUsage(value)
}

func (s *StateStore) appendModelCallLog(value ModelCallLog) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("telemetryをJSON化できません: %w", err)
	}
	path := s.ModelCallLogPath(value.TaskID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func (s *StateStore) recordTokenUsage(value ModelCallLog) {
	s.UpdateTaskStats(func(stats *TaskStats) {
		addInt64(&stats.InputTokensByAlias, value.ModelAlias, value.TreeUsage.InputTokens)
		addInt64(&stats.CacheCreationInputTokensByAlias, value.ModelAlias, value.TreeUsage.CacheCreationInputTokens)
		addInt64(&stats.CacheReadInputTokensByAlias, value.ModelAlias, value.TreeUsage.CacheReadInputTokens)
		addInt64(&stats.OutputTokensByAlias, value.ModelAlias, value.TreeUsage.OutputTokens)
		addInt(&stats.TopLevelTurnsByAlias, value.ModelAlias, value.TopLevelTurns)
		for model, usage := range value.ResolvedModelUsage {
			addInt(&stats.CallTreesByResolvedModel, model, 1)
			addInt64(&stats.InputTokensByResolvedModel, model, usage.InputTokens)
			addInt64(&stats.CacheCreationInputTokensByResolvedModel, model, usage.CacheCreationInputTokens)
			addInt64(&stats.CacheReadInputTokensByResolvedModel, model, usage.CacheReadInputTokens)
			addInt64(&stats.OutputTokensByResolvedModel, model, usage.OutputTokens)
		}
	})
}

func modelCallTreeUsage(value ModelCallLog) TokenUsage {
	if value.TreeUsage != (TokenUsage{}) {
		return value.TreeUsage
	}
	var result TokenUsage
	for _, usage := range value.ResolvedModelUsage {
		result.InputTokens += usage.InputTokens
		result.CacheCreationInputTokens += usage.CacheCreationInputTokens
		result.CacheReadInputTokens += usage.CacheReadInputTokens
		result.OutputTokens += usage.OutputTokens
	}
	if result == (TokenUsage{}) {
		return value.TopLevelUsage
	}
	return result
}

func addInt(values *map[string]int, key string, value int) {
	if key == "" || value == 0 {
		return
	}
	if *values == nil {
		*values = make(map[string]int)
	}
	(*values)[key] += value
}

func addInt64(values *map[string]int64, key string, value int64) {
	if key == "" || value == 0 {
		return
	}
	if *values == nil {
		*values = make(map[string]int64)
	}
	(*values)[key] += value
}

func (s *StateStore) ModelCallLogPath(taskID string) string {
	return s.Path(filepath.Join("telemetry", taskID+".jsonl"))
}

func (s *StateStore) ReadModelCallLogs(taskID string) ([]ModelCallLog, error) {
	file, err := os.Open(s.ModelCallLogPath(taskID))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var result []ModelCallLog
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 4*1024*1024)
	for scanner.Scan() {
		var value ModelCallLog
		if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
			return nil, fmt.Errorf("telemetryを読めません: %w", err)
		}
		if value.Version != modelCallLogVersion {
			continue
		}
		result = append(result, value)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
