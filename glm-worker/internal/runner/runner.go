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
	"sync"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/packet"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// isolationPolicyVersionはworker/reviewer起動の隔離構成を識別する。
// safe-mode・空setting-sources・child env allowlist・inline隔離settingsの組合せが
// 変わったらbumpする。旧versionで採番されたsessionは暗黙入力が混入しているため
// resumeせず新sessionへ切り替える。
const isolationPolicyVersion = "claude-isolation-1"

// subtypeStructuredOutputRetryExhaustedはCLIがschema適合出力を生成できず
// 内部retryを使い切ったときにresult eventへ付ける失敗subtype。この失敗は
// provider消費が進んだ末の契約不成立のため、呼出元はfail closedで扱う。
const subtypeStructuredOutputRetryExhausted = "error_max_structured_output_retries"

// StructuredOutputErrorは--json-schema呼出で結果objectが得られなかった失敗。
// retry枯渇・result event欠落のどちらも結果本文側に修復可能な情報が残らないため、
// 再送やresumeによる自動回復の対象にせずfail closedで扱う。
type StructuredOutputError struct {
	Subtype        string
	TerminalReason string
}

func (e *StructuredOutputError) Error() string {
	if e.Subtype != "" {
		return fmt.Sprintf("structured outputが得られませんでした: subtype=%s", e.Subtype)
	}
	return "structured outputが得られませんでした: result eventにstructured_outputがありません"
}

// IsStructuredOutputErrorはfail closed対象のstructured output契約失敗を判定する。
func IsStructuredOutputError(err error) bool {
	var target *StructuredOutputError
	return errors.As(err, &target)
}

// RetryExhaustedはCLI内部のschema適合retry枯渇による失敗かどうかを返す。
// 成功resultでのstructured_output欠落(Subtype空)とは失敗境界が異なるため、
// retry枯渇頻度を数える集計側でこの区別を使う。
func (e *StructuredOutputError) RetryExhausted() bool {
	return e.Subtype == subtypeStructuredOutputRetryExhausted
}

var (
	workerSchemaOnce   sync.Once
	reviewerSchemaOnce sync.Once
	workerSchemaValue  string
	workerSchemaErr    error
	reviewerSchemaVal  string
	reviewerSchemaFail error
)

// structuredSchemaはrole対応のtyped結果schemaを返す。schemaは自package固定値で
// 構築時に検証済みのため、process内で1回だけ構築して再利用する。
func structuredSchema(role state.SessionRole) (string, error) {
	if role == state.ReviewerRole {
		reviewerSchemaOnce.Do(func() {
			reviewerSchemaVal, reviewerSchemaFail = packet.ReviewerSchemaJSON()
		})
		return reviewerSchemaVal, reviewerSchemaFail
	}
	workerSchemaOnce.Do(func() {
		workerSchemaValue, workerSchemaErr = packet.WorkerSchemaJSON()
	})
	return workerSchemaValue, workerSchemaErr
}

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
	// PlainFailureはresult本文が得られない経路で、JSON eventとして解釈できない
	// plain stdout行にだけ既存provider classifierを適用した結果。5h上限・transientの
	// ときだけKindへ値が入り、raw本文とfatal既定値は保持しない。
	PlainFailure ProviderFailureClass
	// StructuredOutputは--json-schemaで強制された結果objectの権威値。result文字列と
	// 同一内容だが、契約上はこのobjectだけを結果解析へ使う。
	StructuredOutput json.RawMessage
}

