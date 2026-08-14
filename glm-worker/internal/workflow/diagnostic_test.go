package workflow

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/runner"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

func taskLogs(t *testing.T, st *state.StateStore) []state.ModelCallLog {
	t.Helper()
	taskID, err := st.TaskID()
	if err != nil {
		t.Fatal(err)
	}
	logs, err := st.ReadModelCallLogs(taskID)
	if err != nil {
		t.Fatal(err)
	}
	return logs
}

func findLogByPhase(t *testing.T, logs []state.ModelCallLog, phase string) state.ModelCallLog {
	t.Helper()
	for _, l := range logs {
		if l.Phase == phase {
			return l
		}
	}
	t.Fatalf("log phase %qがありません: %+v", phase, phasesOf(logs))
	return state.ModelCallLog{}
}

func phasesOf(logs []state.ModelCallLog) []string {
	out := make([]string, 0, len(logs))
	for _, l := range logs {
		out = append(out, l.Phase)
	}
	return out
}

// HIGH risk workerのreviewer記録へはworker/reviewer/effective riskとrisk floor source/category・
// snapshotが付与され、statsへfloor計上がされる。reemit呼出にはEffectiveRiskが無い(二重計上防止)。
func TestDiagnosticRecordsRiskFloorOnHighRiskReviewer(t *testing.T) {
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

	logs := taskLogs(t, st)
	worker := findLogByPhase(t, logs, "worker-new")
	if worker.WorkerReportedRisk != "HIGH" {
		t.Fatalf("worker reported risk = %q want HIGH", worker.WorkerReportedRisk)
	}
	if worker.EffectiveRisk != "" || worker.RiskFloorCategory != "" {
		t.Fatalf("worker callへrisk floorが漏れている: %+v", worker)
	}
	reviewer := findLogByPhase(t, logs, "reviewer-1")
	if reviewer.ReviewerReportedRisk != "LOW" || reviewer.EffectiveRisk != "HIGH" {
		t.Fatalf("reviewer risk diag = %+v", reviewer)
	}
	if reviewer.RiskFloorSource != "worker-declared" || reviewer.RiskFloorCategory != "worker-declared" {
		t.Fatalf("risk floor source/category = %q/%q", reviewer.RiskFloorSource, reviewer.RiskFloorCategory)
	}
	if reviewer.Snapshot == nil || reviewer.Snapshot.Matched == nil || !*reviewer.Snapshot.Matched {
		t.Fatalf("reviewer成功時の一致snapshotが付与されていません: %+v", reviewer.Snapshot)
	}
	if reviewer.Snapshot.MismatchAxis != "" {
		t.Fatalf("一致snapshotにmismatch軸が設定されている: %q", reviewer.Snapshot.MismatchAxis)
	}
	reemit := findLogByPhase(t, logs, "reviewer-1-risk-floor")
	if reemit.EffectiveRisk != "" || reemit.RiskFloorCategory != "" {
		t.Fatalf("reemit呼出へfloorが二重記録されている: %+v", reemit)
	}

	stats := currentStats(t, st)
	if stats.RiskFloorByCategory["worker-declared"] != 1 {
		t.Fatalf("risk floor集計 = %+v", stats.RiskFloorByCategory)
	}
}

// LOW risk通常経路はrisk floor無し・EffectiveRisk空となる。
func TestDiagnosticNoRiskFloorOnLowRiskPass(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	logs := taskLogs(t, st)
	reviewer := findLogByPhase(t, logs, "reviewer-1")
	if reviewer.EffectiveRisk != "LOW" {
		t.Fatalf("LOW経路のEffectiveRisk = %q want LOW(captured)", reviewer.EffectiveRisk)
	}
	if reviewer.RiskFloorCategory != "" || reviewer.RiskFloorSource != "" {
		t.Fatalf("LOW経路にrisk floor source/categoryが有る: %+v", reviewer)
	}
	if reviewer.Snapshot == nil || reviewer.Snapshot.Matched == nil || !*reviewer.Snapshot.Matched {
		t.Fatalf("LOW経路でも一致snapshotは付与されるべき: %+v", reviewer.Snapshot)
	}
	if stats := currentStats(t, st); len(stats.RiskFloorByCategory) != 0 {
		t.Fatalf("risk floor集計が有る: %+v", stats.RiskFloorByCategory)
	}
}

