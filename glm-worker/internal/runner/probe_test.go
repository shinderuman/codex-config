package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

func newProbeFixture(t *testing.T) (*ClaudeRunner, *state.StateStore, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}
	promptDir := t.TempDir()
	for _, name := range []string{"WORKER.md", "REVIEWER.md"} {
		if err := os.WriteFile(filepath.Join(promptDir, name), []byte("system"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	argumentsPath := filepath.Join(t.TempDir(), "args")
	commandPath := filepath.Join(t.TempDir(), "fake-claude")
	commandScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$GLM_ARGS_FILE\"\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"ok\\n\",\"duration_ms\":7,\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n"
	if err := os.WriteFile(commandPath, []byte(commandScript), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLM_ARGS_FILE", argumentsPath)

	st := newTestStateStore(t)
	if err := st.Write("task.id", "12345678-aaaa-bbbb-cccc-dddddddddddd"); err != nil {
		t.Fatal(err)
	}
	r := NewClaudeRunner(config.AppConfig{
		RepoRoot:        t.TempDir(),
		RepoShort:       "abcdef123456",
		PromptDir:       promptDir,
		ClaudeBin:       commandPath,
		ClaudeConfigDir: filepath.Join(t.TempDir(), "claude-home"),
		EnvAllowlist:    []string{"GLM_ARGS_FILE"},
	}, st)
	return r, st, argumentsPath
}

func TestProbeIsolationAndNoSessionPersistence(t *testing.T) {
	r, st, argumentsPath := newProbeFixture(t)
	if err := st.Write("worker.id", "existing-worker-session"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkReady(state.WorkerRole); err != nil {
		t.Fatal(err)
	}

	probe, err := r.Probe("opus")
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if probe.DurationMS != 7 || probe.Usage.InputTokens != 1 {
		t.Fatalf("probe result = %#v", probe)
	}

	args := readLines(t, argumentsPath)
	if !containsArgument(args, "--no-session-persistence") {
		t.Fatalf("probeへ--no-session-persistenceがありません: %#v", args)
	}
	if containsArgument(args, "--session-id") || containsArgument(args, "--resume") || containsArgument(args, "--name") {
		t.Fatalf("probeはsessionを作成/保存/再開してはいけません: %#v", args)
	}
	if !containsArgument(args, ProbePrompt) {
		t.Fatalf("probeは最小prompt %qを送るべき: %#v", ProbePrompt, args)
	}
	if !containsArgument(args, "--safe-mode") || argumentAfter(args, "--setting-sources") != "" {
		t.Fatalf("probe隔離flag不足: %#v", args)
	}
	if got := argumentAfter(args, "--mcp-config"); got != `{"mcpServers":{}}` {
		t.Fatalf("probe MCP = %q: %#v", got, args)
	}
	if got := argumentAfter(args, "--tools"); got != "" {
		t.Fatalf("probeは--tools \"\"で全toolを無効化すべき: got=%q: %#v", got, args)
	}
	if containsArgument(args, "--disallowedTools") {
		t.Fatalf("probeは--tools \"\"を使い--disallowedTools列挙へ依存すべきでない: %#v", args)
	}

	if id, _ := st.Read("worker.id"); id != "existing-worker-session" {
		t.Fatalf("probeがsession idを書き換えました: %q", id)
	}
	if !st.Exists("worker.ready") {
		t.Fatal("probeがworker.readyを変更しました")
	}
}

func TestProbeReturnsErrorOnExitFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}
	promptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(promptDir, "WORKER.md"), []byte("system"), 0o600); err != nil {
		t.Fatal(err)
	}
	commandPath := filepath.Join(t.TempDir(), "fake-claude")
	commandScript := "#!/bin/sh\nprintf '%s\\n' 'API Error: 503 Service Unavailable'\nexit 1\n"
	if err := os.WriteFile(commandPath, []byte(commandScript), 0o700); err != nil {
		t.Fatal(err)
	}
	st := newTestStateStore(t)
	if err := st.Write("task.id", "12345678-aaaa-bbbb-cccc-dddddddddddd"); err != nil {
		t.Fatal(err)
	}
	r := NewClaudeRunner(config.AppConfig{
		RepoRoot:        t.TempDir(),
		PromptDir:       promptDir,
		ClaudeBin:       commandPath,
		ClaudeConfigDir: filepath.Join(t.TempDir(), "claude-home"),
	}, st)

	_, err := r.Probe("opus")
	if err == nil || !strings.Contains(err.Error(), "probe失敗") {
		t.Fatalf("probe失敗errorを期待: %v", err)
	}
}
