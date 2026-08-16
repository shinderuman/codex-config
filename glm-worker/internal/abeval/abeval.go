// Package abevalはCodex Direct実行対glm-worker orchestrated実行のA/B比較基盤を担う。
// 両modeが共有する比較metadata(Spec)と各modeの実行記録(RunRecord)を読み込み、比較前提の
// 検証・集計(Codex Reduction・Quality Delta・時間・GLM usage)・表示を行う。実runの起動や
// AI呼出は行わず、usage測定のための追加promptも発生させない。実Sol High Direct baseline・
// 本番A/B・複数repeat・Codex枠を消費するbenchmarkはこのpackageの外でユーザーの明示指示に
// よってだけ実行する。
package abeval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

const (
	specVersion      = 1
	runRecordVersion = 1
)

// CanonicalMeasurementBoundaryはA/B評価の計測境界契約。親USER_REQUEST/task開始から
// 最終完了までの親Codex全体とし、委譲前処理・Sol decision/review・fix instruction・
// final acceptanceを含む。Specはこの文面を要求され、mode間で境界定義が変わらない。
const CanonicalMeasurementBoundary = "親USER_REQUEST/task開始から最終完了までの親Codex全体(委譲前処理、Sol decision/review、fix instruction、final acceptanceを含む)"

// GLMUsageSourceTaskStatsはorchestrated実行のGLM usageとproxy指標を既存task stats
// 履歴から解決するsource指定。recordはこのsourceとtask_idを持ち、表示前にstateの
// TaskStatsから値を導入する。schema v1はこれ以外のglm_usage.sourceを受理せず、
// 任意の転記済み実測値sourceは明示的な契約変更とtestを必要とする。
const GLMUsageSourceTaskStats = "glm-worker-task-stats"

// CodexUsageSourceAppExportはactual Codex使用量のschema v1既知source。公式app usage
// export由来の実測値だけを表す。source申告とexport内容の機械照合は行えないため、
// 既知source以外の値はfail closedとし、取得できない場合はunknown(source空)だけを
// 受理する。将来sourceを増やす場合は明示的な契約変更とtestを要する。
const CodexUsageSourceAppExport = "codex-app-usage-export"

type Mode string

const (
	// ModeDirectはglm-worker委譲を使わず通常のCodex能力とtoolだけで同一要求を
	// 実行するmode。探索・context・review等を不自然に制限しない。
	ModeDirect Mode = "direct"
	// ModeOrchestratedは同一要求・品質条件でglm-worker委譲を使用するmode。
	ModeOrchestrated Mode = "orchestrated"
)

// Specは両modeの比較runが共有する固定比較metadata。同一repository snapshot・初期
// working tree・USER_REQUEST・完了条件・品質verification・Codex model/reasoning条件と、
// 独立session/working tree・cache汚染回避の隔離条件を宣言する。
type Spec struct {
	Version              int                   `json:"version"`
	ID                   string                `json:"id"`
	UserRequest          string                `json:"user_request"`
	RepoSnapshotCommit   string                `json:"repo_snapshot_commit"`
	InitialWorktree      string                `json:"initial_worktree"`
	CompletionConditions string                `json:"completion_conditions"`
	QualityVerification  string                `json:"quality_verification"`
	CodexModel           string                `json:"codex_model"`
	CodexReasoningEffort string                `json:"codex_reasoning_effort"`
	MeasurementBoundary  string                `json:"measurement_boundary"`
	Isolation            IsolationRequirements `json:"isolation"`
}

// IsolationRequirementsは比較汚染回避条件。IndependentSession/IndependentWorktreeは
// 比較前提、CacheAvoidanceは先行runやcacheによる汚染を避ける具体手段の宣言。
type IsolationRequirements struct {
	IndependentSession  bool   `json:"independent_session"`
	IndependentWorktree bool   `json:"independent_worktree"`
	CacheAvoidance      string `json:"cache_avoidance"`
}

