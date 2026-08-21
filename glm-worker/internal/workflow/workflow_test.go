package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/packet"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/runner"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

type runnerStep struct {
	output string
	runErr error
	result runner.RunResult
	// structuredは明示的に指定するstructured_output JSON。空ならoutputの表示行textから
	// 変換する(test前提: どのstepも表示行または生textのどちらかで意図を表現する)。
	structured string
}

type scriptedRunner struct {
	steps     []runnerStep
	probeErrs []error
	// probeResponsesは成功probeの応答本文。未指定indexはsentinelへfall backする。
	probeResponses     []string
	probeBlankResponse bool
	// probeIsErrorはexit 0でis_error=trueの偽陽性probeを再現する。
	probeIsError bool
	// onRun/onProbeは各呼出の実行中にclockを進める試験用hook。
	onRun   func()
	onProbe func()
	prompts []string
	models  []string
	phases  []string
	// readOnlyCallsは各Run呼出へ渡されたcapability flagを記録する。
	readOnlyCalls []bool
	probes        []string
	// artifactFiles/taskArtifactDirはscenarioのartifact packet検証用。step出力の
	// {{ARTIFACT_DIR}}予約tokenを現在taskのartifact dirへ置換し、宣言済みfileを保存する。
	// productionのmodelが委譲時に示されたREPORT_ARTIFACT_DIR配下へ保存する動作の再現。
	artifactFiles   []scenarioArtifact
	taskArtifactDir func() (string, error)
}

func (r *scriptedRunner) Run(
	_ state.SessionRole,
	phase string,
	model string,
	readOnly bool,
	_ string,
	prompt string,
	outputPath string,
) (runner.RunResult, error) {
	r.prompts = append(r.prompts, prompt)
	r.models = append(r.models, model)
	r.phases = append(r.phases, phase)
	r.readOnlyCalls = append(r.readOnlyCalls, readOnly)
	index := len(r.prompts) - 1
	if r.onRun != nil {
		r.onRun()
	}
	step := r.steps[index]
	if r.taskArtifactDir != nil && strings.Contains(step.output, scenarioArtifactDirToken) {
		dir, err := r.taskArtifactDir()
		if err != nil {
			return runner.RunResult{}, err
		}
		for _, af := range r.artifactFiles {
			if err := os.WriteFile(filepath.Join(dir, af.Name), []byte(af.Content), 0o600); err != nil {
				return runner.RunResult{}, err
			}
		}
		step.output = strings.ReplaceAll(step.output, scenarioArtifactDirToken, dir)
	}
	if step.output != "" {
		if err := os.WriteFile(outputPath, []byte(step.output), 0o600); err != nil {
			return runner.RunResult{}, err
		}
	}
	result := step.result
	if result.SessionID == "" {
		result.SessionID = "test-session"
	}
	if step.structured != "" {
		result.StructuredOutput = json.RawMessage(step.structured)
	} else {
		result.StructuredOutput = structuredFromScriptedOutput(step.output)
	}
	if result.Response == "" {
		// productionと同じくresult文字列はstructured outputのJSON表現とする。
		result.Response = string(result.StructuredOutput)
	}
	return result, step.runErr
}

// structuredFromScriptedOutputはscripted stepの表示行textをtyped結果JSONへ変換する。
// 変換できない原文(構造破綻の再現用)はそのまま返し、ParseStructuredのschema-mismatch経路へ流す。
func structuredFromScriptedOutput(output string) json.RawMessage {
	if output == "" {
		return nil
	}
	var body []string
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if strings.TrimSpace(line) == "PACKET_BEGIN" || strings.TrimSpace(line) == "PACKET_END" {
			continue
		}
		body = append(body, line)
	}
	if value, err := packet.FromDisplayLines(body); err == nil {
		if data, err := json.Marshal(value); err == nil {
			return data
		}
	}
	return json.RawMessage(output)
}

func (r *scriptedRunner) Probe(model string) (runner.ProbeResult, error) {
	r.probes = append(r.probes, model)
	index := len(r.probes) - 1
	if r.onProbe != nil {
		r.onProbe()
	}
	var err error
	if index < len(r.probeErrs) {
		err = r.probeErrs[index]
	}
	response := runner.ProbeSentinel
	if r.probeBlankResponse {
		response = ""
	} else if index < len(r.probeResponses) {
		response = r.probeResponses[index]
	}
	return runner.ProbeResult{
		Response:      response,
		IsError:       r.probeIsError,
		Usage:         runner.TokenUsage{InputTokens: 1, OutputTokens: 1},
		ModelUsage:    map[string]runner.ModelUsage{"glm-5.3": {InputTokens: 1, OutputTokens: 1, CostUSD: 0.01}},
		DurationMS:    50,
		DurationAPIMS: 100,
		TotalCostUSD:  0.01,
	}, err
}

func implementedPacket(summary string) string {
	return implementedPacketWithArtifacts(summary, "none")
}

func implementedPacketWithArtifacts(summary string, artifacts string) string {
	return "PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: LOW\nSUMMARY: " + summary + "\nREQUIREMENT_COVERAGE: covered\nTESTS: pass\nUNVERIFIED: none\nARTIFACTS: " + artifacts + "\nPACKET_END\n"
}

func implementedPacketWithRisk(summary string, risk string) string {
	return "PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: " + risk + "\nSUMMARY: " + summary + "\nREQUIREMENT_COVERAGE: covered\nTESTS: pass\nUNVERIFIED: none\nARTIFACTS: none\nPACKET_END\n"
}

func duplicatedImplementedPacket() string {
	return implementedPacket("first") + implementedPacket("second")
}

func passPacket() string {
	return "PACKET_BEGIN\nSTATUS: PASS\nRISK: LOW\nSUMMARY: pass\nREQUIREMENT_COVERAGE: covered\nINVARIANTS: preserved\nTEST_EVIDENCE: ev\nISSUES: none\nRESIDUAL_RISK: none\nTARGETS: final diff\nARTIFACTS: none\nPACKET_END\n"
}

func needsSolReviewPacket() string {
	return "PACKET_BEGIN\nSTATUS: NEEDS_SOL_REVIEW\nRISK: HIGH\nSUMMARY: review\nREQUIREMENT_COVERAGE: covered\nINVARIANTS: preserved\nTEST_EVIDENCE: ev\nISSUES: i\nRESIDUAL_RISK: r\nTARGETS: t\nARTIFACTS: none\nSOL_QUESTION: q\nPACKET_END\n"
}

func needsSolDecisionPacket() string {
	return "PACKET_BEGIN\nSTATUS: NEEDS_SOL_DECISION\nRISK: HIGH\nDECISION: d\nEVIDENCE: e\nOPTIONS: o\nRECOMMENDATION: r\nTEST_OBLIGATIONS: tests\nTARGETS: t\nARTIFACTS: none\nPACKET_END\n"
}

func fixRequiredPacket() string {
	return "PACKET_BEGIN\nSTATUS: FIX_REQUIRED\nRISK: HIGH\nSUMMARY: fix\nREQUIREMENT_COVERAGE: covered\nINVARIANTS: preserved\nTEST_EVIDENCE: ev\nISSUES: i\nRESIDUAL_RISK: r\nTARGETS: t\nARTIFACTS: none\nPACKET_END\n"
}

func fixRequiredPacketWithTargets(targets string) string {
	return "PACKET_BEGIN\nSTATUS: FIX_REQUIRED\nRISK: HIGH\nSUMMARY: fix\nREQUIREMENT_COVERAGE: covered\nINVARIANTS: preserved\nTEST_EVIDENCE: ev\nISSUES: i\nRESIDUAL_RISK: r\nTARGETS: " + targets + "\nARTIFACTS: none\nPACKET_END\n"
}

func unknownStatusPacket() string {
	return "PACKET_BEGIN\nSTATUS: UNKNOWN\nRISK: LOW\nSUMMARY: x\nPACKET_END\n"
}

const zaiFiveHourLog = "API Error: Request rejected (429) · [1308][Usage limit reached for 5 hour. Your limit will reset at 2026-07-22 14:06:34]\n"

var fixedSnapshot = state.GitSnapshot{Head: "test-head", IndexDigest: "test-index", WorktreeDigest: "test-worktree"}

func seedReviewStartSnapshot(t *testing.T, st *state.StateStore) {
	t.Helper()
	if err := st.SaveReviewStartSnapshot(fixedSnapshot); err != nil {
		t.Fatal(err)
	}
}

