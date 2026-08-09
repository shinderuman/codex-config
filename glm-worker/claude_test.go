package main

import "testing"

func TestModelForRole(t *testing.T) {
	runner := &claudeRunner{
		config: appConfig{
			WorkerModel:   "opus",
			ReviewerModel: "haiku",
		},
	}

	if got := runner.modelForRole(workerRole); got != "opus" {
		t.Fatalf("worker model = %q", got)
	}
	if got := runner.modelForRole(reviewerRole); got != "haiku" {
		t.Fatalf("reviewer model = %q", got)
	}
}

func TestSessionNameIncludesTaskID(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}
	if err := state.Write("task.id", "12345678-aaaa-bbbb-cccc-dddddddddddd"); err != nil {
		t.Fatal(err)
	}
	runner := &claudeRunner{
		config: appConfig{RepoShort: "abcdef123456"},
		state:  state,
	}

	got := runner.sessionName(workerRole)
	want := "glm-worker-abcdef123456-12345678"
	if got != want {
		t.Fatalf("session name = %q, want %q", got, want)
	}
}
