package workflow

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/runner"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// workflowの全timestamp/durationはNewWorkflowのnow注入seamだけから供給する。
// production codeへwall clock呼出が再混入するとfake clock testが実時間へ分岐し、
// このfileの時刻assertionが黙って裏切られるため、source levelで禁止する。
func TestWorkflowProductionCodeHasNoDirectWallClock(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"time.Now(", "time.Since(", "time.After(", "time.Sleep(",
		"time.NewTimer(", "time.NewTicker(", "time.Tick(", "ModTime()",
	}
	scanned := false
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned = scanned || name == "workflow.go"
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, pattern := range forbidden {
			if strings.Contains(string(data), pattern) {
				t.Fatalf("%sがwall clock %sを直接読んでいる: 注入clock(w.now)へ統一すること", name, pattern)
			}
		}
	}
	if !scanned {
		t.Fatal("workflow.goが走査対象に無い: package source dir以外からの実行")
	}
}

// initial Task Work呼出のtelemetry timestamp/durationとstats集計が注入clockに従う。
func TestRunModelTelemetryTimestampsFollowInjectedClock(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{{output: implementedPacket("done")}}}
	w, clock := newRecoveryWorkflowT(t, st, r)
	w.temp = t.TempDir()
	taskRun := 7 * time.Minute
	r.onRun = func() { clock.now = clock.now.Add(taskRun) }

	if _, err := w.runModel(workerCheckpoint()); err != nil {
		t.Fatal(err)
	}
	logs := taskLogs(t, st)
	if len(logs) != 1 {
		t.Fatalf("telemetry logs = %d", len(logs))
	}
	got := logs[0]
	if got.StartedAt != testFixedTime || got.CompletedAt != testFixedTime.Add(taskRun) {
		t.Fatalf("timestamps = %s/%s want %s/%s", got.StartedAt, got.CompletedAt, testFixedTime, testFixedTime.Add(taskRun))
	}
	if got.WallDurationMS != taskRun.Milliseconds() {
		t.Fatalf("wall duration = %d want %d", got.WallDurationMS, taskRun.Milliseconds())
	}
	stats := currentStats(t, st)
	if stats.ModelDurationMSByAlias["opus"] != taskRun.Milliseconds() {
		t.Fatalf("duration stats = %#v", stats.ModelDurationMSByAlias)
	}
}

// initial transient失敗からbackoff/probe上限でのprovider-unavailable停止まで、
// task/probe/event全recordのtimestampとdurationが注入clockだけで導出される。
func TestRecoveryTelemetryTimestampsFollowInjectedClock(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{
		steps:     []runnerStep{{output: "API Error: 503 Service Unavailable", runErr: errors.New("exit status 1")}},
		probeErrs: []error{errProbeTransient, errProbeTransient, errProbeTransient, errProbeTransient},
	}
	w, clock := newRecoveryWorkflowT(t, st, r)
	w.temp = t.TempDir()
	taskRun := 7 * time.Minute
	probeRun := 30 * time.Second
	r.onRun = func() { clock.now = clock.now.Add(taskRun) }
	r.onProbe = func() { clock.now = clock.now.Add(probeRun) }

	_, err := w.runModel(workerCheckpoint())
	var pErr *runner.ProviderUnavailableError
	if !errors.As(err, &pErr) {
		t.Fatalf("ProviderUnavailableErrorを期待: %v", err)
	}

	recoveryStart := testFixedTime.Add(taskRun)
	scheduleSum := time.Duration(0)
	wantProbeStart := make(map[int]time.Time, len(transientBackoffSchedule))
	at := recoveryStart
	for i, wait := range transientBackoffSchedule {
		scheduleSum += wait
		at = at.Add(wait)
		wantProbeStart[i+1] = at
		at = at.Add(probeRun)
	}
	wantElapsed := scheduleSum + probeRun*time.Duration(len(transientBackoffSchedule))

	if pErr.Elapsed != wantElapsed {
		t.Fatalf("elapsed = %s want %s", pErr.Elapsed, wantElapsed)
	}
	cp, cerr := st.LoadResumeCheckpoint()
	if cerr != nil || !cp.ProviderUnavailableStartedAt.Equal(recoveryStart) {
		t.Fatalf("checkpoint recovery開始時刻 = %v err=%v want %v", cp.ProviderUnavailableStartedAt, cerr, recoveryStart)
	}

	var transient state.ModelCallLog
	var unavailable state.ModelCallLog
	probes := make(map[int]state.ModelCallLog)
	for _, l := range taskLogs(t, st) {
		switch {
		case l.CallType == state.CallTypeTask && l.Outcome == "transient_error":
			transient = l
		case l.CallType == state.CallTypeProbe:
			probes[l.ProbeAttempt] = l
		case l.CallType == state.CallTypeEvent && l.Outcome == "provider_unavailable":
			unavailable = l
		}
	}
	if transient.StartedAt != testFixedTime || transient.CompletedAt != recoveryStart {
		t.Fatalf("initial transient記録 = %s/%s want %s/%s", transient.StartedAt, transient.CompletedAt, testFixedTime, recoveryStart)
	}
	if transient.WallDurationMS != taskRun.Milliseconds() {
		t.Fatalf("initial transient wall duration = %d", transient.WallDurationMS)
	}
	if len(probes) != len(transientBackoffSchedule) {
		t.Fatalf("probe記録 = %d", len(probes))
	}
	for attempt, want := range wantProbeStart {
		got := probes[attempt]
		if got.StartedAt != want || got.CompletedAt != want.Add(probeRun) {
			t.Fatalf("probe %d時刻 = %s/%s want %s/%s", attempt, got.StartedAt, got.CompletedAt, want, want.Add(probeRun))
		}
		if got.WallDurationMS != probeRun.Milliseconds() {
			t.Fatalf("probe %d wall duration = %d", attempt, got.WallDurationMS)
		}
	}
	wantStop := recoveryStart.Add(wantElapsed)
	if unavailable.StartedAt != wantStop || unavailable.CompletedAt != wantStop {
		t.Fatalf("provider-unavailable event時刻 = %s/%s want %s", unavailable.StartedAt, unavailable.CompletedAt, wantStop)
	}
	if unavailable.RetryElapsedMS != wantElapsed.Milliseconds() {
		t.Fatalf("retry elapsed = %d want %d", unavailable.RetryElapsedMS, wantElapsed.Milliseconds())
	}
}