func newStateStoreT(t *testing.T) *state.StateStore {
	t.Helper()
	st, err := state.NewStateStore(config.AppConfig{
		StateBase: t.TempDir(),
		RepoHash:  "testhash",
		RepoRoot:  "/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	return st
}

var testFixedTime = time.Unix(1_700_000_000, 0).UTC()

// fakeClockは実sleepなしでbackoff scheduleとdeadlineを駆動する試験用clock。
// sleepは即座に現在時刻を進め、待機時間を記録する。
type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: testFixedTime}
}

func (c *fakeClock) nowFunc() time.Time { return c.now }

func (c *fakeClock) sleepFunc(d time.Duration) {
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
}

func newWorkflowT(t *testing.T, st *state.StateStore, r *scriptedRunner) *Workflow {
	t.Helper()
	return newWorkflowTWithOutput(t, st, r, io.Discard)
}

// newWorkflowTWithOutputは親境界へ放出される結果packetを検証するtest用のwire先を渡せる版。
func newWorkflowTWithOutput(t *testing.T, st *state.StateStore, r *scriptedRunner, output io.Writer) *Workflow {
	t.Helper()
	w := NewWorkflow(config.AppConfig{
		WorkerModel:           "opus",
		ReviewerModel:         "haiku",
		HighRiskReviewerModel: "sonnet",
		RoutineEffort:         "high",
		MaxAutoFixRounds:      2,
		TelemetryContent:      true,
		RepoRoot:              t.TempDir(),
	}, st, r, output)
	w.captureSnapshot = func(string) (state.GitSnapshot, error) {
		return fixedSnapshot, nil
	}
	w.collectChangedPaths = func(string, string) ([]string, error) {
		return nil, nil
	}
	clock := newFakeClock()
	w.now = clock.nowFunc
	w.sleep = clock.sleepFunc
	w.jitter = identityJitter
	return w
}

// identityJitterはtest用の決定論jitter。待機時間をそのまま返す。
func identityJitter(base time.Duration) time.Duration { return base }

func TestRunModelRecordsPromptResponseAndUsage(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{{
		output: implementedPacket("done"),
		result: runner.RunResult{
			SessionID: "worker-session",
			TopLevelUsage: runner.TokenUsage{
				InputTokens:          1,
				CacheReadInputTokens: 2,
				OutputTokens:         3,
			},
			ModelUsage: map[string]runner.ModelUsage{
				"glm-5.3": {InputTokens: 10, CacheCreationInputTokens: 20, CacheReadInputTokens: 30, OutputTokens: 40},
				"glm-4.7": {InputTokens: 5, CacheReadInputTokens: 7, OutputTokens: 8},
			},
			DurationMS:    1200,
			DurationAPIMS: 900,
			TopLevelTurns: 2,
			SystemPrompt:  "worker system instruction",
		},
	}}}
	w := newWorkflowT(t, st, r)
	w.temp = t.TempDir()

	_, err := w.runModel(state.ResumeCheckpoint{
		Stage:  state.ResumeStageWorker,
		Phase:  "worker-new",
		Role:   state.WorkerRole,
		Model:  "opus",
		Effort: "high",
		Prompt: "implementation instruction",
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.TaskID()
	if err != nil {
		t.Fatal(err)
	}
	logs, err := st.ReadModelCallLogs(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("telemetry logs = %#v", logs)
	}
	got := logs[0]
	wantResponse := string(structuredFromScriptedOutput(implementedPacket("done")))
	if !strings.HasPrefix(got.Prompt, "implementation instruction\n\nREPORT_ARTIFACT_DIR: ") || got.SystemPrompt != "worker system instruction" || got.Response != wantResponse {
		t.Fatalf("telemetry content = %#v", got)
	}
	if !strings.Contains(r.prompts[0], artifactPromptMarker) {
		t.Fatalf("artifact保存先がrunner promptにありません: %q", r.prompts[0])
	}
	if len(r.phases) != 1 || r.phases[0] != "worker-new" {
		t.Fatalf("runnerへ渡したphase = %#v", r.phases)
	}
	if got.TopLevelUsage.CacheReadInputTokens != 2 || got.TreeUsage.CacheReadInputTokens != 37 || got.ResolvedModelUsage["glm-5.3"].OutputTokens != 40 {
		t.Fatalf("telemetry usage = %#v", got)
	}
	stats := currentStats(t, st)
	if stats.CacheReadInputTokensByAlias["opus"] != 37 || stats.OutputTokensByAlias["opus"] != 48 || stats.OutputTokensByResolvedModel["glm-5.3"] != 40 {
		t.Fatalf("token stats = %#v", stats)
	}
}

func TestRunModelCanOmitTelemetryContent(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{{output: implementedPacket("done")}}}
	w := newWorkflowT(t, st, r)
	w.config.TelemetryContent = false
	w.temp = t.TempDir()

	_, err := w.runModel(state.ResumeCheckpoint{
		Stage:  state.ResumeStageWorker,
		Phase:  "worker-new",
		Role:   state.WorkerRole,
		Model:  "opus",
		Effort: "high",
		Prompt: "secret instruction",
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := st.TaskID()
	logs, err := st.ReadModelCallLogs(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if logs[0].Prompt != "" || logs[0].SystemPrompt != "" || logs[0].Response != "" || logs[0].PromptSHA256 == "" || logs[0].ResponseSHA256 == "" {
		t.Fatalf("content無効時のtelemetry = %#v", logs[0])
	}
}

func currentStats(t *testing.T, st *state.StateStore) state.TaskStats {
	t.Helper()
	all, err := st.AllTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.TaskID()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if s.TaskID == taskID {
			return s
		}
	}
	t.Fatalf("current task stats not found: taskID=%s", taskID)
	return state.TaskStats{}
}

// resultFromLinesは表示行からtyped結果を組み立てるtest helper。
func resultFromLines(lines ...string) packet.Result {
	value, err := packet.FromDisplayLines(lines)
	if err != nil {
		panic(err)
	}
	return value
}

// workerResultFromLinesはresume checkpoint用のworker結果pointerを組み立てる。
func workerResultFromLines(lines ...string) *packet.Result {
	value := resultFromLines(lines...)
	return &value
}

// constraintViolatingImplementedPacketはschema適合・意味検証不合格(必須field欠落)を再現する。
func constraintViolatingImplementedPacket() string {
	return "PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: LOW\nSUMMARY: done\nTESTS: pass\nUNVERIFIED: none\nARTIFACTS: none\nPACKET_END\n"
}

// oversizeImplementedPacketはschema適合・意味検証不合格(size)を再現する。
func oversizeImplementedPacket() string {
	return "PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: LOW\nSUMMARY: " + strings.Repeat("x", packet.MaxFieldBytes+1) + "\nREQUIREMENT_COVERAGE: covered\nTESTS: pass\nUNVERIFIED: none\nARTIFACTS: none\nPACKET_END\n"
}

func TestRunModelCorrectsInvalidResultInSameRunner(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: oversizeImplementedPacket()},
		{output: implementedPacket("implemented")},
	}}
	w := newWorkflowT(t, st, r)
	w.temp = t.TempDir()

	result, err := w.runModel(state.ResumeCheckpoint{
		Stage:   state.ResumeStageWorker,
		Phase:   "worker-new",
		Role:    state.WorkerRole,
		Model:   "opus",
		Effort:  "high",
		Prompt:  "original",
		Request: "request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != packet.StatusImplemented {
		t.Fatalf("status = %q", result.Status)
	}
	if len(r.prompts) != 2 || !strings.Contains(r.prompts[1], "意味検証に不合格") {
		t.Fatalf("same runnerで修正再依頼されていません: %#v", r.prompts)
	}
	if r.phases[1] != "worker-new"+resultCorrectionPhaseSuffix {
		t.Fatalf("修正再依頼phase = %q", r.phases[1])
	}

	stats := currentStats(t, st)
	if stats.ModelCalls != 2 || stats.ResultCorrections != 1 {
		t.Fatalf("stats = %#v", stats)
	}
	taskID, _ := st.TaskID()
	logs, logErr := st.ReadModelCallLogs(taskID)
	if logErr != nil {
		t.Fatal(logErr)
	}
	if len(logs) != 2 || logs[0].Outcome != "invalid_packet" || logs[0].PacketRejectReason != "size" || logs[1].Outcome != "success" {
		t.Fatalf("result correction telemetry = %#v", logs)
	}
}

