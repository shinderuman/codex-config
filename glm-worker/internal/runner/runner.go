// Package runnerはClaude Code CLIプロセスの起動とZ.ai 5h上限判定を担う。
package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/shinderuman/codex-config/glm-worker/internal/config"
	"github.com/shinderuman/codex-config/glm-worker/internal/state"
)

// ClaudeRunnerはClaude Code CLIを実際に起動するrunner実装。
type ClaudeRunner struct {
	config config.AppConfig
	state  *state.StateStore
}

// NewClaudeRunnerはClaudeRunnerを構築する。
func NewClaudeRunner(cfg config.AppConfig, st *state.StateStore) *ClaudeRunner {
	return &ClaudeRunner{config: cfg, state: st}
}

// Runはrole/effort/promptでClaude Codeを起動し出力をoutputPathへ書き出す。
// 初回起動時は新規sessionを採番し、2回目以降は同一sessionへresumeする。
func (r *ClaudeRunner) Run(
	role state.SessionRole,
	model string,
	readOnly bool,
	effort string,
	prompt string,
	outputPath string,
) error {
	if model == "" {
		return fmt.Errorf("modelを指定してください")
	}
	taskID, err := r.state.TaskID()
	if err != nil {
		return err
	}
	sessionID, ready, err := r.state.SessionID(role)
	if err != nil {
		return err
	}

	systemFile := filepath.Join(r.config.PromptDir, promptFileName(role))
	if _, err := os.Stat(systemFile); err != nil {
		return fmt.Errorf("required promptがありません: %s", systemFile)
	}

	args := []string{"-p"}
	if ready {
		args = append(args, "--resume", sessionID)
	} else {
		args = append(
			args,
			"--session-id", sessionID,
			"--name", r.sessionName(role, taskID),
		)
	}

	args = append(
		args,
		"--model", model,
		"--effort", effort,
		"--autocompact", "500k",
		"--output-format", "text",
		"--dangerously-skip-permissions",
	)

	if readOnly {
		args = append(args, "--disallowedTools", "Edit", "Write", "NotebookEdit")
	}

	args = append(args, "--append-system-prompt-file", systemFile, prompt)

	output, err := os.Create(outputPath)
	if err != nil {
		return err
	}

	command := exec.Command(r.config.ClaudeBin, args...)
	command.Dir = r.config.RepoRoot
	command.Stdout = output
	command.Stderr = output
	command.Env = envWithDefaults(os.Environ(), map[string]string{
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW":  "500000",
		"CLAUDE_CODE_ALWAYS_ENABLE_EFFORT": "1",
	})

	runErr := command.Run()
	closeErr := output.Close()
	if runErr == nil && closeErr != nil {
		runErr = closeErr
	}

	if runErr != nil {
		return runErr
	}

	if err := r.state.MarkReady(role); err != nil {
		return err
	}
	return nil
}

func promptFileName(role state.SessionRole) string {
	if role == state.ReviewerRole {
		return "REVIEWER.md"
	}
	return "WORKER.md"
}

func (r *ClaudeRunner) sessionName(role state.SessionRole, taskID string) string {
	if len(taskID) > 8 {
		taskID = taskID[:8]
	}
	return fmt.Sprintf("glm-%s-%s-%s", role, r.config.RepoShort, taskID)
}

// envWithDefaultsはbaseにdefaultsの未設定キーだけを追加する。
func envWithDefaults(base []string, defaults map[string]string) []string {
	result := append([]string(nil), base...)
	present := make(map[string]bool)

	for _, item := range base {
		if index := strings.IndexByte(item, '='); index >= 0 {
			present[item[:index]] = true
		}
	}

	for key, value := range defaults {
		if !present[key] {
			result = append(result, key+"="+value)
		}
	}

	return result
}
