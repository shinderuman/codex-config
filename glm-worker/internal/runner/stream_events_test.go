package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// streamFixtureはstream-json出力の縮小再現。secretを含むtext/thinking/tool入出力を
// 混ぜ、event logへcontent本体が落ちないことを検証する。
const streamFixtureSecret = "sk-ant-secret-TOKEN123"

const streamFixtureLines = `{"type":"system","subtype":"init","session_id":"sess-1","model":"glm-5.3"}
{"type":"system","subtype":"thinking_tokens"}
{"type":"system","subtype":"thinking_tokens"}
{"type":"assistant","message":{"id":"msg_1","model":"glm-5.3","content":[{"type":"thinking","thinking":"secret plan ` + streamFixtureSecret + `"},{"type":"text","text":"visible text"},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"echo ` + streamFixtureSecret + `"}}],"usage":{"input_tokens":100,"cache_read_input_tokens":200,"output_tokens":7}}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"` + streamFixtureSecret + ` output","is_error":false}]}}
{"type":"result","subtype":"success","is_error":false,"result":"{\"status\":\"IMPLEMENTED\",\"risk\":\"LOW\",\"summary\":\"done\",\"requirement_coverage\":\"covered\",\"tests\":\"pass\",\"unverified\":\"none\"}","structured_output":{"status":"IMPLEMENTED","risk":"LOW","summary":"done","requirement_coverage":"covered","tests":"pass","unverified":"none"},"duration_ms":1200,"duration_api_ms":900,"num_turns":2,"total_cost_usd":0.5,"usage":{"input_tokens":11,"cache_creation_input_tokens":12,"cache_read_input_tokens":13,"output_tokens":14},"modelUsage":{"glm-5.3":{"inputTokens":11,"cacheCreationInputTokens":12,"cacheReadInputTokens":13,"outputTokens":14}}}
`

func writeStreamFixtureClaude(t *testing.T, output string) string {
	t.Helper()
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "stream.jsonl")
	if err := os.WriteFile(fixturePath, []byte(output), 0o600); err != nil {
		t.Fatal(err)
	}
	commandPath := filepath.Join(dir, "fake-claude")
	script := "#!/bin/sh\ncat \"" + fixturePath + "\"\n"
	if err := os.WriteFile(commandPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return commandPath
}

