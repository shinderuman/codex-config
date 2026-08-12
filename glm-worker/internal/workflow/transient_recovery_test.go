package workflow

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/runner"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

var (
	errProbeTransient    = errors.New("API Error: 503 Service Unavailable")
	errProbeNonTransient = errors.New("401 Unauthorized: invalid api key")
	errProbeLocalExec    = errors.New("exec: 'claude': executable file not found in $PATH")
)

func newRecoveryWorkflowT(t *testing.T, st *state.StateStore, r *scriptedRunner) (*Workflow, *fakeClock) {
	t.Helper()
	w := newWorkflowT(t, st, r)
	clock := newFakeClock()
	w.now = clock.nowFunc
	w.sleep = clock.sleepFunc
	return w, clock
}

func workerCheckpoint() state.ResumeCheckpoint {
	return state.ResumeCheckpoint{
		Stage:   state.ResumeStageWorker,
		Phase:   "worker-new",
		Role:    state.WorkerRole,
		Model:   "opus",
		Effort:  "high",
		Prompt:  "p",
		Request: "req",
	}
}

func equalDurations(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func readRateLimitedFlag(st *state.StateStore) bool {
	cp, err := st.LoadResumeCheckpoint()
	return err == nil && cp.RateLimited
}

func TestRecoveryExhaustsToProviderUnavailable(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{
		steps:     []runnerStep{{output: "API Error: 503 Service Unavailable", runErr: errors.New("exit status 1")}},
		probeErrs: []error{errProbeTransient, errProbeTransient, errProbeTransient, errProbeTransient},
	}
	w, clock := newRecoveryWorkflowT(t, st, r)
	w.temp = t.TempDir()

	_, err := w.runModel(workerCheckpoint())
	var pErr *runner.ProviderUnavailableError
	if !errors.As(err, &pErr) {
		t.Fatalf("ProviderUnavailableErrorを期待: %v", err)
	}
	if pErr.Classification != "http-503" || pErr.Probes != 4 {
		t.Fatalf("classification/probes = %q/%d", pErr.Classification, pErr.Probes)
	}
	if pErr.Elapsed > providerUnavailableDeadline {
		t.Fatalf("elapsed %sがdeadline %sを超過", pErr.Elapsed, providerUnavailableDeadline)
	}
	if st.TaskStatus() != state.TaskStatusProviderUnavailable {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	cp, cerr := st.LoadResumeCheckpoint()
	if cerr != nil || !cp.ProviderUnavailable || cp.ProviderUnavailableProbes != 4 ||
		cp.ProviderUnavailableClassification != "http-503" || cp.RateLimited {
		t.Fatalf("checkpoint = %#v err=%v", cp, cerr)
	}
	if !st.Exists("worker.ready") {
		t.Fatal("同一session/checkpointを保持すべき: worker.readyが無い")
	}
	if len(r.probes) != 4 {
		t.Fatalf("probe回数 = %d", len(r.probes))
	}
	if !equalDurations(clock.sleeps, transientBackoffSchedule) {
		t.Fatalf("sleeps = %v want schedule %v", clock.sleeps, transientBackoffSchedule)
	}
	if len(r.prompts) != 1 {
		t.Fatalf("runner Run呼出 = %d (initialだけ期待)", len(r.prompts))
	}
}

func TestRecoveryProbeSuccessThenResumeCompletes(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{
		steps: []runnerStep{
			{output: "API Error: 503 Service Unavailable", runErr: errors.New("exit status 1")},
			{output: implementedPacket("recovered")},
		},
		probeErrs: []error{errProbeTransient, nil},
	}
	w, _ := newRecoveryWorkflowT(t, st, r)
	w.temp = t.TempDir()

	result, err := w.runModel(workerCheckpoint())
	if err != nil {
		t.Fatalf("回復成功を期待: %v", err)
	}
	if result.Status() != "IMPLEMENTED" {
		t.Fatalf("status = %q", result.Status())
	}
	if len(r.probes) != 2 || len(r.prompts) != 2 {
		t.Fatalf("probes=%d prompts=%d", len(r.probes), len(r.prompts))
	}
	if _, cerr := st.LoadResumeCheckpoint(); cerr == nil {
		t.Fatal("回復成功時はresume checkpointがclearされるべき")
	}
}

func TestRecoveryResumeTransientRetriesNextBackoff(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{
		steps: []runnerStep{
			{output: "API Error: 503 Service Unavailable", runErr: errors.New("exit status 1")},
			{output: "API Error: 504 Gateway Timeout", runErr: errors.New("exit status 1")},
			{output: implementedPacket("recovered")},
		},
		probeErrs: []error{nil, nil},
	}
	w, _ := newRecoveryWorkflowT(t, st, r)
	w.temp = t.TempDir()

	result, err := w.runModel(workerCheckpoint())
	if err != nil {
		t.Fatalf("回復成功を期待: %v", err)
	}
	if result.Status() != "IMPLEMENTED" {
		t.Fatalf("status = %q", result.Status())
	}
	if len(r.probes) != 2 || len(r.prompts) != 3 {
		t.Fatalf("probes=%d prompts=%d", len(r.probes), len(r.prompts))
	}
}

func TestRecoveryStopsAtDeadline(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{
		steps:     []runnerStep{{output: "API Error: 529", runErr: errors.New("exit status 1")}},
		probeErrs: []error{errProbeTransient, errProbeTransient, errProbeTransient, errProbeTransient},
	}
	w, clock := newRecoveryWorkflowT(t, st, r)
	w.temp = t.TempDir()
	// recoveryStart基準の3h deadlineをbackoff中に到達させるため、各sleepで実時間より大きく時間を進める。
	// identity jitterでもschedule合計(155m)では通常deadlineに届かないため、sleepを拡大してdeadline経路へ入れる。
	bigStep := 90 * time.Minute
	w.sleep = func(d time.Duration) {
		clock.sleeps = append(clock.sleeps, d)
		clock.now = clock.now.Add(bigStep)
	}

	_, err := w.runModel(workerCheckpoint())
	var pErr *runner.ProviderUnavailableError
	if !errors.As(err, &pErr) {
		t.Fatalf("ProviderUnavailableErrorを期待: %v", err)
	}
	if pErr.Probes < 1 || pErr.Probes > 4 {
		t.Fatalf("deadline到達時のprobe回数 = %d", pErr.Probes)
	}
	if pErr.Elapsed < providerUnavailableDeadline {
		t.Fatalf("elapsed %sがdeadline %sに満たない", pErr.Elapsed, providerUnavailableDeadline)
	}
	if pErr.Probes == 4 {
		t.Fatalf("deadline到達でprobe4回全消費は通常経路と区別不可: probes=%d", pErr.Probes)
	}
	if st.TaskStatus() != state.TaskStatusProviderUnavailable {
		t.Fatalf("status = %q", st.TaskStatus())
	}
}

func TestRecoveryResumeNonTransientFailsClosed(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{
		steps: []runnerStep{
			{output: "API Error: 503 Service Unavailable", runErr: errors.New("exit status 1")},
			{output: "401 Unauthorized: invalid api key", runErr: errors.New("exit status 1")},
		},
		probeErrs: []error{nil},
	}
	w, _ := newRecoveryWorkflowT(t, st, r)
	w.temp = t.TempDir()

	_, err := w.runModel(workerCheckpoint())
	if err == nil {
		t.Fatal("errorを期待")
	}
	var pErr *runner.ProviderUnavailableError
	if errors.As(err, &pErr) {
		t.Fatalf("probe成功後の非transient resume失敗はprovider-unavailableでなくWORKER_ERRORへ: %v", err)
	}
	if _, cerr := st.LoadResumeCheckpoint(); cerr == nil {
		t.Fatal("非transient error時はresume checkpointがclearされるべき")
	}
}

func TestRecoveryDoesNotTriggerOnFiveHourLimit(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{{output: zaiFiveHourLog, runErr: errors.New("exit status 1")}}}
	w, _ := newRecoveryWorkflowT(t, st, r)
	w.config.RepoRoot = "/repo"
	w.config.RepoShort = "testrepo1234"
	w.temp = t.TempDir()

	_, err := w.runModel(workerCheckpoint())
	if err == nil {
		t.Fatal("errorを期待")
	}
	var pErr *runner.ProviderUnavailableError
	if errors.As(err, &pErr) {
		t.Fatalf("5h上限はprovider-unavailableでなくRATE_LIMITEDへ: %v", err)
	}
	if len(r.probes) != 0 {
		t.Fatalf("5h上限でprobeが呼ばれた: %d", len(r.probes))
	}
	if !readRateLimitedFlag(st) {
		t.Fatal("5h上限でrate-limited checkpointが保存されるべき")
	}
}

func TestRecoveryDoesNotTriggerOnNonTransientError(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{{output: "boom fatal", runErr: errors.New("exit status 1")}}}
	w, _ := newRecoveryWorkflowT(t, st, r)
	w.temp = t.TempDir()

	_, err := w.runModel(workerCheckpoint())
	if err == nil {
		t.Fatal("errorを期待")
	}
	if len(r.probes) != 0 {
		t.Fatalf("非transient errorでprobeが呼ばれた: %d", len(r.probes))
	}
	if _, cerr := st.LoadResumeCheckpoint(); cerr == nil {
		t.Fatal("非transient error時はresume checkpointがclearされるべき")
	}
}

