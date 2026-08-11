package workflow

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shinderuman/codex-config/glm-worker/internal/config"
	"github.com/shinderuman/codex-config/glm-worker/internal/packet"
	"github.com/shinderuman/codex-config/glm-worker/internal/runner"
	"github.com/shinderuman/codex-config/glm-worker/internal/state"
)

type runnerStep struct {
	output string
	runErr error
	result runner.RunResult
}

type scriptedRunner struct {
	steps   []runnerStep
	prompts []string
	models  []string
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
	step := r.steps[index]
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

func implementedPacket(summary string) string {
	return implementedPacketWithArtifacts(summary, "none")
}

func implementedPacketWithArtifacts(summary string, artifacts string) string {
	return "PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: LOW\nSUMMARY: " + summary + "\nREQUIREMENT_COVERAGE: covered\nTESTS: pass\nUNVERIFIED: none\nARTIFACTS: " + artifacts + "\nPACKET_END\n"
}

func implementedPacketWithRisk(summary string, risk string) string {
	return "PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: " + risk + "\nSUMMARY: " + summary + "\nREQUIREMENT_COVERAGE: covered\nTESTS: pass\nUNVERIFIED: none\nARTIFACTS: none\nPACKET_END\n"
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

func unknownStatusPacket() string {
	return "PACKET_BEGIN\nSTATUS: UNKNOWN\nRISK: LOW\nSUMMARY: x\nPACKET_END\n"
}

const zaiFiveHourLog = "API Error: Request rejected (429) · [1308][Usage limit reached for 5 hour. Your limit will reset at 2026-07-22 14:06:34]\n"

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

func newWorkflowT(t *testing.T, st *state.StateStore, r *scriptedRunner) *Workflow {
	t.Helper()
	return NewWorkflow(config.AppConfig{
		WorkerModel:           "opus",
		ReviewerModel:         "haiku",
		HighRiskReviewerModel: "sonnet",
		RoutineEffort:         "high",
		MaxAutoFixRounds:      2,
		TelemetryContent:      true,
	}, st, r, io.Discard)
}

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
				"glm-5.2": {InputTokens: 10, CacheCreationInputTokens: 20, CacheReadInputTokens: 30, OutputTokens: 40},
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
	if got.TopLevelUsage.CacheReadInputTokens != 2 || got.TreeUsage.CacheReadInputTokens != 37 || got.ResolvedModelUsage["glm-5.2"].OutputTokens != 40 {
		t.Fatalf("telemetry usage = %#v", got)
	}
	stats := currentStats(t, st)
	if stats.CacheReadInputTokensByAlias["opus"] != 37 || stats.OutputTokensByAlias["opus"] != 48 || stats.OutputTokensByResolvedModel["glm-5.2"] != 40 {
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
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.models, ",") != "opus,sonnet" {
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
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteDecision("A案で進める"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete || st.Exists("pending-decision") {
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
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteExplicitFix("境界値を修正する"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
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
		output: "503 temporary service error\n",
		runErr: errors.New("exit status 1"),
	}}}
	w := newWorkflowT(t, st, r)
	err := w.ExecuteResume()
	if err == nil || !strings.Contains(err.Error(), "503 temporary service error") {
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
	// reviewerが有効PACKETだがreviewerとして不正なSTATUSを返した場合。
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
	// reviewerが未知STATUSを返し、packet再圧縮後も直らない場合。
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