func newStreamFixtureRunner(t *testing.T, claudeBin string) (*ClaudeRunner, *state.StateStore, string) {
	t.Helper()
	promptDir := t.TempDir()
	for _, name := range []string{"WORKER.md", "REVIEWER.md"} {
		if err := os.WriteFile(filepath.Join(promptDir, name), []byte("system"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	st := newTestStateStore(t)
	taskID := "12345678-aaaa-bbbb-cccc-dddddddddddd"
	if err := st.Write("task.id", taskID); err != nil {
		t.Fatal(err)
	}
	r := NewClaudeRunner(config.AppConfig{
		RepoRoot:  t.TempDir(),
		RepoShort: "abcdef123456",
		PromptDir: promptDir,
		ClaudeBin: claudeBin,
	}, st)
	return r, st, taskID
}

func readTaskEventLines(t *testing.T, st *state.StateStore, taskID string) []state.TaskEventRecord {
	t.Helper()
	data, err := os.ReadFile(st.TaskEventLogPath(taskID))
	if err != nil {
		t.Fatal(err)
	}
	var records []state.TaskEventRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		record, err := state.ParseTaskEventLine([]byte(line))
		if err != nil {
			t.Fatalf("event log破損行: %v: %s", err, line)
		}
		records = append(records, record)
	}
	return records
}

// TestClaudeRunnerStreamEventsAppendSanitizedMetadataはstream-json受動eventが
// 追記安全にmetadataだけ保存され、初回/resume呼出をcall_id・phase・resumedで
// 識別できることをproduction Run経路で検証する。
func TestClaudeRunnerStreamEventsAppendSanitizedMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}

	commandPath := writeStreamFixtureClaude(t, streamFixtureLines)
	r, st, taskID := newStreamFixtureRunner(t, commandPath)

	first, err := r.Run(state.WorkerRole, "worker-new", "worker-model", false, "high", "first prompt", filepath.Join(t.TempDir(), "first.log"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Run(state.WorkerRole, "worker-decision", "worker-model", false, "max", "second prompt", filepath.Join(t.TempDir(), "second.log"))
	if err != nil {
		t.Fatal(err)
	}

	if first.Resumed || !second.Resumed {
		t.Fatalf("resume観測 = %#v / %#v", first.Resumed, second.Resumed)
	}
	if first.Response != "{\"status\":\"IMPLEMENTED\",\"risk\":\"LOW\",\"summary\":\"done\",\"requirement_coverage\":\"covered\",\"tests\":\"pass\",\"unverified\":\"none\"}" || first.TopLevelUsage.InputTokens != 11 || first.DurationMS != 1200 || first.DurationAPIMS != 900 || first.TopLevelTurns != 2 || first.TotalCostUSD != 0.5 {
		t.Fatalf("result event互換のRunResult = %#v", first)
	}
	if !strings.Contains(string(first.StructuredOutput), "\"status\":\"IMPLEMENTED\"") {
		t.Fatalf("structured_outputがRunResultへ抽出されていません: %s", first.StructuredOutput)
	}

	records := readTaskEventLines(t, st, taskID)
	if len(records) != 8 {
		t.Fatalf("event件数 = %d", len(records))
	}
	firstCall := records[0].CallID
	if records[4].CallID == firstCall {
		t.Fatal("呼出ごとにcall_idが変わっていません")
	}
	for index, record := range records {
		if record.TaskID != taskID {
			t.Fatalf("task_id = %q", record.TaskID)
		}
		if record.Role != "worker" || record.ModelAlias != "worker-model" {
			t.Fatalf("role/model = %q/%q", record.Role, record.ModelAlias)
		}
		if record.Seq != index%4+1 {
			t.Fatalf("seq = %d (index %d)", record.Seq, index)
		}
		wantPhase := "worker-new"
		wantResumed := false
		if index >= 4 {
			wantPhase = "worker-decision"
			wantResumed = true
		}
		if record.Phase != wantPhase || record.Resumed != wantResumed {
			t.Fatalf("phase/resumed = %q/%v (index %d)", record.Phase, record.Resumed, index)
		}
	}

	assistant := records[1]
	if assistant.Kind != "assistant" || assistant.MessageModel != "glm-5.3" {
		t.Fatalf("assistant event = %#v", assistant)
	}
	if assistant.Usage == nil || assistant.Usage.InputTokens != 100 || assistant.Usage.CacheReadInputTokens != 200 || assistant.Usage.OutputTokens != 7 {
		t.Fatalf("assistant usage = %#v", assistant.Usage)
	}
	if len(assistant.Blocks) != 3 {
		t.Fatalf("assistant blocks = %#v", assistant.Blocks)
	}
	if assistant.Blocks[0].Type != "thinking" || assistant.Blocks[1].Type != "text" || assistant.Blocks[2].Type != "tool_use" || assistant.Blocks[2].Name != "Bash" {
		t.Fatalf("assistant blocks = %#v", assistant.Blocks)
	}
	for _, block := range assistant.Blocks {
		if block.Bytes <= 0 {
			t.Fatalf("block bytes = %#v", assistant.Blocks)
		}
	}
	if assistant.Blocks[2].ToolID != "toolu_1" {
		t.Fatalf("tool_use id = %#v", assistant.Blocks[2])
	}
	user := records[2]
	if user.Kind != "user" || len(user.Blocks) != 1 {
		t.Fatalf("user event = %#v", user)
	}
	if user.Blocks[0].ToolID != "toolu_1" || user.Blocks[0].Name != "Bash" {
		t.Fatalf("tool_result block = %#v", user.Blocks[0])
	}

	result := records[3]
	if result.Kind != "result" || result.Subtype != "success" || result.DurationMS != 1200 || result.NumTurns != 2 || result.TotalCostUSD != 0.5 || result.Usage.OutputTokens != 14 {
		t.Fatalf("result event = %#v", result)
	}

	raw, err := os.ReadFile(st.TaskEventLogPath(taskID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), streamFixtureSecret) || strings.Contains(string(raw), "visible text") || strings.Contains(string(raw), "secret plan") {
		t.Fatalf("event logへcontent本文・秘密情報が混入しました: %s", raw)
	}
}

// TestClaudeRunnerStreamNonResultContentNeverReachesDiskはstreamの非result event本文
// (thinking・assistant text・tool入出力)が成功経路のどのdisk出力にも現れないことを
// 検証する。stdoutはingesterだけが受け、result event行もdiskへ書かない。
func TestClaudeRunnerStreamNonResultContentNeverReachesDisk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}

	commandPath := writeStreamFixtureClaude(t, streamFixtureLines)
	r, _, _ := newStreamFixtureRunner(t, commandPath)

	outputPath := filepath.Join(t.TempDir(), "out.log")
	result, err := r.Run(state.WorkerRole, "worker-new", "worker-model", false, "high", "prompt", outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Response, streamFixtureSecret) {
		t.Fatalf("RunResult.Responseへ秘密情報が混入しました: %q", result.Response)
	}

	finalOutput, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(finalOutput), "\"status\":\"IMPLEMENTED\"") {
		t.Fatalf("最終outputへresult本文がありません: %s", finalOutput)
	}
	if strings.Contains(string(finalOutput), streamFixtureSecret) || strings.Contains(string(finalOutput), "visible text") || strings.Contains(string(finalOutput), "secret plan") {
		t.Fatalf("最終outputへ非result本文・秘密情報が混入しました: %s", finalOutput)
	}
	if _, err := os.Stat(outputPath + ".json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("raw stream fileが存在してはいけません: %v", err)
	}
	if _, err := os.Stat(outputPath + ".stderr"); err != nil {
		t.Fatalf("stderr file: %v", err)
	}
}