func TestProviderUnavailableTaskBlocksNewTask(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage: state.ResumeStageWorker, Phase: "worker-new", Role: state.WorkerRole,
		Model: "opus", Effort: "high", Prompt: "p", ProviderUnavailable: true,
		ProviderUnavailableClassification: "http-503", ProviderUnavailableProbes: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusProviderUnavailable); err != nil {
		t.Fatal(err)
	}
	w := newWorkflowT(t, st, &scriptedRunner{})

	err := w.ExecuteNewTask("replacement")
	if err == nil || !strings.Contains(err.Error(), "provider-unavailable") {
		t.Fatalf("provider-unavailable taskの新規task開始を拒否すべき: %v", err)
	}
}

func TestResumeFromProviderUnavailableRetriesSameSession(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:                             state.ResumeStageWorker,
		Phase:                             "worker-new",
		Role:                              state.WorkerRole,
		Model:                             "opus",
		Effort:                            "high",
		Prompt:                            "p",
		OriginalPrompt:                    "p",
		Request:                           "req",
		ProviderUnavailable:               true,
		ProviderUnavailableClassification: "http-503",
		ProviderUnavailableProbes:         4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusProviderUnavailable); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteResume(); err != nil {
		t.Fatalf("resume成功を期待: %v", err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("status = %q", st.TaskStatus())
	}
	if len(r.probes) != 1 {
		t.Fatalf("probe成功後に本taskを1回resumeすべき: probes=%d", len(r.probes))
	}
	if len(r.prompts) != 2 {
		t.Fatalf("同一session/checkpointからworker→reviewerへ再試行すべき: prompts=%d", len(r.prompts))
	}
	if !strings.Contains(r.prompts[0], "一時的なprovider障害") {
		t.Fatalf("provider-unavailable resume理由がpromptにない: %q", r.prompts[0])
	}
}

