package workflow

import (
	"bytes"
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
	probes  []string
	// artifactFiles/taskArtifactDirはscenarioのartifact packet検証用。step出力の
	// {{ARTIFACT_DIR}}予約tokenを現在taskのartifact dirへ置換し、宣言済みfileを保存する。
	// productionのmodelが委譲時に示されたREPORT_ARTIFACT_DIR配下へ保存する動作の再現。
	artifactFiles   []scenarioArtifact
	taskArtifactDir func() (string, error)
}

func (r *scriptedRunner) Run(
	_ state.SessionRole,
	model string,
	_ bool,
	_ string,
	prompt string,
	outputPath string,
) (runner.RunResult, error) {
	r.prompts = append(r.prompts, prompt)
	r.models = append(r.models, model)
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
	if result.Response == "" {
		result.Response = step.output
	}
	return result, step.runErr
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
	return "PACKET_BEGIN\nSTATUS: PASS\nRISK: LOW\nSUMMARY: pass\nREQUIREMENT_COVERAGE: covered\nINVARIANTS: preserved\nTEST_EVIDENCE: ev\nISSUES: none\nRESIDUAL_RISK: none\nTARGETS: none\nARTIFACTS: none\nPACKET_END\n"
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
	w := NewWorkflow(config.AppConfig{
		WorkerModel:           "opus",
		ReviewerModel:         "haiku",
		HighRiskReviewerModel: "sonnet",
		RoutineEffort:         "high",
		MaxAutoFixRounds:      2,
		TelemetryContent:      true,
	}, st, r, io.Discard)
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
	if !strings.HasPrefix(got.Prompt, "implementation instruction\n\nREPORT_ARTIFACT_DIR: ") || got.SystemPrompt != "worker system instruction" || got.Response != implementedPacket("done") {
		t.Fatalf("telemetry content = %#v", got)
	}
	if !strings.Contains(r.prompts[0], artifactPromptMarker) {
		t.Fatalf("artifact保存先がrunner promptにありません: %q", r.prompts[0])
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

func TestRunModelRecompactsInvalidPacketInSameRunner(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: "PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: LOW\nSUMMARY: " + strings.Repeat("x", packet.MaxPacketLineBytes+1) + "\nREQUIREMENT_COVERAGE: covered\nTESTS: pass\nUNVERIFIED: none\nARTIFACTS: none\nPACKET_END\n"},
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
	if result.Status() != "IMPLEMENTED" {
		t.Fatalf("status = %q", result.Status())
	}
	if len(r.prompts) != 2 || !strings.Contains(r.prompts[1], "再圧縮") {
		t.Fatalf("same runnerで再圧縮されていません: %#v", r.prompts)
	}

	stats := currentStats(t, st)
	if stats.ModelCalls != 2 || stats.PacketCompactions != 1 {
		t.Fatalf("stats = %#v", stats)
	}
	taskID, _ := st.TaskID()
	logs, logErr := st.ReadModelCallLogs(taskID)
	if logErr != nil {
		t.Fatal(logErr)
	}
	if len(logs) != 2 || logs[0].Outcome != "invalid_packet" || logs[1].Outcome != "success" {
		t.Fatalf("packet compaction telemetry = %#v", logs)
	}
}

func TestRunModelRecompactsMultipleCompletedPackets(t *testing.T) {
	tests := []struct {
		name     string
		role     state.SessionRole
		stage    state.ResumeStage
		readOnly bool
		phase    string
		follow   string
		status   string
	}{
		{name: "worker", role: state.WorkerRole, stage: state.ResumeStageWorker, phase: "worker-new", follow: implementedPacket("compacted"), status: "IMPLEMENTED"},
		{name: "reviewer", role: state.ReviewerRole, stage: state.ResumeStageReview, readOnly: true, phase: "reviewer-1", follow: needsSolReviewPacket(), status: "NEEDS_SOL_REVIEW"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st := newStateStoreT(t)
			r := &scriptedRunner{steps: []runnerStep{
				{output: duplicatedImplementedPacket()},
				{output: test.follow},
			}}
			w := newWorkflowT(t, st, r)
			w.temp = t.TempDir()

			result, err := w.runModel(state.ResumeCheckpoint{
				Stage:    test.stage,
				Phase:    test.phase,
				Role:     test.role,
				ReadOnly: test.readOnly,
				Model:    "opus",
				Effort:   "high",
				Prompt:   "original",
				Request:  "request",
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status() != test.status {
				t.Fatalf("status = %q want %q", result.Status(), test.status)
			}
			if len(r.prompts) != 2 || !strings.Contains(r.prompts[1], "再圧縮") {
				t.Fatalf("same runnerで再圧縮されていません: %#v", r.prompts)
			}
			taskID, _ := st.TaskID()
			logs, logErr := st.ReadModelCallLogs(taskID)
			if logErr != nil {
				t.Fatal(logErr)
			}
			if len(logs) != 2 || logs[0].Outcome != "invalid_packet" || logs[0].PacketRejectReason != "multiple-packets" {
				t.Fatalf("multiple packet reject telemetry = %#v", logs)
			}
			if !strings.Contains(logs[0].Error, "複数検出") {
				t.Fatalf("拒否理由がtelemetryへ記録されていません: %q", logs[0].Error)
			}
			stats := currentStats(t, st)
			if stats.PacketCompactions != 1 || stats.PacketRejectByCategory["multiple-packets"] != 1 {
				t.Fatalf("stats = %#v", stats)
			}
		})
	}
}

func TestExecuteEmitsAcceptedPacketExactlyOnce(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: duplicatedImplementedPacket()},
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
		t.Fatalf("受理packetの出力回数 = %d\n%s", got, buf.String())
	}
	if strings.Contains(buf.String(), "STATUS: PASS") || strings.Contains(buf.String(), "SUMMARY: first") || strings.Contains(buf.String(), "SUMMARY: second") {
		t.Fatalf("旧応答が最終stdoutへ混入しています: %s", buf.String())
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if len(r.prompts) != 4 {
		t.Fatalf("再圧縮・再出力は各1回だけ: calls=%d", len(r.prompts))
	}
	if strings.Join(r.models, ",") != "opus,opus,sonnet,sonnet" {
		t.Fatalf("models = %#v", r.models)
	}
}

func TestRunModelStopsAfterRepeatedMultiplePackets(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: duplicatedImplementedPacket()},
		{output: duplicatedImplementedPacket()},
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
	if err == nil || !strings.Contains(err.Error(), "STATUS: WORKER_ERROR") || !strings.Contains(err.Error(), "複数検出") {
		t.Fatalf("再圧縮後の複数packet停止を期待: %v", err)
	}
	if len(r.prompts) != 2 {
		t.Fatalf("再圧縮は1回だけ実施する: calls=%d", len(r.prompts))
	}
}