// TestClaudeRunnerStreamNoResultKeepsFailureBoundaryはresult eventがないstreamで
// 従来のtype不正失敗区分へ落ち、error・診断output・event logのいずれにも
// assistant/tool本文が転記されないことを検証する。
func TestClaudeRunnerStreamNoResultKeepsFailureBoundary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}

	noResult := strings.Replace(streamFixtureLines, lineOfKind(streamFixtureLines, "result")+"\n", "", 1)
	commandPath := writeStreamFixtureClaude(t, noResult)
	r, st, taskID := newStreamFixtureRunner(t, commandPath)

	outputPath := filepath.Join(t.TempDir(), "out.log")
	_, err := r.Run(state.WorkerRole, "worker-new", "worker-model", false, "high", "prompt", outputPath)
	if err == nil {
		t.Fatal("result eventなしで成功しました")
	}
	if !strings.Contains(err.Error(), "result eventがありません") {
		t.Fatalf("失敗区分 = %v", err)
	}
	if strings.Contains(err.Error(), streamFixtureSecret) || strings.Contains(err.Error(), "visible text") {
		t.Fatalf("error本文へstream contentが転記されました: %v", err)
	}
	if st.Exists("worker.ready") {
		t.Fatal("result eventなしでsession readyになりました")
	}

	finalOutput, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(finalOutput), "result eventがありません") {
		t.Fatalf("診断outputへ構造summaryがありません: %s", finalOutput)
	}
	if strings.Contains(string(finalOutput), streamFixtureSecret) || strings.Contains(string(finalOutput), "visible text") || strings.Contains(string(finalOutput), "secret plan") {
		t.Fatalf("診断outputへ非result本文・秘密情報が混入しました: %s", finalOutput)
	}
	if _, err := os.Stat(outputPath + ".json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("raw stream fileが存在してはいけません: %v", err)
	}

	records := readTaskEventLines(t, st, taskID)
	if len(records) != 3 {
		t.Fatalf("event件数 = %d", len(records))
	}
	raw, err := os.ReadFile(st.TaskEventLogPath(taskID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), streamFixtureSecret) || strings.Contains(string(raw), "visible text") || strings.Contains(string(raw), "secret plan") {
		t.Fatalf("event logへcontent本文・秘密情報が混入しました: %s", raw)
	}
}

