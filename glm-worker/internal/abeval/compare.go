package abeval

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	codexReductionActual  = "actual"
	codexReductionUnknown = "unknown"
)

// Comparisonは検証済みrun記録1組の集計結果。最重要出力のCodexReductionと品質比較、
// および時間とGLM usageを別枠で持つ。GLM tokenとCodex tokenを合算した総合値fieldは
// 持たない。
type Comparison struct {
	Spec                 Spec
	Direct               RunRecord
	Orchestrated         RunRecord
	CodexReduction       CodexReduction
	DirectDuration       time.Duration
	OrchestratedDuration time.Duration
}

// CodexReductionはactual Codex使用量に基づくDirect比の削減率。両modeのactual usageが
// 揃わない場合はStatus=unknownとし、proxy指標や推定値で代替しない。
type CodexReduction struct {
	Status        string
	UnknownReason string
	InputPercent  float64
	OutputPercent float64
}

func Compare(spec Spec, direct, orchestrated RunRecord) Comparison {
	return Comparison{
		Spec:                 spec,
		Direct:               direct,
		Orchestrated:         orchestrated,
		CodexReduction:       codexReduction(direct.CodexUsage, orchestrated.CodexUsage),
		DirectDuration:       direct.Boundary.CompletedAt.Sub(direct.Boundary.StartedAt),
		OrchestratedDuration: orchestrated.Boundary.CompletedAt.Sub(orchestrated.Boundary.StartedAt),
	}
}

func codexReduction(direct, orchestrated CodexUsage) CodexReduction {
	var missing []string
	if !direct.Known() {
		missing = append(missing, "direct")
	}
	if !orchestrated.Known() {
		missing = append(missing, "orchestrated")
	}
	if len(missing) > 0 {
		return CodexReduction{
			Status:        codexReductionUnknown,
			UnknownReason: fmt.Sprintf("actual Codex usageが公式/runtime telemetryから取得できていません: %s", strings.Join(missing, ",")),
		}
	}
	if direct.InputTokens <= 0 && direct.OutputTokens <= 0 {
		return CodexReduction{
			Status:        codexReductionUnknown,
			UnknownReason: "direct actual usageのtoken値が零のため削減率を定義できません",
		}
	}
	result := CodexReduction{Status: codexReductionActual}
	if direct.InputTokens > 0 {
		result.InputPercent = reductionPercent(direct.InputTokens, orchestrated.InputTokens)
	}
	if direct.OutputTokens > 0 {
		result.OutputPercent = reductionPercent(direct.OutputTokens, orchestrated.OutputTokens)
	}
	return result
}

func reductionPercent(direct, orchestrated int64) float64 {
	return float64(direct-orchestrated) / float64(direct) * 100
}

