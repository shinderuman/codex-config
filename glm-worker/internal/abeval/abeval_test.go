package abeval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeRecordJSONはrecordを一時fileへJSON化してpathを返す。
func writeRecordJSON(t *testing.T, name string, record RunRecord) string {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validSpec() Spec {
	return Spec{
		Version:              specVersion,
		ID:                   "fixed-eval-example",
		UserRequest:          "検索結果のcache freshnessを保証する修正を行う",
		RepoSnapshotCommit:   "104a1315e1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6",
		InitialWorktree:      "clean(IMPLEMENTATION_PLAN.local.md以外未commit変更なし)",
		CompletionConditions: "go test -count=1 ./...成功とUSER_REQUEST要件充足",
		QualityVerification:  "test・hidden verification・escaped bug・scope violation確認",
		CodexModel:           "gpt-5.3-codex",
		CodexReasoningEffort: "high",
		MeasurementBoundary:  CanonicalMeasurementBoundary,
		Isolation: IsolationRequirements{
			IndependentSession:  true,
			IndependentWorktree: true,
			CacheAvoidance:      "mode間で独立session・独立worktreeを用い、先行runの出力・cacheを引き継がない",
		},
	}
}

func validDirectRecord(spec Spec) RunRecord {
	start := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	return RunRecord{
		Version:      runRecordVersion,
		SpecID:       spec.ID,
		SpecSHA256:   SpecSHA256(spec),
		Mode:         ModeDirect,
		SessionID:    "codex-session-direct-0001",
		WorktreePath: "/tmp/abeval-fixture/direct-worktree",
		Boundary: Boundary{
			StartedAt:   start,
			CompletedAt: start.Add(92 * time.Minute),
		},
		RunConditions: RunConditions{
			RepoSnapshotCommit:   spec.RepoSnapshotCommit,
			InitialWorktree:      spec.InitialWorktree,
			CodexModel:           spec.CodexModel,
			CodexReasoningEffort: spec.CodexReasoningEffort,
		},
		CodexUsage: CodexUsage{
			Source:       CodexUsageSourceAppExport,
			InputTokens:  1200000,
			OutputTokens: 90000,
		},
		Quality: Quality{
			TestsRun:           246,
			TestFailures:       0,
			HiddenVerification: "pass",
		},
	}
}

func validOrchestratedRecord(spec Spec) RunRecord {
	start := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	return RunRecord{
		Version:      runRecordVersion,
		SpecID:       spec.ID,
		SpecSHA256:   SpecSHA256(spec),
		Mode:         ModeOrchestrated,
		SessionID:    "codex-session-orchestrated-0002",
		WorktreePath: "/tmp/abeval-fixture/orchestrated-worktree",
		Boundary: Boundary{
			StartedAt:   start,
			CompletedAt: start.Add(58 * time.Minute),
		},
		RunConditions: RunConditions{
			RepoSnapshotCommit:   spec.RepoSnapshotCommit,
			InitialWorktree:      spec.InitialWorktree,
			CodexModel:           spec.CodexModel,
			CodexReasoningEffort: spec.CodexReasoningEffort,
		},
		CodexUsage: CodexUsage{
			Source:       CodexUsageSourceAppExport,
			InputTokens:  480000,
			OutputTokens: 42000,
		},
		// task-stats解決済みsnapshotのfake表現。実CLI経路ではglm_usage値は
		// ResolveFromTaskStatsがstats履歴から導入する。
		GLMUsage: GLMUsage{
			Source:       GLMUsageSourceTaskStats,
			TaskID:       "task-fixture-orchestrated-0002",
			InputTokens:  850000,
			OutputTokens: 120000,
			ModelCalls:   5,
		},
		Quality: Quality{
			TestsRun:           246,
			TestFailures:       0,
			HiddenVerification: "pass",
		},
		Proxy: ProxyMetrics{
			SolPacketBytes: 812,
		},
	}
}

func TestValidatePairAcceptsValidPair(t *testing.T) {
	spec := validSpec()
	direct := validDirectRecord(spec)
	orchestrated := validOrchestratedRecord(spec)
	if err := ValidatePair(spec, direct, orchestrated); err != nil {
		t.Fatalf("妥当なpairが拒否されました: %v", err)
	}
}

func TestValidatePairRejectsInvalid(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(spec *Spec, direct *RunRecord, orchestrated *RunRecord)
		want   string
	}{
		{"spec version", func(spec *Spec, _ *RunRecord, _ *RunRecord) { spec.Version = 0 }, "spec version"},
		{"spec empty id", func(spec *Spec, _ *RunRecord, _ *RunRecord) { spec.ID = "" }, "必須fieldが空"},
		{"spec short commit", func(spec *Spec, d *RunRecord, o *RunRecord) {
			spec.RepoSnapshotCommit = "104a131"
			d.RunConditions.RepoSnapshotCommit = spec.RepoSnapshotCommit
			o.RunConditions.RepoSnapshotCommit = spec.RepoSnapshotCommit
		}, "git object hash"},
		{"spec uppercase commit", func(spec *Spec, d *RunRecord, o *RunRecord) {
			spec.RepoSnapshotCommit = strings.ToUpper(spec.RepoSnapshotCommit)
			d.RunConditions.RepoSnapshotCommit = spec.RepoSnapshotCommit
			o.RunConditions.RepoSnapshotCommit = spec.RepoSnapshotCommit
		}, "git object hash"},
		{"spec boundary drift", func(spec *Spec, _ *RunRecord, _ *RunRecord) {
			spec.MeasurementBoundary = "worker開始からworker完了まで"
		}, "measurement_boundary"},
		{"spec isolation session off", func(spec *Spec, _ *RunRecord, _ *RunRecord) {
			spec.Isolation.IndependentSession = false
		}, "独立session"},
		{"spec isolation cache note empty", func(spec *Spec, _ *RunRecord, _ *RunRecord) {
			spec.Isolation.CacheAvoidance = ""
		}, "cache_avoidance"},
		{"record spec hash mismatch", func(_ *Spec, d *RunRecord, _ *RunRecord) {
			d.SpecSHA256 = "deadbeef"
		}, "spec_sha256"},
		{"record run conditions drift", func(spec *Spec, _ *RunRecord, o *RunRecord) {
			o.RunConditions.CodexReasoningEffort = "medium"
		}, "run_conditions"},
		{"record relative worktree", func(_ *Spec, d *RunRecord, _ *RunRecord) {
			d.WorktreePath = "relative/worktree"
		}, "絶対path"},
		{"record empty session", func(_ *Spec, d *RunRecord, _ *RunRecord) {
			d.SessionID = ""
		}, "session_id"},
		{"record boundary not started", func(_ *Spec, d *RunRecord, _ *RunRecord) {
			d.Boundary.StartedAt = time.Time{}
		}, "時刻が未設定"},
		{"record boundary reversed", func(_ *Spec, d *RunRecord, _ *RunRecord) {
			d.Boundary.CompletedAt = d.Boundary.StartedAt
		}, "以降"},
		{"record codex usage estimated without source", func(_ *Spec, d *RunRecord, _ *RunRecord) {
			d.CodexUsage.Source = ""
			d.CodexUsage.InputTokens = 1000
		}, "sourceなしにtoken値"},
		{"record codex usage unknown source", func(_ *Spec, d *RunRecord, _ *RunRecord) {
			d.CodexUsage.Source = "codex-cli-guess"
		}, "codex_usage.sourceは"},
		{"record negative codex usage tokens", func(_ *Spec, d *RunRecord, _ *RunRecord) {
			d.CodexUsage.OutputTokens = -1
		}, "codex_usageのtoken値が負"},
		{"record negative glm usage tokens", func(_ *Spec, _ *RunRecord, o *RunRecord) {
			o.GLMUsage.OutputTokens = -1
		}, "glm_usageの値が負"},
		{"record negative glm model calls", func(_ *Spec, _ *RunRecord, o *RunRecord) {
			o.GLMUsage.ModelCalls = -1
		}, "glm_usageの値が負"},
		{"record hidden verification invalid", func(_ *Spec, _ *RunRecord, o *RunRecord) {
			o.Quality.HiddenVerification = "ok"
		}, "hidden_verification"},
		{"record test failures exceed run", func(_ *Spec, d *RunRecord, _ *RunRecord) {
			d.Quality.TestFailures = d.Quality.TestsRun + 1
		}, "tests_runを超えて"},
		{"record negative escaped bugs", func(_ *Spec, _ *RunRecord, o *RunRecord) {
			o.Quality.EscapedBugs = -1
		}, "escaped_bugs"},
		{"direct record with glm usage", func(_ *Spec, d *RunRecord, _ *RunRecord) {
			d.GLMUsage = GLMUsage{Source: GLMUsageSourceTaskStats, TaskID: "task-direct", ModelCalls: 1}
		}, "glm_usageを持ちません"},
		{"orchestrated record without glm source", func(_ *Spec, _ *RunRecord, o *RunRecord) {
			o.GLMUsage = GLMUsage{}
		}, "glm-worker-task-statsのみ受理"},
		{"orchestrated record transcribed glm source", func(_ *Spec, _ *RunRecord, o *RunRecord) {
			o.GLMUsage.Source = "transcribed-telemetry"
		}, "glm-worker-task-statsのみ受理"},
		{"orchestrated record task stats without task id", func(_ *Spec, _ *RunRecord, o *RunRecord) {
			o.GLMUsage = GLMUsage{Source: GLMUsageSourceTaskStats}
		}, "task_idが必要"},
		{"pair same session", func(_ *Spec, d *RunRecord, o *RunRecord) {
			o.SessionID = d.SessionID
		}, "同一session"},
		{"pair same worktree", func(_ *Spec, d *RunRecord, o *RunRecord) {
			o.WorktreePath = d.WorktreePath
		}, "同一working tree"},
		{"pair modes swapped", func(_ *Spec, d *RunRecord, _ *RunRecord) {
			d.Mode = ModeOrchestrated
		}, "mode directである必要"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			spec := validSpec()
			direct := validDirectRecord(spec)
			orchestrated := validOrchestratedRecord(spec)
			test.mutate(&spec, &direct, &orchestrated)
			err := ValidatePair(spec, direct, orchestrated)
			if err == nil {
				t.Fatal("契約違反が受理されました")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %q want substring %q", err.Error(), test.want)
			}
		})
	}
}