func TestRunModelPreservesMultiPacketRecompactAcrossRateLimit(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: duplicatedImplementedPacket()},
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
		t.Fatalf("再圧縮中のrate limit errorを期待: %v", err)
	}

	checkpoint, loadErr := st.LoadResumeCheckpoint()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !checkpoint.PacketCompacted || !strings.Contains(checkpoint.OriginalPrompt, "PACKETだけを再出力") {
		t.Fatalf("再圧縮promptがcheckpointに保持されていません: %#v", checkpoint)
	}
}

func TestRunModelRecompactsArtifactOutsideTaskDir(t *testing.T) {
	st := newStateStoreT(t)
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithArtifacts("invalid artifact", outside)},
		{output: implementedPacket("compacted")},
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
	if result.Fields["ARTIFACTS"] != "none" || len(r.prompts) != 2 {
		t.Fatalf("artifact pathが再圧縮されていません: result=%#v prompts=%d", result, len(r.prompts))
	}
	taskID, _ := st.TaskID()
	logs, logErr := st.ReadModelCallLogs(taskID)
	if logErr != nil {
		t.Fatal(logErr)
	}
	if len(logs) != 2 || logs[0].Outcome != "invalid_packet" || !strings.Contains(logs[0].Error, "artifact dir配下") {
		t.Fatalf("artifact validation telemetry = %#v", logs)
	}
}

func TestRunModelPreservesPacketCompressionPromptAcrossRateLimit(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: "PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: LOW\nSUMMARY: " + strings.Repeat("x", packet.MaxPacketLineBytes+1) + "\nREQUIREMENT_COVERAGE: covered\nTESTS: pass\nUNVERIFIED: none\nARTIFACTS: none\nPACKET_END\n"},
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
		t.Fatalf("packet再圧縮中のrate limit errorを期待: %v", err)
	}

	checkpoint, loadErr := st.LoadResumeCheckpoint()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !checkpoint.PacketCompacted || !strings.Contains(checkpoint.OriginalPrompt, "PACKETだけを再出力") {
		t.Fatalf("再圧縮promptがcheckpointに保持されていません: %#v", checkpoint)
	}
	resumed := resumePrompt(checkpoint)
	if !strings.Contains(resumed, "PACKETだけを再出力") || strings.Contains(resumed, "original implementation prompt") {
		t.Fatalf("resumeが再圧縮工程を指していません: %s", resumed)
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

	err := w.ExecuteNewTask("request")
	if err == nil || !strings.Contains(err.Error(), "auto-fix-format") {
		t.Fatalf("auto-fix format error = %v", err)
	}
}

