// Package runnerはClaude Code CLIプロセスの起動とZ.ai 5h上限判定を担う。
package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// isolationPolicyVersionはworker/reviewer起動の隔離構成を識別する。
// safe-mode・空setting-sources・child env allowlist・inline隔離settingsの組合せが
// 変わったらbumpする。旧versionで採番されたsessionは暗黙入力が混入しているため
// resumeせず新sessionへ切り替える。
const isolationPolicyVersion = "claude-isolation-1"

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
	TopLevelUsage      TokenUsage
	ModelUsage         map[string]ModelUsage
	DurationMS         int64
	DurationAPIMS      int64
	TopLevelTurns      int
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

func NewClaudeRunner(cfg config.AppConfig, st *state.StateStore) *ClaudeRunner {
	return &ClaudeRunner{config: cfg, state: st}
}

// Runはrole/effort/promptでClaude Codeを起動し出力をoutputPathへ書き出す。
// 初回起動時は新規sessionを採番し、2回目以降は同一sessionへresumeする。
// 起動は全入力経路を隔離する: --safe-modeでcustomization・managed CLAUDE.md・
// managed skills/plugins・policy-configured MCPを一括無効化し、--setting-sources ""
// でfilesystem settingsを読まず、Z.ai接続・model aliasはsettings.jsonからallowlist
// 抽出した最小envを明示注入する。CLAUDE.md/auto memory/hooks/MCP/skills等はinline
// --settingsとflagで追加遮断する。組込みsystem promptとmanaged settings policy
// （認証・権限等の組織policy）だけは遮断不可能な残余として残る。現行の隔離policyと
// 一致しない旧sessionは暗黙入力が混入しているためresumeせず新sessionへ切り替える。
// isolation.policyはtask共通なのでpolicy不一致時はworker/reviewer両roleのsessionを破棄する。
// isolation.policyは成功markerではなくsession IDの起動policyを表すため、SessionID確定時点
// (Claude実行前)に永続化し、5h上限中断後に同一sessionへresume可能な状態を保つ。
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
	if err := r.state.ResetSessionsForPolicy(isolationPolicyVersion); err != nil {
		return RunResult{}, err
	}
	sessionID, ready, err := r.state.SessionID(role)
	if err != nil {
		return RunResult{}, err
	}
	result := RunResult{SessionID: sessionID, Resumed: ready}
	if err := r.state.SetIsolationPolicy(isolationPolicyVersion); err != nil {
		return result, err
	}

	systemFile := filepath.Join(r.config.PromptDir, promptFileName(role))
	systemPrompt, err := os.ReadFile(systemFile)
	if err != nil {
		return result, fmt.Errorf("required promptがありません: %s", systemFile)
	}
	result.SystemPromptBytes = len(systemPrompt)
	result.SystemPrompt = string(systemPrompt)
	systemPromptHash := sha256.Sum256(systemPrompt)
	result.SystemPromptSHA256 = hex.EncodeToString(systemPromptHash[:])

	isolationArgs, err := isolationSettings(r.config.ClaudeConfigDir)
	if err != nil {
		return result, err
	}
	settingEnv, envDeletes, err := loadSettingEnv(r.config.ClaudeConfigDir, r.config.ClaudeSettingsOverride)
	if err != nil {
		return result, err
	}

	args := []string{"-p", "--safe-mode", "--setting-sources", ""}
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
		"--strict-mcp-config",
		"--mcp-config", `{"mcpServers":{}}`,
		"--disable-slash-commands",
		"--settings", isolationArgs,
	)

	if readOnly {
		args = append(args, "--disallowedTools", "Edit", "Write", "NotebookEdit", "Agent")
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
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		output.Close()
		stderr.Close()
		return result, fmt.Errorf("/dev/nullを開けません: %w", err)
	}
	defer devNull.Close()

	command := exec.Command(r.config.ClaudeBin, args...)
	command.Dir = r.config.RepoRoot
	command.Stdin = devNull
	command.Stdout = output
	command.Stderr = stderr
	command.Env = buildChildEnv(r.config.EnvAllowlist, settingEnv, map[string]string{
		"CLAUDE_CONFIG_DIR":                r.config.ClaudeConfigDir,
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW":  "500000",
		"CLAUDE_CODE_ALWAYS_ENABLE_EFFORT": "1",
	}, envDeletes)

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
		result.TopLevelUsage = parsed.Usage
		result.ModelUsage = parsed.ModelUsage
		result.DurationMS = parsed.DurationMS
		result.DurationAPIMS = parsed.DurationAPIMS
		result.TopLevelTurns = parsed.NumTurns
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