// lineOfKindはfixtureから指定typeの行を取り出す。test fixture構築の補助。
func lineOfKind(fixture string, kind string) string {
	for _, line := range strings.Split(fixture, "\n") {
		if strings.Contains(line, `"type":"`+kind+`"`) {
			return line
		}
	}
	return ""
}

// plainStdoutSignalはresult eventを欠くstreamへ混ざるplain stdout障害textの共通fixture。
// secret行を併せ、信号は分類構造値になる一方で本文がどこにも保存されないことを検証する。
const plainStdoutSecretLine = "diagnostic context contains " + streamFixtureSecret

func plainStdoutFixture(signalLine string) string {
	return signalLine + "\n" + plainStdoutSecretLine + "\n"
}

// assertPlainSignalNotPersistedはplain stdout本文(信号行・secret)が最終output・error・
// event log・分類構造値のどこへも残らないことを検証する。
func assertPlainSignalNotPersisted(t *testing.T, outputPath string, runErr error, st *state.StateStore, taskID string, signalFragment string, classText string) {
	t.Helper()
	signals := []string{streamFixtureSecret, signalFragment}
	finalOutput, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range signals {
		if strings.Contains(string(finalOutput), secret) {
			t.Fatalf("最終outputへplain stdout本文が混入しました: %s", finalOutput)
		}
		if runErr != nil && strings.Contains(runErr.Error(), secret) {
			t.Fatalf("error本文へplain stdout本文が混入しました: %v", runErr)
		}
		if strings.Contains(classText, secret) {
			t.Fatalf("分類構造値へplain stdout本文が混入しました: %s", classText)
		}
	}
	raw, err := os.ReadFile(st.TaskEventLogPath(taskID))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range signals {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("event logへplain stdout本文が混入しました: %s", raw)
		}
	}
}

// TestClaudeRunnerPlainStdoutFailureClassificationは、result eventが無い経路で
// plain stdoutに現れた5h上限・transient・明示fatal信号が旧raw fallbackと同じ
// classifierで構造値に分類されることをproduction Run経路で検証する。
func TestClaudeRunnerPlainStdoutFailureClassification(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}

	cases := []struct {
		name string
		line string
		want ProviderFailureClass
	}{
		{
			name: "five hour limit",
			line: "API Error: Request rejected (429) · [1308][Usage limit reached for 5 hour. Your limit will reset at 2026-07-22 14:06:34]",
			want: ProviderFailureClass{
				Kind:          ProviderFailureZaiFiveHour,
				FiveHourLimit: ZaiFiveHourLimit{ResetAtCST: "2026-07-22 14:06:34", ResetAtRFC3339: "2026-07-22T14:06:34+08:00"},
			},
		},
		{
			name: "transient http status",
			line: "API Error: 503 Service Unavailable",
			want: ProviderFailureClass{Kind: ProviderFailureTransient, Detail: "http-503"},
		},
		{
			name: "explicit fatal signal stays workflow default",
			line: "API Error: 401 Unauthorized: invalid api key",
			want: ProviderFailureClass{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			commandPath := writeStreamFixtureClaude(t, plainStdoutFixture(c.line))
			r, st, taskID := newStreamFixtureRunner(t, commandPath)
			outputPath := filepath.Join(t.TempDir(), "out.log")
			result, err := r.Run(state.WorkerRole, "worker-new", "worker-model", false, "high", "prompt", outputPath)
			if err == nil {
				t.Fatal("result eventなしで成功しました")
			}
			if result.PlainFailure != c.want {
				t.Fatalf("PlainFailure = %#v, want %#v", result.PlainFailure, c.want)
			}
			assertPlainSignalNotPersisted(t, outputPath, err, st, taskID, c.line, fmt.Sprintf("%#v", result.PlainFailure))

			records := readTaskEventLines(t, st, taskID)
			if len(records) != 2 {
				t.Fatalf("plain行のevent記録 = %d", len(records))
			}
			for _, record := range records {
				if record.Kind != "unknown" || record.Blocks != nil || record.Subtype != "" {
					t.Fatalf("plain行の縮約record = %#v", record)
				}
			}
			fileClass := ClassifyProviderFailureText(ReadTransientSignal(outputPath))
			if c.want.Kind == "" && fileClass.Kind != ProviderFailureFatal {
				t.Fatalf("file由来分類 = %#v", fileClass)
			}
		})
	}
}

