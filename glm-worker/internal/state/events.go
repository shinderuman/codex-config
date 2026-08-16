package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// taskEventLogVersionはTaskEventRecord JSONのschema version。既存fieldの意味やJSON名を
// 変更するときだけbumpし、ParseTaskEventLineは旧version recordを読み飛ばす(fail-closed)。
// 新規fieldのomitempty追加は後方互換のためbump不要(ModelCallLogと同じ規則)。
const taskEventLogVersion = 1

// TaskBlockSummaryはstream event 1 content blockの非content観測値。
// text/thinking本文・tool入出力などの中身は保存せず、種別・tool名・byte数だけを残す。
// ToolIDはtool_useのid / tool_resultのtool_use_id。DurationMSは同一call内で
// tool_use→tool_resultをIDで対応付けられたときだけ入る観測時間で、対応付けられない
// 場合は0のまま(未測定を推測で埋めない)。
type TaskBlockSummary struct {
	Type       string `json:"type"`
	Name       string `json:"name,omitempty"`
	ToolID     string `json:"tool_id,omitempty"`
	Bytes      int    `json:"bytes"`
	IsError    bool   `json:"is_error,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
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

// WarnTaskEventCapは1 callのevent記録が上限に到達し以後の追記をskipした旨を出す。
// result event捕捉・task本体へは影響しない。
func WarnTaskEventCap(limit int) {
	fmt.Fprintf(statsWarnOut, "WARNING: passive event logの追記がcall当たり上限%d件に到達したため以後のevent記録をskipします（task本体へ影響しません）\n", limit)
}

func (s *StateStore) TaskEventLogPath(taskID string) string {
	return s.Path(filepath.Join("events", taskID+".jsonl"))
}

// retainedTaskEventLogsは新規task開始時に残す旧taskのevent log件数。実測(1 task約
// 数千行)に対し十分な観測履歴を残しつつ、task数に比例した無制限増加を防ぐ最小上限。
const retainedTaskEventLogs = 10

// PruneTaskEventLogsは旧taskのevent logを新しい順にkeep件だけ残して削除する。
// 現taskのlogはmtimeに関係なく削除しない。telemetry・stats履歴・checkpoint・sessionは
// 対象外で、失敗はwarningだけ出し呼出元のtask成否へ影響させない。
func (s *StateStore) PruneTaskEventLogs(keep int, currentTaskID string) {
	paths, err := filepath.Glob(s.Path(filepath.Join("events", "*.jsonl")))
	if err != nil {
		WarnTaskEventPrune(err)
		return
	}
	currentLog := s.TaskEventLogPath(currentTaskID)
	type entry struct {
		path  string
		mtime time.Time
	}
	entries := make([]entry, 0, len(paths))
	for _, path := range paths {
		if path == currentLog {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			WarnTaskEventPrune(err)
			continue
		}
		entries = append(entries, entry{path: path, mtime: info.ModTime()})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mtime.After(entries[j].mtime) })
	for index, item := range entries {
		if index < keep {
			continue
		}
		if err := os.Remove(item.path); err != nil {
			WarnTaskEventPrune(err)
		}
	}
}

// WarnTaskEventPruneは旧event logのretention整理失敗を観測用warningとして出す。
// event logは観測資料のため、整理失敗でtask本体を失敗させない。
func WarnTaskEventPrune(err error) {
	fmt.Fprintf(statsWarnOut, "WARNING: 旧task event logのretention整理に失敗しました（task本体へ影響しません）: %v\n", err)
}
