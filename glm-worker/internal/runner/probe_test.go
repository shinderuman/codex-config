package runner

import (
	"encoding/json"
	"errors"
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
	commandScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$GLM_ARGS_FILE\"\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"GLM_WORKER_PROBE_OK\\n\",\"duration_ms\":7,\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n"
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

func TestProbeRejectsMalformedSuccessResponses(t *testing.T) {
	cases := []struct {
		name    string
		script  string
		wantErr string
		// typedはexit 0でも契約不通過ならProbeInvalidResponseErrorになるかの期待値。
		typed bool
	}{
		{
			name:    "not json",
			script:  "#!/bin/sh\nprintf '%s\\n' 'ok'\n",
			wantErr: "probe不正応答",
			typed:   true,
		},
		{
			name:    "empty result",
			script:  "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n",
			wantErr: "sentinel",
			typed:   true,
		},
		{
			name:    "blank result",
			script:  "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"  \\n\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n",
			wantErr: "sentinel",
			typed:   true,
		},
		{
			name:    "maintenance text",
			script:  "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"Scheduled maintenance is in progress. Please retry later.\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n",
			wantErr: "sentinel",
			typed:   true,
		},
		{
			name:    "access denied text",
			script:  "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"Access denied: authentication required\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n",
			wantErr: "sentinel",
			typed:   true,
		},
		{
			name:    "proxy guard page",
			script:  "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"<html>407 Proxy Authentication Required</html>\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n",
			wantErr: "sentinel",
			typed:   true,
		},
		{
			name:    "sentinel with extra text",
			script:  "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"GLM_WORKER_PROBE_OK The connection works.\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n",
			wantErr: "sentinel",
			typed:   true,
		},
		{
			name:    "zero output tokens",
			script:  "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"GLM_WORKER_PROBE_OK\",\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}'\n",
			wantErr: "usageが出力tokenを含みません",
			typed:   true,
		},
		{
			name:    "is_error true",
			script:  "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"error\",\"is_error\":true,\"result\":\"API Error: 503\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\nexit 1\n",
			wantErr: "probe失敗",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("shell fixtureはUnix系環境向け")
			}
			promptDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(promptDir, "WORKER.md"), []byte("system"), 0o600); err != nil {
				t.Fatal(err)
			}
			commandPath := filepath.Join(t.TempDir(), "fake-claude")
			if err := os.WriteFile(commandPath, []byte(tc.script), 0o700); err != nil {
				t.Fatal(err)
			}
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
			}, st)

			_, err := r.Probe("opus")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
			}
			var typed *ProbeInvalidResponseError
			if tc.typed && !errors.As(err, &typed) {
				t.Fatalf("検証不通過はProbeInvalidResponseErrorであるべき: %v", err)
			}
			if !tc.typed && errors.As(err, &typed) {
				t.Fatalf("このcaseはtyped不正応答でないべき: %v", err)
			}
		})
	}
}

func TestProbeRejectsIsErrorResultOnExitZero(t *testing.T) {
	cases := []struct {
		name   string
		result string
	}{
		{"maintenance", "The service is temporarily unavailable due to scheduled maintenance."},
		{"access denied", "Access denied"},
		{"proxy guard", "Request blocked by proxy: please authenticate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("shell fixtureはUnix系環境向け")
			}
			promptDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(promptDir, "WORKER.md"), []byte("system"), 0o600); err != nil {
				t.Fatal(err)
			}
			commandPath := filepath.Join(t.TempDir(), "fake-claude")
			encoded, err := json.Marshal(map[string]any{
				"type":        "result",
				"subtype":     "error",
				"is_error":    true,
				"result":      tc.result,
				"usage":       map[string]int{"input_tokens": 1, "output_tokens": 1},
				"duration_ms": 5,
			})
			if err != nil {
				t.Fatal(err)
			}
			commandScript := "#!/bin/sh\nprintf '%s\\n' '" + string(encoded) + "'\n"
			if err := os.WriteFile(commandPath, []byte(commandScript), 0o700); err != nil {
				t.Fatal(err)
			}
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
			}, st)

			_, err = r.Probe("opus")
			var typed *ProbeInvalidResponseError
			if !errors.As(err, &typed) {
				t.Fatalf("exit 0でもis_error=trueはProbeInvalidResponseErrorであるべき: %v", err)
			}
			if !strings.Contains(err.Error(), "is_error=true") {
				t.Fatalf("err = %v, want is_error=true理由", err)
			}
		})
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
