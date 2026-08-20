package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/abeval"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/workflow"
)

func evalABTestConfig(t *testing.T) config.AppConfig {
	t.Helper()
	return config.AppConfig{
		RepoRoot:  t.TempDir(),
		RepoHash:  "abeval-test-hash",
		RepoShort: "abeval-test",
		StateBase: t.TempDir(),
	}
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func evalABSpec() abeval.Spec {
	return abeval.Spec{
		Version:              1,
		ID:                   "evalab-cli-spec",
		UserRequest:          "CLI表示のA/B比較をfake記録で検証する",
		RepoSnapshotCommit:   "3f2a9c1d5e7b4a08c9d6e1f2a3b4c5d6e7f8a9b0",
		InitialWorktree:      "clean(未commit変更なし)",
		CompletionConditions: "go test -count=1 ./...成功、USER_REQUEST要件充足",
		QualityVerification:  "test・hidden verification・escaped bug・scope violationの4観点",
		CodexModel:           "gpt-5.3-codex",
		CodexReasoningEffort: "high",
		MeasurementBoundary:  abeval.CanonicalMeasurementBoundary,
		Isolation: abeval.IsolationRequirements{
			IndependentSession:  true,
			IndependentWorktree: true,
			CacheAvoidance:      "mode間で独立session・独立worktreeを使用し、先行runの出力・cacheを引き継がない",
		},
	}
}

func evalABDirectRecord(spec abeval.Spec) abeval.RunRecord {
	start := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	return abeval.RunRecord{
		Version:      1,
		SpecID:       spec.ID,
		SpecSHA256:   abeval.SpecSHA256(spec),
		Mode:         abeval.ModeDirect,
		SessionID:    "cli-direct-session",
		WorktreePath: "/tmp/evalab-cli/direct",
		Boundary: abeval.Boundary{
			StartedAt:   start,
			CompletedAt: start.Add(60 * time.Minute),
		},
		RunConditions: abeval.RunConditions{
			RepoSnapshotCommit:   spec.RepoSnapshotCommit,
			InitialWorktree:      spec.InitialWorktree,
			CodexModel:           spec.CodexModel,
			CodexReasoningEffort: spec.CodexReasoningEffort,
		},
		CodexUsage: abeval.CodexUsage{Source: abeval.CodexUsageSourceAppExport, InputTokens: 1000000, OutputTokens: 80000},
		Quality:    abeval.Quality{TestsRun: 10, TestFailures: 0, HiddenVerification: "pass"},
	}
}

func evalABOrchestratedRecord(spec abeval.Spec) abeval.RunRecord {
	start := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	return abeval.RunRecord{
		Version:      1,
		SpecID:       spec.ID,
		SpecSHA256:   abeval.SpecSHA256(spec),
		Mode:         abeval.ModeOrchestrated,
		SessionID:    "cli-orchestrated-session",
		WorktreePath: "/tmp/evalab-cli/orchestrated",
		Boundary: abeval.Boundary{
			StartedAt:   start,
			CompletedAt: start.Add(40 * time.Minute),
		},
		RunConditions: abeval.RunConditions{
			RepoSnapshotCommit:   spec.RepoSnapshotCommit,
			InitialWorktree:      spec.InitialWorktree,
			CodexModel:           spec.CodexModel,
			CodexReasoningEffort: spec.CodexReasoningEffort,
		},
		CodexUsage: abeval.CodexUsage{Source: abeval.CodexUsageSourceAppExport, InputTokens: 500000, OutputTokens: 40000},
		Quality:    abeval.Quality{TestsRun: 10, TestFailures: 0, HiddenVerification: "pass"},
		GLMUsage:   abeval.GLMUsage{Source: abeval.GLMUsageSourceTaskStats, TaskID: "task-ab-1"},
	}
}

// writeABStatsArchiveは既存stats履歴へv3 TaskStats archiveを書き込む。
// --eval-abはglm_usage.source=glm-worker-task-statsの記録をこの履歴から解決する。
func writeABStatsArchive(t *testing.T, cfg config.AppConfig, stats state.TaskStats) {
	t.Helper()
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(st.Path("stats"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.Path(filepath.Join("stats", stats.TaskID+".json")), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func abFakeTaskStats(taskID string) state.TaskStats {
	return state.TaskStats{
		Version:    3,
		TaskID:     taskID,
		Status:     state.TaskStatusComplete,
		ModelCalls: 3,
		InputTokensByAlias: map[string]int64{
			"opus": 400000,
		},
		OutputTokensByAlias: map[string]int64{
			"opus": 50000,
		},
		SolPacketBytes: 300,
	}
}

func writeEvalABRunDir(t *testing.T, spec abeval.Spec, direct, orchestrated abeval.RunRecord) string {
	t.Helper()
	dir := t.TempDir()
	writeJSONFile(t, filepath.Join(dir, "spec.json"), spec)
	writeJSONFile(t, filepath.Join(dir, "direct.json"), direct)
	writeJSONFile(t, filepath.Join(dir, "orchestrated.json"), orchestrated)
	return dir
}

func TestExecuteEvalABPrintsComparisonFromRunDir(t *testing.T) {
	cfg := evalABTestConfig(t)
	spec := evalABSpec()
	dir := writeEvalABRunDir(t, spec, evalABDirectRecord(spec), evalABOrchestratedRecord(spec))
	writeABStatsArchive(t, cfg, abFakeTaskStats("task-ab-1"))

	var stdout bytes.Buffer
	if err := Execute(Command{Mode: ModeEvalAB, Payload: dir}, cfg, nil, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "CODEX_REDUCTION: input=50.0%, output=50.0% (actual usage") {
		t.Fatalf("actual usage基準のCODEX_REDUCTIONが出力されていません:\n%s", out)
	}
	if !strings.Contains(out, "QUALITY_DELTA: tests direct=0fail/10run orchestrated=0fail/10run") {
		t.Fatalf("QUALITY_DELTAが出力されていません:\n%s", out)
	}
	if !strings.Contains(out, "TIME: direct=1h0m0s; orchestrated=40m0s; delta=-20m0s") {
		t.Fatalf("TIMEが出力されていません:\n%s", out)
	}
	if !strings.Contains(out, "orchestrated=input=400000, cache-creation=0, cache-read=0, output=50000, model-calls=3 (source=glm-worker-task-stats)") {
		t.Fatalf("task stats解決済みGLM usageが出力されていません:\n%s", out)
	}
}

func TestExecuteEvalABResolvesGLMUsageFromTaskStats(t *testing.T) {
	cfg := evalABTestConfig(t)
	spec := evalABSpec()
	orchestrated := evalABOrchestratedRecord(spec)
	orchestrated.GLMUsage.TaskID = "task-ab-resolve"
	dir := writeEvalABRunDir(t, spec, evalABDirectRecord(spec), orchestrated)
	writeABStatsArchive(t, cfg, state.TaskStats{
		Version:    3,
		TaskID:     "task-ab-resolve",
		Status:     state.TaskStatusComplete,
		ModelCalls: 4,
		InputTokensByAlias: map[string]int64{
			"opus": 500000,
		},
		OutputTokensByAlias: map[string]int64{
			"opus": 60000,
		},
		SolPacketBytes: 700,
	})

	var stdout bytes.Buffer
	if err := Execute(Command{Mode: ModeEvalAB, Payload: dir}, cfg, nil, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "orchestrated=input=500000, cache-creation=0, cache-read=0, output=60000, model-calls=4 (source=glm-worker-task-stats)") {
		t.Fatalf("task stats解決済みGLM usageが出力されていません:\n%s", out)
	}
	if !strings.Contains(out, "orchestrated=sol-packet-bytes=700") {
		t.Fatalf("task stats解決済みproxy指標が出力されていません:\n%s", out)
	}
}

func TestExecuteEvalABFailsClosedOnMissingTaskStats(t *testing.T) {
	spec := evalABSpec()
	orchestrated := evalABOrchestratedRecord(spec)
	orchestrated.GLMUsage = abeval.GLMUsage{Source: abeval.GLMUsageSourceTaskStats, TaskID: "missing-task"}
	dir := writeEvalABRunDir(t, spec, evalABDirectRecord(spec), orchestrated)

	var stdout bytes.Buffer
	err := Execute(Command{Mode: ModeEvalAB, Payload: dir}, evalABTestConfig(t), nil, &stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "missing-task") {
		t.Fatalf("stats不在taskでfail closedしませんでした: %v", err)
	}
	if stdout.Len() > 0 {
		t.Fatalf("失敗時に比較結果を出力していません: %s", stdout.String())
	}
}

func TestExecuteEvalABRejectsInvalidPairWithoutOutput(t *testing.T) {
	cfg := evalABTestConfig(t)
	spec := evalABSpec()
	direct := evalABDirectRecord(spec)
	orchestrated := evalABOrchestratedRecord(spec)
	orchestrated.CodexUsage = abeval.CodexUsage{InputTokens: 12345}
	dir := writeEvalABRunDir(t, spec, direct, orchestrated)
	writeABStatsArchive(t, cfg, abFakeTaskStats("task-ab-1"))

	var stdout bytes.Buffer
	err := Execute(Command{Mode: ModeEvalAB, Payload: dir}, cfg, nil, &stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "sourceなしにtoken値") {
		t.Fatalf("推定usage入力が拒否されていません: %v", err)
	}
	if stdout.Len() > 0 {
		t.Fatalf("検証失敗時に比較結果を出力していません: %s", stdout.String())
	}
}

func TestExecuteEvalABRejectsMissingRunDir(t *testing.T) {
	var stdout bytes.Buffer
	err := Execute(Command{Mode: ModeEvalAB, Payload: filepath.Join(t.TempDir(), "absent")}, evalABTestConfig(t), nil, &stdout, &bytes.Buffer{})
	if err == nil {
		t.Fatal("存在しないrun dirが受理されました")
	}
}

func TestExecuteEvalABRejectsRunDirWithUnknownField(t *testing.T) {
	cfg := evalABTestConfig(t)
	spec := evalABSpec()
	dir := writeEvalABRunDir(t, spec, evalABDirectRecord(spec), evalABOrchestratedRecord(spec))
	writeABStatsArchive(t, cfg, abFakeTaskStats("task-ab-1"))

	path := filepath.Join(dir, "spec.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	withTypo := []byte(strings.Replace(string(data), "{", `{"typo_field":1,`, 1))
	if err := os.WriteFile(path, withTypo, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err = Execute(Command{Mode: ModeEvalAB, Payload: dir}, cfg, nil, &stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("未知field入りのrun dirが拒否されていません: %v", err)
	}
	if stdout.Len() > 0 {
		t.Fatalf("strict decode失敗時に比較結果を出力していません: %s", stdout.String())
	}
}

// unusedRunnerFactoryは--eval-abがrunner/workflow構築へ到達しないことを
// production entrypoint側で検出するための呼出記録付きfactory。
func unusedRunnerFactory(calls *int) RunnerFactory {
	return func(_ config.AppConfig, _ *state.StateStore) workflow.ModelRunner {
		*calls++
		return nil
	}
}

func TestExecuteEvalABDoesNotCreateStateDirectory(t *testing.T) {
	cfg := evalABTestConfig(t)
	spec := evalABSpec()
	dir := writeEvalABRunDir(t, spec, evalABDirectRecord(spec), evalABOrchestratedRecord(spec))
	stateDir := filepath.Join(cfg.StateBase, cfg.RepoHash)
	runnerCalls := 0
	rf := unusedRunnerFactory(&runnerCalls)

	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("前提違反: stateディレクトリが既に存在します: %s", stateDir)
	}

	err := Execute(Command{Mode: ModeEvalAB, Payload: dir}, cfg, rf, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "task-ab-1") {
		t.Fatalf("stats不在taskでfail closedしませんでした: %v", err)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("実行後にstateディレクトリが作成されました: %s", stateDir)
	}

	missingDir := Execute(Command{Mode: ModeEvalAB, Payload: filepath.Join(t.TempDir(), "absent")}, cfg, rf, &bytes.Buffer{}, &bytes.Buffer{})
	if missingDir == nil {
		t.Fatal("存在しないrun dirが受理されました")
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("run dir検証失敗時にもstateディレクトリが作成されました: %s", stateDir)
	}
	if runnerCalls != 0 {
		t.Fatalf("--eval-abでrunnerが%d回構築されました", runnerCalls)
	}
}

func listStateFiles(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	return paths
}

func TestExecuteEvalABKeepsExistingStateUnchanged(t *testing.T) {
	cfg := evalABTestConfig(t)
	spec := evalABSpec()
	dir := writeEvalABRunDir(t, spec, evalABDirectRecord(spec), evalABOrchestratedRecord(spec))
	writeABStatsArchive(t, cfg, abFakeTaskStats("task-ab-1"))
	runnerCalls := 0

	// writeFileAtomicはtemp+renameで必ずmtimeを更新するため、
	// repo-rootを過去の時刻へ固定すれば再書込みはmtime変化として検出できる。
	repoRootPath := filepath.Join(cfg.StateBase, cfg.RepoHash, "repo-root")
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(repoRootPath, past, past); err != nil {
		t.Fatal(err)
	}
	beforeContent, err := os.ReadFile(repoRootPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(repoRootPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeFiles := listStateFiles(t, filepath.Join(cfg.StateBase, cfg.RepoHash))

	var stdout bytes.Buffer
	if err := Execute(Command{Mode: ModeEvalAB, Payload: dir}, cfg, unusedRunnerFactory(&runnerCalls), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "CODEX_REDUCTION:") {
		t.Fatalf("比較結果が出力されていません: %s", stdout.String())
	}

	afterContent, err := os.ReadFile(repoRootPath)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(repoRootPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterContent) != string(beforeContent) {
		t.Fatalf("repo-rootの内容が更新されました: %q -> %q", string(beforeContent), string(afterContent))
	}
	if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("repo-rootのmtimeが更新されました: %s -> %s", beforeInfo.ModTime(), afterInfo.ModTime())
	}
	afterFiles := listStateFiles(t, filepath.Join(cfg.StateBase, cfg.RepoHash))
	if !slices.Equal(afterFiles, beforeFiles) {
		t.Fatalf("stateファイル構成が変化しました: %v -> %v", beforeFiles, afterFiles)
	}
	if runnerCalls != 0 {
		t.Fatalf("--eval-abでrunnerが%d回構築されました", runnerCalls)
	}
}