// snapshot不一致はtelemetryへsnapshot_mismatch eventとmismatch軸を記録し、statsへ軸別計上する。
func TestDiagnosticRecordsSnapshotMismatch(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)
	workerEnd := state.GitSnapshot{Head: "a", IndexDigest: "a", WorktreeDigest: "a"}
	reviewStart := state.GitSnapshot{Head: "b", IndexDigest: "b", WorktreeDigest: "b"}
	calls := 0
	w.captureSnapshot = func(string) (state.GitSnapshot, error) {
		snaps := []state.GitSnapshot{workerEnd, reviewStart}
		s := snaps[calls]
		calls++
		return s, nil
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	var mismatch state.ModelCallLog
	found := false
	for _, l := range taskLogs(t, st) {
		if l.Outcome == "snapshot_mismatch" {
			mismatch = l
			found = true
		}
	}
	if !found {
		t.Fatal("snapshot_mismatch eventがありません")
	}
	if mismatch.Snapshot == nil || mismatch.Snapshot.Matched == nil || *mismatch.Snapshot.Matched {
		t.Fatalf("mismatch snapshotのMatched=falseではありません: %+v", mismatch.Snapshot)
	}
	axis := mismatch.Snapshot.MismatchAxis
	for _, want := range []string{"head", "index", "worktree"} {
		if !strings.Contains(axis, want) {
			t.Fatalf("mismatch axis %qに%qがありません", axis, want)
		}
	}
	if mismatch.Snapshot.Previous == nil || mismatch.Snapshot.Current == nil {
		t.Fatalf("両snapshot digestが記録されているべき: %+v", mismatch.Snapshot)
	}
	if mismatch.Snapshot.Previous.Head != "a" || mismatch.Snapshot.Current.Head != "b" {
		t.Fatalf("snapshot digestのprevious/currentが区別されていません: %+v", mismatch.Snapshot)
	}
	stats := currentStats(t, st)
	if stats.SnapshotMismatches != 1 || stats.SnapshotMismatchByAxis["head"] != 1 || stats.SnapshotMismatchByAxis["worktree"] != 1 {
		t.Fatalf("snapshot mismatch集計 = %+v", stats.SnapshotMismatchByAxis)
	}
}

// snapshot取得失敗は比較未実施(snapshot_unavailable)で、mismatch軸集計へ混ぜない。
func TestDiagnosticSnapshotCaptureFailureNotCountedAsMismatch(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.captureSnapshot = func(string) (state.GitSnapshot, error) {
		return state.GitSnapshot{}, errors.New("snapshot unavailable")
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	var event state.ModelCallLog
	found := false
	for _, l := range taskLogs(t, st) {
		if l.Outcome == "snapshot_unavailable" {
			event = l
			found = true
		}
	}
	if !found {
		t.Fatal("snapshot_unavailable eventがありません")
	}
	if event.Snapshot == nil || event.Snapshot.Matched != nil {
		t.Fatalf("取得失敗はMatched=nil(未比較)のべき: %+v", event.Snapshot)
	}
	stats := currentStats(t, st)
	if stats.SnapshotMismatches != 0 || len(stats.SnapshotMismatchByAxis) != 0 {
		t.Fatalf("取得失敗がmismatch集計へ混ざっている: %+v", stats.SnapshotMismatchByAxis)
	}
}

// invalid_packetはreject categoryを記録し、statsへcategory別計上する。
func TestDiagnosticRecordsPacketReject(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: "PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: LOW\nSUMMARY: " + strings.Repeat("x", 5000) + "\nREQUIREMENT_COVERAGE: covered\nTESTS: pass\nUNVERIFIED: none\nARTIFACTS: none\nPACKET_END\n"},
		{output: implementedPacket("ok")},
	}}
	w := newWorkflowT(t, st, r)
	w.temp = t.TempDir()

	if _, err := w.runModel(state.ResumeCheckpoint{
		Stage: state.ResumeStageWorker, Phase: "worker-new", Role: state.WorkerRole,
		Model: "opus", Effort: "high", Prompt: "p", Request: "req",
	}); err != nil {
		t.Fatal(err)
	}
	logs := taskLogs(t, st)
	invalid := findLogByPhase(t, logs, "worker-new")
	if invalid.Outcome != "invalid_packet" || invalid.PacketRejectReason != "size" {
		t.Fatalf("invalid_packet diag = outcome=%q reject=%q", invalid.Outcome, invalid.PacketRejectReason)
	}
	if stats := currentStats(t, st); stats.PacketRejectByCategory["size"] != 1 {
		t.Fatalf("packet reject集計 = %+v", stats.PacketRejectByCategory)
	}
}

