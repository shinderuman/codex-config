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
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/shinderuman/codex-config/glm-worker/internal/config"
	"github.com/shinderuman/codex-config/glm-worker/internal/state"
)

// isolationPolicyVersionはworker/reviewer起動の隔離構成を識別する。
// safe-mode・空setting-sources・child env allowlist・inline隔離settingsの組合せが
// 変わったらbumpする。旧versionで採番されたsessionは暗黙入力が混入しているため
// resumeせず新sessionへ切り替える。
const isolationPolicyVersion = "claude-isolation-1"

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

// NewClaudeRunnerはClaudeRunnerを構築する。
func NewClaudeRunner(cfg config.AppConfig, st *state.StateStore) *ClaudeRunner {
	return &ClaudeRunner{config: cfg, state: st}
}

// Runはrole/effort/promptでClaude Codeを起動し出力をoutputPathへ書き出す。
// 初回起動時は新規sessionを採番し、2回目以降は同一sessionへresumeする。
// 起動は全入力経路を隔離する: --safe-modeでcustomizationを一括無効化し、
// --setting-sources ""でfilesystem settingsを読まず、Z.ai接続・model aliasは
// settings.jsonからallowlist抽出した最小envを明示注入する。CLAUDE.md/auto memory/
// hooks/MCP/skills等はinline --settingsとflagで追加遮断する。組込みsystem promptと
// managed/policy設定だけは遮断不可能な残余として残る。現行の隔離policyと一致しない
// 旧sessionは暗黙入力が混入しているためresumeせず新sessionへ切り替える。
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
	if err := detectUnavoidableManagedInstructionMemory(managedSettingsBases()); err != nil {
		return RunResult{}, err
	}
	if err := detectManagedMDMInstructionMemory(managedMDMPlistPaths()); err != nil {
		return RunResult{}, err
	}
	sessionID, ready, err := r.state.SessionID(role)
	if err != nil {
		return RunResult{}, err
	}
	if ready && r.state.IsolationPolicy() != isolationPolicyVersion {
		if err := r.state.ResetRoleSession(role); err != nil {
			return RunResult{}, err
		}
		sessionID, ready, err = r.state.SessionID(role)
		if err != nil {
			return RunResult{}, err
		}
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

	isolationArgs, err := isolationSettings(r.config.ClaudeConfigDir)
	if err != nil {
		return result, err
	}
	settingEnv, err := loadSettingEnv(r.config.ClaudeConfigDir)
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
	if err := r.state.SetIsolationPolicy(isolationPolicyVersion); err != nil {
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
// 組込みsystem prompt・権限へは影響しない。managed/policy設定は除外・無効化できない。
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
// 値だけを返す。ファイル不在時は空map(Z.ai未設定ならclaudeがauth errorを返す)。
func loadSettingEnv(claudeConfigDir string) (map[string]string, error) {
	configDir, err := resolveClaudeConfigDir(claudeConfigDir)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(configDir, "settings.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("Claude settings.jsonを読み込めません: %w", err)
	}
	var parsed struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("Claude settings.jsonを解析できません: %w", err)
	}
	result := make(map[string]string)
	for _, key := range essentialSettingEnvKeys {
		if value, ok := parsed.Env[key]; ok && value != "" {
			result[key] = value
		}
	}
	return result, nil
}

// osEssentialEnvKeysは親process環境から受け継ぐ実行必須key。
// Claudeの入力経路にはならない(CLAUDE_CODE_*/ANTHROPIC_*を含まない)。
var osEssentialEnvKeys = []string{
	"PATH", "HOME", "TMPDIR", "SHELL", "USER", "LOGNAME",
	"LANG", "LC_ALL", "LC_CTYPE", "TZ", "TERM",
}

// buildChildEnvは隔離されたchild process環境を構築する。
// OS必須keyとextraAllowだけを親envから取り出し、settingEnv(Z.ai)とadditionsで
// 上書き注入する。暗黙の入力経路となるenvは親から引き継がない。
// 同一環境になるようkeyを整列して返す。
func buildChildEnv(extraAllow []string, settingEnv, additions map[string]string) []string {
	allowed := make(map[string]struct{}, len(osEssentialEnvKeys)+len(extraAllow))
	for _, key := range osEssentialEnvKeys {
		allowed[key] = struct{}{}
	}
	for _, key := range extraAllow {
		allowed[key] = struct{}{}
	}

	child := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if _, ok := allowed[key]; ok {
			child[key] = value
		}
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

// resolveClaudeConfigDirは空値を既定~/.claudeへ解決する。
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

// managedSettingsBasesは現行CLI仕様(claude 2.1.226)のadmin-managed/policy settingsの
// 標準base directory候補をOS別に返す。これら配下のmemoryは --safe-mode でも
// claudeMdExcludesでも無効化できず必ず適用される。本関数はpath計算だけ行いIOしない。
//
// macOSはpublished docs(/Library/Application Support/ClaudeCode)とCLI binaryの挙動
// (/etc/claude-code へのfall-through)が一致しないため、fail closedを優先し両候補を調べる。
// 各baseは次のmanaged memoryを持ち得る(全て --safe-mode で残存):
//   - managed-settings.json と managed-settings.d/*.json の `claudeMd` key
//   - <base>/CLAUDE.md (dedicated managed memory)
//   - <base>/.claude/rules/*.md (dedicated managed rules)
//
// macOS MDM plist(/Library/Managed Preferences[/USER]/com.anthropic.claudecode.plist)は
// detectManagedMDMInstructionMemory で別途検出する。Windows GroupPolicy registryも同じ
// policy layerへ配信されるが現platform対象外のためdocs注記へ残す(検出残差)。
func managedSettingsBases() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Library/Application Support/ClaudeCode",
			"/etc/claude-code",
		}
	case "windows":
		return []string{`C:\Program Files\ClaudeCode`}
	default:
		return []string{"/etc/claude-code"}
	}
}