func TestWorkerRejectsReviewerStatus(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{{output: passPacket()}}}
	w := newWorkflowT(t, st, r)

	err := w.ExecuteNewTask("request")
	if err == nil || !strings.Contains(err.Error(), "worker-format") {
		t.Fatalf("worker format error = %v", err)
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
		WorkerPacket: []string{
			"STATUS: IMPLEMENTED",
			"RISK: LOW",
			"SUMMARY: done",
			"REQUIREMENT_COVERAGE: covered",
			"TESTS: pass",
			"UNVERIFIED: none",
			"ARTIFACTS: none",
		},
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
	if err == nil || !strings.Contains(err.Error(), "reviewer-format") {
		t.Fatalf("reviewer format errorを期待: %v", err)
	}
}

func TestReviewerUnknownStatusStopsAfterRecompact(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: unknownStatusPacket()},
		{output: unknownStatusPacket()},
	}}
	w := newWorkflowT(t, st, r)

	err := w.ExecuteNewTask("request")
	if err == nil || !strings.Contains(err.Error(), "packet-compact-format") {
		t.Fatalf("再圧縮後の未知STATUS停止を期待: %v", err)
	}
}

func TestReviewNeedsHighRiskFloor(t *testing.T) {
	lowWorker := packet.FromLines([]string{"STATUS: IMPLEMENTED", "RISK: LOW"})
	highWorker := packet.FromLines([]string{"STATUS: IMPLEMENTED", "RISK: HIGH"})

	tests := []struct {
		name           string
		workerPacket   packet.Packet
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
			if got := reviewNeedsHighRiskFloor(tt.workerPacket, tt.autoFixes, tt.hasDecision, tt.hasPriorReview); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestRiskFloorFailClosedPacketIsValid(t *testing.T) {
	passPkt := packet.FromLines([]string{
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
	})

	enforced := riskFloorFailClosedPacket(passPkt)
	if enforced.Status() != "NEEDS_SOL_REVIEW" || enforced.Risk() != "HIGH" {
		t.Fatalf("status=%s risk=%s", enforced.Status(), enforced.Risk())
	}
	if err := packet.Validate(enforced); err != nil {
		t.Fatalf("fail closed packetがvalidate不合格: %v", err)
	}
	if enforced.Fields["REQUIREMENT_COVERAGE"] == "covered" {
		t.Fatalf("reviewerのPASS内容をfail closed packetへ捏造している: %#v", enforced.Fields)
	}
}

func TestResolveRiskFloorReemitAcceptsCompliantAndFailsClosed(t *testing.T) {
	compliant := packet.FromLines([]string{
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
	})
	if resolved := resolveRiskFloorReemit(compliant); resolved.Status() != "NEEDS_SOL_REVIEW" || resolved.Lines[0] != "STATUS: NEEDS_SOL_REVIEW" {
		t.Fatalf("準拠再出力はそのまま採用すべき: %#v", resolved)
	}

	passed := packet.FromLines([]string{
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
	})
	closed := resolveRiskFloorReemit(passed)
	if closed.Status() != "NEEDS_SOL_REVIEW" || !strings.Contains(closed.Fields["SUMMARY"], "PASS") {
		t.Fatalf("再違反はfail closedのNEEDS_SOL_REVIEWへ昇格すべき: %#v", closed)
	}
}

func TestRiskFloorReemitPromptConstraints(t *testing.T) {
	prompt := riskFloorReemitPrompt()
	for _, want := range []string{
		"NEEDS_SOL_REVIEW (RISK: HIGH) だけ",
		"実装・調査・テストをやり直さず",
		"PACKETだけを再出力",
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
		"PACKET/reportだけを再出力",
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
	if strings.Contains(implementation, "PACKET/reportだけを再出力") {
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
		WorkerPacket: []string{
			"STATUS: IMPLEMENTED",
			"RISK: HIGH",
			"SUMMARY: done",
			"REQUIREMENT_COVERAGE: covered",
			"TESTS: pass",
			"UNVERIFIED: none",
			"ARTIFACTS: none",
		},
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
		WorkerPacket:    []string{"STATUS: IMPLEMENTED", "RISK: HIGH", "SUMMARY: done", "REQUIREMENT_COVERAGE: covered", "TESTS: pass", "UNVERIFIED: none", "ARTIFACTS: none"},
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
		WorkerPacket:    []string{"STATUS: IMPLEMENTED", "RISK: HIGH", "SUMMARY: done", "REQUIREMENT_COVERAGE: covered", "TESTS: pass", "UNVERIFIED: none", "ARTIFACTS: none"},
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