// RunRecordは1 mode分の実行記録。計測境界はSpecのCanonicalMeasurementBoundaryに従い
// 親Codex全体を対象とする。actual usageは公式/runtime telemetry由来のみを受け付け、
// 取得できない値はunknown(Source空)とし推定値を記録しない。
type RunRecord struct {
	Version       int           `json:"version"`
	SpecID        string        `json:"spec_id"`
	SpecSHA256    string        `json:"spec_sha256"`
	Mode          Mode          `json:"mode"`
	SessionID     string        `json:"session_id"`
	WorktreePath  string        `json:"worktree_path"`
	Boundary      Boundary      `json:"boundary"`
	RunConditions RunConditions `json:"run_conditions"`
	CodexUsage    CodexUsage    `json:"codex_usage"`
	GLMUsage      GLMUsage      `json:"glm_usage"`
	Quality       Quality       `json:"quality"`
	Proxy         ProxyMetrics  `json:"proxy"`
}

// Boundaryは計測境界の実測時刻。StartedAtは親USER_REQUEST/task開始、CompletedAtは
// 最終完了(final acceptance)。
type Boundary struct {
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

// RunConditionsはrecord作成時に確認した実行開始条件。Specの宣言と一致することを
// 検証し、specへ固定した条件から実際のrun開始状態がずれていないかを機械検査する。
type RunConditions struct {
	RepoSnapshotCommit   string `json:"repo_snapshot_commit"`
	InitialWorktree      string `json:"initial_worktree"`
	CodexModel           string `json:"codex_model"`
	CodexReasoningEffort string `json:"codex_reasoning_effort"`
}

// CodexUsageはactual Codex使用量。Sourceはschema v1ではCodexUsageSourceAppExportのみを
// 受け付ける既知source識別で、空のときはtoken値を持てずunknown扱いになる。
// proxy指標や推定値での代替を拒否する。
type CodexUsage struct {
	Source       string `json:"source"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}

// Knownはactual使用量が取得元つきで記録されているか。falseのとき値はunknown。
func (u CodexUsage) Known() bool {
	return u.Source != ""
}

// GLMUsageはglm-worker側の実測使用量。direct modeはglm-worker委譲を行わないため
// 全field零・Source空を要求される。
type GLMUsage struct {
	Source                   string `json:"source"`
	TaskID                   string `json:"task_id,omitempty"`
	InputTokens              int64  `json:"input_tokens"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens"`
	OutputTokens             int64  `json:"output_tokens"`
	ModelCalls               int    `json:"model_calls"`
}

// IsZeroはglm-worker未使用(direct mode)の零値か。
func (u GLMUsage) IsZero() bool {
	return u == GLMUsage{}
}

// Qualityは品質verificationの実測結果。test・hidden verification・escaped bug・
// scope violationをA/B評価の品質証拠とし、LLM自己採点fieldは持たない。
type Quality struct {
	TestsRun           int    `json:"tests_run"`
	TestFailures       int    `json:"test_failures"`
	HiddenVerification string `json:"hidden_verification"`
	EscapedBugs        int    `json:"escaped_bugs"`
	ScopeViolations    int    `json:"scope_violations"`
}

// ProxyMetricsはactual usageではない代理指標。Codex使用量の代用にせず、actual usageと
// 区別した表示だけに使う。
type ProxyMetrics struct {
	SolPacketBytes      int `json:"sol_packet_bytes"`
	SolDecisionCommands int `json:"sol_decision_commands"`
	SolFixCommands      int `json:"sol_fix_commands"`
	AutoFixRounds       int `json:"auto_fix_rounds"`
}

// SpecSHA256はSpecの正準JSONのhash。record作成時点のspecと現在のspecが一致することを
// 検証し、条件変更後の旧記録再利用による比較を拒否する。
func SpecSHA256(spec Spec) string {
	// Specはstring/int/boolのみでmapや関数を含まないためmarshalは常に成功し、
	// field順はstruct定義順で固定される。
	data, _ := json.Marshal(spec)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
