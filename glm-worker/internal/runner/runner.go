// Package runnerはClaude Code CLIプロセスの起動とZ.ai 5h上限判定を担う。
package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// TokenUsageはClaude CLIが返す1回の実行全体のtoken使用量。
type TokenUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

// ModelUsageはClaude CLIが実モデル別に返すtoken使用量。
type ModelUsage struct {
	InputTokens              int64   `json:"inputTokens"`
	CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
	CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
	OutputTokens             int64   `json:"outputTokens"`
	CostUSD                  float64 `json:"costUSD,omitempty"`
}

// RunResultはmodel呼出しの応答と観測値。error時も取得できた値を返す。
type RunResult struct {
	SessionID          string
	Resumed            bool
	Response           string
	Usage              TokenUsage
	ModelUsage         map[string]ModelUsage
	DurationMS         int64
	DurationAPIMS      int64
	NumTurns           int
	TotalCostUSD       float64
	SystemPrompt       string
	SystemPromptBytes  int
	SystemPromptSHA256 string
}

type claudeJSONResult struct {
	Type          string                `json:"type"`
	Subtype       string                `json:"subtype"`
	IsError       bool                  `json:"is_error"`
	Result        string                `json:"result"`
	DurationMS    int64                 `json:"duration_ms"`
	DurationAPIMS int64                 `json:"duration_api_ms"`
	NumTurns      int                   `json:"num_turns"`
	TotalCostUSD  float64               `json:"total_cost_usd"`
	Usage         TokenUsage            `json:"usage"`
	ModelUsage    map[string]ModelUsage `json:"modelUsage"`
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
) (RunResult, error) {
	if model == "" {
		return RunResult{}, fmt.Errorf("modelを指定してください")
	}
	taskID, err := r.state.TaskID()
	if err != nil {
		return RunResult{}, err
	}
	sessionID, ready, err := r.state.SessionID(role)
	if err != nil {
		return RunResult{}, err
	}
	result := RunResult{SessionID: sessionID, Resumed: ready}

	systemFile := filepath.Join(r.config.PromptDir, promptFileName(role))
	systemPrompt, err := os.ReadFile(systemFile)
	if err != nil {
		return result, fmt.Errorf("required promptがありません: %s", systemFile)
	}
	result.SystemPromptBytes = len(systemPrompt)
	result.SystemPrompt = string(systemPrompt)
	systemPromptHash := sha256.Sum256(systemPrompt)
	result.SystemPromptSHA256 = hex.EncodeToString(systemPromptHash[:])

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
		"--output-format", "json",
		"--dangerously-skip-permissions",
	)

	if readOnly {
		args = append(args, "--disallowedTools", "Edit", "Write", "NotebookEdit")
	}

	args = append(args, "--append-system-prompt-file", systemFile, prompt)

	rawOutputPath := outputPath + ".json"
	stderrPath := outputPath + ".stderr"
	output, err := createPrivateFile(rawOutputPath)
	if err != nil {
		return result, err
	}
	stderr, err := createPrivateFile(stderrPath)
	if err != nil {
		output.Close()
		return result, err
	}

	command := exec.Command(r.config.ClaudeBin, args...)
	command.Dir = r.config.RepoRoot
	command.Stdout = output
	command.Stderr = stderr
	command.Env = envWithDefaults(os.Environ(), map[string]string{
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW":  "500000",
		"CLAUDE_CODE_ALWAYS_ENABLE_EFFORT": "1",
	})

	runErr := command.Run()
	outputCloseErr := output.Close()
	stderrCloseErr := stderr.Close()
	if runErr == nil {
		if outputCloseErr != nil {
			runErr = outputCloseErr
		} else if stderrCloseErr != nil {
			runErr = stderrCloseErr
		}
	}

	parsed, parseErr := parseClaudeJSONResult(rawOutputPath)
	if parseErr == nil {
		result.Response = parsed.Result
		result.Usage = parsed.Usage
		result.ModelUsage = parsed.ModelUsage
		result.DurationMS = parsed.DurationMS
		result.DurationAPIMS = parsed.DurationAPIMS
		result.NumTurns = parsed.NumTurns
		result.TotalCostUSD = parsed.TotalCostUSD
	}

	if err := writeResultOutput(outputPath, result.Response, rawOutputPath, stderrPath); err != nil {
		return result, err
	}
	if runErr != nil {
		return result, runErr
	}
	if parseErr != nil {
		return result, parseErr
	}
	if parsed.IsError {
		return result, fmt.Errorf("Claude CLIがerror結果を返しました")
	}

	if err := r.state.MarkReady(role); err != nil {
		return result, err
	}
	return result, nil
}

func createPrivateFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
}

func parseClaudeJSONResult(path string) (claudeJSONResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return claudeJSONResult{}, err
	}
	var result claudeJSONResult
	if err := json.Unmarshal(data, &result); err != nil {
		return claudeJSONResult{}, fmt.Errorf("Claude CLIのJSON出力を解析できません: %w", err)
	}
	if result.Type != "result" {
		return claudeJSONResult{}, fmt.Errorf("Claude CLIのJSON出力typeが不正です: %q", result.Type)
	}
	if result.Usage == (TokenUsage{}) {
		for _, usage := range result.ModelUsage {
			result.Usage.InputTokens += usage.InputTokens
			result.Usage.CacheCreationInputTokens += usage.CacheCreationInputTokens
			result.Usage.CacheReadInputTokens += usage.CacheReadInputTokens
			result.Usage.OutputTokens += usage.OutputTokens
		}
	}
	return result, nil
}

func writeResultOutput(outputPath string, response string, rawOutputPath string, stderrPath string) error {
	var data []byte
	if response != "" {
		data = []byte(response)
		if data[len(data)-1] != '\n' {
			data = append(data, '\n')
		}
	} else if raw, err := os.ReadFile(rawOutputPath); err == nil {
		data = raw
	}
	if stderr, err := os.ReadFile(stderrPath); err == nil && len(stderr) > 0 {
		data = append(data, stderr...)
	}
	return os.WriteFile(outputPath, data, 0o600)
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
