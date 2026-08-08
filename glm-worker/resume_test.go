package main

import (
	"strings"
	"testing"
)

func TestResumeCheckpointPersists(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}
	checkpoint := resumeCheckpoint{
		Stage:          resumeStageReview,
		Phase:          "reviewer-2",
		Role:           reviewerRole,
		ReadOnly:       true,
		Prompt:         "original",
		OriginalPrompt: "original",
		Request:        "request",
		ReviewNumber:   2,
		AutoFixes:      1,
		RateLimited:    true,
		ResetAtCST:     "2026-07-22 14:06:34",
		ResetAtRFC3339: "2026-07-22T14:06:34+08:00",
	}

	if err := state.SaveResumeCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}

	got, err := state.LoadResumeCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != checkpoint.Phase || got.ResetAtRFC3339 != checkpoint.ResetAtRFC3339 {
		t.Fatalf("unexpected checkpoint: %#v", got)
	}
}

func TestResumePromptUsesOriginalPrompt(t *testing.T) {
	checkpoint := resumeCheckpoint{
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