// isolationSettingsはworker/reviewer sessionの入力を隔離する追加設定を
// --settings経由で渡すJSON文字列を構築する。safe-mode/空setting-sourcesと併用し、
// claudeMdExcludesで全階層のCLAUDE.mdを、autoMemoryEnabledでauto memoryを、
// disableAllHooks/disableBundledSkills/disableWorkflowsで各customizationを無効化する。
// これらはmemory・customization読込経路だけへ作用し、auth(Z.ai env)・model・
// 組込みsystem prompt・権限へは影響しない。managed settings policy（認証・権限等の
// 組織policy）は--safe-modeでも残存する唯一の残余であり、この関数では除去しない。
//
// claudeMdExcludesは user/project/local memory だけへ効き絶対pathとglobの両方を
// 持たせる: `**/CLAUDE.md`/`**/CLAUDE.local.md` で cwd 配下の全階層を捕捉し、
// 解決済み絶対path `<configDir>/CLAUDE.md`・`<configDir>/rules/**` で user global
// memoryを確実に除外する(globだけでは相対path解釈に依存し確実さが足りないため)。
func isolationSettings(claudeConfigDir string) (string, error) {
	configDir, err := resolveClaudeConfigDir(claudeConfigDir)
	if err != nil {
		return "", err
	}
	settings := map[string]any{
		"claudeMdExcludes": []string{
			"**/CLAUDE.md",
			"**/CLAUDE.local.md",
			filepath.Join(configDir, "CLAUDE.md"),
			filepath.Join(configDir, "rules", "**"),
		},
		"autoMemoryEnabled":    false,
		"disableAllHooks":      true,
		"disableBundledSkills": true,
		"disableWorkflows":     true,
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("隔離settingsを構築できません: %w", err)
	}
	return string(encoded), nil
}

// essentialSettingEnvKeysは<claudeConfigDir>/settings.jsonのenv blockから抽出して
// workerへ明示注入する確認済みのkey。Z.ai接続・model alias・最小runtimeのみ。
// これ以外のsettings env(任意のANTHROPIC_*/CLAUDE_CODE_*等)は引き継がない。
var essentialSettingEnvKeys = []string{
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"API_TIMEOUT_MS",
	"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
}

// loadSettingEnvはsettings.jsonのenv blockからessentialSettingEnvKeysに一致する
// 値だけを取り出し、続けて端末local overrideのset/deleteを再適用する。
// 戻り値のdeletesはoverrideのnull key(tombstone)で、buildChildEnvへ渡して
// 親envのOS必須・extraAllow経由での再流入も遮断する。
// overrideで明示setした任意keyはessential key以外でも子envへ許可する。
func loadSettingEnv(claudeConfigDir string, overridePath string) (map[string]string, []string, error) {
	configDir, err := resolveClaudeConfigDir(claudeConfigDir)
	if err != nil {
		return nil, nil, err
	}
	result := make(map[string]string)
	data, err := os.ReadFile(filepath.Join(configDir, "settings.json"))
	if err == nil {
		var parsed struct {
			Env map[string]string `json:"env"`
		}
		if err := json.Unmarshal(data, &parsed); err != nil {
			return nil, nil, fmt.Errorf("Claude settings.jsonを解析できません: %w", err)
		}
		for _, key := range essentialSettingEnvKeys {
			if value, ok := parsed.Env[key]; ok && value != "" {
				result[key] = value
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("Claude settings.jsonを読み込めません: %w", err)
	}

	override, err := parseClaudeEnvOverride(overridePath)
	if err != nil {
		return nil, nil, fmt.Errorf("env override: %w", err)
	}
	for _, key := range override.deletes {
		delete(result, key)
	}
	for key, value := range override.sets {
		result[key] = value
	}
	return result, override.deletes, nil
}

// osEssentialEnvKeysは親process環境から受け継ぐ実行必須key。
// Claudeの入力経路にはならない(CLAUDE_CODE_*/ANTHROPIC_*を含まない)。
var osEssentialEnvKeys = []string{
	"PATH", "HOME", "TMPDIR", "SHELL", "USER", "LOGNAME",
	"LANG", "LC_ALL", "LC_CTYPE", "TZ", "TERM",
}

// buildChildEnvは隔離されたchild process環境を構築する。
// OS必須keyとextraAllowだけを親envから取り出すが、deletes(overrideのtombstone)は
// この経路からも除外し親envへの再流入を防ぐ。続けてsettingEnvとadditionsで上書き注入する。
// 暗黙の入力経路となるenvは親から引き継がない。
func buildChildEnv(extraAllow []string, settingEnv, additions map[string]string, deletes []string) []string {
	allowed := make(map[string]struct{}, len(osEssentialEnvKeys)+len(extraAllow))
	for _, key := range osEssentialEnvKeys {
		allowed[key] = struct{}{}
	}
	for _, key := range extraAllow {
		allowed[key] = struct{}{}
	}
	denied := make(map[string]struct{}, len(deletes))
	for _, key := range deletes {
		denied[key] = struct{}{}
	}

	child := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if _, ok := allowed[key]; !ok {
			continue
		}
		if _, deny := denied[key]; deny {
			continue
		}
		child[key] = value
	}
	for key, value := range settingEnv {
		child[key] = value
	}
	for key, value := range additions {
		child[key] = value
	}

	keys := make([]string, 0, len(child))
	for key := range child {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+child[key])
	}
	return result
}

func resolveClaudeConfigDir(claudeConfigDir string) (string, error) {
	if claudeConfigDir != "" {
		return claudeConfigDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("ホームディレクトリを取得できません: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
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