// schemaが強制する構造は結果objectで保証済みのため、構造破綻textは変換不能=契約ミスマッチとして
// 修正再依頼なしのfail closedへ落ちる。worker/reviewer両roleで同じsemanticsを保証する。
func TestRunModelFailsClosedOnStructuredMismatch(t *testing.T) {
	tests := []struct {
		name     string
		role     state.SessionRole
		stage    state.ResumeStage
		readOnly bool
		phase    string
	}{
		{name: "worker", role: state.WorkerRole, stage: state.ResumeStageWorker, phase: "worker-new"},
		{name: "reviewer", role: state.ReviewerRole, stage: state.ResumeStageReview, readOnly: true, phase: "reviewer-1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st := newStateStoreT(t)
			r := &scriptedRunner{steps: []runnerStep{
				{output: duplicatedImplementedPacket()},
				{output: implementedPacket("unreachable")},
			}}
			w := newWorkflowT(t, st, r)
			w.temp = t.TempDir()

			_, err := w.runModel(state.ResumeCheckpoint{
				Stage:    test.stage,
				Phase:    test.phase,
				Role:     test.role,
				ReadOnly: test.readOnly,
				Model:    "opus",
				Effort:   "high",
				Prompt:   "original",
				Request:  "request",
			})
			if err == nil || !strings.Contains(err.Error(), "STATUS: WORKER_ERROR") {
				t.Fatalf("mismatch fail closedを期待: %v", err)
			}
			if len(r.prompts) != 1 {
				t.Fatalf("mismatchで修正再依頼は実行しない: calls=%d", len(r.prompts))
			}
			taskID, _ := st.TaskID()
			logs, logErr := st.ReadModelCallLogs(taskID)
			if logErr != nil {
				t.Fatal(logErr)
			}
			if len(logs) != 1 || logs[0].Outcome != "invalid_packet" || logs[0].PacketRejectReason != "schema-mismatch" {
				t.Fatalf("structured mismatch telemetry = %#v", logs)
			}
			if !strings.Contains(logs[0].Error, "structured_output") {
				t.Fatalf("拒否理由がtelemetryへ記録されていません: %q", logs[0].Error)
			}
			stats := currentStats(t, st)
			if stats.ResultCorrections != 0 || stats.PacketRejectByCategory["schema-mismatch"] != 1 {
				t.Fatalf("stats = %#v", stats)
			}
		})
	}
}

// StructuredOutputError(retry exhaust・成功時structured_output欠損)はprovider障害ではなく
// 契約破綻のため、transient recoveryへ入らず即座にfail closedする。checkpointを清除し、
// resume前提を残さない。
func TestRunModelFailsClosedOnStructuredOutputError(t *testing.T) {
	cases := []struct {
		name             string
		runErr           error
		want             string
		wantRetryMetrics int
	}{
		{"retry exhausted", &runner.StructuredOutputError{Subtype: "error_max_structured_output_retries", TerminalReason: "gave up after 3 attempts"}, "error_max_structured_output_retries", 1},
		{"missing on success", &runner.StructuredOutputError{}, "result eventにstructured_outputがありません", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newStateStoreT(t)
			r := &scriptedRunner{steps: []runnerStep{
				{output: "", runErr: c.runErr},
				{output: implementedPacket("unreachable")},
			}}
			w := newWorkflowT(t, st, r)
			w.temp = t.TempDir()
			w.config.RepoRoot = "/repo"
			w.config.RepoShort = "testrepo1234"

			_, err := w.runModel(state.ResumeCheckpoint{
				Stage:   state.ResumeStageWorker,
				Phase:   "worker-new",
				Role:    state.WorkerRole,
				Model:   "opus",
				Effort:  "high",
				Prompt:  "p",
				Request: "req",
			})
			if err == nil || !strings.Contains(err.Error(), "STATUS: WORKER_ERROR") || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("structured output fail closedを期待: %v", err)
			}
			if len(r.prompts) != 1 {
				t.Fatalf("transient retryにも修正再依頼にも入らない: calls=%d", len(r.prompts))
			}
			taskID, _ := st.TaskID()
			logs, logErr := st.ReadModelCallLogs(taskID)
			if logErr != nil {
				t.Fatal(logErr)
			}
			if len(logs) != 1 || logs[0].Outcome != "invalid_packet" || logs[0].PacketRejectReason != "structured-output" {
				t.Fatalf("structured output telemetry = %#v", logs)
			}
			if _, cpErr := st.LoadResumeCheckpoint(); cpErr == nil {
				t.Fatal("resume checkpointが残っています")
			}
			stats := currentStats(t, st)
			// structured_retry_exhaustedはretry枯渇だけを数え、成功時structured_output欠損で
			// 汚染しない。欠損側の観測はPacketRejectReason=structured-outputだけが担う。
			if stats.StructuredRetryExhausted != c.wantRetryMetrics || stats.ResultCorrections != 0 {
				t.Fatalf("stats = %#v", stats)
			}
		})
	}
}

func TestExecuteEmitsAcceptedResultExactlyOnce(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: constraintViolatingImplementedPacket()},
		{output: implementedPacketWithRisk("done", "HIGH")},
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)
	buf := &bytes.Buffer{}
	w.output = buf

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(buf.String(), "STATUS: "); got != 1 {
		t.Fatalf("受理結果の出力回数 = %d\n%s", got, buf.String())
	}
	if strings.Contains(buf.String(), "STATUS: PASS") || strings.Contains(buf.String(), "SUMMARY: done") {
		t.Fatalf("旧応答が最終stdoutへ混入しています: %s", buf.String())
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if len(r.prompts) != 4 {
		t.Fatalf("修正再依頼・再出力は各1回だけ: calls=%d", len(r.prompts))
	}
	if strings.Join(r.models, ",") != "opus,opus,sonnet,sonnet" {
		t.Fatalf("models = %#v", r.models)
	}
}

func TestRunModelStopsAfterRepeatedConstraintViolations(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: constraintViolatingImplementedPacket()},
		{output: constraintViolatingImplementedPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.temp = t.TempDir()

	_, err := w.runModel(state.ResumeCheckpoint{
		Stage:   state.ResumeStageWorker,
		Phase:   "worker-new",
		Role:    state.WorkerRole,
		Model:   "opus",
		Effort:  "high",
		Prompt:  "original",
		Request: "request",
	})
	if err == nil || !strings.Contains(err.Error(), "STATUS: WORKER_ERROR") || !strings.Contains(err.Error(), "必須field REQUIREMENT_COVERAGE") {
		t.Fatalf("修正再依頼後の不合格停止を期待: %v", err)
	}
	if len(r.prompts) != 2 {
		t.Fatalf("修正再依頼は1回だけ実施する: calls=%d", len(r.prompts))
	}
}

func TestRunModelPreservesResultCorrectionAcrossRateLimit(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: constraintViolatingImplementedPacket()},
		{output: zaiFiveHourLog, runErr: errors.New("exit status 1")},
	}}
	w := newWorkflowT(t, st, r)
	w.temp = t.TempDir()

	_, err := w.runModel(state.ResumeCheckpoint{
		Stage:          state.ResumeStageWorker,
		Phase:          "worker-new",
		Role:           state.WorkerRole,
		Model:          "opus",
		Effort:         "high",
		Prompt:         "original implementation prompt",
		OriginalPrompt: "original implementation prompt",
		Request:        "request",
	})
	if err == nil || !strings.Contains(err.Error(), "STATUS: RATE_LIMITED") {
		t.Fatalf("修正再依頼中のrate limit errorを期待: %v", err)
	}

	checkpoint, loadErr := st.LoadResumeCheckpoint()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !checkpoint.ResultCorrection || !strings.Contains(checkpoint.OriginalPrompt, "意味検証に不合格") {
		t.Fatalf("修正再依頼promptがcheckpointに保持されていません: %#v", checkpoint)
	}
}

func TestRunModelCorrectsArtifactOutsideTaskDir(t *testing.T) {
	st := newStateStoreT(t)
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithArtifacts("invalid artifact", outside)},
		{output: implementedPacket("corrected")},
	}}
	w := newWorkflowT(t, st, r)
	w.temp = t.TempDir()

	result, err := w.runModel(state.ResumeCheckpoint{
		Stage:   state.ResumeStageWorker,
		Phase:   "worker-new",
		Role:    state.WorkerRole,
		Model:   "opus",
		Effort:  "high",
		Prompt:  "original",
		Request: "request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Artifacts) != 0 || len(r.prompts) != 2 {
		t.Fatalf("artifact pathが修正されていません: result=%#v prompts=%d", result, len(r.prompts))
	}
	taskID, _ := st.TaskID()
	logs, logErr := st.ReadModelCallLogs(taskID)
	if logErr != nil {
		t.Fatal(logErr)
	}
	if len(logs) != 2 || logs[0].Outcome != "invalid_packet" || logs[0].PacketRejectReason != "artifacts" || !strings.Contains(logs[0].Error, "artifact dir配下") {
		t.Fatalf("artifact validation telemetry = %#v", logs)
	}
}