// detectUnavoidableManagedInstructionMemoryはmanaged/policy settingsの標準base配下を
// 調べ、除外不能な命令memory(claudeMd key・managed CLAUDE.md・managed rules)が一つでも
// 存在すればerrorでfail closedする。これらは --safe-mode・claudeMdExcludes いちらでも
// 除外できないため、混入を黙認せず起動前に中止する。pureなpermissions/env policy
// (命令memoryなし)は組織policyなので迂回せずそのまま適用させ、不可避性は出力/docsへ明示。
// 読めない・解析できない場合も隔離保証できないためfail closedする。
func detectUnavoidableManagedInstructionMemory(bases []string) error {
	for _, base := range bases {
		if err := detectManagedMemoryInBase(base); err != nil {
			return err
		}
	}
	return nil
}

// managedMDMPlistPathsは現行CLI(claude 2.1.226)が参照するmacOS MDM managed preferences
// plistの標準pathを返す。device-level(/Library/Managed Preferences/com.anthropic.claudecode.plist)
// とper-user(同 <user>/...)の両方。非darwinでは空(対象外)。user取得失敗時はdevice-levelのみ。
func managedMDMPlistPaths() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	const name = "com.anthropic.claudecode.plist"
	paths := []string{filepath.Join("/Library/Managed Preferences", name)}
	if currentUser, err := user.Current(); err == nil && currentUser.Username != "" {
		paths = append(paths, filepath.Join("/Library/Managed Preferences", currentUser.Username, name))
	}
	return paths
}

// plistToJSONはplistファイルをJSON byte列へ変換する。plutilはmacOS標準toolで確実に
// 変換できるためclaudeMd命令memoryの有無を安全に判別できる。testで差し替え可能。
var plistToJSON = defaultPlistToJSON

func defaultPlistToJSON(path string) ([]byte, error) {
	return exec.Command("plutil", "-convert", "json", "-o", "-", path).Output()
}

