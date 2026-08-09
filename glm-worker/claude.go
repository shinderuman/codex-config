package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type modelRunner interface {
	Run(role sessionRole, readOnly bool, effort string, prompt string, outputPath string) error
}

type claudeRunner struct {
	config appConfig
	state  *stateStore
}

func newClaudeRunner(config appConfig, state *stateStore) *claudeRunner {
	return &claudeRunner{config: config, state: state}
}

func (r *claudeRunner) Run(
	role sessionRole,
	readOnly bool,
	effort string,
	prompt string,
	outputPath string,
) error {
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
			"--name", r.sessionName(role),
		)
	}

	args = append(
		args,
		"--model", r.modelForRole(role),
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

func promptFileName(role sessionRole) string {
	if role == reviewerRole {
		return "REVIEWER.md"
	}
	return "WORKER.md"
}

func (r *claudeRunner) modelForRole(role sessionRole) string {
	if role == reviewerRole {
		return r.config.ReviewerModel
	}
	return r.config.WorkerModel
}

func (r *claudeRunner) sessionName(role sessionRole) string {
	taskID := r.state.TaskID()
	if len(taskID) > 8 {
		taskID = taskID[:8]
	}
	return fmt.Sprintf("glm-%s-%s-%s", role, r.config.RepoShort, taskID)
}