func TestRunModelPreservesResultCorrectionPromptAcrossRateLimit(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: oversizeImplementedPacket()},
		{output: zaiFiveHourLog, runErr: errors.New("exit status 1")},
	}}
	w := newWorkflowT(t, st, r)
	w.temp = t.TempDir()

	_, err := w.runModel(state.ResumeCheckpoint{
		Stage:          state.ResumeStageWorker,
		Phase:          "worker-new",
		Role:           state.WorkerRole,
		Model:          "opus",
		Effort:         "high",
		Prompt:         "original implementation prompt",
		OriginalPrompt: "original implementation prompt",
		Request:        "request",
	})
	if err == nil || !strings.Contains(err.Error(), "STATUS: RATE_LIMITED") {
		t.Fatalf("修正再依頼中のrate limit errorを期待: %v", err)
	}

	checkpoint, loadErr := st.LoadResumeCheckpoint()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !checkpoint.ResultCorrection || !strings.Contains(checkpoint.OriginalPrompt, "意味検証に不合格") {
		t.Fatalf("修正再依頼promptがcheckpointに保持されていません: %#v", checkpoint)
	}
	resumed := resumePrompt(checkpoint)
	if !strings.Contains(resumed, "意味検証に不合格") || strings.Contains(resumed, "original implementation prompt") {
		t.Fatalf("resumeが修正再依頼工程を指していません: %s", resumed)
	}
}

func TestExecuteExplicitFixRejectsCompletedTask(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.Write("last-request", "request"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusComplete); err != nil {
		t.Fatal(err)
	}

	w := newWorkflowT(t, st, &scriptedRunner{})
	err := w.ExecuteExplicitFix("fix")
	if err == nil || !strings.Contains(err.Error(), "only available after NEEDS_SOL_REVIEW") {
		t.Fatalf("completed taskの--fixを拒否する必要があります: %v", err)
	}
}

func TestResumePromptUsesOriginalPrompt(t *testing.T) {
	checkpoint := state.ResumeCheckpoint{
		Prompt:         "already wrapped resume prompt",
		OriginalPrompt: "ORIGINAL TASK",
	}

	prompt := resumePrompt(checkpoint)
	if !strings.Contains(prompt, "ORIGINAL TASK") {
		t.Fatalf("original prompt missing: %s", prompt)
	}
	if strings.Contains(prompt, "already wrapped resume prompt") {
		t.Fatalf("resume prompt nested previous resume prompt: %s", prompt)
	}
}

func TestExecuteNewTaskReachesPass(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if strings.Join(r.models, ",") != "opus,haiku" {
		t.Fatalf("models = %#v", r.models)
	}
	if !strings.Contains(r.prompts[0], artifactPromptMarker) {
		t.Fatalf("worker promptにartifact保存先がありません: %q", r.prompts[0])
	}
	if strings.Contains(r.prompts[1], artifactPromptMarker) {
		t.Fatalf("read-only reviewerへartifact書込指示を渡しています: %q", r.prompts[1])
	}
}

func TestHighRiskWorkerUsesHighRiskReviewer(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithRisk("done", "HIGH")},
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.models, ",") != "opus,sonnet,sonnet" {
		t.Fatalf("models = %#v", r.models)
	}
}

func TestExecuteNewTaskNeedsSolDecision(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: needsSolDecisionPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingDecision {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if !st.Exists("pending-decision") {
		t.Fatal("pending-decisionが設定されていません")
	}
}

func TestExecuteNewTaskNeedsSolReview(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
}

func TestExecuteDecisionContinuesPendingTask(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.Write("last-request", "request"); err != nil {
		t.Fatal(err)
	}
	if err := st.Touch("pending-decision"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusWaitingDecision); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("decision applied")},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteDecision("A案で進める"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview || st.Exists("pending-decision") {
		t.Fatalf("decision後のstate: status=%q pending=%t", st.TaskStatus(), st.Exists("pending-decision"))
	}
	if decision := st.ReadOr("last-decision", ""); decision != "A案で進める" {
		t.Fatalf("last-decision = %q", decision)
	}
	if len(r.prompts) == 0 || !strings.Contains(r.prompts[0], "A案で進める") {
		t.Fatalf("decision prompt = %#v", r.prompts)
	}
	if strings.Join(r.models, ",") != "opus,sonnet" {
		t.Fatalf("models = %#v", r.models)
	}
}

func TestExecuteExplicitFixContinuesSolReviewTask(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.Write("last-request", "request"); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("last-review", "review"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusWaitingSolReview); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("explicit fix")},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteExplicitFix("境界値を修正する"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if len(r.prompts) == 0 || !strings.Contains(r.prompts[0], "境界値を修正する") {
		t.Fatalf("fix prompt = %#v", r.prompts)
	}
	if stats := currentStats(t, st); stats.FixCommands != 1 {
		t.Fatalf("fix stats = %#v", stats)
	}
	if strings.Join(r.models, ",") != "opus,sonnet" {
		t.Fatalf("models = %#v", r.models)
	}
}

func TestAutoFixNonConvergence(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: fixRequiredPacket()},
		{output: implementedPacket("fix")},
		{output: fixRequiredPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.config.MaxAutoFixRounds = 1

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if strings.Join(r.models, ",") != "opus,haiku,opus,sonnet" {
		t.Fatalf("models = %#v", r.models)
	}
}

func TestAutoFixCanRequestSolDecision(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: fixRequiredPacket()},
		{output: needsSolDecisionPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingDecision || !st.Exists("pending-decision") {
		t.Fatalf("auto-fix decision state: status=%q pending=%t", st.TaskStatus(), st.Exists("pending-decision"))
	}
}

func TestAutoFixRejectsReviewerStatus(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: fixRequiredPacket()},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)

	// auto-fix workerがreviewer statusを返すのはrole別schema違反のため、修正再依頼なしの
	// fail closed停止になる。
	err := w.ExecuteNewTask("request")
	if err == nil || !strings.Contains(err.Error(), "STATUS: WORKER_ERROR") || !strings.Contains(err.Error(), "worker結果のstatus") {
		t.Fatalf("auto-fix role status error = %v", err)
	}
	if len(r.prompts) != 3 {
		t.Fatalf("修正再依頼はしない: calls=%d", len(r.prompts))
	}
}

func TestWorkerRejectsReviewerStatus(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{{output: passPacket()}}}
	w := newWorkflowT(t, st, r)

	err := w.ExecuteNewTask("request")
	if err == nil || !strings.Contains(err.Error(), "worker結果のstatusとして許容されません") {
		t.Fatalf("worker role status error = %v", err)
	}
	if len(r.prompts) != 1 {
		t.Fatalf("schema保証範囲のため修正再依頼はしない: calls=%d", len(r.prompts))
	}
}

func TestRunModelSurfacesZaiFiveHourLimit(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{{
		output: zaiFiveHourLog,
		runErr: errors.New("exit status 1"),
	}}}
	w := newWorkflowT(t, st, r)
	w.config.RepoRoot = "/repo"
	w.config.RepoShort = "testrepo1234"
	w.temp = t.TempDir()

	_, err := w.runModel(state.ResumeCheckpoint{
		Stage:   state.ResumeStageWorker,
		Phase:   "worker-new",
		Role:    state.WorkerRole,
		Model:   "opus",
		Effort:  "high",
		Prompt:  "p",
		Request: "req",
	})
	if err == nil || !strings.Contains(err.Error(), "STATUS: RATE_LIMITED") {
		t.Fatalf("rate limit errorを期待: %v", err)
	}
	taskID, taskErr := st.TaskID()
	if taskErr != nil {
		t.Fatal(taskErr)
	}
	for _, value := range []string{
		"TASK_ID: " + taskID,
		"REPO_ROOT: /repo",
		"AUTO_RESUME_AVAILABLE: true",
		"AUTO_RESUME_AT_RFC3339: 2026-07-22T14:08:34+08:00",
		"AUTO_RESUME_KEY: glm-worker-resume-testrepo1234-" + taskID[:8],
	} {
		if !strings.Contains(err.Error(), value) {
			t.Fatalf("rate limit errorに%qがありません: %v", value, err)
		}
	}

	cp, cerr := st.LoadResumeCheckpoint()
	if cerr != nil || !cp.RateLimited {
		t.Fatalf("resume checkpointがrate-limitedで保存されていません: %v", cerr)
	}
	if st.TaskStatus() != state.TaskStatusRateLimited {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	logs, logErr := st.ReadModelCallLogs(taskID)
	if logErr != nil {
		t.Fatal(logErr)
	}
	if len(logs) != 1 || logs[0].Outcome != "rate_limited" {
		t.Fatalf("rate limit telemetry = %#v", logs)
	}
}