// TestClaudeRunnerStreamNumbersAndJSONContentNotClassifiedは、任意のevent数と
// assistant/tool JSON content内の数値がtransient誤一致しないことを検証する。
// 旧summaryの(stream events=N)形式へ503が露出した場合の回帰固定でもある。
func TestClaudeRunnerStreamNumbersAndJSONContentNotClassified(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}

	jsonEvent := strings.Repeat(`{"type":"system","subtype":"tick"}`+"\n", 503)
	jsonContent := `{"type":"assistant","message":{"model":"glm-5.3","content":[{"type":"thinking","thinking":"wait 503"},{"type":"text","text":"503"},{"type":"tool_use","name":"Bash","input":{"command":"sleep 503"}}],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
	for name, fixture := range map[string]string{"event count 503": jsonEvent, "json content 503": jsonContent} {
		t.Run(name, func(t *testing.T) {
			commandPath := writeStreamFixtureClaude(t, fixture)
			r, st, taskID := newStreamFixtureRunner(t, commandPath)
			outputPath := filepath.Join(t.TempDir(), "out.log")
			result, err := r.Run(state.WorkerRole, "worker-new", "worker-model", false, "high", "prompt", outputPath)
			if err == nil {
				t.Fatal("result eventなしで成功しました")
			}
			if result.PlainFailure != (ProviderFailureClass{}) {
				t.Fatalf("JSON eventのみのPlainFailure = %#v", result.PlainFailure)
			}
			finalOutput, readErr := os.ReadFile(outputPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.Contains(string(finalOutput), "503") || strings.Contains(string(finalOutput), "events=") {
				t.Fatalf("分類入力へ数値が露出しました: %s", finalOutput)
			}
			if class := ClassifyProviderFailureText(ReadTransientSignal(outputPath)); class.Kind != ProviderFailureFatal {
				t.Fatalf("event数・JSON content由来の誤分類 = %#v", class)
			}
			raw, readErr := os.ReadFile(st.TaskEventLogPath(taskID))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.Contains(string(raw), streamFixtureSecret) || strings.Contains(string(raw), `"command":"sleep 503"`) {
				t.Fatalf("event logへcontent本文が混入しました: %s", raw)
			}
		})
	}
}

// TestClaudeRunnerStreamEventsSurviveCorruptTailAndAppendは既存event logの部分破損行が
// 以降の追記・parseを壊さない(破損がその行へ局所化される)ことを検証する。
func TestClaudeRunnerStreamEventsSurviveCorruptTailAndAppend(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}

	commandPath := writeStreamFixtureClaude(t, streamFixtureLines)
	r, st, taskID := newStreamFixtureRunner(t, commandPath)

	if _, err := r.Run(state.WorkerRole, "worker-new", "worker-model", false, "high", "first prompt", filepath.Join(t.TempDir(), "first.log")); err != nil {
		t.Fatal(err)
	}

	path := st.TaskEventLogPath(taskID)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"version":1,"kind":"assistant","blocks":[{"type":"text","text":"trunc`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Run(state.WorkerRole, "worker-new", "worker-model", false, "high", "second prompt", filepath.Join(t.TempDir(), "second.log")); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	parsed, skipped := 0, 0
	var lastRecord state.TaskEventRecord
	for _, line := range lines {
		record, err := state.ParseTaskEventLine([]byte(line))
		if err != nil {
			skipped++
			continue
		}
		parsed++
		lastRecord = record
	}
	// 改行なし破損tailは直後の追記1行と連結して1破損行になり、以後の行は無傷のまま。
	if parsed != 7 || skipped != 1 {
		t.Fatalf("parse可能行 = %d, skip行 = %d", parsed, skipped)
	}
	if lastRecord.Kind != "result" || lastRecord.Seq != 4 || lastRecord.CallID == "" {
		t.Fatalf("破損局所化後の最終行 = %#v", lastRecord)
	}
}

