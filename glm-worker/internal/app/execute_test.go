package app

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/codex-config/glm-worker/internal/config"
	"github.com/shinderuman/codex-config/glm-worker/internal/state"
	"github.com/shinderuman/codex-config/glm-worker/internal/workflow"
)

type fakeStep struct {
	output string
	runErr error
}

// fakeRunnerはExecute経由の統合テスト用のModelRunner偽装実装。
type fakeRunner struct {
	steps   []fakeStep
	prompts []string
	models  []string
}

func (r *fakeRunner) Run(
	_ state.SessionRole,
	model string,
	_ bool,
	_ string,
	prompt string,
	outputPath string,
) error {
	r.prompts = append(r.prompts, prompt)
	r.models = append(r.models, model)
	index := len(r.prompts) - 1
	step := r.steps[index]
	if step.output != "" {
		if err := os.WriteFile(outputPath, []byte(step.output), 0o600); err != nil {
			return err
		}
	}
	return step.runErr
}

func (r *fakeRunner) factory() RunnerFactory {
	return func(_ config.AppConfig, _ *state.StateStore) workflow.ModelRunner { return r }
}

func implementedPacketApp(summary string) string {
	return "PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: LOW\nSUMMARY: " + summary + "\nREQUIREMENT_COVERAGE: covered\nTESTS: pass\nUNVERIFIED: none\nPACKET_END\n"
}

func passPacketApp() string {
	return "PACKET_BEGIN\nSTATUS: PASS\nRISK: LOW\nSUMMARY: pass\nREQUIREMENT_COVERAGE: covered\nINVARIANTS: preserved\nTEST_EVIDENCE: ev\nISSUES: none\nRESIDUAL_RISK: none\nTARGETS: none\nPACKET_END\n"
}

func newAppConfig(t *testing.T) config.AppConfig {
	t.Helper()
	return config.AppConfig{
		StateBase:             t.TempDir(),
		RepoHash:              "apphash",
		RepoRoot:              "/repo",
		RepoShort:             "appshort1234",
		RoutineEffort:         "high",
		MaxAutoFixRounds:      2,
		WorkerModel:           "opus",
		ReviewerModel:         "haiku",
		HighRiskReviewerModel: "sonnet",
	}
}

func TestExecuteStatusReportsEmptyState(t *testing.T) {
	cfg := newAppConfig(t)
	var out bytes.Buffer

	if err := Execute(Command{Mode: ModeStatus}, cfg, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "TASK_ID: none") {
		t.Fatalf("空状態のstatus出力がありません: %q", out.String())
	}
	if !strings.Contains(out.String(), "PENDING_DECISION: no") {
		t.Fatalf("空状態のpending decision出力がありません: %q", out.String())
	}
}

func TestExecuteStatsReportsEmptyState(t *testing.T) {
	cfg := newAppConfig(t)
	var out bytes.Buffer

	if err := Execute(Command{Mode: ModeStats}, cfg, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "TASKS: 0") {
		t.Fatalf("空状態のstats出力がありません: %q", out.String())
	}
	if !strings.Contains(out.String(), "MODEL_CALLS_BY_ALIAS: none") || !strings.Contains(out.String(), "RATE_LIMITS_BY_ALIAS: none") {
		t.Fatalf("空状態のmodel別stats出力がありません: %q", out.String())
	}
}

func TestPrintStatsAggregatesAndSortsModelAliases(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	st.RecordModelCall(state.WorkerRole, "opus")
	st.RecordModelCall(state.ReviewerRole, "haiku")
	st.RecordModelCall(state.ReviewerRole, "sonnet")
	st.RecordModelDuration("sonnet", 2*time.Second)
	st.RecordRateLimit("opus")

	var out bytes.Buffer
	if err := printStats(st, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "MODEL_CALLS_BY_ALIAS: haiku=1,opus=1,sonnet=1") {
		t.Fatalf("model別statsが安定順で集計されていません: %q", out.String())
	}
	if !strings.Contains(out.String(), "RATE_LIMITS_BY_ALIAS: opus=1") {
		t.Fatalf("model別rate limitが集計されていません: %q", out.String())
	}
	if !strings.Contains(out.String(), "MODEL_DURATION_MS_BY_ALIAS: sonnet=2000") {
		t.Fatalf("model別実行時間が集計されていません: %q", out.String())
	}
}

func TestExecuteResetClearsTask(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Execute(Command{Mode: ModeReset}, cfg, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "STATUS: RESET") {
		t.Fatalf("RESET出力がありません: %q", out.String())
	}
	if st.Exists("task.id") {
		t.Fatal("reset後もtask.idが残っています")
	}
}

func TestExecuteResumeRejectsNonRateLimited(t *testing.T) {
	cfg := newAppConfig(t)
	r := &fakeRunner{}

	err := Execute(Command{Mode: ModeResume}, cfg, r.factory(), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "resumable task is not available") {
		t.Fatalf("resumableでない--resumeを拒否する必要があります: %v", err)
	}
}

func TestExecuteNewTaskReachesPass(t *testing.T) {
	cfg := newAppConfig(t)
	r := &fakeRunner{steps: []fakeStep{
		{output: implementedPacketApp("done")},
		{output: passPacketApp()},
	}}

	if err := Execute(Command{Mode: ModeNewTask, Payload: "request"}, cfg, r.factory(), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}

	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("status = %q", st.TaskStatus())
	}
}

func TestExecuteAcquiresAndReleasesLock(t *testing.T) {
	cfg := newAppConfig(t)

	first := &fakeRunner{steps: []fakeStep{
		{output: implementedPacketApp("done")},
		{output: passPacketApp()},
	}}
	if err := Execute(Command{Mode: ModeNewTask, Payload: "request"}, cfg, first.factory(), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}

	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Exists("lock") {
		t.Fatal("lockファイルが作成されていません")
	}

	second := &fakeRunner{steps: []fakeStep{
		{output: implementedPacketApp("done")},
		{output: passPacketApp()},
	}}
	if err := Execute(Command{Mode: ModeNewTask, Payload: "request2"}, cfg, second.factory(), io.Discard, io.Discard); err != nil {
		t.Fatalf("前回実行のロック解放後も2回目の実行が失敗しました: %v", err)
	}
}

func TestExecutePropagatesWorkerFailure(t *testing.T) {
	cfg := newAppConfig(t)
	r := &fakeRunner{steps: []fakeStep{{runErr: errors.New("boom")}}}

	err := Execute(Command{Mode: ModeNewTask, Payload: "request"}, cfg, r.factory(), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "STATUS: WORKER_ERROR") {
		t.Fatalf("worker失敗を伝播する必要があります: %v", err)
	}
}

func TestRunUsesInjectedDependencies(t *testing.T) {
	cfg := newAppConfig(t)
	r := &fakeRunner{steps: []fakeStep{
		{output: implementedPacketApp("done")},
		{output: passPacketApp()},
	}}
	var out bytes.Buffer

	err := run(
		[]string{"request"},
		func() (config.AppConfig, error) { return cfg, nil },
		r.factory(),
		&out,
		io.Discard,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "STATUS: PASS") {
		t.Fatalf("packetが指定stdoutへ出力されていません: %q", out.String())
	}
}

func TestRunStopsWhenConfigLoadFails(t *testing.T) {
	want := errors.New("config failure")
	err := run(
		[]string{"request"},
		func() (config.AppConfig, error) { return config.AppConfig{}, want },
		nil,
		io.Discard,
		io.Discard,
	)
	if !errors.Is(err, want) {
		t.Fatalf("config error = %v", err)
	}
}