// 旧raw fallback相当の分類入力として、result本文が無い経路のplain stdout 5h上限信号が
// outputPath本文へ信号が無くてもrunnerの構造値からrate-limited状態へ到達すること。
func TestRunModelSurfacesPlainStdoutFiveHourLimit(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{{
		runErr: errors.New("exit status 1"),
		result: runner.RunResult{PlainFailure: runner.ProviderFailureClass{
			Kind:          runner.ProviderFailureZaiFiveHour,
			FiveHourLimit: runner.ZaiFiveHourLimit{ResetAtRFC3339: "2026-07-22T14:06:34+08:00"},
		}},
	}}}
	w := newWorkflowT(t, st, r)
	w.config.RepoRoot = "/repo"
	w.config.RepoShort = "testrepo1234"
	w.temp = t.TempDir()

	_, err := w.runModel(state.ResumeCheckpoint{
		Stage:   state.ResumeStageWorker,
		Phase:   "worker-new",
		Role:    state.WorkerRole,
		Model:   "opus",
		Effort:  "high",
		Prompt:  "p",
		Request: "req",
	})
	if err == nil || !strings.Contains(err.Error(), "STATUS: RATE_LIMITED") {
		t.Fatalf("rate limit errorを期待: %v", err)
	}
	if st.TaskStatus() != state.TaskStatusRateLimited {
		t.Fatalf("status = %q", st.TaskStatus())
	}
}

// mergePlainFailureClassは旧classifierの全体一致順序(5h→transient)をfile由来と
// plain由来の統合でも保つ。fatal既定はどちらの上書きもしない。
func TestMergePlainFailureClassPriority(t *testing.T) {
	fiveHour := runner.ProviderFailureClass{Kind: runner.ProviderFailureZaiFiveHour}
	transientFile := runner.ProviderFailureClass{Kind: runner.ProviderFailureTransient, Detail: "network:dial tcp"}
	transientPlain := runner.ProviderFailureClass{Kind: runner.ProviderFailureTransient, Detail: "http-503"}
	fatal := runner.ProviderFailureClass{Kind: runner.ProviderFailureFatal}
	empty := runner.ProviderFailureClass{}

	cases := []struct {
		name  string
		base  runner.ProviderFailureClass
		plain runner.ProviderFailureClass
		want  runner.ProviderFailureClass
	}{
		{"plain 5h over file transient", transientFile, fiveHour, fiveHour},
		{"file 5h over plain transient", fiveHour, transientPlain, fiveHour},
		{"file transient keeps detail", transientFile, transientPlain, transientFile},
		{"plain transient over fatal", fatal, transientPlain, transientPlain},
		{"plain empty keeps file class", transientFile, empty, transientFile},
		{"both empty stays fatal default", fatal, empty, fatal},
	}
	for _, c := range cases {
		if got := mergePlainFailureClass(c.base, c.plain); got != c.want {
			t.Fatalf("%s: merge = %#v, want %#v", c.name, got, c.want)
		}
	}
}

func TestRateLimitStateSurvivesArtifactProtectionError(t *testing.T) {
	st := newStateStoreT(t)
	artifactDir, err := st.PrepareArtifactDir()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(artifactDir, "link")); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{{
		output: zaiFiveHourLog,
		runErr: errors.New("exit status 1"),
	}}}
	w := newWorkflowT(t, st, r)
	w.temp = t.TempDir()

	_, err = w.runModel(state.ResumeCheckpoint{
		Stage:   state.ResumeStageWorker,
		Phase:   "worker-new",
		Role:    state.WorkerRole,
		Model:   "opus",
		Effort:  "high",
		Prompt:  "p",
		Request: "req",
	})
	if err == nil || !strings.Contains(err.Error(), "STATUS: RATE_LIMITED") || !strings.Contains(err.Error(), "ARTIFACT_WARNING:") {
		t.Fatalf("artifact警告付きrate limit errorを期待: %v", err)
	}
	checkpoint, loadErr := st.LoadResumeCheckpoint()
	if loadErr != nil || !checkpoint.RateLimited {
		t.Fatalf("rate-limit checkpointが保存されていません: checkpoint=%#v err=%v", checkpoint, loadErr)
	}
	if st.TaskStatus() != state.TaskStatusRateLimited {
		t.Fatalf("status = %q", st.TaskStatus())
	}
}

func TestExecuteResumeContinuesAfterRateLimit(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:          state.ResumeStageWorker,
		Phase:          "worker-new",
		Role:           state.WorkerRole,
		Model:          "opus",
		Effort:         "high",
		Prompt:         "p",
		OriginalPrompt: "p",
		Request:        "req",
		RateLimited:    true,
		ResetAtCST:     "2026-07-22 14:06:34",
		ResetAtRFC3339: "2026-07-22T14:06:34+08:00",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}

	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("status = %q", st.TaskStatus())
	}
}

func TestExecuteResumeRestoresRateLimitedStatusAfterRunnerError(t *testing.T) {
	st := newStateStoreT(t)
	original := state.ResumeCheckpoint{
		Stage:          state.ResumeStageWorker,
		Phase:          "worker-new",
		Role:           state.WorkerRole,
		Model:          "opus",
		Effort:         "high",
		Prompt:         "p",
		OriginalPrompt: "p",
		Request:        "req",
		RateLimited:    true,
		ResetAtCST:     "2026-07-22 14:06:34",
		ResetAtRFC3339: "2026-07-22T14:06:34+08:00",
	}
	if err := st.SaveResumeCheckpoint(original); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}

	r := &scriptedRunner{steps: []runnerStep{{
		output: "boom fatal session error\n",
		runErr: errors.New("exit status 1"),
	}}}
	w := newWorkflowT(t, st, r)
	err := w.ExecuteResume()
	if err == nil || !strings.Contains(err.Error(), "boom fatal session error") {
		t.Fatalf("runner errorを期待: %v", err)
	}
	if st.TaskStatus() != state.TaskStatusRateLimited {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	restored, loadErr := st.LoadResumeCheckpoint()
	if loadErr != nil || !restored.RateLimited {
		t.Fatalf("rate-limit checkpointが復元されていません: checkpoint=%#v err=%v", restored, loadErr)
	}
	if len(r.prompts) != 1 {
		t.Fatalf("runner calls = %d", len(r.prompts))
	}
}

func TestExecuteResumeContinuesReviewerStage(t *testing.T) {
	st := newStateStoreT(t)
	seedReviewStartSnapshot(t, st)
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:          state.ResumeStageReview,
		Phase:          "reviewer-1",
		Role:           state.ReviewerRole,
		Model:          "sonnet",
		ReadOnly:       true,
		Effort:         "high",
		Prompt:         "review",
		OriginalPrompt: "review",
		Request:        "request",
		WorkerResult: workerResultFromLines(
			"STATUS: IMPLEMENTED",
			"RISK: LOW",
			"SUMMARY: done",
			"REQUIREMENT_COVERAGE: covered",
			"TESTS: pass",
			"UNVERIFIED: none",
			"ARTIFACTS: none",
		),
		ReviewNumber: 1,
		RateLimited:  true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{{output: passPacket()}}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if strings.Join(r.models, ",") != "sonnet" {
		t.Fatalf("resume model = %#v", r.models)
	}
}

func TestExecuteResumeContinuesAutoFixStage(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:          state.ResumeStageAutoFix,
		Phase:          "worker-auto-fix-1",
		Role:           state.WorkerRole,
		Model:          "opus",
		Effort:         "high",
		Prompt:         "fix",
		OriginalPrompt: "fix",
		Request:        "request",
		ReviewNumber:   1,
		AutoFixes:      1,
		RateLimited:    true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("fixed")},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
}

func TestExecuteResumeRejectsUnknownStage(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:          state.ResumeStage("unknown"),
		Phase:          "unknown",
		Role:           state.WorkerRole,
		Model:          "opus",
		Prompt:         "prompt",
		OriginalPrompt: "prompt",
		RateLimited:    true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{{output: implementedPacket("done")}}}
	w := newWorkflowT(t, st, r)

	err := w.ExecuteResume()
	if err == nil || !strings.Contains(err.Error(), "unknown resume stage") {
		t.Fatalf("unknown stage error = %v", err)
	}
	if len(r.prompts) != 0 {
		t.Fatalf("不正stageでrunnerが呼ばれました: calls=%d", len(r.prompts))
	}
	checkpoint, loadErr := st.LoadResumeCheckpoint()
	if loadErr != nil || !checkpoint.RateLimited || checkpoint.Stage != state.ResumeStage("unknown") {
		t.Fatalf("不正stageのcheckpointが保持されていません: checkpoint=%#v err=%v", checkpoint, loadErr)
	}
	if st.TaskStatus() != state.TaskStatusRateLimited {
		t.Fatalf("status = %q", st.TaskStatus())
	}
}