// TestClaudeRunnerStreamEventsBestEffortOnUnwritableLogはevent logへ追記できないときも
// 本体実行・result解析・session readyが成功することを検証する。
func TestClaudeRunnerStreamEventsBestEffortOnUnwritableLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}

	commandPath := writeStreamFixtureClaude(t, streamFixtureLines)
	r, st, taskID := newStreamFixtureRunner(t, commandPath)

	eventsDir := filepath.Join(st.Path("events"))
	if err := os.MkdirAll(eventsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(eventsDir, taskID+".jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(eventsDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chmod(eventsDir, 0o700)
	}()

	outputPath := filepath.Join(t.TempDir(), "out.log")
	result, err := r.Run(state.WorkerRole, "worker-new", "worker-model", false, "high", "prompt", outputPath)
	if err != nil {
		t.Fatalf("event log書込失敗で本taskが失敗しました: %v", err)
	}
	if result.Response != "{\"status\":\"IMPLEMENTED\",\"risk\":\"LOW\",\"summary\":\"done\",\"requirement_coverage\":\"covered\",\"tests\":\"pass\",\"unverified\":\"none\"}" {
		t.Fatalf("response = %q", result.Response)
	}
	if !st.Exists("worker.ready") {
		t.Fatal("event log書込失敗でsession readyが落ちました")
	}
}

// TestReduceStreamEventUnknownLineは未知種別・不正JSON行をcontent無しで観測記録する。
func TestReduceStreamEventUnknownLine(t *testing.T) {
	base := state.TaskEventRecord{TaskID: "t", CallID: "c", Role: "worker", Phase: "worker-new"}
	observedAt := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)

	unknown := reduceStreamEvent([]byte(`{"type":"stream_event","payload":{"secret":"`+streamFixtureSecret+`"}}`), base, 3, observedAt)
	if unknown.Kind != "stream_event" || unknown.Seq != 3 {
		t.Fatalf("unknown event = %#v", unknown)
	}

	invalid := reduceStreamEvent([]byte("not json"), base, 4, observedAt)
	if invalid.Kind != "unknown" || invalid.Seq != 4 {
		t.Fatalf("invalid event = %#v", invalid)
	}
}

// newFakeClockIngesterはevent観測時刻を制御できるingesterを返す。event 1件の記録ごとに
// clockを進め、tool timing対応付けのduration検証を決定的にする。
func newFakeClockIngester(t *testing.T, st *state.StateStore, advance time.Duration) *streamEventIngester {
	t.Helper()
	current := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	ingester := newStreamEventIngester(st, "t", "c", state.WorkerRole, "worker-new", "opus", "sess", false)
	ingester.now = func() time.Time {
		current = current.Add(advance)
		return current
	}
	return ingester
}

