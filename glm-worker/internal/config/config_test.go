package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEnvOrDefault(t *testing.T) {
	t.Setenv("GLM_TEST_ENVVAR", "value")
	if got := envOrDefault("GLM_TEST_ENVVAR", "fallback"); got != "value" {
		t.Fatalf("got %q", got)
	}
	if got := envOrDefault("GLM_TEST_ENVVAR_UNSET", "fallback"); got != "fallback" {
		t.Fatalf("got %q", got)
	}
}

func TestIntEnvPrimary(t *testing.T) {
	t.Setenv("GLM_TEST_INT", "5")
	got, err := intEnv("GLM_TEST_INT", 2)
	if err != nil || got != 5 {
		t.Fatalf("got %d err %v", got, err)
	}
}

func TestIntEnvDefault(t *testing.T) {
	got, err := intEnv("GLM_TEST_INT_NEITHER", 2)
	if err != nil || got != 2 {
		t.Fatalf("got %d err %v", got, err)
	}
}

func TestIntEnvDoesNotUseRemovedLegacyName(t *testing.T) {
	t.Setenv("GLM_WORKER_MAX_REVIEW_ROUNDS", "7")
	got, err := intEnv("GLM_WORKER_MAX_AUTO_FIX_ROUNDS", 2)
	if err != nil || got != 2 {
		t.Fatalf("removed legacy envを参照しました: got %d err %v", got, err)
	}
}

func TestIntEnvRejectsInvalid(t *testing.T) {
	t.Setenv("GLM_TEST_INT_BAD", "abc")
	if _, err := intEnv("GLM_TEST_INT_BAD", 2); err == nil {
		t.Fatal("非整数値を拒否する必要があります")
	}

	t.Setenv("GLM_TEST_INT_NEG", "-1")
	if _, err := intEnv("GLM_TEST_INT_NEG", 2); err == nil {
		t.Fatal("負値を拒否する必要があります")
	}
}

func TestBoolEnv(t *testing.T) {
	got, err := boolEnv("GLM_TEST_BOOL_UNSET", true)
	if err != nil || !got {
		t.Fatalf("default bool = %t, err = %v", got, err)
	}
	t.Setenv("GLM_TEST_BOOL", "false")
	got, err = boolEnv("GLM_TEST_BOOL", true)
	if err != nil || got {
		t.Fatalf("configured bool = %t, err = %v", got, err)
	}
	t.Setenv("GLM_TEST_BOOL_BAD", "invalid")
	if _, err := boolEnv("GLM_TEST_BOOL_BAD", true); err == nil {
		t.Fatal("不正な真偽値を拒否する必要があります")
	}
}

func TestLoadBuildsConfigFromRepositoryAndEnvironment(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "init", "--quiet", repository)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}

	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repository); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDirectory) })

	home := t.TempDir()
	stateHome := filepath.Join(t.TempDir(), "state")
	promptDir := filepath.Join(t.TempDir(), "prompts")
	t.Setenv("HOME", home)
	// 既定ClaudeConfigDir($HOME/.claude)を検証するため、外部envのCLAUDE_CONFIG_DIRを空にする。
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("GLM_WORKER_HOME", stateHome)
	t.Setenv("GLM_WORKER_PROMPT_DIR", promptDir)
	t.Setenv("GLM_WORKER_CLAUDE_BIN", "claude-test")
	t.Setenv("GLM_WORKER_WORKER_MODEL", "worker-test")
	t.Setenv("GLM_WORKER_REVIEWER_MODEL", "reviewer-test")
	t.Setenv("GLM_WORKER_HIGH_RISK_REVIEWER_MODEL", "reviewer-high-test")
	t.Setenv("GLM_WORKER_EFFORT", "medium")
	t.Setenv("GLM_WORKER_ESCALATED_EFFORT", "high")
	t.Setenv("GLM_WORKER_MAX_AUTO_FIX_ROUNDS", "4")
	t.Setenv("GLM_WORKER_TELEMETRY_CONTENT", "false")

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	resolvedRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(resolvedRepository))
	wantHash := hex.EncodeToString(hash[:])

	if loaded.RepoRoot != resolvedRepository || loaded.RepoHash != wantHash || loaded.RepoShort != wantHash[:12] {
		t.Fatalf("repository config = %#v", loaded)
	}
	if loaded.StateBase != filepath.Join(stateHome, "sessions") || loaded.PromptDir != promptDir {
		t.Fatalf("path config = %#v", loaded)
	}
	if loaded.ClaudeConfigDir != filepath.Join(home, ".claude") {
		t.Fatalf("ClaudeConfigDir = %q, want %q", loaded.ClaudeConfigDir, filepath.Join(home, ".claude"))
	}
	if loaded.ClaudeBin != "claude-test" || loaded.WorkerModel != "worker-test" || loaded.ReviewerModel != "reviewer-test" || loaded.HighRiskReviewerModel != "reviewer-high-test" {
		t.Fatalf("runner config = %#v", loaded)
	}
	if loaded.RoutineEffort != "medium" || loaded.EscalatedEffort != "high" || loaded.MaxAutoFixRounds != 4 || loaded.TelemetryContent {
		t.Fatalf("workflow config = %#v", loaded)
	}
}

