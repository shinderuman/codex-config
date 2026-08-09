package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shinderuman/codex-config/glm-worker/internal/config"
	"github.com/shinderuman/codex-config/glm-worker/internal/state"
)

func newTestStateStore(t *testing.T) *state.StateStore {
	t.Helper()
	st, err := state.NewStateStore(config.AppConfig{
		StateBase: t.TempDir(),
		RepoHash:  "runnerhash",
		RepoRoot:  "/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestSessionNameIncludesTaskID(t *testing.T) {
	st := newTestStateStore(t)
	if err := st.Write("task.id", "12345678-aaaa-bbbb-cccc-dddddddddddd"); err != nil {
		t.Fatal(err)
	}
	r := &ClaudeRunner{
		config: config.AppConfig{RepoShort: "abcdef123456"},
		state:  st,
	}

	got := r.sessionName(state.WorkerRole)
	want := "glm-worker-abcdef123456-12345678"
	if got != want {
		t.Fatalf("session name = %q, want %q", got, want)
	}
}

func TestClaudeRunnerRunStartsThenResumesSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}

	repository := t.TempDir()
	promptDir := t.TempDir()
	for _, name := range []string{"WORKER.md", "REVIEWER.md"} {
		if err := os.WriteFile(filepath.Join(promptDir, name), []byte("system"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	argumentsPath := filepath.Join(t.TempDir(), "args")
	commandPath := filepath.Join(t.TempDir(), "fake-claude")
	commandScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$GLM_ARGS_FILE\"\nprintf '%s\\n' 'runner output'\n"
	if err := os.WriteFile(commandPath, []byte(commandScript), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLM_ARGS_FILE", argumentsPath)

	st := newTestStateStore(t)
	if err := st.Write("task.id", "12345678-aaaa-bbbb-cccc-dddddddddddd"); err != nil {
		t.Fatal(err)
	}
	r := NewClaudeRunner(config.AppConfig{
		RepoRoot:      repository,
		RepoShort:     "abcdef123456",
		PromptDir:     promptDir,
		ClaudeBin:     commandPath,
		WorkerModel:   "worker-model",
		ReviewerModel: "reviewer-model",
	}, st)

	firstOutput := filepath.Join(t.TempDir(), "first.log")
	if err := r.Run(state.WorkerRole, "worker-model", false, "high", "first prompt", firstOutput); err != nil {
		t.Fatal(err)
	}
	firstArguments := readLines(t, argumentsPath)
	if !containsArgument(firstArguments, "--session-id") || containsArgument(firstArguments, "--resume") {
		t.Fatalf("初回引数 = %#v", firstArguments)
	}
	if !containsArgument(firstArguments, "worker-model") || !containsArgument(firstArguments, "first prompt") {
		t.Fatalf("worker引数 = %#v", firstArguments)
	}
	if output, err := os.ReadFile(firstOutput); err != nil || string(output) != "runner output\n" {
		t.Fatalf("output = %q, err = %v", output, err)
	}

	secondOutput := filepath.Join(t.TempDir(), "second.log")
	if err := r.Run(state.WorkerRole, "override-model", true, "max", "second prompt", secondOutput); err != nil {
		t.Fatal(err)
	}
	secondArguments := readLines(t, argumentsPath)
	if !containsArgument(secondArguments, "--resume") || containsArgument(secondArguments, "--session-id") {
		t.Fatalf("resume引数 = %#v", secondArguments)
	}
	for _, argument := range []string{"--disallowedTools", "Edit", "Write", "NotebookEdit", "second prompt"} {
		if !containsArgument(secondArguments, argument) {
			t.Fatalf("read-only引数%qがありません: %#v", argument, secondArguments)
		}
	}
	if !containsArgument(secondArguments, "override-model") {
		t.Fatalf("model overrideがありません: %#v", secondArguments)
	}
}

func TestClaudeRunnerRejectsMissingPrompt(t *testing.T) {
	st := newTestStateStore(t)
	if err := st.Write("task.id", "12345678-aaaa-bbbb-cccc-dddddddddddd"); err != nil {
		t.Fatal(err)
	}
	r := NewClaudeRunner(config.AppConfig{
		PromptDir: t.TempDir(),
		ClaudeBin: "unused",
	}, st)

	err := r.Run(state.WorkerRole, "worker-model", false, "high", "prompt", filepath.Join(t.TempDir(), "output"))
	if err == nil || !strings.Contains(err.Error(), "required promptがありません") {
		t.Fatalf("missing prompt error = %v", err)
	}
}

func TestPromptFileNameByRole(t *testing.T) {
	if got := promptFileName(state.WorkerRole); got != "WORKER.md" {
		t.Fatalf("worker prompt = %q", got)
	}
	if got := promptFileName(state.ReviewerRole); got != "REVIEWER.md" {
		t.Fatalf("reviewer prompt = %q", got)
	}
}

func TestEnvWithDefaultsPreservesExistingValues(t *testing.T) {
	result := envWithDefaults(
		[]string{"EXISTING=original", "OTHER=value"},
		map[string]string{"EXISTING": "default", "ADDED": "new"},
	)
	joined := strings.Join(result, "\n")
	if !strings.Contains(joined, "EXISTING=original") || strings.Contains(joined, "EXISTING=default") {
		t.Fatalf("existing value = %#v", result)
	}
	if !strings.Contains(joined, "ADDED=new") {
		t.Fatalf("default value = %#v", result)
	}
}

func TestZaiRateLimitErrorIncludesResumeMetadata(t *testing.T) {
	err := ZaiRateLimitError{
		Phase:     "reviewer-1",
		TaskID:    "12345678-aaaa-bbbb-cccc-dddddddddddd",
		RepoRoot:  "/repo",
		RepoShort: "abcdef123456",
		Limit: ZaiFiveHourLimit{
			ResetAtCST:     "2026-08-09 22:35:58",
			ResetAtRFC3339: "2026-08-09T22:35:58+08:00",
		},
	}.Error()

	for _, value := range []string{
		"STATUS: RATE_LIMITED",
		"PHASE: reviewer-1",
		"TASK_ID: 12345678-aaaa-bbbb-cccc-dddddddddddd",
		"REPO_ROOT: /repo",
		"RESET_AT_CST: 2026-08-09 22:35:58",
		"AUTO_RESUME_AVAILABLE: true",
		"AUTO_RESUME_AT_RFC3339: 2026-08-09T22:37:58+08:00",
		"AUTO_RESUME_KEY: glm-worker-resume-abcdef123456-12345678",
		"RESUME_COMMAND: glm-worker --resume",
	} {
		if !strings.Contains(err, value) {
			t.Fatalf("rate limit errorに%qがありません: %s", value, err)
		}
	}
}

func TestZaiRateLimitErrorDisablesAutoResumeWithoutResetTime(t *testing.T) {
	err := ZaiRateLimitError{Phase: "worker-new"}.Error()
	for _, value := range []string{
		"TASK_ID: unknown",
		"REPO_ROOT: unknown",
		"AUTO_RESUME_AVAILABLE: false",
		"AUTO_RESUME_AT_RFC3339: unknown",
		"AUTO_RESUME_KEY: glm-worker-resume-unknown-repo-unknown-task",
	} {
		if !strings.Contains(err, value) {
			t.Fatalf("rate limit fallbackに%qがありません: %s", value, err)
		}
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func containsArgument(arguments []string, want string) bool {
	for _, argument := range arguments {
		if argument == want {
			return true
		}
	}
	return false
}