// provider一時障害はclassificationをtransient記録へ、elapsed/probesをprovider-unavailable eventへ残す。
func TestDiagnosticRecordsProviderClassificationAndElapsed(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{
		steps:     []runnerStep{{output: "API Error: 503 Service Unavailable", runErr: errors.New("exit status 1")}},
		probeErrs: []error{errProbeTransient, errProbeTransient, errProbeTransient, errProbeTransient},
	}
	w, _ := newRecoveryWorkflowT(t, st, r)
	w.temp = t.TempDir()

	_, err := w.runModel(workerCheckpoint())
	var pErr *runner.ProviderUnavailableError
	if !errors.As(err, &pErr) {
		t.Fatalf("ProviderUnavailableErrorを期待: %v", err)
	}
	logs := taskLogs(t, st)
	var transient state.ModelCallLog
	var unavailable state.ModelCallLog
	for _, l := range logs {
		if l.Outcome == "transient_error" && transient.Phase == "" {
			transient = l
		}
		if l.Outcome == "provider_unavailable" {
			unavailable = l
		}
	}
	if transient.ProviderClassification != "http-503" {
		t.Fatalf("transient記録のclassification = %q want http-503", transient.ProviderClassification)
	}
	if unavailable.ProviderClassification != "http-503" || unavailable.ProbeAttempt != 4 {
		t.Fatalf("provider-unavailable event = class=%q probes=%d", unavailable.ProviderClassification, unavailable.ProbeAttempt)
	}
	if unavailable.RetryElapsedMS <= 0 {
		t.Fatalf("retry elapsed = %d", unavailable.RetryElapsedMS)
	}
	stats := currentStats(t, st)
	if stats.ProbeOutcome["probe_failure"] != 4 {
		t.Fatalf("probe outcome集計 = %+v", stats.ProbeOutcome)
	}
}

// probe呼出はattempt番号を構造化fieldへ残す。
func TestDiagnosticRecordsProbeAttempt(t *testing.T) {
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

	if _, err := w.runModel(workerCheckpoint()); err != nil {
		t.Fatal(err)
	}
	logs := taskLogs(t, st)
	var attempts []int
	for _, l := range logs {
		if l.Outcome == "probe_failure" || l.Outcome == "probe_success" {
			attempts = append(attempts, l.ProbeAttempt)
		}
	}
	if len(attempts) != 2 || attempts[0] != 1 || attempts[1] != 2 {
		t.Fatalf("probe attempts = %+v", attempts)
	}
}

// resume直後の呼出にresume source(rate-limit/provider-unavailable)が記録される。
func TestDiagnosticRecordsResumeSourceRateLimit(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage: state.ResumeStageWorker, Phase: "worker-new", Role: state.WorkerRole,
		Model: "opus", Effort: "high", Prompt: "p", OriginalPrompt: "p",
		Request: "req", RateLimited: true,
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
	logs := taskLogs(t, st)
	worker := findLogByPhase(t, logs, "worker-new")
	if worker.ResumeSource != "rate-limit" {
		t.Fatalf("resume source = %q want rate-limit", worker.ResumeSource)
	}
	reviewer := findLogByPhase(t, logs, "reviewer-1")
	if reviewer.ResumeSource != "" {
		t.Fatalf("2呼出目へresume sourceが漏れている: %q", reviewer.ResumeSource)
	}
}