func TestExecuteNewTaskRejectsPendingAndRateLimitedTasks(t *testing.T) {
	t.Run("pending decision", func(t *testing.T) {
		st := newStateStoreT(t)
		if err := st.Touch("pending-decision"); err != nil {
			t.Fatal(err)
		}
		w := newWorkflowT(t, st, &scriptedRunner{})
		err := w.ExecuteNewTask("replacement")
		if err == nil || !strings.Contains(err.Error(), "waiting for Sol decision") {
			t.Fatalf("pending error = %v", err)
		}
	})

	t.Run("rate limited", func(t *testing.T) {
		st := newStateStoreT(t)
		if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{Model: "opus", RateLimited: true}); err != nil {
			t.Fatal(err)
		}
		w := newWorkflowT(t, st, &scriptedRunner{})
		err := w.ExecuteNewTask("replacement")
		if err == nil || !strings.Contains(err.Error(), "rate-limited") {
			t.Fatalf("rate limit error = %v", err)
		}
	})
}

func TestRunModelSurfacesWorkerError(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{{runErr: errors.New("boom")}}}
	w := newWorkflowT(t, st, r)
	w.temp = t.TempDir()

	_, err := w.runModel(state.ResumeCheckpoint{
		Stage:   state.ResumeStageWorker,
		Phase:   "worker-new",
		Role:    state.WorkerRole,
		Model:   "opus",
		Effort:  "high",
		Prompt:  "p",
		Request: "req",
	})
	if err == nil || !strings.Contains(err.Error(), "STATUS: WORKER_ERROR") {
		t.Fatalf("worker errorを期待: %v", err)
	}
	if _, cerr := st.LoadResumeCheckpoint(); cerr == nil {
		t.Fatal("resume checkpointはクリアされる必要があります")
	}
}

func TestRunModelRejectsMissingModelBeforeRunnerCall(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{}
	w := newWorkflowT(t, st, r)
	w.temp = t.TempDir()

	_, err := w.runModel(state.ResumeCheckpoint{
		Stage:  state.ResumeStageWorker,
		Phase:  "worker-new",
		Role:   state.WorkerRole,
		Prompt: "p",
	})
	if err == nil || !strings.Contains(err.Error(), "checkpoint model is missing") {
		t.Fatalf("missing model error = %v", err)
	}
	if len(r.prompts) != 0 {
		t.Fatalf("model未指定でrunnerが呼ばれました: calls=%d", len(r.prompts))
	}
}

func TestReviewerFormatError(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: needsSolDecisionPacket()},
	}}
	w := newWorkflowT(t, st, r)

	err := w.ExecuteNewTask("request")
	if err == nil || !strings.Contains(err.Error(), "reviewer結果のstatusとして許容されません") {
		t.Fatalf("reviewer role status error = %v", err)
	}
	if len(r.prompts) != 2 {
		t.Fatalf("schema保証範囲のため修正再依頼はしない: calls=%d", len(r.prompts))
	}
}

func TestReviewerUnknownStatusFailsClosedWithoutCorrection(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: unknownStatusPacket()},
	}}
	w := newWorkflowT(t, st, r)

	err := w.ExecuteNewTask("request")
	if err == nil || !strings.Contains(err.Error(), "STATUS: WORKER_ERROR") {
		t.Fatalf("未知STATUSのfail closed停止を期待: %v", err)
	}
	if len(r.prompts) != 2 {
		t.Fatalf("schema保証範囲のため修正再依頼はしない: calls=%d", len(r.prompts))
	}
}

func TestReviewNeedsHighRiskFloor(t *testing.T) {
	lowWorker := resultFromLines("STATUS: IMPLEMENTED", "RISK: LOW")
	highWorker := resultFromLines("STATUS: IMPLEMENTED", "RISK: HIGH")

	tests := []struct {
		name           string
		workerResult   packet.Result
		autoFixes      int
		hasDecision    bool
		hasPriorReview bool
		want           bool
	}{
		{"low worker fresh", lowWorker, 0, false, false, false},
		{"high worker", highWorker, 0, false, false, true},
		{"after autofix", lowWorker, 1, false, false, true},
		{"after decision", lowWorker, 0, true, false, true},
		{"after prior review", lowWorker, 0, false, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reviewNeedsHighRiskFloor(tt.workerResult, tt.autoFixes, tt.hasDecision, tt.hasPriorReview); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestRiskFloorFailClosedPacketIsValid(t *testing.T) {
	passPkt := resultFromLines(
		"STATUS: PASS",
		"RISK: LOW",
		"SUMMARY: reviewer pass",
		"REQUIREMENT_COVERAGE: covered",
		"INVARIANTS: preserved",
		"TEST_EVIDENCE: ev",
		"ISSUES: none",
		"RESIDUAL_RISK: none",
		"TARGETS: none",
		"ARTIFACTS: none",
	)

	enforced := riskFloorFailClosedResult(passPkt)
	if enforced.Status != packet.StatusNeedsSolReview || enforced.Risk != packet.RiskHigh {
		t.Fatalf("status=%s risk=%s", enforced.Status, enforced.Risk)
	}
	if err := validateTypedResult(enforced); err != nil {
		t.Fatalf("fail closed結果がvalidate不合格: %v", err)
	}
	if enforced.RequirementCoverage == "covered" {
		t.Fatalf("reviewerのPASS内容をfail closed結果へ捏造している: %#v", enforced)
	}
}

func TestResolveRiskFloorReemitAcceptsCompliantAndFailsClosed(t *testing.T) {
	compliant := resultFromLines(
		"STATUS: NEEDS_SOL_REVIEW",
		"RISK: HIGH",
		"SUMMARY: reviewer reemit",
		"REQUIREMENT_COVERAGE: covered",
		"INVARIANTS: preserved",
		"TEST_EVIDENCE: ev",
		"ISSUES: i",
		"RESIDUAL_RISK: r",
		"TARGETS: t",
		"ARTIFACTS: none",
		"SOL_QUESTION: q",
	)
	if resolved := resolveRiskFloorReemit(compliant); resolved.Status != packet.StatusNeedsSolReview {
		t.Fatalf("準拠再出力はそのまま採用すべき: %#v", resolved)
	}

	passed := resultFromLines(
		"STATUS: PASS",
		"RISK: LOW",
		"SUMMARY: pass again",
		"REQUIREMENT_COVERAGE: covered",
		"INVARIANTS: preserved",
		"TEST_EVIDENCE: ev",
		"ISSUES: none",
		"RESIDUAL_RISK: none",
		"TARGETS: none",
		"ARTIFACTS: none",
	)
	closed := resolveRiskFloorReemit(passed)
	if closed.Status != packet.StatusNeedsSolReview || !strings.Contains(closed.Summary, "PASS") {
		t.Fatalf("再違反はfail closedのNEEDS_SOL_REVIEWへ昇格すべき: %#v", closed)
	}
}

func TestRiskFloorReemitPromptConstraints(t *testing.T) {
	prompt := riskFloorReemitPrompt()
	for _, want := range []string{
		"NEEDS_SOL_REVIEW (RISK: HIGH) だけ",
		"実装・調査・テストをやり直さず",
		"結果だけを再出力",
		"TARGETSにはnoneを指定できません",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("reemit promptに%qがありません: %s", want, prompt)
		}
	}
}

func TestFixRequiredTargetsPacketDispatchesReportOnlyPrompt(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithRisk("high risk work", "HIGH")},
		{output: fixRequiredPacketWithTargets("PACKET")},
		{output: implementedPacketWithRisk("report re-emitted", "HIGH")},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q want waiting-sol-review", st.TaskStatus())
	}
	if got, want := strings.Join(r.models, ","), "opus,sonnet,opus,sonnet"; got != want {
		t.Fatalf("model routing = %q want %q", got, want)
	}
	if len(r.prompts) != 4 {
		t.Fatalf("prompt count = %d want 4", len(r.prompts))
	}
	reportOnly := r.prompts[2]
	for _, want := range []string{
		"報告だけを再出力",
		"実装・working tree変更・追加調査・test/lint/build/self-reviewをやり直さず",
	} {
		if !strings.Contains(reportOnly, want) {
			t.Fatalf("report-only promptに%qがありません: %s", want, reportOnly)
		}
	}
	for _, forbidden := range []string{
		"独立reviewerの指摘を修正してください",
		"修正後に必要なテスト・lint・build・自己レビューまで行ってください",
	} {
		if strings.Contains(reportOnly, forbidden) {
			t.Fatalf("report-only promptにimplementation fix文言%qが入っています: %s", forbidden, reportOnly)
		}
	}
	var reportOnlyPhases []string
	for _, l := range taskLogs(t, st) {
		if l.CallType == state.CallTypeTask {
			reportOnlyPhases = append(reportOnlyPhases, l.Phase)
		}
	}
	if !slices.Contains(reportOnlyPhases, "worker-report-only-1") {
		t.Fatalf("telemetryへreport-only phaseが識別できません: %v", reportOnlyPhases)
	}
}

