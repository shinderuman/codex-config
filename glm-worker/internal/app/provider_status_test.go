package app

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

func TestExecuteStatusShowsProviderUnavailable(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().Add(-51 * time.Minute).UTC()
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:                             state.ResumeStageWorker,
		Phase:                             "worker-new",
		Role:                              state.WorkerRole,
		Model:                             "opus",
		Effort:                            "high",
		Prompt:                            "p",
		Request:                           "req",
		ProviderUnavailable:               true,
		ProviderUnavailableClassification: "http-503",
		ProviderUnavailableProbes:         4,
		ProviderUnavailableStartedAt:      startedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusProviderUnavailable); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Execute(Command{Mode: ModeStatus}, cfg, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{
		"TASK_STATUS: provider-unavailable",
		"PROVIDER_UNAVAILABLE: yes",
		"PROVIDER_PHASE: worker-new",
		"PROVIDER_CLASSIFICATION: http-503",
		"PROVIDER_PROBES: 4",
		"RESUME_AVAILABLE: yes",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("status出力に%qがありません:\n%s", want, body)
		}
	}
	if strings.Contains(body, "RATE_LIMITED: yes") {
		t.Fatalf("provider-unavailable時にRATE_LIMITED: yesが出てはいけない:\n%s", body)
	}
}

func TestExecuteStatusReportsProviderUnavailableNoWhenClean(t *testing.T) {
	cfg := newAppConfig(t)
	var out bytes.Buffer
	if err := Execute(Command{Mode: ModeStatus}, cfg, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "PROVIDER_UNAVAILABLE: no") {
		t.Fatalf("空状態でPROVIDER_UNAVAILABLE: noが出るべき: %q", out.String())
	}
	if !strings.Contains(out.String(), "RESUME_AVAILABLE: no") {
		t.Fatalf("空状態でRESUME_AVAILABLE: noが出るべき: %q", out.String())
	}
}

func TestPrintStatsAggregatesProviderUnavailable(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	st.RecordProviderUnavailable("opus")
	st.RecordProviderUnavailable("opus")
	st.RecordProviderUnavailable("haiku")

	var out bytes.Buffer
	if err := printStats(st, &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, "PROVIDER_UNAVAILABLE: 3") {
		t.Fatalf("provider-unavailable集計がありません: %q", body)
	}
	if !strings.Contains(body, "PROVIDER_UNAVAILABLE_BY_ALIAS: haiku=1,opus=2") {
		t.Fatalf("model別provider-unavailable集計がありません: %q", body)
	}
}

// 新診断集計(risk floor / snapshot mismatch axis / packet reject / probe outcome)が
// --statsへ少数表示される。空ならnone。
func TestPrintStatsReportsDiagnosticAggregates(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	st.RecordRiskFloor("worker-declared")
	st.RecordRiskFloor("worker-declared")
	st.RecordRiskFloor("self-protection")
	st.RecordSnapshotMismatch("head,index")
	st.RecordPacketReject("size")
	st.RecordPacketReject("malformed")
	st.RecordProbeOutcome("probe_failure")
	st.RecordProbeOutcome("probe_success")
	st.RecordTransientRetry()

	var out bytes.Buffer
	if err := printStats(st, &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{
		"RISK_FLOOR_BY_CATEGORY: self-protection=1,worker-declared=2",
		"SNAPSHOT_MISMATCHES: 1",
		"SNAPSHOT_MISMATCH_BY_AXIS: head=1,index=1",
		"PACKET_REJECT_BY_CATEGORY: malformed=1,size=1",
		"PROBE_OUTCOME: probe_failure=1,probe_success=1",
		"PROBE_CALLS: 2",
		"TOTAL_AI_CALLS: 2",
		"TRANSIENT_RETRIES: 1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("stats出力に%qがありません:\n%s", want, body)
		}
	}
}

func TestPrintStatsReportsDiagnosticAggregatesEmpty(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printStats(st, &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{
		"RISK_FLOOR_BY_CATEGORY: none",
		"SNAPSHOT_MISMATCHES: 0",
		"SNAPSHOT_MISMATCH_BY_AXIS: none",
		"PACKET_REJECT_BY_CATEGORY: none",
		"PROBE_OUTCOME: none",
		"PROBE_CALLS: 0",
		"TOTAL_AI_CALLS: 0",
		"TRANSIENT_RETRIES: 0",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("空状態のstats出力に%qがありません:\n%s", want, body)
		}
	}
}