func TestSpecSHA256DistinguishesSpecRevision(t *testing.T) {
	first := validSpec()
	second := validSpec()
	second.CompletionConditions = first.CompletionConditions + "; 追加条件"
	if SpecSHA256(first) == SpecSHA256(second) {
		t.Fatal("spec内容が異なるのにhashが一致しました")
	}
}

func TestLoadPairRejectsUnknownModeAndVersion(t *testing.T) {
	spec := validSpec()
	direct := validDirectRecord(spec)
	direct.Mode = "hybrid"
	if _, err := LoadRecord(writeRecordJSON(t, "direct.json", direct)); err == nil {
		t.Fatal("未知のmodeが受理されました")
	}
	orchestrated := validOrchestratedRecord(spec)
	orchestrated.Version = 2
	if _, err := LoadRecord(writeRecordJSON(t, "orchestrated.json", orchestrated)); err == nil {
		t.Fatal("未知のversionが受理されました")
	}
}

func marshalOrFatal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeRunDirRaw(t *testing.T, specJSON, directJSON, orchestratedJSON []byte) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string][]byte{
		"spec.json":         specJSON,
		"direct.json":       directJSON,
		"orchestrated.json": orchestratedJSON,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestLoadPairRejectsUnknownFieldsAndTrailingJSONはstrict decode契約の検証。
// 未知field(field typo)と末尾の第2JSON値を黙って捨てずfail closedにする。
func TestLoadPairRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	spec := validSpec()
	specJSON := marshalOrFatal(t, spec)
	directJSON := marshalOrFatal(t, validDirectRecord(spec))
	orchestratedJSON := marshalOrFatal(t, validOrchestratedRecord(spec))

	withTypo := []byte(strings.Replace(string(specJSON), "{", `{"typo_field":1,`, 1))
	if _, _, _, err := LoadPair(writeRunDirRaw(t, withTypo, directJSON, orchestratedJSON)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("specの未知fieldが拒否されていません: %v", err)
	}

	directWithSecondValue := append(append([]byte{}, directJSON...), []byte(` {"second":"value"}`)...)
	if _, _, _, err := LoadPair(writeRunDirRaw(t, specJSON, directWithSecondValue, orchestratedJSON)); err == nil || !strings.Contains(err.Error(), "複数のJSON値") {
		t.Fatalf("末尾の第2JSON値が拒否されていません: %v", err)
	}

	orchestratedWithGarbage := append(append([]byte{}, orchestratedJSON...), []byte(" not-json")...)
	if _, _, _, err := LoadPair(writeRunDirRaw(t, specJSON, directJSON, orchestratedWithGarbage)); err == nil || !strings.Contains(err.Error(), "複数のJSON値") {
		t.Fatalf("末尾の不正JSONが拒否されていません: %v", err)
	}

	if _, err := LoadSpec(writeRunDirRaw(t, withTypo, directJSON, orchestratedJSON) + "/spec.json"); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("LoadSpecが未知fieldを拒否していません: %v", err)
	}
}
