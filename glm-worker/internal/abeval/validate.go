package abeval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LoadPairはrun dirからspec.json・direct.json・orchestrated.jsonを読み込む。
// ここでは構造だけを検証し、比較前提の検証はValidatePairが行う。
func LoadPair(dir string) (Spec, RunRecord, RunRecord, error) {
	spec, err := LoadSpec(filepath.Join(dir, "spec.json"))
	if err != nil {
		return Spec{}, RunRecord{}, RunRecord{}, err
	}
	direct, err := LoadRecord(filepath.Join(dir, "direct.json"))
	if err != nil {
		return Spec{}, RunRecord{}, RunRecord{}, fmt.Errorf("direct記録の読み込み: %w", err)
	}
	orchestrated, err := LoadRecord(filepath.Join(dir, "orchestrated.json"))
	if err != nil {
		return Spec{}, RunRecord{}, RunRecord{}, fmt.Errorf("orchestrated記録の読み込み: %w", err)
	}
	return spec, direct, orchestrated, nil
}

func LoadSpec(path string) (Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, fmt.Errorf("specを読めません: %w", err)
	}
	var spec Spec
	if err := decodeStrict(data, "spec", &spec); err != nil {
		return Spec{}, err
	}
	if spec.Version != specVersion {
		return Spec{}, fmt.Errorf("spec versionは%dである必要があります: %d", specVersion, spec.Version)
	}
	return spec, nil
}

func LoadRecord(path string) (RunRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RunRecord{}, fmt.Errorf("run記録を読めません: %w", err)
	}
	var record RunRecord
	if err := decodeStrict(data, "run記録", &record); err != nil {
		return RunRecord{}, err
	}
	if record.Version != runRecordVersion {
		return RunRecord{}, fmt.Errorf("run記録versionは%dである必要があります: %d", runRecordVersion, record.Version)
	}
	if record.Mode != ModeDirect && record.Mode != ModeOrchestrated {
		return RunRecord{}, fmt.Errorf("modeは%sか%sである必要があります: %s", ModeDirect, ModeOrchestrated, record.Mode)
	}
	return record, nil
}

// decodeStrictは未知fieldと末尾の第2JSON値を拒否してJSONへdecodeする。field typoや
// spec hash対象外の追加metadataを黙って捨てないための契約入口で、run dirの全JSON
// fileの読み込みに使う。
func decodeStrict(data []byte, what string, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%sを読めません: %w", what, err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%sには複数のJSON値が含まれています: %v", what, err)
	}
	return nil
}

// ValidateSpecは固定比較metadataの必須条件を検証する。計測境界は契約文面と完全一致を
// 要求し、mode間で境界定義が変わる比較を成立させない。
func ValidateSpec(spec Spec) error {
	if spec.Version != specVersion {
		return fmt.Errorf("spec versionは%dである必要があります: %d", specVersion, spec.Version)
	}
	var empty []string
	if spec.ID == "" {
		empty = append(empty, "id")
	}
	if spec.UserRequest == "" {
		empty = append(empty, "user_request")
	}
	if spec.RepoSnapshotCommit == "" {
		empty = append(empty, "repo_snapshot_commit")
	}
	if spec.InitialWorktree == "" {
		empty = append(empty, "initial_worktree")
	}
	if spec.CompletionConditions == "" {
		empty = append(empty, "completion_conditions")
	}
	if spec.QualityVerification == "" {
		empty = append(empty, "quality_verification")
	}
	if spec.CodexModel == "" {
		empty = append(empty, "codex_model")
	}
	if spec.CodexReasoningEffort == "" {
		empty = append(empty, "codex_reasoning_effort")
	}
	if len(empty) > 0 {
		return fmt.Errorf("specの必須fieldが空です: %s", strings.Join(empty, ","))
	}
	if !isGitObjectHash(spec.RepoSnapshotCommit) {
		return fmt.Errorf("repo_snapshot_commitがgit object hash形式ではありません: %s", spec.RepoSnapshotCommit)
	}
	if spec.MeasurementBoundary != CanonicalMeasurementBoundary {
		return fmt.Errorf("measurement_boundaryは計測境界契約と一致する必要があります: %q", spec.MeasurementBoundary)
	}
	if !spec.Isolation.IndependentSession || !spec.Isolation.IndependentWorktree {
		return fmt.Errorf("isolationは独立session・独立working treeを比較前提として要求します: %+v", spec.Isolation)
	}
	if spec.Isolation.CacheAvoidance == "" {
		return fmt.Errorf("isolation.cache_avoidanceが空です。先行runやcacheによる比較汚染回避手段を宣言してください")
	}
	return nil
}