func TestResumeFromProviderUnavailableRestoresStatusAfterRunnerError(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:                             state.ResumeStageWorker,
		Phase:                             "worker-new",
		Role:                              state.WorkerRole,
		Model:                             "opus",
		Effort:                            "high",
		Prompt:                            "p",
		OriginalPrompt:                    "p",
		Request:                           "req",
		ProviderUnavailable:               true,
		ProviderUnavailableClassification: "http-503",
		ProviderUnavailableProbes:         4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusProviderUnavailable); err != nil {
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
	if st.TaskStatus() != state.TaskStatusProviderUnavailable {
		t.Fatalf("resume失敗時はprovider-unavailable statusへ復元すべき: %q", st.TaskStatus())
	}
	restored, loadErr := st.LoadResumeCheckpoint()
	if loadErr != nil || !restored.ProviderUnavailable {
		t.Fatalf("provider-unavailable checkpointが復元されていません: checkpoint=%#v err=%v", restored, loadErr)
	}
}

func seedProviderUnavailableCheckpoint(t *testing.T, st *state.StateStore) {
	t.Helper()
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:                             state.ResumeStageWorker,
		Phase:                             "worker-new",
		Role:                              state.WorkerRole,
		Model:                             "opus",
		Effort:                            "high",
		Prompt:                            "p",
		OriginalPrompt:                    "p",
		Request:                           "req",
		ProviderUnavailable:               true,
		ProviderUnavailableClassification: "http-503",
		ProviderUnavailableProbes:         4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusProviderUnavailable); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryProbeNonTransientFailsClosed(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{
		steps:     []runnerStep{{output: "API Error: 503 Service Unavailable", runErr: errors.New("exit status 1")}},
		probeErrs: []error{errProbeNonTransient},
	}
	w, _ := newRecoveryWorkflowT(t, st, r)
	w.temp = t.TempDir()

	_, err := w.runModel(workerCheckpoint())
	if err == nil {
		t.Fatal("errorを期待")
	}
	var pErr *runner.ProviderUnavailableError
	if errors.As(err, &pErr) {
		t.Fatalf("非transient probe errorはprovider-unavailableでなくWORKER_ERRORへ: %v", err)
	}
	if len(r.probes) != 1 {
		t.Fatalf("非transient probeで即fail closedすべき: probes=%d", len(r.probes))
	}
	if _, cerr := st.LoadResumeCheckpoint(); cerr == nil {
		t.Fatal("fail closed時はresume checkpointがclearされるべき")
	}
}

