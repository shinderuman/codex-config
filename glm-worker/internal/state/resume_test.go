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
	if got.Phase != checkpoint.Phase || got.ResetAtRFC3339 != checkpoint.ResetAtRFC3339 || got.Effort != "high" {
		t.Fatalf("unexpected checkpoint: %#v", got)
	}
}

func TestClearResumeCheckpoint(t *testing.T) {
	st := &StateStore{dir: t.TempDir()}
	if err := st.SaveResumeCheckpoint(ResumeCheckpoint{Stage: ResumeStageWorker}); err != nil {
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

	if err := os.WriteFile(st.Path(resumeStateFile), []byte("{\"version\":2}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadResumeCheckpoint(); err == nil || !strings.Contains(err.Error(), "unsupported resume state version") {
		t.Fatalf("version error = %v", err)
	}
}