func TestDiagnosticRecordsResumeSourceProviderUnavailable(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage: state.ResumeStageWorker, Phase: "worker-new", Role: state.WorkerRole,
		Model: "opus", Effort: "high", Prompt: "p", OriginalPrompt: "p",
		Request: "req", ProviderUnavailable: true,
		ProviderUnavailableClassification: "http-503", ProviderUnavailableProbes: 4,
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
		t.Fatal(err)
	}
	logs := taskLogs(t, st)
	worker := findLogByPhase(t, logs, "worker-new")
	if worker.ResumeSource != "provider-unavailable" {
		t.Fatalf("resume source = %q want provider-unavailable", worker.ResumeSource)
	}
}

// rate-limitで中断されたreviewer resumeは、保存HIGH floorをtelemetryでも引き継ぐ。
func TestDiagnosticResumePreservesSavedHighRiskFloor(t *testing.T) {
	st := newStateStoreT(t)
	seedReviewStartSnapshot(t, st)
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:               state.ResumeStageReview,
		Phase:               "reviewer-1",
		Role:                state.ReviewerRole,
		Model:               "sonnet",
		ReadOnly:            true,
		Effort:              "high",
		Prompt:              "review",
		OriginalPrompt:      "review",
		Request:             "request",
		WorkerPacket:        workerPacketLines(),
		ReviewNumber:        1,
		RateLimited:         true,
		EffectiveRisk:       "HIGH",
		EffectiveRiskSource: "worker-declared",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	logs := taskLogs(t, st)
	reviewer := findLogByPhase(t, logs, "reviewer-1")
	if reviewer.EffectiveRisk != "HIGH" || reviewer.RiskFloorSource != "worker-declared" {
		t.Fatalf("resume後のrisk floorが失われた: %+v", reviewer)
	}
	if reviewer.RiskFloorCategory != "worker-declared" {
		t.Fatalf("resume後のrisk floor category = %q", reviewer.RiskFloorCategory)
	}
	if reviewer.ResumeSource != "rate-limit" {
		t.Fatalf("resume source = %q want rate-limit", reviewer.ResumeSource)
	}
	stats := currentStats(t, st)
	if stats.RiskFloorByCategory["worker-declared"] != 1 {
		t.Fatalf("risk floor集計 = %+v", stats.RiskFloorByCategory)
	}
}

// reviewerがrisk floor再出力要求を無視してPASSを返しても、reemit呼出の記録には
// floor情報が無く(floor自体は元reviewer呼出へ1回だけ計上)、reporter riskのみ残る。
func TestDiagnosticRiskFloorReemitCallHasNoFloorDiagnostics(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithRisk("risky", "HIGH")},
		{output: passPacket()},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	logs := taskLogs(t, st)
	reviewer := findLogByPhase(t, logs, "reviewer-1")
	if reviewer.EffectiveRisk != "HIGH" || reviewer.RiskFloorCategory != "worker-declared" {
		t.Fatalf("reviewer floor diag = %+v", reviewer)
	}
	reemit := findLogByPhase(t, logs, "reviewer-1-risk-floor")
	if reemit.EffectiveRisk != "" || reemit.RiskFloorCategory != "" || reemit.RiskFloorSource != "" {
		t.Fatalf("reemit呼出へfloorが記録されている: %+v", reemit)
	}
	if reemit.ReviewerReportedRisk != "LOW" {
		t.Fatalf("reemit reporter risk = %q", reemit.ReviewerReportedRisk)
	}
	if reemit.Snapshot != nil {
		t.Fatalf("reemit呼出へsnapshotが付与されている: %+v", reemit.Snapshot)
	}
	stats := currentStats(t, st)
	if stats.RiskFloorByCategory["worker-declared"] != 1 {
		t.Fatalf("reemitでfloorが二重計上された: %+v", stats.RiskFloorByCategory)
	}
}