// --resumeのprobe gateは即時probeで始まり、再開task呼出・後続reviewer呼出のtimestamp/durationも
// 注入clockに従う。sleepなしでresumeされることも同じ経路で固定する。
func TestResumeTelemetryTimestampsFollowInjectedClock(t *testing.T) {
	st := newStateStoreT(t)
	seedProviderUnavailableCheckpoint(t, st)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("resumed")},
		{output: passPacket()},
	}}
	w, clock := newRecoveryWorkflowT(t, st, r)
	taskRun := 7 * time.Minute
	probeRun := 30 * time.Second
	r.onRun = func() { clock.now = clock.now.Add(taskRun) }
	r.onProbe = func() { clock.now = clock.now.Add(probeRun) }

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if len(clock.sleeps) != 0 {
		t.Fatalf("resume直後の即時probe前にsleepすべきでない: %v", clock.sleeps)
	}

	resumedStart := testFixedTime.Add(probeRun)
	var taskRecords []state.ModelCallLog
	for _, l := range taskLogs(t, st) {
		switch {
		case l.CallType == state.CallTypeProbe:
			if l.StartedAt != testFixedTime || l.CompletedAt != resumedStart {
				t.Fatalf("probe gate時刻 = %s/%s want %s/%s", l.StartedAt, l.CompletedAt, testFixedTime, resumedStart)
			}
			if l.WallDurationMS != probeRun.Milliseconds() {
				t.Fatalf("probe gate wall duration = %d", l.WallDurationMS)
			}
		case l.CallType == state.CallTypeTask:
			taskRecords = append(taskRecords, l)
		}
	}
	if len(taskRecords) != 2 {
		t.Fatalf("task記録 = %d want 2(worker resume+reviewer)", len(taskRecords))
	}
	reviewStart := resumedStart.Add(taskRun)
	wantTaskSpan := []struct{ startedAt, completedAt time.Time }{
		{resumedStart, reviewStart},
		{reviewStart, reviewStart.Add(taskRun)},
	}
	for i, want := range wantTaskSpan {
		got := taskRecords[i]
		if got.StartedAt != want.startedAt || got.CompletedAt != want.completedAt {
			t.Fatalf("task %d時刻 = %s/%s want %s/%s", i, got.StartedAt, got.CompletedAt, want.startedAt, want.completedAt)
		}
		if got.WallDurationMS != taskRun.Milliseconds() {
			t.Fatalf("task %d wall duration = %d", i, got.WallDurationMS)
		}
	}
	stats := currentStats(t, st)
	if stats.ModelDurationMSByAlias["opus"] != taskRun.Milliseconds() || stats.ModelDurationMSByAlias["haiku"] != taskRun.Milliseconds() {
		t.Fatalf("duration stats = %#v", stats.ModelDurationMSByAlias)
	}
}
