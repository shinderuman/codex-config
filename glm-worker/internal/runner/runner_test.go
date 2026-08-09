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

	got := r.sessionName(state.WorkerRole, "12345678-aaaa-bbbb-cccc-dddddddddddd")
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
	commandScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$GLM_ARGS_FILE\"\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"runner output\\n\",\"duration_ms\":1200,\"duration_api_ms\":900,\"num_turns\":2,\"usage\":{\"input_tokens\":11,\"cache_creation_input_tokens\":12,\"cache_read_input_tokens\":13,\"output_tokens\":14},\"modelUsage\":{\"glm-5.2\":{\"inputTokens\":11,\"cacheCreationInputTokens\":12,\"cacheReadInputTokens\":13,\"outputTokens\":14}}}'\n"
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
	firstResult, err := r.Run(state.WorkerRole, "worker-model", false, "high", "first prompt", firstOutput)
	if err != nil {
		t.Fatal(err)
	}
	firstArguments := readLines(t, argumentsPath)
	if !containsArgument(firstArguments, "--session-id") || containsArgument(firstArguments, "--resume") {
		t.Fatalf("初回引数 = %#v", firstArguments)
	}
	if !containsArgument(firstArguments, "worker-model") || !containsArgument(firstArguments, "first prompt") {
		t.Fatalf("worker引数 = %#v", firstArguments)
	}
	if !containsArgument(firstArguments, "json") {
		t.Fatalf("JSON出力指定がありません: %#v", firstArguments)
	}
	if output, err := os.ReadFile(firstOutput); err != nil || string(output) != "runner output\n" {
		t.Fatalf("output = %q, err = %v", output, err)
	}
	if firstResult.Usage.InputTokens != 11 || firstResult.Usage.CacheReadInputTokens != 13 || firstResult.Usage.OutputTokens != 14 {
		t.Fatalf("usage = %#v", firstResult.Usage)
	}
	if firstResult.ModelUsage["glm-5.2"].OutputTokens != 14 || firstResult.SystemPromptSHA256 == "" || firstResult.SystemPrompt != "system" {
		t.Fatalf("run result = %#v", firstResult)
	}

	secondOutput := filepath.Join(t.TempDir(), "second.log")
	secondResult, err := r.Run(state.WorkerRole, "override-model", true, "max", "second prompt", secondOutput)
	if err != nil {
		t.Fatal(err)
	}
	if !secondResult.Resumed {
		t.Fatal("2回目がresumeとして記録されていません")
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

	_, err := r.Run(state.WorkerRole, "worker-model", false, "high", "prompt", filepath.Join(t.TempDir(), "output"))
	if err == nil || !strings.Contains(err.Error(), "required promptがありません") {
		t.Fatalf("missing prompt error = %v", err)
	}
}

func TestClaudeRunnerRejectsMissingTaskID(t *testing.T) {
	st := newTestStateStore(t)
	r := NewClaudeRunner(config.AppConfig{}, st)

	_, err := r.Run(state.WorkerRole, "worker-model", false, "high", "prompt", filepath.Join(t.TempDir(), "output"))
	if err == nil || !strings.Contains(err.Error(), "task.idがありません") {
		t.Fatalf("missing task ID error = %v", err)
	}
}

func TestClaudeRunnerPreservesErrorResultAndUsage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}
	repository := t.TempDir()
	promptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(promptDir, "WORKER.md"), []byte("system"), 0o600); err != nil {
		t.Fatal(err)
	}
	commandPath := filepath.Join(t.TempDir(), "fake-claude")
	commandScript := "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"error\",\"is_error\":true,\"result\":\"API Error: Request rejected (429) [1308][Usage limit reached for 5 hour.]\",\"usage\":{\"input_tokens\":5,\"output_tokens\":6}}'\nprintf '%s\\n' 'stderr diagnostic' >&2\nexit 1\n"
	if err := os.WriteFile(commandPath, []byte(commandScript), 0o700); err != nil {
		t.Fatal(err)
	}
	st := newTestStateStore(t)
	if err := st.Write("task.id", "12345678-aaaa-bbbb-cccc-dddddddddddd"); err != nil {
		t.Fatal(err)
	}
	r := NewClaudeRunner(config.AppConfig{
		RepoRoot:  repository,
		PromptDir: promptDir,
		ClaudeBin: commandPath,
	}, st)
	outputPath := filepath.Join(t.TempDir(), "error.log")
	result, err := r.Run(state.WorkerRole, "opus", false, "high", "prompt", outputPath)
	if err == nil {
		t.Fatal("exit statusを返す必要があります")
	}
	if result.Usage.InputTokens != 5 || result.Usage.OutputTokens != 6 {
		t.Fatalf("error usage = %#v", result.Usage)
	}
	data, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(data), "Usage limit reached") || !strings.Contains(string(data), "stderr diagnostic") {
		t.Fatalf("error output = %q", data)
	}
}

func TestClaudeRunnerRejectsInvalidJSONWithoutMarkingSessionReady(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}
	repository := t.TempDir()
	promptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(promptDir, "WORKER.md"), []byte("system"), 0o600); err != nil {
		t.Fatal(err)
	}
	commandPath := filepath.Join(t.TempDir(), "fake-claude")
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\nprintf '%s\\n' 'not json'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	st := newTestStateStore(t)
	if err := st.Write("task.id", "12345678-aaaa-bbbb-cccc-dddddddddddd"); err != nil {
		t.Fatal(err)
	}
	r := NewClaudeRunner(config.AppConfig{
		RepoRoot:  repository,
		PromptDir: promptDir,
		ClaudeBin: commandPath,
	}, st)
	_, err := r.Run(state.WorkerRole, "opus", false, "high", "prompt", filepath.Join(t.TempDir(), "output.log"))
	if err == nil || !strings.Contains(err.Error(), "JSON出力を解析できません") {
		t.Fatalf("invalid JSON error = %v", err)
	}
	if st.Exists("worker.ready") {
		t.Fatal("不正JSONでsessionをreadyにしてはいけません")
	}
}

func TestParseClaudeJSONResultAggregatesModelUsageFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	data := `{"type":"result","result":"packet","modelUsage":{"glm-4.7":{"inputTokens":3,"cacheCreationInputTokens":4,"cacheReadInputTokens":5,"outputTokens":6},"glm-5.1":{"inputTokens":7,"outputTokens":8}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := parseClaudeJSONResult(path)
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage.InputTokens != 10 || result.Usage.CacheCreationInputTokens != 4 || result.Usage.CacheReadInputTokens != 5 || result.Usage.OutputTokens != 14 {
		t.Fatalf("fallback usage = %#v", result.Usage)
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
