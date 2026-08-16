package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// defaultWatchFollowIntervalは--watchのtail polling間隔。保存済みlogのlocal監視だけで
// provider/workerへの問い合わせは行わないため、この間隔での外部影響はない。
const defaultWatchFollowInterval = 500 * time.Millisecond

// printWatchは現在taskの受動event logを読み取り専用で表示する。state書換・repo lock・
// AI call・provider/workerへの問い合わせを行わない。event logがまだ無いtaskはその旨を
// 表示して即座に終了し、存在すれば既存行を表示した後追記をfollowする。stopはtest用の
// 打ち切り信号で、nilなら中断されるまでfollowし続ける。
func printWatch(st *state.StateStore, stdout io.Writer, followInterval time.Duration, stop <-chan struct{}) error {
	taskID := st.ReadOr("task.id", "none")
	fmt.Fprintf(stdout, "TASK_ID: %s\n", taskID)
	if taskID == "none" {
		fmt.Fprintln(stdout, "EVENT_LOG: none")
		return nil
	}
	path := st.TaskEventLogPath(taskID)
	fmt.Fprintf(stdout, "EVENT_LOG: %s\n", path)
	file, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(stdout, "EVENT_LOG_STATUS: empty")
		return nil
	}
	defer file.Close()
	fmt.Fprintln(stdout, "EVENT_LOG_STATUS: following")
	return watchTaskEvents(file, path, stdout, followInterval, stop)
}

func watchTaskEvents(file *os.File, path string, stdout io.Writer, followInterval time.Duration, stop <-chan struct{}) error {
	pending, err := drainTaskEvents(file, stdout, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-stop:
			return nil
		case <-time.After(followInterval):
		}
		if _, err := os.Stat(path); err != nil {
			fmt.Fprintln(stdout, "EVENT_LOG_STATUS: removed")
			return nil
		}
		pending, err = drainTaskEvents(file, stdout, pending)
		if err != nil {
			return err
		}
	}
}

// drainTaskEventsは読み取り位置以降の新規bytesを行ごとに表示する。改行で終端しない
// 末尾部分は次回へ持ち越し、書き込み途中の行を破損表示にしない。
func drainTaskEvents(file *os.File, stdout io.Writer, pending []byte) ([]byte, error) {
	buffer := make([]byte, 32*1024)
	for {
		read, err := file.Read(buffer)
		if read > 0 {
			pending = append(pending, buffer[:read]...)
			for {
				index := bytes.IndexByte(pending, '\n')
				if index < 0 {
					break
				}
				renderTaskEventLine(pending[:index], stdout)
				pending = pending[index+1:]
			}
		}
		if err == io.EOF {
			return pending, nil
		}
		if err != nil {
			return pending, err
		}
	}
}

func renderTaskEventLine(line []byte, stdout io.Writer) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return
	}
	record, err := state.ParseTaskEventLine(trimmed)
	if err != nil {
		fmt.Fprintf(stdout, "EVENT_SKIPPED: unparseable line: %v\n", err)
		return
	}
	fmt.Fprintln(stdout, formatTaskEvent(record))
}

// formatTaskEventはevent 1件を1行へ圧縮する。thinkingはbyte数のlabelだけで本文を
// 表示しない。
func formatTaskEvent(record state.TaskEventRecord) string {
	parts := []string{
		record.Timestamp.UTC().Format(time.RFC3339),
		record.Phase,
		record.Role,
	}
	if record.Resumed {
		parts = append(parts, "resumed")
	}
	parts = append(parts, record.Kind)
	if record.Subtype != "" {
		parts = append(parts, record.Subtype)
	}
	if record.MessageModel != "" {
		parts = append(parts, "model="+record.MessageModel)
	}
	for _, block := range record.Blocks {
		label := block.Type
		if block.Name != "" {
			label += "(" + block.Name + ")"
		}
		if block.IsError {
			label += "!"
		}
		if block.DurationMS != 0 {
			parts = append(parts, fmt.Sprintf("%s:%db/%dms", label, block.Bytes, block.DurationMS))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:%db", label, block.Bytes))
	}
	if record.Usage != nil {
		promptTokens := record.Usage.InputTokens +
			record.Usage.CacheCreationInputTokens +
			record.Usage.CacheReadInputTokens
		parts = append(parts, fmt.Sprintf("in=%d out=%d", promptTokens, record.Usage.OutputTokens))
	}
	if record.NumTurns != 0 {
		parts = append(parts, fmt.Sprintf("turns=%d", record.NumTurns))
	}
	if record.TotalCostUSD != 0 {
		parts = append(parts, fmt.Sprintf("cost=%.4f", record.TotalCostUSD))
	}
	if record.DurationMS != 0 {
		parts = append(parts, fmt.Sprintf("dur=%dms", record.DurationMS))
	}
	if record.IsError {
		parts = append(parts, "is_error=true")
	}
	return strings.Join(parts, " ")
}