type claudeJSONResult struct {
	Type           string                `json:"type"`
	Subtype        string                `json:"subtype"`
	IsError        bool                  `json:"is_error"`
	Result         string                `json:"result"`
	StructuredOut  json.RawMessage       `json:"structured_output"`
	TerminalReason string                `json:"terminal_reason"`
	DurationMS     int64                 `json:"duration_ms"`
	DurationAPIMS  int64                 `json:"duration_api_ms"`
	NumTurns       int                   `json:"num_turns"`
	TotalCostUSD   float64               `json:"total_cost_usd"`
	Usage          TokenUsage            `json:"usage"`
	ModelUsage     map[string]ModelUsage `json:"modelUsage"`
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
// 出力はstream-jsonで受け、stdoutはevent ingesterだけが処理する。実行中に返る受動
// eventはmetadataへ縮約してtask単位event logへbest-effort追記し、非result eventの
// raw本文・thinking・tool入出力をdisk・task log・診断tail・telemetryへ保存しない。
// 最終result eventだけをboundedに保持し、result eventは--output-format jsonの出力と
// 同一schemaのためresult解析・session/resume semanticsは変わらず、追加の
// prompt/model callは発生しない。結果protocolはrole対応の--json-schemaで強制される
// typed structured output単一であり、成功時にstructured_outputが得られなければ
// fail closedのStructuredOutputErrorを返す。JSON eventとして解釈できないplain stdout行だけは
// 旧raw fallbackの分類 semanticsを保つため既存classifierへ読ませ、5h上限・transient
// の構造値だけをRunResult.PlainFailureへ渡す。event追記失敗はtask成否へ影響させない。
func (r *ClaudeRunner) Run(
	role state.SessionRole,
	phase string,
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
	schema, err := structuredSchema(role)
	if err != nil {
		return result, fmt.Errorf("structured output schemaを構築できません: %w", err)
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
		"--output-format", "stream-json",
		"--verbose",
		"--dangerously-skip-permissions",
		"--strict-mcp-config",
		"--mcp-config", `{"mcpServers":{}}`,
		"--disable-slash-commands",
		"--settings", isolationArgs,
		"--json-schema", schema,
	)

	if readOnly {
		args = append(args, "--disallowedTools", "Edit", "Write", "NotebookEdit", "Agent")
	}

	args = append(args, "--append-system-prompt-file", systemFile, prompt)

	stderrPath := outputPath + ".stderr"
	stderr, err := createPrivateFile(stderrPath)
	if err != nil {
		return result, err
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		stderr.Close()
		return result, fmt.Errorf("/dev/nullを開けません: %w", err)
	}
	defer devNull.Close()

	ingester := r.newTaskEventIngester(taskID, role, phase, model, sessionID, ready)

	command := exec.Command(r.config.ClaudeBin, args...)
	command.Dir = r.config.RepoRoot
	command.Stdin = devNull
	// stdoutはingesterだけが受け、非result eventのraw本文をdiskへ書かない。
	// ingesterは行ごとにmetadataへ縮約し、最終result event行だけboundedに保持する。
	command.Stdout = ingester
	command.Stderr = stderr
	command.Env = buildChildEnv(r.config.EnvAllowlist, settingEnv, map[string]string{
		"CLAUDE_CONFIG_DIR":                r.config.ClaudeConfigDir,
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW":  "500000",
		"CLAUDE_CODE_ALWAYS_ENABLE_EFFORT": "1",
	}, envDeletes)

	runErr := command.Run()
	ingester.flush()
	stderrCloseErr := stderr.Close()
	if runErr == nil && stderrCloseErr != nil {
		runErr = stderrCloseErr
	}

	parsed, parseErr := parseCapturedStreamResult(ingester.result())
	if parseErr == nil {
		result.Response = parsed.Result
		result.StructuredOutput = parsed.StructuredOut
		result.TopLevelUsage = parsed.Usage
		result.ModelUsage = parsed.ModelUsage
		result.DurationMS = parsed.DurationMS
		result.DurationAPIMS = parsed.DurationAPIMS
		result.TopLevelTurns = parsed.NumTurns
		result.TotalCostUSD = parsed.TotalCostUSD
	}
	// 旧json出力はresult本文が空のときraw stdout fileを分類入力へコピーしていた。
	// stream化後はplain stdoutをraw保存せず、既存classifierへの構造値だけを渡す。
	if result.Response == "" {
		result.PlainFailure = classifyPlainStdoutFailure(ingester.plainSignal())
	}

	if err := writeResultOutput(outputPath, result.Response, streamResultSummary(parsed, parseErr), stderrPath); err != nil {
		return result, err
	}
	// retry枯渇はCLIがexit 1で終えるためrunErrより先に判定し、exit statusの一般errorへ
	// 埋もれさせない。result/structured_outputはnullで、再送しても同じ契約不成立に
	// 至る可能性が高いためfail closed扱いの型付きerrorを返す。
	if parseErr == nil && parsed.Subtype == subtypeStructuredOutputRetryExhausted {
		return result, &StructuredOutputError{Subtype: parsed.Subtype, TerminalReason: parsed.TerminalReason}
	}
	if runErr != nil {
		return result, runErr
	}
	if parseErr != nil {
		return result, parseErr
	}
	if parsed.IsError {
		return result, fmt.Errorf("Claude CLIがerror結果を返しました: subtype=%s", parsed.Subtype)
	}
	// 成功経路でstructured_outputがなければ契約破綻。旧テキストprotocolへの
	// 暗黙fallbackは存在しないため、ここでfail closedとする。
	if !structuredOutputPresent(result.StructuredOutput) {
		return result, &StructuredOutputError{}
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

// parseCapturedStreamResultはingesterが保持した最終result event行を解析する。result eventは
// --output-format jsonの出力objectと同一schemaのため、取り出した後の取り扱いはjson出力と
// 同じ意味を保つ。result eventがない場合(起動失敗・途中kill等)はjson出力のtype不正と同じ
// 失敗区分へ落とす。
func parseCapturedStreamResult(line []byte, found bool) (claudeJSONResult, error) {
	if !found {
		return claudeJSONResult{}, fmt.Errorf("Claude CLIのJSON出力typeが不正です: result eventがありません")
	}
	var parsed claudeJSONResult
	if err := json.Unmarshal(line, &parsed); err != nil {
		return claudeJSONResult{}, fmt.Errorf("Claude CLIのJSON出力を解析できません: %w", err)
	}
	return parsed, nil
}

// structuredOutputPresentはresult eventのstructured_outputがnull/欠落でないかを判定する。
// json.RawMessageはJSON nullを"null" bytesとして保持するため、長さ検査だけでは不足する。
func structuredOutputPresent(raw json.RawMessage) bool {
	return len(raw) != 0 && string(raw) != "null"
}

// newTaskEventIngesterはこのcall分の受動event記録を用意する。call ID生成に失敗した
// 場合は以後何も記録しないingesterを返し、本体実行へ影響させない。
func (r *ClaudeRunner) newTaskEventIngester(
	taskID string,
	role state.SessionRole,
	phase string,
	model string,
	sessionID string,
	resumed bool,
) *streamEventIngester {
	callID, err := state.NewUUID()
	if err != nil {
		state.WarnTaskEventSkip("call ID生成", err)
		return &streamEventIngester{closed: true}
	}
	return newStreamEventIngester(r.state, taskID, callID, role, phase, model, sessionID, resumed)
}

// streamResultSummaryはresult本文が得られない経路へ出力する安全な構造summary。
// assistant/tool本文・thinking等のcontentを含まず、失敗分類とtransient signal検出に
// 必要な構造情報(解析error・subtype・is_error)だけを残す。event数等の任意数値は
// 分類入力となるこの経路へ出さず、transient HTTP status signatureへの誤一致を防ぐ。
func streamResultSummary(parsed claudeJSONResult, parseErr error) string {
	if parseErr != nil {
		return fmt.Sprintf("stream-json result unavailable: %v\n", parseErr)
	}
	if parsed.Result == "" {
		return fmt.Sprintf("stream result event: subtype=%s is_error=%v\n", parsed.Subtype, parsed.IsError)
	}
	return ""
}

// classifyPlainStdoutFailureはplain stdoutのsignal本文を旧raw fallbackと同じclassifierへ
// 通し、5h上限・transientの構造値だけを返す。明示fatal信号とその他の区別は既存
// classifier上どちらもfatal既定のため、何も一致しないときは空を返して呼出元の
// file由来分類へ委ねる。
func classifyPlainStdoutFailure(plain string) ProviderFailureClass {
	class := ClassifyProviderFailureText(plain)
	if class.Kind == ProviderFailureZaiFiveHour || class.Kind == ProviderFailureTransient {
		return class
	}
	return ProviderFailureClass{}
}

// writeResultOutputは最終outputPathへresponse(result本文)・構造summary・stderrだけを
// 0600で書き出す。raw stream全体の転記は行わず、失敗時の診断tail・telemetryへ
// 非result event本文が流れる経路を作らない。
func writeResultOutput(outputPath string, response string, summary string, stderrPath string) error {
	var data []byte
	if response != "" {
		data = []byte(response)
		if data[len(data)-1] != '\n' {
			data = append(data, '\n')
		}
	}
	if summary != "" {
		data = append(data, summary...)
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