func TestResolveRepoRootFallsBackToCurrentDirectory(t *testing.T) {
	directory := t.TempDir()
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDirectory) })

	resolved, err := resolveRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("root = %q, want %q", resolved, want)
	}
}

func TestLoadRejectsInvalidReviewRounds(t *testing.T) {
	t.Setenv("GLM_WORKER_MAX_AUTO_FIX_ROUNDS", "invalid")
	if _, err := Load(); err == nil {
		t.Fatal("invalid review roundsを拒否する必要があります")
	}
}

func TestLoadRespectsClaudeConfigDirEnv(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "init", "--quiet", repository)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}

	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repository); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDirectory) })

	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "/override/claude-config")

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ClaudeConfigDir != "/override/claude-config" {
		t.Fatalf("ClaudeConfigDir = %q, want /override/claude-config", loaded.ClaudeConfigDir)
	}
}

func TestLoadParsesEnvAllowlist(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "init", "--quiet", repository)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repository); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDirectory) })

	t.Setenv("HOME", t.TempDir())
	t.Setenv("GLM_WORKER_ENV_ALLOWLIST", "GOPATH, ,GOFLAGS")

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GOPATH", "GOFLAGS"}
	if len(loaded.EnvAllowlist) != len(want) {
		t.Fatalf("EnvAllowlist = %#v, want %#v", loaded.EnvAllowlist, want)
	}
	for index, key := range want {
		if loaded.EnvAllowlist[index] != key {
			t.Fatalf("EnvAllowlist[%d] = %q, want %q", index, loaded.EnvAllowlist[index], key)
		}
	}
}

func TestLoadLeavesEnvAllowlistNilByDefault(t *testing.T) {
	t.Setenv("GLM_WORKER_ENV_ALLOWLIST", "")
	if loaded, err := Load(); err != nil {
		t.Fatal(err)
	} else if loaded.EnvAllowlist != nil {
		t.Fatalf("EnvAllowlist = %#v, want nil", loaded.EnvAllowlist)
	}
}

func TestResolveClaudeSettingsOverrideResolution(t *testing.T) {
	tests := []struct {
		name string
		home string
		env  map[string]string
		want string
	}{
		{name: "default", home: "/h", want: "/h/.config/codex-config/claude-settings.local.json"},
		{name: "xdg", home: "/h", env: map[string]string{"XDG_CONFIG_HOME": "/xdg"}, want: "/xdg/codex-config/claude-settings.local.json"},
		{name: "explicit override", home: "/h", env: map[string]string{"CODEX_CONFIG_CLAUDE_SETTINGS_OVERRIDE": "/custom/o.json"}, want: "/custom/o.json"},
		{name: "explicit overrides xdg", home: "/h", env: map[string]string{"XDG_CONFIG_HOME": "/xdg", "CODEX_CONFIG_CLAUDE_SETTINGS_OVERRIDE": "/c/o.json"}, want: "/c/o.json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("CODEX_CONFIG_CLAUDE_SETTINGS_OVERRIDE", "")
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			got := resolveClaudeSettingsOverride(test.home)
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}
