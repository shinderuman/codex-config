package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// taskEventLogVersionはTaskEventRecord JSONのschema version。既存fieldの意味やJSON名を
// 変更するときだけbumpし、ParseTaskEventLineは旧version recordを読み飛ばす(fail-closed)。
// 新規fieldのomitempty追加は後方互換のためbump不要(ModelCallLogと同じ規則)。
const taskEventLogVersion = 1

// TaskBlockSummaryはstream event 1 content blockの非content観測値。
// text/thinking本文・tool入出力などの中身は保存せず、種別・tool名・byte数だけを残す。
type TaskBlockSummary struct {
	Type    string `json:"type"`
	Name    string `json:"name,omitempty"`
	Bytes   int    `json:"bytes"`
	IsError bool   `json:"is_error,omitempty"`
}

// TaskEventUsageはassistant message / result eventに付与されるtoken観測値。
type TaskEventUsage struct {
	InputTokens              int64 `json:"input_tokens,omitempty"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens,omitempty"`
	OutputTokens             int64 `json:"output_tokens,omitempty"`
}

// TaskEventRecordは追加AI callなしで既存model実行から受動的に得られたevent 1件の
// metadata record。task/call/session/role/phaseでresumeを跨いだ識別ができる。
// content本文・thinking・prompt・response・秘密情報はfieldとして持たない。
type TaskEventRecord struct {
	Version    int       `json:"version"`
	TaskID     string    `json:"task_id"`
	CallID     string    `json:"call_id"`
	SessionID  string    `json:"session_id,omitempty"`
	Role       string    `json:"role"`
	Phase      string    `json:"phase"`
	ModelAlias string    `json:"model_alias,omitempty"`
	Resumed    bool      `json:"resumed,omitempty"`
	Seq        int       `json:"seq"`
	Timestamp  time.Time `json:"timestamp"`
	Kind       string    `json:"kind"`
	Subtype    string    `json:"subtype,omitempty"`
	// MessageModelはassistant message / system initが報告した実model ID。
	MessageModel  string             `json:"message_model,omitempty"`
	Blocks        []TaskBlockSummary `json:"blocks,omitempty"`
	Usage         *TaskEventUsage    `json:"usage,omitempty"`
	IsError       bool               `json:"is_error,omitempty"`
	DurationMS    int64              `json:"duration_ms,omitempty"`
	DurationAPIMS int64              `json:"duration_api_ms,omitempty"`
	NumTurns      int                `json:"num_turns,omitempty"`
	TotalCostUSD  float64            `json:"total_cost_usd,omitempty"`
}

// AppendTaskEventはtask単位event logへ1行を追記する。追記失敗は呼出元(best-effort観測)
// へ返し、ここではwarningを出さない(警告は観測経路の責務で一度だけ出す)。
func (s *StateStore) AppendTaskEvent(record TaskEventRecord) error {
	if record.Version == 0 {
		record.Version = taskEventLogVersion
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("task eventをJSON化できません: %w", err)
	}
	path := s.TaskEventLogPath(record.TaskID)
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

// ParseTaskEventLineはevent log 1行をdecodeする。破損行・旧version recordはerrorとなり、
// 呼出元がその行だけをskipできる(破損をlog全体へ波及させない)。
func ParseTaskEventLine(data []byte) (TaskEventRecord, error) {
	var record TaskEventRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return TaskEventRecord{}, fmt.Errorf("task eventを読めません: %w", err)
	}
	if record.Version != taskEventLogVersion {
		return TaskEventRecord{}, fmt.Errorf("unsupported task event version: %d", record.Version)
	}
	return record, nil
}

// WarnTaskEventSkipは受動event記録をbest-effortで諦めた旨を観測用warningとして出す。
// event logは観測資料であり、正規workflow・task成否へ影響させない。
func WarnTaskEventSkip(operation string, err error) {
	fmt.Fprintf(statsWarnOut, "WARNING: passive event logの%sに失敗したためevent記録をskipします（task本体へ影響しません）: %v\n", operation, err)
}

func (s *StateStore) TaskEventLogPath(taskID string) string {
	return s.Path(filepath.Join("events", taskID+".jsonl"))
}