func TestFixRequiredOtherTargetsKeepsImplementationAutoFix(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithRisk("high risk work", "HIGH")},
		{output: fixRequiredPacketWithTargets("glm-worker/internal/state/store.go:Read")},
		{output: implementedPacketWithRisk("fixed implementation", "HIGH")},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q want waiting-sol-review", st.TaskStatus())
	}
	implementation := r.prompts[2]
	for _, want := range []string{
		"独立reviewerの指摘を修正してください",
		"修正後に必要なテスト・lint・build・自己レビューまで行ってください",
	} {
		if !strings.Contains(implementation, want) {
			t.Fatalf("implementation auto-fix promptから%qが失われています: %s", want, implementation)
		}
	}
	if strings.Contains(implementation, "報告だけを再出力") {
		t.Fatalf("通常FIX_REQUIREDへreport-only promptが使われています: %s", implementation)
	}
	var phases []string
	for _, l := range taskLogs(t, st) {
		if l.CallType == state.CallTypeTask {
			phases = append(phases, l.Phase)
		}
	}
	if !slices.Contains(phases, "worker-auto-fix-1") {
		t.Fatalf("telemetryへimplementation auto-fix phaseがありません: %v", phases)
	}
}

// TARGETSなしFIX_REQUIREDはauto-fix promptへ修正対象を伝えられないため、
// 意味検証不合格として同一sessionの修正再依頼へ置き換わり、targets付き再出力だけが
// 通常auto-fixへdispatchする。旧protocolのstatus別必須field契約復元のproduction因果。
func TestFixRequiredWithoutTargetsCorrectsBeforeAutoFix(t *testing.T) {
	st := newStateStoreT(t)
	fixWithoutTargets := `{"status":"FIX_REQUIRED","risk":"HIGH","summary":"fix","requirement_coverage":"covered","invariants":"preserved","test_evidence":"ev","issues":"i","residual_risk":"r","targets":[],"artifacts":[]}`
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithRisk("high risk work", "HIGH")},
		{structured: fixWithoutTargets},
		{output: fixRequiredPacketWithTargets("glm-worker/internal/state/store.go:Read")},
		{output: implementedPacketWithRisk("fixed implementation", "HIGH")},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	wantPhases := []string{"worker-new", "reviewer-1", "reviewer-1" + resultCorrectionPhaseSuffix, "worker-auto-fix-1", "reviewer-2"}
	if strings.Join(r.phases, ",") != strings.Join(wantPhases, ",") {
		t.Fatalf("phases = %v, want %v", r.phases, wantPhases)
	}
	if !strings.Contains(r.prompts[2], "意味検証に不合格") {
		t.Fatalf("修正再依頼promptが選ばれていません: %s", r.prompts[2])
	}
	autoFix := r.prompts[3]
	if !strings.Contains(autoFix, "TARGETS: glm-worker/internal/state/store.go:Read") {
		t.Fatalf("auto-fix promptへ修正対象targetsが伝わっていません: %s", autoFix)
	}
	for i, prompt := range r.prompts {
		if i != 3 && strings.Contains(prompt, "MODE: APPLY_REVIEW_FIX") {
			t.Fatalf("targets未確定のFIX_REQUIREDがauto-fixへdispatchしています: prompt %d", i)
		}
	}
	taskID, _ := st.TaskID()
	logs, logErr := st.ReadModelCallLogs(taskID)
	if logErr != nil {
		t.Fatal(logErr)
	}
	if len(logs) != 5 || logs[1].Outcome != "invalid_packet" || logs[1].PacketRejectReason != "targets-none" {
		t.Fatalf("telemetry = %#v", logs)
	}
	stats := currentStats(t, st)
	if stats.ResultCorrections != 1 {
		t.Fatalf("result corrections = %d, stats = %#v", stats.ResultCorrections, stats)
	}
}

// TARGETSなしNEEDS_SOL_DECISIONはSolが現物確認すべき対象を失うため、pending-decisionへの
// 遷移と親境界への結果放出より前に、意味検証不合格として同一sessionの修正再依頼へ置き換わる。
// targets付き再出力だけが親へ渡る。旧protocolのworker status別必須field契約復元のproduction因果。
func TestNeedsSolDecisionWithoutTargetsCorrectsBeforeParentDispatch(t *testing.T) {
	st := newStateStoreT(t)
	decisionWithoutTargets := `{"status":"NEEDS_SOL_DECISION","risk":"HIGH","decision":"d","evidence":"e","options":"o","recommendation":"r","test_obligations":"t","targets":[],"artifacts":[]}`
	r := &scriptedRunner{steps: []runnerStep{
		{structured: decisionWithoutTargets},
		{output: needsSolDecisionPacket()},
	}}
	var emitted bytes.Buffer
	w := newWorkflowTWithOutput(t, st, r, &emitted)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	wantPhases := []string{"worker-new", "worker-new" + resultCorrectionPhaseSuffix}
	if strings.Join(r.phases, ",") != strings.Join(wantPhases, ",") {
		t.Fatalf("phases = %v, want %v", r.phases, wantPhases)
	}
	if !strings.Contains(r.prompts[1], "意味検証に不合格") {
		t.Fatalf("修正再依頼promptが選ばれていません: %s", r.prompts[1])
	}
	if st.TaskStatus() != state.TaskStatusWaitingDecision || !st.Exists("pending-decision") {
		t.Fatalf("decision待ち状態へ遷移していません: status=%q pending=%v", st.TaskStatus(), st.Exists("pending-decision"))
	}
	if !strings.Contains(emitted.String(), "STATUS: NEEDS_SOL_DECISION") || !strings.Contains(emitted.String(), "TARGETS: t") {
		t.Fatalf("親境界の結果packetへ修正後targetsが伝わっていません: %s", emitted.String())
	}
	taskID, _ := st.TaskID()
	logs, logErr := st.ReadModelCallLogs(taskID)
	if logErr != nil {
		t.Fatal(logErr)
	}
	if len(logs) != 2 || logs[0].Outcome != "invalid_packet" || logs[0].PacketRejectReason != "targets-none" {
		t.Fatalf("telemetry = %#v", logs)
	}
	if stats := currentStats(t, st); stats.ResultCorrections != 1 {
		t.Fatalf("result corrections = %d, stats = %#v", stats.ResultCorrections, stats)
	}
}

func TestRiskFloorRejectsPassOnHighRiskWorker(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithRisk("high risk work", "HIGH")},
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("HIGH risk workerへのreviewer PASSを拒否すべき: status=%q", st.TaskStatus())
	}
	review := st.ReadOr("last-review", "")
	if !strings.Contains(review, "STATUS: NEEDS_SOL_REVIEW") || !strings.Contains(review, "RISK: HIGH") {
		t.Fatalf("risk floor強制packetでない: %s", review)
	}
	if strings.Contains(review, "STATUS: PASS") {
		t.Fatalf("PASSが通っている: %s", review)
	}
	if !strings.Contains(review, "SUMMARY: review") {
		t.Fatalf("reviewer自身の再出力NEEDS_SOL_REVIEWを採用すべき(捏造でない): %s", review)
	}
	if len(r.prompts) != 3 || !strings.Contains(r.prompts[2], "NEEDS_SOL_REVIEW (RISK: HIGH) だけ") {
		t.Fatalf("同一sessionへ再出力promptを送るべき: %#v", r.prompts)
	}
	if strings.Join(r.models, ",") != "opus,sonnet,sonnet" {
		t.Fatalf("再出力もHighRiskReviewerModelを使うべき: %#v", r.models)
	}
}