// isGitObjectHashはSHA-1(40桁)またはSHA-256(64桁)の小写hex形式かを判定する。
func isGitObjectHash(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ValidatePairはspecとdirect/orchestrated両記録の比較前提を検証する。spec自体の
// 固定比較条件、mode間でsession・working treeが独立していること(比較汚染回避)、
// actual usage契約(unknown推定禁止・direct modeのGLM未使用)を含む。
func ValidatePair(spec Spec, direct, orchestrated RunRecord) error {
	if err := ValidateSpec(spec); err != nil {
		return err
	}
	if direct.Mode != ModeDirect {
		return fmt.Errorf("1つ目の記録はmode %sである必要があります: %s", ModeDirect, direct.Mode)
	}
	if orchestrated.Mode != ModeOrchestrated {
		return fmt.Errorf("2つ目の記録はmode %sである必要があります: %s", ModeOrchestrated, orchestrated.Mode)
	}
	if err := validateRecord(spec, direct); err != nil {
		return fmt.Errorf("direct記録: %w", err)
	}
	if err := validateRecord(spec, orchestrated); err != nil {
		return fmt.Errorf("orchestrated記録: %w", err)
	}
	if direct.SessionID == orchestrated.SessionID {
		return fmt.Errorf("両modeが同一session %qを使っています。独立sessionで比較してください", direct.SessionID)
	}
	if filepath.Clean(direct.WorktreePath) == filepath.Clean(orchestrated.WorktreePath) {
		return fmt.Errorf("両modeが同一working tree %qを使っています。独立working treeで比較してください", direct.WorktreePath)
	}
	return nil
}

func validateRecord(spec Spec, record RunRecord) error {
	if record.SpecID != spec.ID {
		return fmt.Errorf("spec_id %qはspec %qと一致しません", record.SpecID, spec.ID)
	}
	if record.SpecSHA256 != SpecSHA256(spec) {
		return fmt.Errorf("spec_sha256が現在のspecと一致しません。recordは別revisionのspecで作成されています")
	}
	if record.SessionID == "" {
		return fmt.Errorf("session_idが空です")
	}
	if !filepath.IsAbs(record.WorktreePath) {
		return fmt.Errorf("worktree_pathは絶対pathである必要があります: %s", record.WorktreePath)
	}
	expected := RunConditions{
		RepoSnapshotCommit:   spec.RepoSnapshotCommit,
		InitialWorktree:      spec.InitialWorktree,
		CodexModel:           spec.CodexModel,
		CodexReasoningEffort: spec.CodexReasoningEffort,
	}
	if record.RunConditions != expected {
		return fmt.Errorf("run_conditionsがspecの固定条件と一致しません: %+v want %+v", record.RunConditions, expected)
	}
	if record.Boundary.StartedAt.IsZero() || record.Boundary.CompletedAt.IsZero() {
		return fmt.Errorf("boundaryの時刻が未設定です")
	}
	if !record.Boundary.StartedAt.Before(record.Boundary.CompletedAt) {
		return fmt.Errorf("boundaryの開始時刻が完了時刻以降です: %s >= %s", record.Boundary.StartedAt, record.Boundary.CompletedAt)
	}
	if record.CodexUsage.Source != "" && record.CodexUsage.Source != CodexUsageSourceAppExport {
		return fmt.Errorf("codex_usage.sourceは%sのみ受理します。取得できない場合はunknown(source空)としてください: %q", CodexUsageSourceAppExport, record.CodexUsage.Source)
	}
	if record.CodexUsage.InputTokens < 0 || record.CodexUsage.OutputTokens < 0 {
		return fmt.Errorf("codex_usageのtoken値が負です: %+v", record.CodexUsage)
	}
	if !record.CodexUsage.Known() && (record.CodexUsage.InputTokens != 0 || record.CodexUsage.OutputTokens != 0) {
		return fmt.Errorf("codex_usageはsourceなしにtoken値を持てません。actual usageを取得できない場合はunknown(零値)としてください")
	}
	switch record.Quality.HiddenVerification {
	case "pass", "fail", "not-run":
	default:
		return fmt.Errorf("quality.hidden_verificationはpass/fail/not-runである必要があります: %q", record.Quality.HiddenVerification)
	}
	if record.Quality.TestsRun < 0 || record.Quality.TestFailures < 0 {
		return fmt.Errorf("qualityのtest数が負です: %+v", record.Quality)
	}
	if record.Quality.TestFailures > record.Quality.TestsRun {
		return fmt.Errorf("quality.test_failuresがtests_runを超えています: %+v", record.Quality)
	}
	if record.Quality.EscapedBugs < 0 || record.Quality.ScopeViolations < 0 {
		return fmt.Errorf("qualityのescaped_bugs/scope_violationsが負です: %+v", record.Quality)
	}
	if record.GLMUsage.InputTokens < 0 || record.GLMUsage.CacheCreationInputTokens < 0 ||
		record.GLMUsage.CacheReadInputTokens < 0 || record.GLMUsage.OutputTokens < 0 || record.GLMUsage.ModelCalls < 0 {
		return fmt.Errorf("glm_usageの値が負です: %+v", record.GLMUsage)
	}
	switch record.Mode {
	case ModeDirect:
		if !record.GLMUsage.IsZero() {
			return fmt.Errorf("direct modeはglm-worker委譲を使わないためglm_usageを持ちません: %+v", record.GLMUsage)
		}
	case ModeOrchestrated:
		if record.GLMUsage.Source != GLMUsageSourceTaskStats {
			return fmt.Errorf("orchestrated modeのglm_usage.sourceは%sのみ受理します。任意sourceへの転記は明示的な契約変更が必要です: %q", GLMUsageSourceTaskStats, record.GLMUsage.Source)
		}
		if record.GLMUsage.TaskID == "" {
			return fmt.Errorf("glm_usage.sourceが%sのときtask_idが必要です", GLMUsageSourceTaskStats)
		}
	}
	if (record.Proxy != ProxyMetrics{}) {
		if record.Proxy.SolPacketBytes < 0 || record.Proxy.SolDecisionCommands < 0 || record.Proxy.SolFixCommands < 0 || record.Proxy.AutoFixRounds < 0 {
			return fmt.Errorf("proxyの値が負です: %+v", record.Proxy)
		}
	}
	return nil
}
