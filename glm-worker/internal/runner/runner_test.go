package runner

import (
	"encoding/json"
	"errors"
	"fmt"
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
	claudeConfigDir := filepath.Join(t.TempDir(), "claude-home")
	r := NewClaudeRunner(config.AppConfig{
		RepoRoot:        repository,
		RepoShort:       "abcdef123456",
		PromptDir:       promptDir,
		ClaudeBin:       commandPath,
		ClaudeConfigDir: claudeConfigDir,
		EnvAllowlist:    []string{"GLM_ARGS_FILE"},
		WorkerModel:     "worker-model",
		ReviewerModel:   "reviewer-model",
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
	settingsValue := argumentAfter(firstArguments, "--settings")
	if settingsValue == "" {
		t.Fatalf("隔離--settingsがありません: %#v", firstArguments)
	}
	var settingsPayload struct {
		ClaudeMdExcludes     []string `json:"claudeMdExcludes"`
		AutoMemoryEnabled    bool     `json:"autoMemoryEnabled"`
		DisableAllHooks      bool     `json:"disableAllHooks"`
		DisableBundledSkills bool     `json:"disableBundledSkills"`
		DisableWorkflows     bool     `json:"disableWorkflows"`
	}
	if err := json.Unmarshal([]byte(settingsValue), &settingsPayload); err != nil {
		t.Fatalf("--settingsの値がJSONではありません: %v: %q", err, settingsValue)
	}
	wantRules := filepath.Join(claudeConfigDir, "rules", "**")
	wantUserGlobal := filepath.Join(claudeConfigDir, "CLAUDE.md")
	if !containsString(settingsPayload.ClaudeMdExcludes, "**/CLAUDE.md") ||
		!containsString(settingsPayload.ClaudeMdExcludes, "**/CLAUDE.local.md") ||
		!containsString(settingsPayload.ClaudeMdExcludes, wantUserGlobal) ||
		!containsString(settingsPayload.ClaudeMdExcludes, wantRules) {
		t.Fatalf("claudeMdExcludes = %#v", settingsPayload.ClaudeMdExcludes)
	}
	if settingsPayload.AutoMemoryEnabled || !settingsPayload.DisableAllHooks || !settingsPayload.DisableBundledSkills || !settingsPayload.DisableWorkflows {
		t.Fatalf("隔離settings = %#v", settingsPayload)
	}
	if !containsArgument(firstArguments, "--safe-mode") {
		t.Fatalf("--safe-modeがありません: %#v", firstArguments)
	}
	if argumentAfter(firstArguments, "--setting-sources") != "" {
		t.Fatalf("setting-sourcesを空にする必要があります: %#v", firstArguments)
	}
	if !containsArgument(firstArguments, "--strict-mcp-config") {
		t.Fatalf("--strict-mcp-configがありません: %#v", firstArguments)
	}
	if got := argumentAfter(firstArguments, "--mcp-config"); got != `{"mcpServers":{}}` {
		t.Fatalf("--mcp-config = %q", got)
	}
	if !containsArgument(firstArguments, "--disable-slash-commands") {
		t.Fatalf("--disable-slash-commandsがありません: %#v", firstArguments)
	}
	if output, err := os.ReadFile(firstOutput); err != nil || string(output) != "runner output\n" {
		t.Fatalf("output = %q, err = %v", output, err)
	}
	if firstResult.TopLevelUsage.InputTokens != 11 || firstResult.TopLevelUsage.CacheReadInputTokens != 13 || firstResult.TopLevelUsage.OutputTokens != 14 {
		t.Fatalf("usage = %#v", firstResult.TopLevelUsage)
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
	for _, argument := range []string{"--disallowedTools", "Edit", "Write", "NotebookEdit", "Agent", "second prompt"} {
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
	if result.TopLevelUsage.InputTokens != 5 || result.TopLevelUsage.OutputTokens != 6 {
		t.Fatalf("error usage = %#v", result.TopLevelUsage)
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

func TestParseClaudeJSONResultKeepsTopLevelAndTreeUsageSeparate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	data := `{"type":"result","result":"packet","modelUsage":{"glm-4.7":{"inputTokens":3,"cacheCreationInputTokens":4,"cacheReadInputTokens":5,"outputTokens":6},"glm-5.1":{"inputTokens":7,"outputTokens":8}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := parseClaudeJSONResult(path)
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage != (TokenUsage{}) {
		t.Fatalf("top-level usage = %#v", result.Usage)
	}
	if result.ModelUsage["glm-4.7"].InputTokens != 3 || result.ModelUsage["glm-5.1"].OutputTokens != 8 {
		t.Fatalf("model usage = %#v", result.ModelUsage)
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

func TestBuildChildEnvDropsInjectionChannelsAndKeepsEssentials(t *testing.T) {
	t.Setenv("PATH", "/bin:/usr/bin")
	t.Setenv("HOME", "/tmp/child-home")
	t.Setenv("ANTHROPIC_BASE_URL", "/should/be/dropped/parent")
	t.Setenv("CLAUDE_CODE_SECRET_LEAK", "should-be-dropped")
	t.Setenv("ARBITRARY_USER_VAR", "should-be-dropped")

	result := buildChildEnv(
		nil,
		map[string]string{"ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic"},
		map[string]string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "500000"},
	)
	joined := strings.Join(result, "\n")
	if !strings.Contains(joined, "PATH=/bin:/usr/bin") || !strings.Contains(joined, "HOME=/tmp/child-home") {
		t.Fatalf("OS必須envが落ちています: %#v", result)
	}
	if !strings.Contains(joined, "ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic") {
		t.Fatalf("Z.ai設定envが注入されていません: %#v", result)
	}
	if strings.Contains(joined, "should/be/dropped/parent") || strings.Contains(joined, "should-be-dropped") {
		t.Fatalf("親のANTHROPIC_*/任意envが漏れています: %#v", result)
	}
	if !strings.Contains(joined, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=500000") {
		t.Fatalf("runner追加envが入りません: %#v", result)
	}
}

func TestBuildChildEnvHonorsExtraAllowlist(t *testing.T) {
	t.Setenv("GOPATH", "/custom/go")
	t.Setenv("UNRELATED", "no")

	result := buildChildEnv(
		[]string{"GOPATH"},
		nil,
		nil,
	)
	joined := strings.Join(result, "\n")
	if !strings.Contains(joined, "GOPATH=/custom/go") {
		t.Fatalf("extra allowlistが反映されていません: %#v", result)
	}
	if strings.Contains(joined, "UNRELATED=") {
		t.Fatalf("allowlist外のenvが漏れています: %#v", result)
	}
}

func TestBuildChildEnvSettingEnvOverridesParent(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "parent-token")

	result := buildChildEnv(
		nil,
		map[string]string{"ANTHROPIC_AUTH_TOKEN": "settings-token"},
		nil,
	)
	joined := strings.Join(result, "\n")
	if strings.Contains(joined, "parent-token") || !strings.Contains(joined, "ANTHROPIC_AUTH_TOKEN=settings-token") {
		t.Fatalf("settings.json由来envが親envへ上書きされていません: %#v", result)
	}
}

func TestZaiRateLimitErrorIncludesResumeMetadata(t *testing.T) {
	err := ZaiRateLimitError{
		Phase:           "reviewer-1",
		TaskID:          "12345678-aaaa-bbbb-cccc-dddddddddddd",
		RepoRoot:        "/repo",
		RepoShort:       "abcdef123456",
		ArtifactWarning: "artifactを保護できません",
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
		"ARTIFACT_WARNING: artifactを保護できません",
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

func argumentAfter(arguments []string, flag string) string {
	for index, argument := range arguments {
		if argument == flag && index+1 < len(arguments) {
			return arguments[index+1]
		}
	}
	return ""
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestIsolationSettingsBlocksAllScopesAndCustomizations(t *testing.T) {
	claudeConfigDir := filepath.Join(t.TempDir(), "claude-home")

	encoded, err := isolationSettings(claudeConfigDir)
	if err != nil {
		t.Fatal(err)
	}

	var payload struct {
		ClaudeMdExcludes     []string `json:"claudeMdExcludes"`
		AutoMemoryEnabled    bool     `json:"autoMemoryEnabled"`
		DisableAllHooks      bool     `json:"disableAllHooks"`
		DisableBundledSkills bool     `json:"disableBundledSkills"`
		DisableWorkflows     bool     `json:"disableWorkflows"`
	}
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		t.Fatalf("隔離settings JSONを解析できません: %v: %s", err, encoded)
	}
	wantRules := filepath.Join(claudeConfigDir, "rules", "**")
	wantUserGlobal := filepath.Join(claudeConfigDir, "CLAUDE.md")
	if !containsString(payload.ClaudeMdExcludes, "**/CLAUDE.md") ||
		!containsString(payload.ClaudeMdExcludes, "**/CLAUDE.local.md") ||
		!containsString(payload.ClaudeMdExcludes, wantUserGlobal) ||
		!containsString(payload.ClaudeMdExcludes, wantRules) {
		t.Fatalf("全階層CLAUDE.md除外が不足: %#v", payload.ClaudeMdExcludes)
	}
	if payload.AutoMemoryEnabled || !payload.DisableAllHooks || !payload.DisableBundledSkills || !payload.DisableWorkflows {
		t.Fatalf("customization無効化が不足: %#v", payload)
	}
}

func TestIsolationSettingsFallsBackToHomeForRules(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	encoded, err := isolationSettings("")
	if err != nil {
		t.Fatal(err)
	}

	var payload struct {
		ClaudeMdExcludes []string `json:"claudeMdExcludes"`
	}
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		t.Fatalf("隔離settings JSONを解析できません: %v: %s", err, encoded)
	}
	wantRules := filepath.Join(home, ".claude", "rules", "**")
	wantUserGlobal := filepath.Join(home, ".claude", "CLAUDE.md")
	if !containsString(payload.ClaudeMdExcludes, wantRules) {
		t.Fatalf("fallback時のrules除外がありません: %#v", payload.ClaudeMdExcludes)
	}
	if !containsString(payload.ClaudeMdExcludes, wantUserGlobal) {
		t.Fatalf("fallback時のuser global CLAUDE.md除外がありません: %#v", payload.ClaudeMdExcludes)
	}
}

func TestLoadSettingEnvExtractsOnlyAllowlistedKeys(t *testing.T) {
	claudeConfigDir := t.TempDir()
	settings := map[string]any{
		"env": map[string]any{
			"ANTHROPIC_AUTH_TOKEN":                     "zai-token",
			"ANTHROPIC_BASE_URL":                       "https://api.z.ai/api/anthropic",
			"ANTHROPIC_DEFAULT_OPUS_MODEL":             "glm-5.2",
			"API_TIMEOUT_MS":                           "3000000",
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
			"UNRELATED_ENV":                            "dropped",
		},
		"model":          "opus",
		"enabledPlugins": []string{"leak"},
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeConfigDir, "settings.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := loadSettingEnv(claudeConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	if result["ANTHROPIC_AUTH_TOKEN"] != "zai-token" || result["ANTHROPIC_BASE_URL"] != "https://api.z.ai/api/anthropic" {
		t.Fatalf("Z.ai必須keyが抽出されていません: %#v", result)
	}
	if result["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "glm-5.2" || result["API_TIMEOUT_MS"] != "3000000" {
		t.Fatalf("model alias/runtime keyが抽出されていません: %#v", result)
	}
	for key, value := range result {
		if key == "UNRELATED_ENV" || strings.Contains(value, "leak") {
			t.Fatalf("allowlist外のkey/valueが漏れています: %#v", result)
		}
	}
}

func TestLoadSettingEnvToleratesMissingFile(t *testing.T) {
	result, err := loadSettingEnv(t.TempDir())
	if err != nil {
		t.Fatalf("settings.json不在時は空mapが期待: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("不在時は空mapが期待: %#v", result)
	}
}

func TestClaudeRunnerReMintSessionOnStaleIsolationPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}
	promptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(promptDir, "WORKER.md"), []byte("system"), 0o600); err != nil {
		t.Fatal(err)
	}
	argumentsPath := filepath.Join(t.TempDir(), "args")
	commandPath := filepath.Join(t.TempDir(), "fake-claude")
	commandScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$GLM_ARGS_FILE\"\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"ok\\n\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n"
	if err := os.WriteFile(commandPath, []byte(commandScript), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLM_ARGS_FILE", argumentsPath)

	st := newTestStateStore(t)
	if err := st.Write("task.id", "12345678-aaaa-bbbb-cccc-dddddddddddd"); err != nil {
		t.Fatal(err)
	}
	staleSession := "stale-session-id"
	if err := st.Write("worker.id", staleSession); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkReady(state.WorkerRole); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsolationPolicy("claude-isolation-stale"); err != nil {
		t.Fatal(err)
	}

	r := NewClaudeRunner(config.AppConfig{
		RepoRoot:        t.TempDir(),
		PromptDir:       promptDir,
		ClaudeBin:       commandPath,
		ClaudeConfigDir: t.TempDir(),
		EnvAllowlist:    []string{"GLM_ARGS_FILE"},
	}, st)

	if _, err := r.Run(state.WorkerRole, "worker-model", false, "high", "prompt", filepath.Join(t.TempDir(), "out")); err != nil {
		t.Fatal(err)
	}

	arguments := readLines(t, argumentsPath)
	if containsArgument(arguments, "--resume") {
		t.Fatalf("旧policy sessionをresumeしました: %#v", arguments)
	}
	if !containsArgument(arguments, "--session-id") {
		t.Fatalf("新session採番がありません: %#v", arguments)
	}
	if containsArgument(arguments, staleSession) {
		t.Fatalf("旧session idが再利用されています: %#v", arguments)
	}
	if policy := st.IsolationPolicy(); policy != isolationPolicyVersion {
		t.Fatalf("policy = %q, want %q", policy, isolationPolicyVersion)
	}
}

// assertFullIsolationArgsはworker/reviewer・新規/resumeの全経路で同じ隔離arg setが
// 渡ることを検証する共有helper。一経路でも欠ければ全sessionで隔離が崩れるため、
// 個別testではなくここへ集約する。
func assertFullIsolationArgs(t *testing.T, args []string, claudeConfigDir string, expectReviewerAgentBlock bool) {
	t.Helper()
	if !containsArgument(args, "--safe-mode") {
		t.Fatalf("--safe-modeがありません: %#v", args)
	}
	if argumentAfter(args, "--setting-sources") != "" {
		t.Fatalf("setting-sourcesを空にする必要があります: %#v", args)
	}
	if !containsArgument(args, "--strict-mcp-config") {
		t.Fatalf("--strict-mcp-configがありません: %#v", args)
	}
	if got := argumentAfter(args, "--mcp-config"); got != `{"mcpServers":{}}` {
		t.Fatalf("--mcp-config = %q: %#v", got, args)
	}
	if !containsArgument(args, "--disable-slash-commands") {
		t.Fatalf("--disable-slash-commandsがありません: %#v", args)
	}
	settingsValue := argumentAfter(args, "--settings")
	if settingsValue == "" {
		t.Fatalf("隔離--settingsがありません: %#v", args)
	}
	var payload struct {
		ClaudeMdExcludes     []string `json:"claudeMdExcludes"`
		AutoMemoryEnabled    bool     `json:"autoMemoryEnabled"`
		DisableAllHooks      bool     `json:"disableAllHooks"`
		DisableBundledSkills bool     `json:"disableBundledSkills"`
		DisableWorkflows     bool     `json:"disableWorkflows"`
	}
	if err := json.Unmarshal([]byte(settingsValue), &payload); err != nil {
		t.Fatalf("隔離--settingsがJSONではありません: %v: %q", err, settingsValue)
	}
	wantRules := filepath.Join(claudeConfigDir, "rules", "**")
	wantUserGlobal := filepath.Join(claudeConfigDir, "CLAUDE.md")
	if !containsString(payload.ClaudeMdExcludes, "**/CLAUDE.md") ||
		!containsString(payload.ClaudeMdExcludes, "**/CLAUDE.local.md") ||
		!containsString(payload.ClaudeMdExcludes, wantUserGlobal) ||
		!containsString(payload.ClaudeMdExcludes, wantRules) {
		t.Fatalf("claudeMdExcludesが不完全: %#v", payload.ClaudeMdExcludes)
	}
	if payload.AutoMemoryEnabled || !payload.DisableAllHooks || !payload.DisableBundledSkills || !payload.DisableWorkflows {
		t.Fatalf("customization無効化が不完全: %#v", payload)
	}
	// reviewerはAgent禁止、workerはAgent許可。経路(resume含む)で変わらない。
	hasAgentDisallowed := false
	for _, argument := range args {
		if argument == "--disallowedTools" {
			hasAgentDisallowed = true
			break
		}
	}
	if expectReviewerAgentBlock != hasAgentDisallowed {
		t.Fatalf("reviewer Agent禁止/worker Agent許可が期待と違います(expect=%v): %#v", expectReviewerAgentBlock, args)
	}
	if expectReviewerAgentBlock {
		for _, blocked := range []string{"Edit", "Write", "NotebookEdit", "Agent"} {
			if !containsArgument(args, blocked) {
				t.Fatalf("reviewerのdisallowedTools%qがありません: %#v", blocked, args)
			}
		}
	}
}

// TestIsolationArgsIdenticalAcrossRoleAndResumeはworker/reviewer × 新規/resume の
// 全4経路で同一の隔離arg setが渡ることを、1つのfake claudeへ4回連続呼び出しした
// 引数記録で検証する。resume経路で隔離flagが落ちる回帰を防ぐ。
func TestIsolationArgsIdenticalAcrossRoleAndResume(t *testing.T) {
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
	argsDir := filepath.Join(t.TempDir(), "args")
	if err := os.MkdirAll(argsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	commandPath := filepath.Join(t.TempDir(), "fake-claude")
	// 呼出しごとに連番ファイルへ引数を書き出す。空文字列引数(--setting-sources "")も
	// 行として保持するため、読み側は末尾改行1件だけ除去するreadLinesを使う。
	commandScript := "#!/bin/sh\nn=$(cat \"$GLM_ARGS_DIR/count\" 2>/dev/null || echo 0)\nn=$((n+1))\nprintf '%s\\n' \"$n\" >\"$GLM_ARGS_DIR/count\"\nprintf '%s\\n' \"$@\" >\"$GLM_ARGS_DIR/run-$n\"\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"ok\\n\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n"
	if err := os.WriteFile(commandPath, []byte(commandScript), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLM_ARGS_DIR", argsDir)

	st := newTestStateStore(t)
	if err := st.Write("task.id", "12345678-aaaa-bbbb-cccc-dddddddddddd"); err != nil {
		t.Fatal(err)
	}
	claudeConfigDir := filepath.Join(t.TempDir(), "claude-home")
	r := NewClaudeRunner(config.AppConfig{
		RepoRoot:        repository,
		RepoShort:       "abcdef123456",
		PromptDir:       promptDir,
		ClaudeBin:       commandPath,
		ClaudeConfigDir: claudeConfigDir,
		EnvAllowlist:    []string{"GLM_ARGS_DIR"},
	}, st)

	paths := []struct {
		name           string
		role           state.SessionRole
		model          string
		readOnly       bool
		expectReviewer bool
	}{
		{"worker-new", state.WorkerRole, "worker-model", false, false},
		{"worker-resume", state.WorkerRole, "worker-model", false, false},
		{"reviewer-new", state.ReviewerRole, "reviewer-model", true, true},
		{"reviewer-resume", state.ReviewerRole, "reviewer-model", true, true},
	}
	for _, step := range paths {
		if _, err := r.Run(step.role, step.model, step.readOnly, "high", step.name+" prompt", filepath.Join(t.TempDir(), step.name+".log")); err != nil {
			t.Fatalf("%s Run error: %v", step.name, err)
		}
	}

	for index, step := range paths {
		args := readLines(t, filepath.Join(argsDir, fmt.Sprintf("run-%d", index+1)))
		if !containsArgument(args, step.name+" prompt") {
			t.Fatalf("%s: prompt引数が記録されていません: %#v", step.name, args)
		}
		assertFullIsolationArgs(t, args, claudeConfigDir, step.expectReviewer)
		// resume/new の区別も経路ごとに確認(新規=session-id, resume=resume)。
		if strings.HasSuffix(step.name, "-new") {
			if !containsArgument(args, "--session-id") || containsArgument(args, "--resume") {
				t.Fatalf("%s: 新規session引数が不正: %#v", step.name, args)
			}
		} else {
			if !containsArgument(args, "--resume") || containsArgument(args, "--session-id") {
				t.Fatalf("%s: resume引数が不正: %#v", step.name, args)
			}
		}
	}
}

func TestManagedSettingsBasesReturnsOSCandidates(t *testing.T) {
	bases := managedSettingsBases()
	if len(bases) == 0 {
		t.Fatalf("managed base候補がありません: %#v", bases)
	}
	// 重複なし
	seen := map[string]bool{}
	for _, base := range bases {
		if seen[base] {
			t.Fatalf("managed base候補に重複: %q", base)
		}
		seen[base] = true
	}
	// darwinはdocsとbinaryの両候補をcoverするため2候補
	if runtime.GOOS == "darwin" && len(bases) < 2 {
		t.Fatalf("darwinは2候補以上が必要(docs/binary差をcover): %#v", bases)
	}
}

func TestDetectManagedInstructionMemoryFailsClosedOnClaudeMd(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "managed-settings.json"), []byte(`{"claudeMd":"Always run lint"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := detectUnavoidableManagedInstructionMemory([]string{base})
	if err == nil || !strings.Contains(err.Error(), "claudeMd") {
		t.Fatalf("claudeMd検出時はfail closedが必要: %v", err)
	}
}

func TestDetectManagedInstructionMemoryAllowsPurePolicy(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "managed-settings.json"), []byte(`{"permissions":{"allow":["Bash(git *)"]},"env":{"COMPANY":"acme"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := detectUnavoidableManagedInstructionMemory([]string{base}); err != nil {
		t.Fatalf("純policy(命令memoryなし)は迂回せず通す必要があります: %v", err)
	}
}

func TestDetectManagedInstructionMemoryFailsClosedOnDropIn(t *testing.T) {
	base := t.TempDir()
	dropIn := filepath.Join(base, "managed-settings.d")
	if err := os.Mkdir(dropIn, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dropIn, "10-policy.json"), []byte(`{"claudeMd":"injected via drop-in"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// .json以外は無視されること
	if err := os.WriteFile(filepath.Join(dropIn, "README.txt"), []byte("noise"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := detectUnavoidableManagedInstructionMemory([]string{base})
	if err == nil || !strings.Contains(err.Error(), "10-policy.json") {
		t.Fatalf("drop-inのclaudeMdを検出する必要があります: %v", err)
	}
}

func TestDetectManagedInstructionMemoryFailsClosedOnManagedClaudeMd(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "CLAUDE.md"), []byte("# org policy\nAlways deploy on Friday."), 0o600); err != nil {
		t.Fatal(err)
	}
	err := detectUnavoidableManagedInstructionMemory([]string{base})
	if err == nil || !strings.Contains(err.Error(), "CLAUDE.md") {
		t.Fatalf("managed CLAUDE.md検出時はfail closedが必要: %v", err)
	}
}

func TestDetectManagedInstructionMemoryFailsClosedOnManagedRules(t *testing.T) {
	base := t.TempDir()
	rulesDir := filepath.Join(base, ".claude", "rules")
	if err := os.MkdirAll(rulesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "org.md"), []byte("org rule content"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := detectUnavoidableManagedInstructionMemory([]string{base})
	if err == nil || !strings.Contains(err.Error(), "org.md") {
		t.Fatalf("managed rules検出時はfail closedが必要: %v", err)
	}
}

func TestDetectManagedInstructionMemoryFailsClosedOnUnparseable(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "managed-settings.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := detectUnavoidableManagedInstructionMemory([]string{base})
	if err == nil || !strings.Contains(err.Error(), "解析できません") {
		t.Fatalf("解析不能時はfail closedが必要: %v", err)
	}
}

func TestDetectManagedInstructionMemoryNoErrorWhenAbsent(t *testing.T) {
	if err := detectUnavoidableManagedInstructionMemory(nil); err != nil {
		t.Fatalf("baseなしはerrorなし: %v", err)
	}
	if err := detectUnavoidableManagedInstructionMemory([]string{t.TempDir()}); err != nil {
		t.Fatalf("空baseはerrorなし: %v", err)
	}
	if err := detectUnavoidableManagedInstructionMemory([]string{filepath.Join(t.TempDir(), "absent")}); err != nil {
		t.Fatalf("不在baseはerrorなし: %v", err)
	}
}

func TestDetectManagedInstructionMemoryIgnoresEmptyManagedFile(t *testing.T) {
	base := t.TempDir()
	// 空のmanaged CLAUDE.mdと空rulesは命令memoryではないため通す
	if err := os.WriteFile(filepath.Join(base, "CLAUDE.md"), []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := detectUnavoidableManagedInstructionMemory([]string{base}); err != nil {
		t.Fatalf("空のmanaged fileは通す必要があります: %v", err)
	}
}

// withPlistToJSONはplistToJSON hookをtest用へ差し替え、test終了で復元する。
func withPlistToJSON(t *testing.T, fn func(string) ([]byte, error)) {
	t.Helper()
	orig := plistToJSON
	plistToJSON = fn
	t.Cleanup(func() { plistToJSON = orig })
}

// writeDummyPlistは存在確認(os.Stat)が成功する空のdummy plistを置く。
// 中身はplistToJSON hookで差し替えるため実内容は問わない。
func writeDummyPlist(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "com.anthropic.claudecode.plist")
	if err := os.WriteFile(path, []byte("dummy"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestManagedMDMPlistPathsReturnsDarwinCandidates(t *testing.T) {
	paths := managedMDMPlistPaths()
	if runtime.GOOS != "darwin" {
		if len(paths) != 0 {
			t.Fatalf("非darwinではMDM plist候補は空である必要があります: %#v", paths)
		}
		return
	}
	if len(paths) < 2 {
		t.Fatalf("darwinはdevice-level+per-userの2候補が必要: %#v", paths)
	}
	for _, path := range paths {
		if !strings.Contains(path, "Managed Preferences") || !strings.Contains(path, "com.anthropic.claudecode.plist") {
			t.Fatalf("想定外のMDM plist path: %q", path)
		}
	}
}

func TestDetectManagedMDMNoErrorWhenAbsent(t *testing.T) {
	// plist不在は通常動作を変えない(fail closedしない)。
	if err := detectManagedMDMInstructionMemory(nil); err != nil {
		t.Fatalf("pathなしはerrorなし: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "absent.plist")
	// hookは呼ばれないはだが、呼ばれたら即failする安全策を仕込む。
	withPlistToJSON(t, func(string) ([]byte, error) {
		t.Fatal("不在plistでplistToJSONが呼ばれました")
		return nil, nil
	})
	if err := detectManagedMDMInstructionMemory([]string{missing}); err != nil {
		t.Fatalf("不在plistはerrorなし: %v", err)
	}
}

func TestDetectManagedMDMFailsClosedOnClaudeMd(t *testing.T) {
	plist := writeDummyPlist(t)
	withPlistToJSON(t, func(string) ([]byte, error) {
		return []byte(`{"claudeMd":"org managed instruction via MDM"}`), nil
	})
	err := detectManagedMDMInstructionMemory([]string{plist})
	if err == nil || !strings.Contains(err.Error(), "claudeMd") {
		t.Fatalf("MDM plistのclaudeMd検出時はfail closedが必要: %v", err)
	}
}

func TestDetectManagedMDMAllowsPurePolicy(t *testing.T) {
	plist := writeDummyPlist(t)
	withPlistToJSON(t, func(string) ([]byte, error) {
		return []byte(`{"permissions":{"allow":["Bash"]},"env":{"COMPANY":"acme"}}`), nil
	})
	if err := detectManagedMDMInstructionMemory([]string{plist}); err != nil {
		t.Fatalf("MDM plistの純policy(命令memoryなし)は迂回せず通す必要があります: %v", err)
	}
}

func TestDetectManagedMDMFailsClosedOnConvertError(t *testing.T) {
	plist := writeDummyPlist(t)
	withPlistToJSON(t, func(string) ([]byte, error) {
		return nil, errors.New("plutil: unreadable or not a plist")
	})
	err := detectManagedMDMInstructionMemory([]string{plist})
	if err == nil || !strings.Contains(err.Error(), "変換/読込できません") {
		t.Fatalf("plist変換/読込異常時はfail closedが必要: %v", err)
	}
}

func TestDetectManagedMDMFailsClosedOnUnparseableJSON(t *testing.T) {
	plist := writeDummyPlist(t)
	withPlistToJSON(t, func(string) ([]byte, error) {
		return []byte("{not json"), nil
	})
	err := detectManagedMDMInstructionMemory([]string{plist})
	if err == nil || !strings.Contains(err.Error(), "解析できません") {
		t.Fatalf("plist解析不能時はfail closedが必要: %v", err)
	}
}