func TestRiskFloorRejectsPassAfterDecision(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.Write("last-request", "request"); err != nil {
		t.Fatal(err)
	}
	if err := st.Touch("pending-decision"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusWaitingDecision); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithRisk("decision applied", "LOW")},
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteDecision("A案で進める"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("decision後のreviewer PASSを拒否すべき: status=%q", st.TaskStatus())
	}
	review := st.ReadOr("last-review", "")
	if !strings.Contains(review, "STATUS: NEEDS_SOL_REVIEW") || !strings.Contains(review, "RISK: HIGH") {
		t.Fatalf("risk floor強制packetでない: %s", review)
	}
	if !strings.Contains(review, "SUMMARY: review") {
		t.Fatalf("reviewer自身の再出力を採用すべき: %s", review)
	}
	if strings.Join(r.models, ",") != "opus,sonnet,sonnet" {
		t.Fatalf("models = %#v", r.models)
	}
}

func TestRiskFloorRejectsPassAfterAutoFix(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: fixRequiredPacket()},
		{output: implementedPacket("fixed")},
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("auto-fix後のreviewer PASSを拒否すべき: status=%q", st.TaskStatus())
	}
	review := st.ReadOr("last-review", "")
	if !strings.Contains(review, "STATUS: NEEDS_SOL_REVIEW") || !strings.Contains(review, "RISK: HIGH") {
		t.Fatalf("risk floor強制packetでない: %s", review)
	}
	if !strings.Contains(review, "SUMMARY: review") {
		t.Fatalf("reviewer自身の再出力を採用すべき: %s", review)
	}
	if strings.Join(r.models, ",") != "opus,haiku,opus,sonnet,sonnet" {
		t.Fatalf("models = %#v", r.models)
	}
}

func TestRiskFloorRejectsPassAfterExplicitFix(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.Write("last-request", "request"); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("last-review", "previous review"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusWaitingSolReview); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("explicit fix")},
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteExplicitFix("境界値を修正する"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("explicit fix後のreviewer PASSを拒否すべき: status=%q", st.TaskStatus())
	}
	review := st.ReadOr("last-review", "")
	if !strings.Contains(review, "STATUS: NEEDS_SOL_REVIEW") || !strings.Contains(review, "RISK: HIGH") {
		t.Fatalf("risk floor強制packetでない: %s", review)
	}
	if !strings.Contains(review, "SUMMARY: review") {
		t.Fatalf("reviewer自身の再出力を採用すべき: %s", review)
	}
	if strings.Join(r.models, ",") != "opus,sonnet,sonnet" {
		t.Fatalf("models = %#v", r.models)
	}
}

func TestRiskFloorRejectsPassAfterResume(t *testing.T) {
	st := newStateStoreT(t)
	seedReviewStartSnapshot(t, st)
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:          state.ResumeStageReview,
		Phase:          "reviewer-1",
		Role:           state.ReviewerRole,
		Model:          "sonnet",
		ReadOnly:       true,
		Effort:         "high",
		Prompt:         "review",
		OriginalPrompt: "review",
		Request:        "request",
		WorkerResult: workerResultFromLines(
			"STATUS: IMPLEMENTED",
			"RISK: HIGH",
			"SUMMARY: done",
			"REQUIREMENT_COVERAGE: covered",
			"TESTS: pass",
			"UNVERIFIED: none",
			"ARTIFACTS: none",
		),
		ReviewNumber: 1,
		RateLimited:  true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("resume後のreviewer PASSを拒否すべき: status=%q", st.TaskStatus())
	}
	review := st.ReadOr("last-review", "")
	if !strings.Contains(review, "STATUS: NEEDS_SOL_REVIEW") || !strings.Contains(review, "RISK: HIGH") {
		t.Fatalf("risk floor強制packetでない: %s", review)
	}
	if !strings.Contains(review, "SUMMARY: review") {
		t.Fatalf("reviewer自身の再出力を採用すべき: %s", review)
	}
	if strings.Join(r.models, ",") != "sonnet,sonnet" {
		t.Fatalf("resume後のreviewと再出力models = %#v", r.models)
	}
}

func TestRiskFloorAllowsLowRiskPass(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("LOW risk通常PASSは完遂すべき: status=%q", st.TaskStatus())
	}
	review := st.ReadOr("last-review", "")
	if !strings.Contains(review, "STATUS: PASS") || !strings.Contains(review, "RISK: LOW") {
		t.Fatalf("PASS/LOWが保持されるべき: %s", review)
	}
	if strings.Join(r.models, ",") != "opus,haiku" {
		t.Fatalf("通常ReviewerModelを使うべき: %#v", r.models)
	}
}

func TestRiskFloorReemitFailClosedOnRepeatedPass(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithRisk("high risk work", "HIGH")},
		{output: passPacket()},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("再違反時はfail closedでSol確認待ちへ: status=%q", st.TaskStatus())
	}
	review := st.ReadOr("last-review", "")
	if !strings.Contains(review, "STATUS: NEEDS_SOL_REVIEW") || !strings.Contains(review, "RISK: HIGH") {
		t.Fatalf("fail closed packetでない: %s", review)
	}
	if !strings.Contains(review, "PASS") {
		t.Fatalf("再違反のfail closed summaryは非許容STATUSを明示すべき: %s", review)
	}
	if strings.Contains(review, "REQUIREMENT_COVERAGE: covered") {
		t.Fatalf("reviewerのPASS内容を捏造してはいけない: %s", review)
	}
	if len(r.prompts) != 3 {
		t.Fatalf("再出力は1回だけ行い無限反復しない: calls=%d", len(r.prompts))
	}
	if _, err := st.LoadResumeCheckpoint(); err == nil {
		t.Fatal("fail closed後はresume checkpointを残さない")
	}
}

func TestRiskFloorReemitResumeCompliant(t *testing.T) {
	st := newStateStoreT(t)
	seedReviewStartSnapshot(t, st)
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:           state.ResumeStageReview,
		Phase:           "reviewer-1-risk-floor",
		Role:            state.ReviewerRole,
		Model:           "sonnet",
		ReadOnly:        true,
		Effort:          "high",
		Prompt:          "reemit",
		OriginalPrompt:  "reemit",
		Request:         "request",
		WorkerResult:    workerResultFromLines("STATUS: IMPLEMENTED", "RISK: HIGH", "SUMMARY: done", "REQUIREMENT_COVERAGE: covered", "TESTS: pass", "UNVERIFIED: none", "ARTIFACTS: none"),
		ReviewNumber:    1,
		RateLimited:     true,
		RiskFloorReemit: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{{output: needsSolReviewPacket()}}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("再出力resumeの準拠結果はSol確認待ちへ: status=%q", st.TaskStatus())
	}
	review := st.ReadOr("last-review", "")
	if !strings.Contains(review, "SUMMARY: review") {
		t.Fatalf("reviewer自身の再出力NEEDS_SOL_REVIEWを採用すべき: %s", review)
	}
	if len(r.prompts) != 1 || !strings.Contains(r.prompts[0], "再開") {
		t.Fatalf("再出力工程からresume再開すべき: %#v", r.prompts)
	}
	if len(r.models) != 1 || r.models[0] != "sonnet" {
		t.Fatalf("models = %#v", r.models)
	}
}

func TestRiskFloorReemitResumeFailClosed(t *testing.T) {
	st := newStateStoreT(t)
	seedReviewStartSnapshot(t, st)
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:           state.ResumeStageReview,
		Phase:           "reviewer-1-risk-floor",
		Role:            state.ReviewerRole,
		Model:           "sonnet",
		ReadOnly:        true,
		Effort:          "high",
		Prompt:          "reemit",
		OriginalPrompt:  "reemit",
		Request:         "request",
		WorkerResult:    workerResultFromLines("STATUS: IMPLEMENTED", "RISK: HIGH", "SUMMARY: done", "REQUIREMENT_COVERAGE: covered", "TESTS: pass", "UNVERIFIED: none", "ARTIFACTS: none"),
		ReviewNumber:    1,
		RateLimited:     true,
		RiskFloorReemit: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{{output: passPacket()}}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("再出力resumeの再違反もfail closedでSol確認待ちへ: status=%q", st.TaskStatus())
	}
	review := st.ReadOr("last-review", "")
	if !strings.Contains(review, "STATUS: NEEDS_SOL_REVIEW") || !strings.Contains(review, "PASS") {
		t.Fatalf("fail closed packetでない: %s", review)
	}
	if strings.Contains(review, "REQUIREMENT_COVERAGE: covered") {
		t.Fatalf("reviewerのPASS内容を捏造してはいけない: %s", review)
	}
	if len(r.prompts) != 1 {
		t.Fatalf("再出力resume後は追加呼出しない: calls=%d", len(r.prompts))
	}
}