// TestStreamEventIngesterSuppressesProgressEventsは実運用で大量観測された
// system/thinking_tokens進捗eventを記録せず、他のsystem subtypeは記録し続けることを
// 検証する。seqは抑止分を消費せず連続する。
func TestStreamEventIngesterSuppressesProgressEvents(t *testing.T) {
	st := newTestStateStore(t)
	ingester := newFakeClockIngester(t, st, time.Second)

	for i := 0; i < 500; i++ {
		if _, err := ingester.Write([]byte("{\"type\":\"system\",\"subtype\":\"thinking_tokens\"}\n")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ingester.Write([]byte("{\"type\":\"system\",\"subtype\":\"init\",\"model\":\"glm-5.3\"}\n")); err != nil {
		t.Fatal(err)
	}
	ingester.flush()

	records := readTaskEventLines(t, st, "t")
	if len(records) != 1 {
		t.Fatalf("記録件数 = %d: %#v", len(records), records)
	}
	if records[0].Kind != "system" || records[0].Subtype != "init" || records[0].Seq != 1 {
		t.Fatalf("init record = %#v", records[0])
	}
}

// TestStreamEventIngesterPairsToolTimingByIDはtool_use idとtool_resultのtool_use_idの
// 正確な対応付けだけを記録し、片側しか観測できない組へdurationを書かないことを検証する。
func TestStreamEventIngesterPairsToolTimingByID(t *testing.T) {
	st := newTestStateStore(t)
	ingester := newFakeClockIngester(t, st, 2*time.Second)

	lines := []string{
		`{"type":"assistant","message":{"model":"glm-5.3","content":[{"type":"tool_use","id":"toolu_ok","name":"Bash","input":{}},{"type":"tool_use","id":"toolu_open","name":"Read","input":{}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_unknown","content":"x"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_ok","content":"y"}]}}`,
		`{"type":"assistant","message":{"model":"glm-5.3","content":[{"type":"tool_use","id":"toolu_open","name":"Read","input":{}}]}}`,
	}
	for _, line := range lines {
		if _, err := ingester.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	ingester.flush()

	records := readTaskEventLines(t, st, "t")
	if len(records) != 4 {
		t.Fatalf("記録件数 = %d", len(records))
	}

	firstUse := records[0].Blocks
	if firstUse[0].ToolID != "toolu_ok" || firstUse[0].DurationMS != 0 || firstUse[1].ToolID != "toolu_open" {
		t.Fatalf("tool_use blocks = %#v", firstUse)
	}

	unmatched := records[1].Blocks[0]
	if unmatched.ToolID != "toolu_unknown" || unmatched.DurationMS != 0 || unmatched.Name != "" {
		t.Fatalf("未対応tool_result = %#v", unmatched)
	}

	paired := records[2].Blocks[0]
	if paired.ToolID != "toolu_ok" || paired.Name != "Bash" {
		t.Fatalf("対応済tool_result = %#v", paired)
	}
	// tool_use観測は1番目のclock呼出(+2s)、tool_result観測は3番目(+6s)で差は2event分=4s。
	if paired.DurationMS != 4000 {
		t.Fatalf("対応済duration = %d", paired.DurationMS)
	}

	reuse := records[3].Blocks[0]
	if reuse.ToolID != "toolu_open" || reuse.DurationMS != 0 {
		t.Fatalf("再登場tool_use = %#v", reuse)
	}
}

// TestStreamEventIngesterCapKeepsResultCaptureは記録上限到達後の追記停止と、
// result event行捕捉の維持を検証する。
func TestStreamEventIngesterCapKeepsResultCapture(t *testing.T) {
	st := newTestStateStore(t)
	ingester := newFakeClockIngester(t, st, 0)
	ingester.seq = maxStreamEventRecordsPerCall - 1

	if _, err := ingester.Write([]byte("{\"type\":\"system\",\"subtype\":\"init\"}\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := ingester.Write([]byte(lineOfKind(streamFixtureLines, "system") + "\n")); err != nil {
		t.Fatal(err)
	}
	resultLine := lineOfKind(streamFixtureLines, "result")
	if _, err := ingester.Write([]byte(resultLine + "\n")); err != nil {
		t.Fatal(err)
	}
	ingester.flush()

	records := readTaskEventLines(t, st, "t")
	if len(records) != 1 || records[0].Subtype != "init" {
		t.Fatalf("上限後の記録件数 = %d: %#v", len(records), records)
	}
	if captured, ok := ingester.result(); !ok || !strings.Contains(string(captured), `"type":"result"`) {
		t.Fatalf("上限後のresult捕捉 = %q / %v", string(captured), ok)
	}
}