// detectManagedMDMInstructionMemoryはmacOS MDM plist配下のmanaged settingsを調べ、
// claudeMd命令memoryが存在すればfail closedする。plutilで確実にJSON化できるため命令memory
// の有無で判定し、純permissions/env policy(命令memoryなし)は組織policyとして迂回せず通す。
// plistの読めない・変換/解析不能な場合も隔離保証できないためfail closedする。
func detectManagedMDMInstructionMemory(paths []string) error {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("managed MDM plist %sを確認できません(隔離を保証できません): %w", path, err)
		}
		if info.IsDir() {
			return fmt.Errorf("managed MDM plist %sがdirectoryです(隔離を保証できません)", path)
		}
		jsonBytes, err := plistToJSON(path)
		if err != nil {
			return fmt.Errorf("managed MDM plist %sを変換/読込できません(隔離を保証できません): %w", path, err)
		}
		if err := failOnClaudeMdInstructionMemory(jsonBytes, "managed MDM plist "+path); err != nil {
			return err
		}
	}
	return nil
}

// failOnClaudeMdInstructionMemoryはJSON化されたmanaged settings/policyを調べ、非空の
// claudeMd命令memoryがあればfail closedする。解析不能もfail closed。空なら通す。
func failOnClaudeMdInstructionMemory(data []byte, label string) error {
	var parsed struct {
		ClaudeMd string `json:"claudeMd"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("%sを解析できません(隔離を保証できません): %w", label, err)
	}
	if strings.TrimSpace(parsed.ClaudeMd) != "" {
		return fmt.Errorf(
			"%sにclaudeMd命令memoryが存在し、--safe-modeでも除外できません(隔離を保証できないため起動を中止)",
			label,
		)
	}
	return nil
}

// detectManagedMemoryInBaseは単一base配下のmanaged memoryを検査する。
func detectManagedMemoryInBase(base string) error {
	settingsCandidates := []string{
		filepath.Join(base, "managed-settings.json"),
		filepath.Join(base, "managed-settings.d"),
	}
	settingsFiles, err := expandJSONFiles(settingsCandidates)
	if err != nil {
		return err
	}
	for _, path := range settingsFiles {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("managed settings %sを読み込めません(隔離を保証できません): %w", path, readErr)
		}
		if err := failOnClaudeMdInstructionMemory(data, "managed settings "+path); err != nil {
			return err
		}
	}

	if err := detectNonEmptyManagedFile(filepath.Join(base, "CLAUDE.md")); err != nil {
		return err
	}

	rulesDir := filepath.Join(base, ".claude", "rules")
	rules, err := expandMarkdownFiles(rulesDir)
	if err != nil {
		return err
	}
	for _, path := range rules {
		if err := detectNonEmptyManagedFile(path); err != nil {
			return err
		}
	}
	return nil
}

// detectNonEmptyManagedFileはpathが存在し非空の場合、除外不能なmanaged memoryとして
// fail closedする。空(内容が空白のみ)なら通す。読めなければfail closed。
func detectNonEmptyManagedFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("managed memory %sを読み込めません(隔離を保証できません): %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil
	}
	return fmt.Errorf(
		"managed memory %sが存在し、--safe-modeでも除外できません(隔離を保証できないため起動を中止)",
		path,
	)
}

// expandJSONFilesはpath列中の *.json ファイルとdirectory配下の *.json を展開して返す。
// 不在のpathは除外する。
func expandJSONFiles(paths []string) ([]string, error) {
	return expandFiles(paths, ".json")
}

// expandMarkdownFilesはdirectory配下の *.md ファイルを返す。directory不在なら空。
func expandMarkdownFiles(dir string) ([]string, error) {
	files, err := expandFiles([]string{dir}, ".md")
	if err != nil {
		return nil, fmt.Errorf("managed rules %sを読めません: %w", dir, err)
	}
	return files, nil
}

func expandFiles(paths []string, suffix string) ([]string, error) {
	var files []string
	for _, basePath := range paths {
		info, err := os.Stat(basePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("%sを確認できません: %w", basePath, err)
		}
		if info.IsDir() {
			entries, err := os.ReadDir(basePath)
			if err != nil {
				return nil, fmt.Errorf("%sを読めません: %w", basePath, err)
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
					continue
				}
				files = append(files, filepath.Join(basePath, entry.Name()))
			}
		} else if strings.HasSuffix(basePath, suffix) {
			files = append(files, basePath)
		}
	}
	return files, nil
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