// TelemetryContent=falseはprompt/response/system contentだけを隠し、risk・snapshot等の
// 診断metadataは隠さない(contentはtoken集計やhashで別途保持)。
func TestDiagnosticPersistsWhenTelemetryContentDisabled(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithRisk("secret work", "HIGH")},
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.config.TelemetryContent = false

	if err := w.ExecuteNewTask("secret request"); err != nil {
		t.Fatal(err)
	}
	logs := taskLogs(t, st)
	worker := findLogByPhase(t, logs, "worker-new")
	if worker.Prompt != "" || worker.Response != "" || worker.SystemPrompt != "" {
		t.Fatalf("content無効時はprompt/response/system contentが隠れるべき: %+v", worker)
	}
	if worker.WorkerReportedRisk != "HIGH" {
		t.Fatalf("content無効時もworker報告riskは保持されるべき: %q", worker.WorkerReportedRisk)
	}
	reviewer := findLogByPhase(t, logs, "reviewer-1")
	if reviewer.Prompt != "" || reviewer.Response != "" {
		t.Fatalf("content無効時はreviewer contentも隠れるべき: %+v", reviewer)
	}
	if reviewer.ReviewerReportedRisk != "LOW" || reviewer.EffectiveRisk != "HIGH" || reviewer.RiskFloorCategory != "worker-declared" {
		t.Fatalf("content無効時もrisk診断metadataは保持されるべき: %+v", reviewer)
	}
	if reviewer.Snapshot == nil || reviewer.Snapshot.Matched == nil || !*reviewer.Snapshot.Matched {
		t.Fatalf("content無効時も一致snapshotは保持されるべき: %+v", reviewer.Snapshot)
	}
	stats := currentStats(t, st)
	if stats.RiskFloorByCategory["worker-declared"] != 1 {
		t.Fatalf("content無効時もrisk floor集計は計上されるべき: %+v", stats.RiskFloorByCategory)
	}
}

// 比較は一致でもSaveSnapshotComparisonが失敗したfail-closedはsnapshot_save_failedとして
// 記録し、snapshot_mismatchと取り違えない(mismatch集計へも計上しない)。
func TestDiagnosticSnapshotSaveFailureNotMismatch(t *testing.T) {
	st := newStateStoreT(t)
	if err := st.SaveWorkerEndSnapshot(state.GitSnapshot{Head: "a", IndexDigest: "a", WorktreeDigest: "a"}); err != nil {
		t.Fatal(err)
	}
	// comparison file pathへ非空dirを置きSaveSnapshotComparisonを失敗させる。
	blockerDir := st.Path("snapshot-comparison.json")
	if err := os.MkdirAll(blockerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockerDir, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := newWorkflowT(t, st, &scriptedRunner{})
	w.captureSnapshot = func(string) (state.GitSnapshot, error) {
		return state.GitSnapshot{Head: "a", IndexDigest: "a", WorktreeDigest: "a"}, nil
	}

	stopped, err := w.verifyReviewStartSnapshot()
	if err != nil || !stopped {
		t.Fatalf("save失敗はfail closed停止すべき: stopped=%v err=%v", stopped, err)
	}
	var event state.ModelCallLog
	found := false
	for _, l := range taskLogs(t, st) {
		if l.Outcome == "snapshot_save_failed" {
			event = l
			found = true
		}
	}
	if !found {
		t.Fatalf("snapshot_save_failed eventがありません: %+v", phasesOf(taskLogs(t, st)))
	}
	if event.Snapshot == nil || event.Snapshot.Matched == nil || !*event.Snapshot.Matched {
		t.Fatalf("比較一致のsave失敗はMatched=trueのべき: %+v", event.Snapshot)
	}
	if event.Snapshot.MismatchAxis != "" {
		t.Fatalf("一致比較にmismatch軸が設定されている: %q", event.Snapshot.MismatchAxis)
	}
	stats := currentStats(t, st)
	if stats.SnapshotMismatches != 0 || len(stats.SnapshotMismatchByAxis) != 0 {
		t.Fatalf("save失敗がmismatch集計へ混ざっている: %+v", stats.SnapshotMismatchByAxis)
	}
}
