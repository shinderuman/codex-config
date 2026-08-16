package runner

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// plainSignalMaxBytesはJSON eventとして解釈できないplain stdout行を分類のため
// メモリ内へ保持する上限。旧raw fallbackと違い全量は持たず、末尾側だけを残す。
const plainSignalMaxBytes = 64 * 1024

// streamEventIngesterはClaude CLIのstream-json出力を行単位で受け取り、各eventを
// content本文を含まないmetadataだけへ縮約してtask単位event logへbest-effort追記する。
// streamの非result event本文はどこへも書き出さず、既存result解析に必要な最終
// type=result event行だけをboundedな内部表現へ保持する。JSON eventとして解釈できない
// plain stdout行はraw保存の代わりに分類用signal bufferへ末尾boundedで保持する。
// stdout経路の一部としてchild processへ組み込むためWriteは決してerrorを返さず、
// 追記失敗時はこのcallのevent記録を無効化して本体実行へ影響させない。
type streamEventIngester struct {
	state      *state.StateStore
	base       state.TaskEventRecord
	seq        int
	pending    []byte
	closed     bool
	resultLine []byte
	plain      []byte
}

func newStreamEventIngester(
	st *state.StateStore,
	taskID string,
	callID string,
	role state.SessionRole,
	phase string,
	model string,
	sessionID string,
	resumed bool,
) *streamEventIngester {
	return &streamEventIngester{
		state: st,
		base: state.TaskEventRecord{
			TaskID:     taskID,
			CallID:     callID,
			SessionID:  sessionID,
			Role:       string(role),
			Phase:      phase,
			ModelAlias: model,
			Resumed:    resumed,
		},
	}
}

// resultは保持した最終result event行を返す。未観測のときはfalse。
func (g *streamEventIngester) result() ([]byte, bool) {
	if len(g.resultLine) == 0 {
		return nil, false
	}
	return g.resultLine, true
}

func (g *streamEventIngester) Write(p []byte) (int, error) {
	g.pending = append(g.pending, p...)
	for {
		index := bytes.IndexByte(g.pending, '\n')
		if index < 0 {
			break
		}
		line := g.pending[:index]
		g.pending = g.pending[index+1:]
		g.ingestLine(line)
	}
	return len(p), nil
}

// flushはprocess終了後に改行で終端しなかった最終行を取り込む。
func (g *streamEventIngester) flush() {
	if len(bytes.TrimSpace(g.pending)) > 0 {
		g.ingestLine(g.pending)
	}
	g.pending = nil
}

func (g *streamEventIngester) ingestLine(line []byte) {
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	if streamResultEvent(line) {
		g.resultLine = append(g.resultLine[:0], line...)
	}
	g.capturePlainSignal(line)
	if g.closed {
		return
	}
	record := reduceStreamEvent(line, g.base, g.seq+1)
	if err := g.state.AppendTaskEvent(record); err != nil {
		state.WarnTaskEventSkip("追記", err)
		g.closed = true
		return
	}
	g.seq++
}

// capturePlainSignalはJSON eventとして解釈できないplain stdout行だけを分類用bufferへ
// 追記する。assistant/thinking/tool等のJSON content内の数値・文字列をprovider信号へ
// 誤認しないための境界で、有効なJSON object行はここへ入らない。bufferは生本文を
// 保持するが分類後破棄され、外部へ出るのは分類Kind・Detailのような構造値だけ。
func (g *streamEventIngester) capturePlainSignal(line []byte) {
	var event streamEvent
	if json.Unmarshal(line, &event) == nil {
		return
	}
	g.plain = append(g.plain, line...)
	g.plain = append(g.plain, '\n')
	if excess := len(g.plain) - plainSignalMaxBytes; excess > 0 {
		g.plain = g.plain[excess:]
	}
}

// plainSignalは分類対象のplain stdout末尾を返す。保持していないときは空文字。
func (g *streamEventIngester) plainSignal() string {
	return string(g.plain)
}

// streamResultEventは行がtype=result eventかどうかをcontentを読まずに判定する。
func streamResultEvent(line []byte) bool {
	var head struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(line, &head) == nil && head.Type == "result"
}

// streamEventはstream-json 1行から観測に必要な非content fieldだけを取り出すための
// 受け皿。content本文はjson.RawMessageのままblock単位のbyte数へ縮約され、recordへは
// 入らない。未知のevent種別もkind名だけ残し schema driftを観測可能にする。
type streamEvent struct {
	Type          string                `json:"type"`
	Subtype       string                `json:"subtype"`
	Model         string                `json:"model"`
	Message       json.RawMessage       `json:"message"`
	IsError       bool                  `json:"is_error"`
	DurationMS    int64                 `json:"duration_ms"`
	DurationAPIMS int64                 `json:"duration_api_ms"`
	NumTurns      int                   `json:"num_turns"`
	TotalCostUSD  float64               `json:"total_cost_usd"`
	Usage         *state.TaskEventUsage `json:"usage"`
}

type streamMessage struct {
	Model   string                `json:"model"`
	Usage   *state.TaskEventUsage `json:"usage"`
	Content []json.RawMessage     `json:"content"`
}

type streamBlock struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	IsError bool   `json:"is_error"`
}

func reduceStreamEvent(line []byte, base state.TaskEventRecord, seq int) state.TaskEventRecord {
	record := base
	record.Version = 0
	record.Seq = seq
	record.Timestamp = time.Now().UTC()
	var event streamEvent
	if err := json.Unmarshal(line, &event); err != nil {
		record.Kind = "unknown"
		return record
	}
	record.Kind = event.Type
	record.Subtype = event.Subtype
	switch event.Type {
	case "system":
		record.MessageModel = event.Model
	case "assistant", "user":
		var message streamMessage
		if err := json.Unmarshal(event.Message, &message); err == nil {
			record.MessageModel = message.Model
			record.Usage = message.Usage
			record.Blocks = reduceStreamBlocks(message.Content)
		}
	case "result":
		record.IsError = event.IsError
		record.DurationMS = event.DurationMS
		record.DurationAPIMS = event.DurationAPIMS
		record.NumTurns = event.NumTurns
		record.TotalCostUSD = event.TotalCostUSD
		record.Usage = event.Usage
	}
	return record
}

func reduceStreamBlocks(content []json.RawMessage) []state.TaskBlockSummary {
	blocks := make([]state.TaskBlockSummary, 0, len(content))
	for _, raw := range content {
		var block streamBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			blocks = append(blocks, state.TaskBlockSummary{Type: "unknown", Bytes: len(raw)})
			continue
		}
		blocks = append(blocks, state.TaskBlockSummary{
			Type:    block.Type,
			Name:    block.Name,
			Bytes:   len(raw),
			IsError: block.IsError,
		})
	}
	if len(blocks) == 0 {
		return nil
	}
	return blocks
}
