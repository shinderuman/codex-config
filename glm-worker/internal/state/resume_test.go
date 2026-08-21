package state

import (
	"os"
	"strings"
	"testing"
)

func TestResumeCheckpointPersists(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	checkpoint := ResumeCheckpoint{
		Stage:          ResumeStageReview,
		Phase:          "reviewer-2",
		Role:           ReviewerRole,
		Model:          "sonnet",
		ReadOnly:       true,
		Effort:         "high",
		Prompt:         "original",
		OriginalPrompt: "original",
		Request:        "request",
		ReviewNumber:   2,
		AutoFixes:      1,
		RateLimited:    true,
		ResetAtCST:     "2026-07-22 14:06:34",
		ResetAtRFC3339: "2026-07-22T14:06:34+08:00",
	}

	if err := st.SaveResumeCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}

	got, err := st.LoadResumeCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != checkpoint.Phase || got.ResetAtRFC3339 != checkpoint.ResetAtRFC3339 || got.Effort != "high" || got.Model != "sonnet" {
		t.Fatalf("unexpected checkpoint: %#v", got)
	}

	checkpoint.Stage = ResumeStageAutoFix
	checkpoint.Phase = "worker-report-only-1"
	checkpoint.ReadOnly = true
	checkpoint.ReportOnly = true
	if err := st.SaveResumeCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	got, err = st.LoadResumeCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if !got.ReportOnly || !got.ReadOnly {
		t.Fatalf("report-only checkpoint field round-trip = %#v", got)
	}
}

func TestClearResumeCheckpoint(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if err := st.SaveResumeCheckpoint(ResumeCheckpoint{Stage: ResumeStageWorker, Model: "opus"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ClearResumeCheckpoint(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadResumeCheckpoint(); err == nil || !strings.Contains(err.Error(), "resumable task is not available") {
		t.Fatalf("clear後のload error = %v", err)
	}
}

func TestLoadResumeCheckpointRejectsCorruptionAndVersion(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if err := os.WriteFile(st.Path(resumeStateFile), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadResumeCheckpoint(); err == nil || !strings.Contains(err.Error(), "resume stateを読めません") {
		t.Fatalf("corruption error = %v", err)
	}

	if err := os.WriteFile(st.Path(resumeStateFile), []byte("{\"version\":1}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadResumeCheckpoint(); err == nil || !strings.Contains(err.Error(), "unsupported resume state version") {
		t.Fatalf("version error = %v", err)
	}
}

func TestResumeCheckpointRequiresModel(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if err := st.SaveResumeCheckpoint(ResumeCheckpoint{Stage: ResumeStageWorker}); err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("save error = %v", err)
	}

	if err := os.WriteFile(st.Path(resumeStateFile), []byte("{\"version\":2,\"stage\":\"worker\"}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadResumeCheckpoint(); err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("load error = %v", err)
	}
}

// v2 checkpoint(worker_packet表示行・packet_compacted)はv3表現へ等価変換してresumeさせる。
// 5h上限中断中のtaskがprotocol移行でresume不能にならないことの保証。
func TestLoadResumeCheckpointConvertsLegacyV2(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	legacy := `{"version":2,"stage":"reviewer","phase":"reviewer-1","model":"sonnet","request":"req","worker_packet":["STATUS: IMPLEMENTED","RISK: LOW","SUMMARY: s","TESTS: t"],"packet_compacted":true}`
	if err := os.WriteFile(st.Path(resumeStateFile), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadResumeCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkerResult == nil || got.WorkerResult.Status != "IMPLEMENTED" || got.WorkerResult.Summary != "s" {
		t.Fatalf("worker result変換 = %#v", got.WorkerResult)
	}
	if !got.ResultCorrection {
		t.Fatal("packet_compactedはresult_correctionへ読み替える")
	}
	if got.Version != resumeStateVersion {
		t.Fatalf("version = %d", got.Version)
	}

	broken := `{"version":2,"stage":"worker","model":"opus","worker_packet":["plain text"]}`
	if err := os.WriteFile(st.Path(resumeStateFile), []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadResumeCheckpoint(); err == nil || !strings.Contains(err.Error(), "worker_packetを変換できません") {
		t.Fatalf("broken v2 error = %v", err)
	}
}

func TestResumeCheckpointStopParentFilesRoundTrip(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	stop := &ParentFileStates{
		Plan:    ParentFileState{Exists: true, SHA256: "plan-sha"},
		History: ParentFileState{Exists: true, SHA256: "history-sha"},
	}
	if err := st.SaveResumeCheckpoint(ResumeCheckpoint{
		Stage:           ResumeStageReview,
		Phase:           "reviewer-1",
		Model:           "sonnet",
		RateLimited:     true,
		StopParentFiles: stop,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadResumeCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if got.StopParentFiles == nil || *got.StopParentFiles != *stop {
		t.Fatalf("stop parent files round-trip = %#v", got.StopParentFiles)
	}
}

func TestResumeCheckpointLegacyWithoutStopParentFiles(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	legacy := `{"version":3,"stage":"reviewer","phase":"reviewer-1","role":"reviewer","model":"sonnet","prompt":"p","request":"r","rate_limited":true}`
	if err := os.WriteFile(st.Path(resumeStateFile), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadResumeCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if got.StopParentFiles != nil {
		t.Fatalf("旧binary checkpointのstop_parent_filesはnil: %#v", got.StopParentFiles)
	}
}