func TestRecoveryProbeLocalExecErrorFailsClosed(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{
		steps:     []runnerStep{{output: "API Error: 503 Service Unavailable", runErr: errors.New("exit status 1")}},
		probeErrs: []error{errProbeLocalExec},
	}
	w, _ := newRecoveryWorkflowT(t, st, r)
	w.temp = t.TempDir()

	_, err := w.runModel(workerCheckpoint())
	if err == nil {
		t.Fatal("errorを期待")
	}
	var pErr *runner.ProviderUnavailableError
	if errors.As(err, &pErr) {
		t.Fatalf("local実行errorはprovider-unavailableでなくWORKER_ERRORへ: %v", err)
	}
	if len(r.probes) != 1 {
		t.Fatalf("local実行errorは即fail closedすべき: probes=%d", len(r.probes))
	}
}

func TestRecoveryAppliesJitter(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{
		steps:     []runnerStep{{output: "API Error: 503 Service Unavailable", runErr: errors.New("exit status 1")}},
		probeErrs: []error{errProbeTransient, errProbeTransient, errProbeTransient, errProbeTransient},
	}
	w, clock := newRecoveryWorkflowT(t, st, r)
	w.temp = t.TempDir()
	// base + 1分の固定jitter。合計159分でdeadline(180分)に届かずclamp不発。
	w.jitter = func(base time.Duration) time.Duration { return base + time.Minute }

	_, err := w.runModel(workerCheckpoint())
	var pErr *runner.ProviderUnavailableError
	if !errors.As(err, &pErr) {
		t.Fatalf("ProviderUnavailableErrorを期待: %v", err)
	}
	want := []time.Duration{6 * time.Minute, 16 * time.Minute, 46 * time.Minute, 91 * time.Minute}
	if !equalDurations(clock.sleeps, want) {
		t.Fatalf("jitter適用後のsleeps = %v want %v", clock.sleeps, want)
	}
	if pErr.Elapsed > providerUnavailableDeadline {
		t.Fatalf("elapsed %sがdeadline %sを超過", pErr.Elapsed, providerUnavailableDeadline)
	}
}

func TestResumeProbeGateProviderStillDownZeroFullRuns(t *testing.T) {
	st := newStateStoreT(t)
	seedProviderUnavailableCheckpoint(t, st)
	r := &scriptedRunner{
		steps:     []runnerStep{{output: implementedPacket("never used")}},
		probeErrs: []error{errProbeTransient, errProbeTransient, errProbeTransient, errProbeTransient},
	}
	w, _ := newRecoveryWorkflowT(t, st, r)

	err := w.ExecuteResume()
	var pErr *runner.ProviderUnavailableError
	if !errors.As(err, &pErr) {
		t.Fatalf("ProviderUnavailableErrorを期待: %v", err)
	}
	if len(r.prompts) != 0 {
		t.Fatalf("provider未回復時は本task Runが0回であるべき: %d", len(r.prompts))
	}
	if len(r.probes) != 4 {
		t.Fatalf("probe回数 = %d", len(r.probes))
	}
	if st.TaskStatus() != state.TaskStatusProviderUnavailable {
		t.Fatalf("status = %q", st.TaskStatus())
	}
}

func TestResumeProbeGateFailClosedOnNonTransientProbe(t *testing.T) {
	st := newStateStoreT(t)
	seedProviderUnavailableCheckpoint(t, st)
	r := &scriptedRunner{
		steps:     []runnerStep{{output: implementedPacket("never used")}},
		probeErrs: []error{errProbeNonTransient},
	}
	w, _ := newRecoveryWorkflowT(t, st, r)

	err := w.ExecuteResume()
	if err == nil {
		t.Fatal("errorを期待")
	}
	var pErr *runner.ProviderUnavailableError
	if errors.As(err, &pErr) {
		t.Fatalf("非transient probe errorはprovider-unavailableでなくfail closedへ: %v", err)
	}
	if len(r.prompts) != 0 {
		t.Fatalf("本task Runが0回であるべき: %d", len(r.prompts))
	}
	if _, cerr := st.LoadResumeCheckpoint(); cerr == nil {
		t.Fatal("fail closed時はresume checkpointがclearされるべき")
	}
}