// Formatは比較結果をKEY: value形式の表示へ組み立てる。Codex ReductionとQuality Deltaを
// 先頭に置き、時間とGLM usageは別行とし、actual usageとproxy指標・unknownを区別した
// 表記だけを出力する。
func Format(c Comparison) string {
	var b strings.Builder
	fmt.Fprintf(&b, "SPEC: %s\n", c.Spec.ID)
	fmt.Fprintf(&b, "MODES: %s vs %s\n", ModeDirect, ModeOrchestrated)
	fmt.Fprintf(&b, "COMPARISON_METADATA: commit=%s; initial-worktree=%s; codex-model=%s; codex-reasoning=%s; request-sha256=%s\n",
		c.Spec.RepoSnapshotCommit,
		c.Spec.InitialWorktree,
		c.Spec.CodexModel,
		c.Spec.CodexReasoningEffort,
		shortSHA256(c.Spec.UserRequest))
	fmt.Fprintf(&b, "MEASUREMENT_BOUNDARY: %s\n", c.Spec.MeasurementBoundary)
	fmt.Fprintf(&b, "ISOLATION: independent-session=%t; independent-worktree=%t; cache-avoidance=%s\n",
		c.Spec.Isolation.IndependentSession,
		c.Spec.Isolation.IndependentWorktree,
		c.Spec.Isolation.CacheAvoidance)
	fmt.Fprintf(&b, "CODEX_REDUCTION: %s\n", formatCodexReduction(c))
	fmt.Fprintf(&b, "QUALITY_DELTA: tests direct=%dfail/%drun orchestrated=%dfail/%drun; hidden-verification direct=%s orchestrated=%s; escaped-bugs direct=%d orchestrated=%d; scope-violations direct=%d orchestrated=%d\n",
		c.Direct.Quality.TestFailures, c.Direct.Quality.TestsRun,
		c.Orchestrated.Quality.TestFailures, c.Orchestrated.Quality.TestsRun,
		c.Direct.Quality.HiddenVerification, c.Orchestrated.Quality.HiddenVerification,
		c.Direct.Quality.EscapedBugs, c.Orchestrated.Quality.EscapedBugs,
		c.Direct.Quality.ScopeViolations, c.Orchestrated.Quality.ScopeViolations)
	fmt.Fprintf(&b, "TIME: direct=%s; orchestrated=%s; delta=%s\n",
		c.DirectDuration.Truncate(time.Second),
		c.OrchestratedDuration.Truncate(time.Second),
		(c.OrchestratedDuration - c.DirectDuration).Truncate(time.Second))
	fmt.Fprintf(&b, "CODEX_USAGE: direct=%s; orchestrated=%s\n", formatCodexUsage(c.Direct.CodexUsage), formatCodexUsage(c.Orchestrated.CodexUsage))
	fmt.Fprintf(&b, "GLM_USAGE: direct=%s; orchestrated=%s\n", formatDirectGLMUsage(), formatGLMUsage(c.Orchestrated.GLMUsage))
	fmt.Fprintf(&b, "PROXY_METRICS: direct=%s; orchestrated=%s\n", formatProxy(c.Direct.Proxy), formatProxy(c.Orchestrated.Proxy))
	fmt.Fprintf(&b, "NOTES: GLM tokenとCodex tokenの合算値は算出しない。actual Codex usageは公式/runtime telemetry由来のみで、取得できない値はunknownとし推定しない。codex_usage.sourceは申告値であり公式export内容との機械照合は行わない。usage測定用の追加AI promptは発生させていない。\n")
	return b.String()
}

func formatCodexReduction(c Comparison) string {
	r := c.CodexReduction
	if r.Status == codexReductionUnknown {
		return fmt.Sprintf("unknown (%s)", r.UnknownReason)
	}
	var parts []string
	if c.Direct.CodexUsage.InputTokens > 0 {
		parts = append(parts, fmt.Sprintf("input=%.1f%%", r.InputPercent))
	}
	if c.Direct.CodexUsage.OutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("output=%.1f%%", r.OutputPercent))
	}
	return fmt.Sprintf("%s (actual usage, direct-source=%s, orchestrated-source=%s)",
		strings.Join(parts, ", "),
		c.Direct.CodexUsage.Source,
		c.Orchestrated.CodexUsage.Source)
}

func formatCodexUsage(usage CodexUsage) string {
	if !usage.Known() {
		return "unknown"
	}
	return fmt.Sprintf("actual(source=%s, input=%d, output=%d)", usage.Source, usage.InputTokens, usage.OutputTokens)
}

func formatDirectGLMUsage() string {
	return "not-used(direct modeはglm-worker委譲なし)"
}

func formatGLMUsage(usage GLMUsage) string {
	return fmt.Sprintf("input=%d, cache-creation=%d, cache-read=%d, output=%d, model-calls=%d (source=%s)",
		usage.InputTokens, usage.CacheCreationInputTokens, usage.CacheReadInputTokens, usage.OutputTokens, usage.ModelCalls, usage.Source)
}

func formatProxy(proxy ProxyMetrics) string {
	if (proxy == ProxyMetrics{}) {
		return "none"
	}
	return fmt.Sprintf("sol-packet-bytes=%d, sol-decision-commands=%d, sol-fix-commands=%d, auto-fix-rounds=%d",
		proxy.SolPacketBytes, proxy.SolDecisionCommands, proxy.SolFixCommands, proxy.AutoFixRounds)
}

func shortSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}
