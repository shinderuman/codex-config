package workflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/packet"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/runner"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

type scenarioStep struct {
	Lines []string `json:"lines"`
	Error string   `json:"error"`
	// Signalは出力fileへ書くprovider障害signal本文。packet行とは共存しない。
	Signal string `json:"signal,omitempty"`
	// Rawはpacket品質gate違反を再現する生の出力本文。packet.Validateを通らないmarker構造をscenarioへ入力する。
	Raw string `json:"raw,omitempty"`
	// Usage/CostUSDは成功stepのRunResult観測値(token/cost telemetry検証用)。
	// signal/error stepでは未観測(0)のままにする。
	Usage   scenarioUsage `json:"usage,omitempty"`
	CostUSD float64       `json:"cost_usd,omitempty"`
}

type scenarioUsage struct {
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
}

// scenarioStatsはTask Work CallとProvider Probe Callの呼出数が重複・欠落なく数えられる
// ことを固定する期待値。TotalAICalls = ModelCalls + probe成功/失敗の合計。
type scenarioStats struct {
	ModelCalls       int `json:"model_calls"`
	WorkerCalls      int `json:"worker_calls"`
	ReviewerCalls    int `json:"reviewer_calls"`
	TransientRetries int `json:"transient_retries"`
	ResumeCommands   int `json:"resume_commands"`
	ProbeSuccess     int `json:"probe_success"`
	ProbeFailure     int `json:"probe_failure"`
	TotalAICalls     int `json:"total_ai_calls"`
}

// scenarioTelemetryは呼出種別別のJSONL記録数とtoken/cost期待値。種別はcall_typeで
// 判別し、task集計へprobeが混ざらないことを呼出種別別の和で検証する。
type scenarioTelemetry struct {
	TaskCalls          int     `json:"task_calls"`
	ProbeCalls         int     `json:"probe_calls"`
	EventCalls         int     `json:"event_calls"`
	TaskInputTokens    int64   `json:"task_input_tokens"`
	TaskOutputTokens   int64   `json:"task_output_tokens"`
	TaskCostUSD        float64 `json:"task_cost_usd"`
	ProbeInputTokens   int64   `json:"probe_input_tokens"`
	ProbeOutputTokens  int64   `json:"probe_output_tokens"`
	ProbeCostUSD       float64 `json:"probe_cost_usd"`
	ProbeResolvedModel string  `json:"probe_resolved_model"`
}

// promptExpectationはscripted終端へ至るまでのproduction prompt選択(dispatch因果)を直接検証する
// 期待値。予約markerによるprompt分岐など、終端期待だけでは観測できない選択を固定する。
type promptExpectation struct {
	Index       int      `json:"index"`
	Contains    []string `json:"contains"`
	NotContains []string `json:"not_contains,omitempty"`
}

type scenarioDoc struct {
	ID                   string         `json:"id"`
	Behavior             string         `json:"behavior"`
	Request              string         `json:"request"`
	Entry                string         `json:"entry"`
	InstructionFiles     []string       `json:"instruction_files"`
	ChangedPaths         []string       `json:"changed_paths"`
	RunnerSteps          []scenarioStep `json:"runner_steps"`
	ExpectedModels       []string       `json:"expected_models"`
	ExpectedPacketStatus string         `json:"expected_packet_status"`
	ExpectedPacketRisk   string         `json:"expected_packet_risk"`
	ExpectedTaskStatus   string         `json:"expected_task_status"`
	MustNotPass          bool           `json:"must_not_pass"`
	// ExpectedPromptsはprompt呼出indexごとの期待内容。FIX_REQUIREDのTARGETS予約値が
	// production dispatchで専用promptを選んだ因果を終端期待と独立に検証する。
	ExpectedPrompts []promptExpectation `json:"expected_prompts,omitempty"`
	// ExpectedErrorStatusはerror terminal終端scenarioの期待STATUS。設定時はpacket終端を検証しない。
	ExpectedErrorStatus string `json:"expected_error_status,omitempty"`
	// ExpectedProbeCountはprobe呼出の期待回数。
	ExpectedProbeCount int `json:"expected_probe_count,omitempty"`
	// ExpectedRunCountは本task Run呼出の期待回数。未設定時はrunner_steps通り検証する。
	ExpectedRunCount *int `json:"expected_run_count,omitempty"`
	// ForbiddenErrorStatusはerror terminalの誤分類検出用の排他STATUS。
	ForbiddenErrorStatus string `json:"forbidden_error_status,omitempty"`
	// ProbeErrorsはprobe呼出へ順に返すerror本文。空要素は成功probeを表す。
	ProbeErrors []string `json:"probe_errors,omitempty"`
	// ProbeBlankは成功probeが空応答を返す偽陽性を再現する。
	ProbeBlank bool `json:"probe_blank,omitempty"`
	// ProbeResponsesはexit 0の成功probeがsentinel以外の本文を返す偽陽性を再現する。
	ProbeResponses []string `json:"probe_responses,omitempty"`
	// ProbeIsErrorはexit 0でもis_error=trueを返す偽陽性probeを再現する。
	ProbeIsError bool `json:"probe_is_error,omitempty"`
	// SleepAdvanceMinutesは各backoff待機後にfake clockを進める分数。
	// backoff schedule合計では到達しないhard deadline経路など、時間経過を要するscenarioだけが設定する。
	SleepAdvanceMinutes int `json:"sleep_advance_minutes,omitempty"`
	// ReviewerMutatesWorktreeはreviewerがBash相当でrepositoryを変更するscenarioで有効化する。
	ReviewerMutatesWorktree bool `json:"reviewer_mutates_worktree,omitempty"`
	// ExpectedStatsは終端後のtask/probe呼出数集計の期待値。
	ExpectedStats *scenarioStats `json:"expected_stats,omitempty"`
	// ExpectedTelemetryは終端後のJSONL telemetryの種別別記録数とtoken/costの期待値。
	ExpectedTelemetry *scenarioTelemetry `json:"expected_telemetry,omitempty"`
	// ExpectedCheckpointは終端時のresume checkpoint状態(none/rate-limited/provider-unavailable)。
	ExpectedCheckpoint string `json:"expected_checkpoint,omitempty"`
	// ExpectedProviderClassificationはprovider-unavailable停止時のcheckpoint分類期待値。
	ExpectedProviderClassification string `json:"expected_provider_classification,omitempty"`
}

type scenarioFile struct {
	Version   int           `json:"version"`
	Scenarios []scenarioDoc `json:"scenarios"`
}

type manifestEntry struct {
	Path      string   `json:"path"`
	SHA256    string   `json:"sha256"`
	Scenarios []string `json:"scenarios"`
}

type manifestFile struct {
	Version          int             `json:"version"`
	InstructionFiles []manifestEntry `json:"instruction_files"`
}

func scenarioRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join("glm-worker", "scenarios", "scenarios.json")
	for d := dir; d != string(filepath.Separator); d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, marker)); err == nil {
			return d
		}
	}
	t.Fatalf("scenario corpus root not found from %s", dir)
	return ""
}

func loadCorpus(t *testing.T) (scenarioFile, manifestFile) {
	t.Helper()
	base := filepath.Join(scenarioRepoRoot(t), "glm-worker", "scenarios")

	var sc scenarioFile
	scenarioBytes, err := os.ReadFile(filepath.Join(base, "scenarios.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(scenarioBytes, &sc); err != nil {
		t.Fatalf("scenarios.json parse: %v", err)
	}

	var mf manifestFile
	manifestBytes, err := os.ReadFile(filepath.Join(base, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifestBytes, &mf); err != nil {
		t.Fatalf("manifest.json parse: %v", err)
	}
	return sc, mf
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func validateCorpus(sc scenarioFile, mf manifestFile) error {
	if sc.Version != 1 {
		return fmt.Errorf("corpus version must be 1: got %d", sc.Version)
	}
	if mf.Version != 1 {
		return fmt.Errorf("manifest version must be 1: got %d", mf.Version)
	}

	knownEntry := map[string]bool{"new_task": true, "resume": true}
	knownStatus := map[string]bool{"IMPLEMENTED": true, "PASS": true, "FIX_REQUIRED": true, "NEEDS_SOL_DECISION": true, "NEEDS_SOL_REVIEW": true}
	knownError := map[string]bool{"PROVIDER_UNAVAILABLE": true, "RATE_LIMITED": true, "WORKER_ERROR": true}
	knownRisk := map[string]bool{"LOW": true, "HIGH": true}
	knownTask := map[string]bool{"active": true, "waiting-decision": true, "waiting-sol-review": true, "complete": true, "rate-limited": true, "provider-unavailable": true}

	seenID := make(map[string]bool, len(sc.Scenarios))
	for _, s := range sc.Scenarios {
		if s.ID == "" {
			return errors.New("scenario ID empty")
		}
		if seenID[s.ID] {
			return fmt.Errorf("duplicate scenario ID %q", s.ID)
		}
		seenID[s.ID] = true
		if s.Behavior == "" {
			return fmt.Errorf("scenario %s behavior empty", s.ID)
		}
		if s.Request == "" {
			return fmt.Errorf("scenario %s request empty", s.ID)
		}
		if !knownEntry[s.Entry] {
			return fmt.Errorf("scenario %s unknown entry %q", s.ID, s.Entry)
		}
		if len(s.InstructionFiles) == 0 {
			return fmt.Errorf("scenario %s instruction_files empty", s.ID)
		}
		if len(s.RunnerSteps) == 0 {
			return fmt.Errorf("scenario %s runner_steps empty", s.ID)
		}
		if len(s.ExpectedModels) == 0 {
			return fmt.Errorf("scenario %s expected_models empty", s.ID)
		}
		if !knownStatus[s.ExpectedPacketStatus] {
			return fmt.Errorf("scenario %s unknown expected packet status %q", s.ID, s.ExpectedPacketStatus)
		}
		if !knownRisk[s.ExpectedPacketRisk] {
			return fmt.Errorf("scenario %s unknown expected packet risk %q", s.ID, s.ExpectedPacketRisk)
		}
		if !knownTask[s.ExpectedTaskStatus] {
			return fmt.Errorf("scenario %s unknown expected task status %q", s.ID, s.ExpectedTaskStatus)
		}
		if s.MustNotPass && s.ExpectedPacketStatus == "PASS" {
			return fmt.Errorf("scenario %s must_not_pass with expected PASS", s.ID)
		}
		if s.ExpectedErrorStatus != "" {
			if !knownError[s.ExpectedErrorStatus] {
				return fmt.Errorf("scenario %s unknown expected error status %q", s.ID, s.ExpectedErrorStatus)
			}
			if s.ForbiddenErrorStatus != "" && s.ForbiddenErrorStatus == s.ExpectedErrorStatus {
				return fmt.Errorf("scenario %s forbidden error status equals expected", s.ID)
			}
		}
		if s.ForbiddenErrorStatus != "" && !knownError[s.ForbiddenErrorStatus] {
			return fmt.Errorf("scenario %s unknown forbidden error status %q", s.ID, s.ForbiddenErrorStatus)
		}
		if s.Entry == "resume" && s.ExpectedErrorStatus == "" && s.RunnerSteps[len(s.RunnerSteps)-1].Error == "" && s.RunnerSteps[len(s.RunnerSteps)-1].Signal == "" && s.RunnerSteps[len(s.RunnerSteps)-1].Raw == "" && len(s.RunnerSteps[len(s.RunnerSteps)-1].Lines) == 0 {
			return fmt.Errorf("scenario %s empty terminal step", s.ID)
		}
		knownCheckpoint := map[string]bool{"": true, "none": true, "rate-limited": true, "provider-unavailable": true}
		if !knownCheckpoint[s.ExpectedCheckpoint] {
			return fmt.Errorf("scenario %s unknown expected checkpoint %q", s.ID, s.ExpectedCheckpoint)
		}
		if s.SleepAdvanceMinutes < 0 {
			return fmt.Errorf("scenario %s negative sleep_advance_minutes", s.ID)
		}
		if s.ExpectedProviderClassification != "" && s.ExpectedCheckpoint != "provider-unavailable" {
			return fmt.Errorf("scenario %s expected_provider_classification without provider-unavailable checkpoint", s.ID)
		}
		if es := s.ExpectedStats; es != nil {
			if es.WorkerCalls+es.ReviewerCalls != es.ModelCalls {
				return fmt.Errorf("scenario %s worker+reviewer calls != model calls", s.ID)
			}
			if es.ModelCalls+es.ProbeSuccess+es.ProbeFailure != es.TotalAICalls {
				return fmt.Errorf("scenario %s total_ai_calls != model+probe calls", s.ID)
			}
		}
		if et := s.ExpectedTelemetry; et != nil && s.ExpectedStats != nil {
			if et.TaskCalls != s.ExpectedStats.ModelCalls || et.ProbeCalls != s.ExpectedStats.ProbeSuccess+s.ExpectedStats.ProbeFailure {
				return fmt.Errorf("scenario %s telemetry call counts disagree with expected_stats", s.ID)
			}
		}
		if len(s.RunnerSteps) != len(s.ExpectedModels) {
			return fmt.Errorf("scenario %s runner_steps/expected_models count mismatch: %d vs %d", s.ID, len(s.RunnerSteps), len(s.ExpectedModels))
		}
		for i, step := range s.RunnerSteps {
			hasLines := len(step.Lines) > 0
			hasErr := step.Error != ""
			hasSignal := step.Signal != ""
			hasRaw := step.Raw != ""
			kinds := 0
			for _, present := range []bool{hasLines, hasErr, hasSignal, hasRaw} {
				if present {
					kinds++
				}
			}
			if kinds == 0 {
				return fmt.Errorf("scenario %s step %d empty", s.ID, i)
			}
			if kinds > 1 {
				return fmt.Errorf("scenario %s step %d has multiple terminal kinds", s.ID, i)
			}
			if hasLines {
				if err := packet.Validate(packet.FromLines(step.Lines)); err != nil {
					return fmt.Errorf("scenario %s step %d invalid packet: %w", s.ID, i, err)
				}
			}
		}
		for i, exp := range s.ExpectedPrompts {
			if exp.Index < 0 {
				return fmt.Errorf("scenario %s expected_prompts %d negative index", s.ID, i)
			}
			if len(exp.Contains) == 0 {
				return fmt.Errorf("scenario %s expected_prompts %d contains empty", s.ID, i)
			}
			for _, want := range exp.Contains {
				if want == "" {
					return fmt.Errorf("scenario %s expected_prompts %d empty contains entry", s.ID, i)
				}
			}
			for _, forbidden := range exp.NotContains {
				if forbidden == "" {
					return fmt.Errorf("scenario %s expected_prompts %d empty not_contains entry", s.ID, i)
				}
			}
		}
	}

	manifestPaths := make(map[string]manifestEntry, len(mf.InstructionFiles))
	for _, e := range mf.InstructionFiles {
		if e.Path == "" {
			return errors.New("manifest path empty")
		}
		if _, dup := manifestPaths[e.Path]; dup {
			return fmt.Errorf("duplicate manifest path %q", e.Path)
		}
		if e.SHA256 == "" {
			return fmt.Errorf("manifest %s sha256 empty", e.Path)
		}
		seenScenario := make(map[string]bool, len(e.Scenarios))
		for _, sid := range e.Scenarios {
			if sid == "" {
				return fmt.Errorf("manifest %s empty scenario ref", e.Path)
			}
			if seenScenario[sid] {
				return fmt.Errorf("manifest %s duplicate scenario %q", e.Path, sid)
			}
			seenScenario[sid] = true
			if !seenID[sid] {
				return fmt.Errorf("manifest %s references unknown scenario %q", e.Path, sid)
			}
		}
		manifestPaths[e.Path] = e
	}

	for _, s := range sc.Scenarios {
		seenFile := make(map[string]bool, len(s.InstructionFiles))
		for _, f := range s.InstructionFiles {
			if f == "" {
				return fmt.Errorf("scenario %s empty instruction_files entry", s.ID)
			}
			if seenFile[f] {
				return fmt.Errorf("scenario %s duplicate instruction_files %q", s.ID, f)
			}
			seenFile[f] = true
			entry, ok := manifestPaths[f]
			if !ok {
				return fmt.Errorf("scenario %s grounds on %s not pinned in manifest", s.ID, f)
			}
			listed := false
			for _, sid := range entry.Scenarios {
				if sid == s.ID {
					listed = true
					break
				}
			}
			if !listed {
				return fmt.Errorf("scenario %s grounds on %s but not listed by manifest", s.ID, f)
			}
		}
	}

	scenarioFiles := make(map[string]map[string]bool, len(sc.Scenarios))
	for _, s := range sc.Scenarios {
		set := make(map[string]bool, len(s.InstructionFiles))
		for _, f := range s.InstructionFiles {
			set[f] = true
		}
		scenarioFiles[s.ID] = set
	}
	for _, e := range mf.InstructionFiles {
		for _, sid := range e.Scenarios {
			if !scenarioFiles[sid][e.Path] {
				return fmt.Errorf("manifest %s lists scenario %q that does not declare path in instruction_files", e.Path, sid)
			}
		}
	}
	return nil
}

func TestScenarioCorpusContract(t *testing.T) {
	sc, mf := loadCorpus(t)
	if err := validateCorpus(sc, mf); err != nil {
		t.Fatalf("corpus contract violation: %v", err)
	}
	// hash pinはmanaged instruction fileの変更を検知して人手でscenario期待結果を再確認させるgateであり、
	// 自然言語の意味適合を自動証明するものではない。modelが規則へ従うかは後続固定Evalの対象。
	root := scenarioRepoRoot(t)
	for _, entry := range mf.InstructionFiles {
		actual := sha256File(t, filepath.Join(root, entry.Path))
		if actual != entry.SHA256 {
			t.Errorf("instruction file %s changed: expected %s got %s; re-confirm scenarios %v", entry.Path, entry.SHA256, actual, entry.Scenarios)
		}
	}
}

func validCorpus() (scenarioFile, manifestFile) {
	sc := scenarioFile{
		Version: 1,
		Scenarios: []scenarioDoc{{
			ID:               "s1",
			Behavior:         "b",
			Request:          "req",
			Entry:            "new_task",
			InstructionFiles: []string{"codex/glm-worker/prompts/WORKER.md"},
			RunnerSteps: []scenarioStep{
				{Lines: []string{"STATUS: IMPLEMENTED", "RISK: LOW", "SUMMARY: s", "REQUIREMENT_COVERAGE: c", "TESTS: t", "UNVERIFIED: none", "ARTIFACTS: none"}},
				{Lines: []string{"STATUS: PASS", "RISK: LOW", "SUMMARY: s", "REQUIREMENT_COVERAGE: c", "INVARIANTS: i", "TEST_EVIDENCE: e", "ISSUES: none", "RESIDUAL_RISK: none", "TARGETS: none", "ARTIFACTS: none"}},
			},
			ExpectedModels:       []string{"opus", "haiku"},
			ExpectedPacketStatus: "PASS",
			ExpectedPacketRisk:   "LOW",
			ExpectedTaskStatus:   "complete",
			MustNotPass:          false,
		}},
	}
	mf := manifestFile{
		Version: 1,
		InstructionFiles: []manifestEntry{{
			Path:      "codex/glm-worker/prompts/WORKER.md",
			SHA256:    "deadbeef",
			Scenarios: []string{"s1"},
		}},
	}
	return sc, mf
}

func TestScenarioCorpusContractRejectsInvalid(t *testing.T) {
	sc, mf := validCorpus()
	if err := validateCorpus(sc, mf); err != nil {
		t.Fatalf("baseline corpus must be valid: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(sc *scenarioFile, mf *manifestFile)
		want   string
	}{
		{"corpus version", func(sc *scenarioFile, _ *manifestFile) { sc.Version = 0 }, "corpus version"},
		{"manifest version", func(_ *scenarioFile, mf *manifestFile) { mf.Version = 2 }, "manifest version"},
		{"empty scenario ID", func(sc *scenarioFile, _ *manifestFile) { sc.Scenarios[0].ID = "" }, "scenario ID empty"},
		{"duplicate scenario ID", func(sc *scenarioFile, _ *manifestFile) {
			dup := sc.Scenarios[0]
			dup.ID = "s1"
			sc.Scenarios = append(sc.Scenarios, dup)
		}, "duplicate scenario ID"},
		{"empty behavior", func(sc *scenarioFile, _ *manifestFile) { sc.Scenarios[0].Behavior = "" }, "behavior empty"},
		{"empty request", func(sc *scenarioFile, _ *manifestFile) { sc.Scenarios[0].Request = "" }, "request empty"},
		{"unknown entry", func(sc *scenarioFile, _ *manifestFile) { sc.Scenarios[0].Entry = "decision" }, "unknown entry"},
		{"empty instruction_files", func(sc *scenarioFile, _ *manifestFile) { sc.Scenarios[0].InstructionFiles = nil }, "instruction_files empty"},
		{"empty runner_steps", func(sc *scenarioFile, _ *manifestFile) { sc.Scenarios[0].RunnerSteps = nil }, "runner_steps empty"},
		{"empty expected_models", func(sc *scenarioFile, _ *manifestFile) { sc.Scenarios[0].ExpectedModels = nil }, "expected_models empty"},
		{"unknown expected packet status", func(sc *scenarioFile, _ *manifestFile) { sc.Scenarios[0].ExpectedPacketStatus = "DONE" }, "expected packet status"},
		{"unknown expected packet risk", func(sc *scenarioFile, _ *manifestFile) { sc.Scenarios[0].ExpectedPacketRisk = "MEDIUM" }, "expected packet risk"},
		{"unknown expected task status", func(sc *scenarioFile, _ *manifestFile) { sc.Scenarios[0].ExpectedTaskStatus = "done" }, "expected task status"},
		{"must_not_pass with expected PASS", func(sc *scenarioFile, _ *manifestFile) { sc.Scenarios[0].MustNotPass = true }, "must_not_pass"},
		{"step count mismatch", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ExpectedModels = append(sc.Scenarios[0].ExpectedModels, "sonnet")
		}, "count mismatch"},
		{"empty step", func(sc *scenarioFile, _ *manifestFile) { sc.Scenarios[0].RunnerSteps[0] = scenarioStep{} }, "step 0 empty"},
		{"step both packet and error", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].RunnerSteps[0].Error = "boom"
		}, "multiple terminal kinds"},
		{"step both packet and raw", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].RunnerSteps[0].Raw = "PACKET_BEGIN\nSTATUS: IMPLEMENTED\nPACKET_END\n"
		}, "multiple terminal kinds"},
		{"step invalid packet", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].RunnerSteps[0] = scenarioStep{Lines: []string{"STATUS: PASS", "RISK: LOW", "SUMMARY: s"}}
		}, "invalid packet"},
		{"duplicate manifest path", func(_ *scenarioFile, mf *manifestFile) {
			mf.InstructionFiles = append(mf.InstructionFiles, manifestEntry{Path: "codex/glm-worker/prompts/WORKER.md", SHA256: "x", Scenarios: []string{"s1"}})
		}, "duplicate manifest path"},
		{"manifest empty sha256", func(_ *scenarioFile, mf *manifestFile) { mf.InstructionFiles[0].SHA256 = "" }, "sha256 empty"},
		{"manifest duplicate scenario ref", func(_ *scenarioFile, mf *manifestFile) {
			mf.InstructionFiles[0].Scenarios = []string{"s1", "s1"}
		}, "duplicate scenario"},
		{"manifest unknown scenario ref", func(_ *scenarioFile, mf *manifestFile) {
			mf.InstructionFiles[0].Scenarios = []string{"nope"}
		}, "unknown scenario"},
		{"scenario grounds on unpinned file", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].InstructionFiles = []string{"codex/instructions/worker/go.md"}
		}, "not pinned in manifest"},
		{"scenario not listed by manifest", func(sc *scenarioFile, mf *manifestFile) {
			second := sc.Scenarios[0]
			second.ID = "s2"
			sc.Scenarios = append(sc.Scenarios, second)
			mf.InstructionFiles[0].Scenarios = []string{"s2"}
		}, "not listed by manifest"},
		{"manifest lists scenario missing path", func(_ *scenarioFile, mf *manifestFile) {
			mf.InstructionFiles = append(mf.InstructionFiles, manifestEntry{Path: "codex/glm-worker/prompts/REVIEWER.md", SHA256: "x", Scenarios: []string{"s1"}})
		}, "does not declare path"},
		{"unknown expected checkpoint", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ExpectedCheckpoint = "waiting"
		}, "unknown expected checkpoint"},
		{"negative sleep advance", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].SleepAdvanceMinutes = -1
		}, "negative sleep_advance_minutes"},
		{"classification without unavailable checkpoint", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ExpectedProviderClassification = "probe-contract"
		}, "without provider-unavailable checkpoint"},
		{"stats role counts disagree", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ExpectedStats = &scenarioStats{ModelCalls: 2, WorkerCalls: 2, ReviewerCalls: 1, TotalAICalls: 5, ProbeSuccess: 1, ProbeFailure: 2}
		}, "worker+reviewer calls != model calls"},
		{"stats total ai calls disagree", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ExpectedStats = &scenarioStats{ModelCalls: 2, WorkerCalls: 1, ReviewerCalls: 1, ProbeSuccess: 1, TotalAICalls: 5}
		}, "total_ai_calls != model+probe calls"},
		{"telemetry counts disagree with stats", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ExpectedStats = &scenarioStats{ModelCalls: 2, WorkerCalls: 1, ReviewerCalls: 1, TotalAICalls: 2}
			sc.Scenarios[0].ExpectedTelemetry = &scenarioTelemetry{TaskCalls: 3, ProbeCalls: 0}
		}, "telemetry call counts disagree"},
		{"expected prompts negative index", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ExpectedPrompts = []promptExpectation{{Index: -1, Contains: []string{"MODE:"}}}
		}, "negative index"},
		{"expected prompts empty contains", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ExpectedPrompts = []promptExpectation{{Index: 0}}
		}, "contains empty"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			sc, mf := validCorpus()
			c.mutate(&sc, &mf)
			err := validateCorpus(sc, mf)
			if err == nil {
				t.Fatal("expected contract violation, got nil")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %q, want substring %q", err.Error(), c.want)
			}
		})
	}
}

func stepsFromScenario(doc scenarioDoc) []runnerStep {
	steps := make([]runnerStep, len(doc.RunnerSteps))
	for i, s := range doc.RunnerSteps {
		output := "PACKET_BEGIN\n" + strings.Join(s.Lines, "\n") + "\nPACKET_END\n"
		var runErr error
		if s.Error != "" {
			runErr = errors.New(s.Error)
		}
		if s.Signal != "" {
			output = s.Signal
			runErr = errors.New("exit status 1")
		}
		if s.Raw != "" {
			output = s.Raw
		}
		result := runner.RunResult{
			TopLevelUsage: runner.TokenUsage{
				InputTokens:  s.Usage.InputTokens,
				OutputTokens: s.Usage.OutputTokens,
			},
			TotalCostUSD: s.CostUSD,
		}
		steps[i] = runnerStep{output: output, runErr: runErr, result: result}
	}
	return steps
}

func lastPacketFromOutput(t *testing.T, out string) packet.Packet {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	emitted := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			emitted = append(emitted, ln)
		}
	}
	if len(emitted) == 0 {
		t.Fatalf("no emitted packet in output:\n%s", out)
	}
	return packet.FromLines(emitted)
}

func runScenario(t *testing.T, doc scenarioDoc) {
	t.Helper()
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: stepsFromScenario(doc)}
	if doc.ProbeErrors != nil {
		r.probeErrs = make([]error, len(doc.ProbeErrors))
		for i, text := range doc.ProbeErrors {
			if text != "" {
				r.probeErrs[i] = errors.New(text)
			}
		}
	}
	r.probeBlankResponse = doc.ProbeBlank
	r.probeResponses = doc.ProbeResponses
	r.probeIsError = doc.ProbeIsError
	w := newWorkflowT(t, st, r)
	if doc.SleepAdvanceMinutes > 0 {
		// 各待機でscheduleどおりの時間を記録しつつ、clockだけ大きく進めてdeadline経路へ入れる。
		clock := newFakeClock()
		step := time.Duration(doc.SleepAdvanceMinutes) * time.Minute
		w.now = clock.nowFunc
		w.sleep = func(d time.Duration) {
			clock.sleeps = append(clock.sleeps, d)
			clock.now = clock.now.Add(step)
		}
	}
	buf := &bytes.Buffer{}
	w.output = buf
	var mutationRepoRoot string
	if doc.ReviewerMutatesWorktree {
		mutationRepoRoot = initMutationRepo(t)
		mr := &mutatingRunner{
			repoRoot: mutationRepoRoot,
			steps:    stepsFromScenario(doc),
			mutate: func(root string) error {
				return os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("mutated\n"), 0o644)
			},
		}
		w.runner = mr
		w.config.RepoRoot = mutationRepoRoot
		w.captureSnapshot = state.CaptureGitSnapshot
	}
	if doc.ChangedPaths != nil {
		changedPaths := doc.ChangedPaths
		w.collectChangedPaths = func(string, string) ([]string, error) { return changedPaths, nil }
	}

	var scenarioErr error
	switch doc.Entry {
	case "new_task":
		scenarioErr = w.ExecuteNewTask(doc.Request)
	case "resume":
		if err := st.Write("last-request", doc.Request); err != nil {
			t.Fatal(err)
		}
		// resume entryは常にprovider-unavailable停止状態から開始する。
		{
			if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
				Stage:                             state.ResumeStageWorker,
				Phase:                             "worker-new",
				Role:                              state.WorkerRole,
				Model:                             "opus",
				Effort:                            "high",
				Prompt:                            "p",
				OriginalPrompt:                    "p",
				Request:                           doc.Request,
				ProviderUnavailable:               true,
				ProviderUnavailableClassification: "http-503",
				ProviderUnavailableProbes:         4,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.SetTaskStatus(state.TaskStatusProviderUnavailable); err != nil {
				t.Fatal(err)
			}
		}
		scenarioErr = w.ExecuteResume()
	default:
		t.Fatalf("unsupported entry %q for scenario %s", doc.Entry, doc.ID)
	}

	if doc.ReviewerMutatesWorktree {
		mr := w.runner.(*mutatingRunner)
		r.prompts = mr.prompts
		r.models = mr.models
		if content, err := os.ReadFile(filepath.Join(mutationRepoRoot, "tracked.txt")); err != nil || string(content) != "mutated\n" {
			t.Fatalf("reviewer mutationが保持されていません: %q %v", content, err)
		}
	}
	if len(r.prompts) == 0 && doc.ExpectedProbeCount == 0 {
		t.Fatal("production runner was not invoked")
	}
	for i, p := range r.prompts {
		isReformat := strings.Contains(p, "PACKETだけを再出力")
		hasRequest := strings.Contains(p, doc.Request)
		hasMode := strings.Contains(p, "MODE:") || strings.Contains(p, "REVIEW_MODE:")
		// resume promptは保存済み前回指示の再送であり、seed時のrequest文言は含まない。
		isResume := strings.Contains(p, "再開してください")
		if !(hasMode || isReformat || isResume) {
			t.Fatalf("prompt %d is not a production-generated prompt:\n%s", i, p)
		}
		if !isReformat && !isResume && !hasRequest {
			t.Fatalf("prompt %d does not transmit USER_REQUEST:\n%s", i, p)
		}
	}
	if len(r.prompts) > 0 && !strings.Contains(r.prompts[0], "MODE:") && !strings.Contains(r.prompts[0], "再開してください") {
		t.Fatalf("worker prompt is not a production NEW_TASK/RESUME prompt:\n%s", r.prompts[0])
	}

	if doc.ExpectedRunCount != nil {
		if len(r.prompts) != *doc.ExpectedRunCount {
			t.Fatalf("本task Run回数 = %d want %d", len(r.prompts), *doc.ExpectedRunCount)
		}
	}
	if doc.ExpectedProbeCount > 0 {
		if len(r.probes) != doc.ExpectedProbeCount {
			t.Fatalf("probe回数 = %d want %d", len(r.probes), doc.ExpectedProbeCount)
		}
	}
	// expected_run_count設定時は呼出回数自体を検証済みのためmodel列は検証しない。
	if doc.ExpectedRunCount == nil {
		if got, want := strings.Join(r.models, ","), strings.Join(doc.ExpectedModels, ","); got != want {
			t.Fatalf("model routing = %q want %q", got, want)
		}
	}
	// expected_promptsはproduction dispatchがscripted packet列の予約値から期待promptを選んだ
	// 因果を直接検証する。終端STATUS/routing期待とは独立したgate。
	for _, exp := range doc.ExpectedPrompts {
		if exp.Index >= len(r.prompts) {
			t.Fatalf("expected_prompts index %d out of range: prompts=%d", exp.Index, len(r.prompts))
		}
		got := r.prompts[exp.Index]
		for _, want := range exp.Contains {
			if !strings.Contains(got, want) {
				t.Fatalf("prompt %d does not contain %q:\n%s", exp.Index, want, got)
			}
		}
		for _, forbidden := range exp.NotContains {
			if strings.Contains(got, forbidden) {
				t.Fatalf("prompt %d must not contain %q:\n%s", exp.Index, forbidden, got)
			}
		}
	}

	if doc.ExpectedErrorStatus != "" {
		if scenarioErr == nil {
			t.Fatalf("expected error terminal %s, got success with packet:\n%s", doc.ExpectedErrorStatus, buf.String())
		}
		if !strings.Contains(scenarioErr.Error(), "STATUS: "+doc.ExpectedErrorStatus) {
			t.Fatalf("error terminal = %q want STATUS: %s\n%s", scenarioErr.Error(), doc.ExpectedErrorStatus, scenarioErr)
		}
		if doc.ForbiddenErrorStatus != "" && strings.Contains(scenarioErr.Error(), "STATUS: "+doc.ForbiddenErrorStatus) {
			t.Fatalf("error terminal must not be %s:\n%s", doc.ForbiddenErrorStatus, scenarioErr)
		}
	} else {
		if scenarioErr != nil {
			t.Fatalf("scenario execution error: %v", scenarioErr)
		}
		pkt := lastPacketFromOutput(t, buf.String())
		if err := packet.Validate(pkt); err != nil {
			t.Fatalf("emitted packet fails production contract: %v", err)
		}
		if pkt.Status() != doc.ExpectedPacketStatus {
			t.Fatalf("packet status = %q want %q", pkt.Status(), doc.ExpectedPacketStatus)
		}
		if pkt.Risk() != doc.ExpectedPacketRisk {
			t.Fatalf("packet risk = %q want %q", pkt.Risk(), doc.ExpectedPacketRisk)
		}
		if doc.MustNotPass && pkt.Status() == "PASS" {
			t.Fatalf("must_not_pass scenario ended in PASS")
		}
	}
	if got := string(st.TaskStatus()); got != doc.ExpectedTaskStatus {
		t.Fatalf("task status = %q want %q", got, doc.ExpectedTaskStatus)
	}
	if doc.ExpectedCheckpoint != "" {
		cp, cpErr := st.LoadResumeCheckpoint()
		switch doc.ExpectedCheckpoint {
		case "none":
			if cpErr == nil {
				t.Fatalf("resume checkpoint = %#v want none", cp)
			}
		case "rate-limited":
			if cpErr != nil || !cp.RateLimited {
				t.Fatalf("rate-limited checkpointが保存されていない: %#v err=%v", cp, cpErr)
			}
		case "provider-unavailable":
			if cpErr != nil || !cp.ProviderUnavailable {
				t.Fatalf("provider-unavailable checkpointが保存されていない: %#v err=%v", cp, cpErr)
			}
			if doc.ExpectedProviderClassification != "" && cp.ProviderUnavailableClassification != doc.ExpectedProviderClassification {
				t.Fatalf("provider-unavailable分類 = %q want %q", cp.ProviderUnavailableClassification, doc.ExpectedProviderClassification)
			}
		}
	}
	if doc.ExpectedStats != nil {
		verifyScenarioStats(t, st, *doc.ExpectedStats)
	}
	if doc.ExpectedTelemetry != nil {
		verifyScenarioTelemetry(t, st, *doc.ExpectedTelemetry)
	}
}

// verifyScenarioStatsはtask/probe呼出数の加法整合性(total = task + probe)を検証する。
func verifyScenarioStats(t *testing.T, st *state.StateStore, want scenarioStats) {
	t.Helper()
	stats := currentStats(t, st)
	probeCalls := stats.ProbeOutcome["probe_success"] + stats.ProbeOutcome["probe_failure"]
	got := scenarioStats{
		ModelCalls:       stats.ModelCalls,
		WorkerCalls:      stats.WorkerCalls,
		ReviewerCalls:    stats.ReviewerCalls,
		TransientRetries: stats.TransientRetries,
		ResumeCommands:   stats.ResumeCommands,
		ProbeSuccess:     stats.ProbeOutcome["probe_success"],
		ProbeFailure:     stats.ProbeOutcome["probe_failure"],
		TotalAICalls:     stats.ModelCalls + probeCalls,
	}
	if got != want {
		t.Fatalf("stats = %+v want %+v", got, want)
	}
}

// verifyScenarioTelemetryはcall_type別のJSONL記録数とtoken/costを検証する。
// probeはtoken/cost/resolved modelをJSONLへ残し、task集計へ混ざらない。
func verifyScenarioTelemetry(t *testing.T, st *state.StateStore, want scenarioTelemetry) {
	t.Helper()
	got := scenarioTelemetry{}
	for _, l := range taskLogs(t, st) {
		switch l.CallType {
		case state.CallTypeTask:
			got.TaskCalls++
			got.TaskInputTokens += l.TopLevelUsage.InputTokens
			got.TaskOutputTokens += l.TopLevelUsage.OutputTokens
			got.TaskCostUSD += l.TotalCostUSD
		case state.CallTypeProbe:
			got.ProbeCalls++
			got.ProbeInputTokens += l.TopLevelUsage.InputTokens
			got.ProbeOutputTokens += l.TopLevelUsage.OutputTokens
			got.ProbeCostUSD += l.TotalCostUSD
			if want.ProbeResolvedModel != "" {
				usage, ok := l.ResolvedModelUsage[want.ProbeResolvedModel]
				if !ok || usage.OutputTokens <= 0 {
					t.Fatalf("probe recordのresolved model %qが記録されていない: %+v", want.ProbeResolvedModel, l.ResolvedModelUsage)
				}
			}
		case state.CallTypeEvent:
			got.EventCalls++
		}
	}
	if got.TaskCalls != want.TaskCalls || got.ProbeCalls != want.ProbeCalls || got.EventCalls != want.EventCalls {
		t.Fatalf("telemetry call counts = task/probe/event %d/%d/%d want %d/%d/%d",
			got.TaskCalls, got.ProbeCalls, got.EventCalls, want.TaskCalls, want.ProbeCalls, want.EventCalls)
	}
	if got.TaskInputTokens != want.TaskInputTokens || got.TaskOutputTokens != want.TaskOutputTokens ||
		got.ProbeInputTokens != want.ProbeInputTokens || got.ProbeOutputTokens != want.ProbeOutputTokens {
		t.Fatalf("telemetry tokens = %+v want %+v", got, want)
	}
	if got.TaskCostUSD != want.TaskCostUSD || got.ProbeCostUSD != want.ProbeCostUSD {
		t.Fatalf("telemetry cost = task/probe %v/%v want %v/%v", got.TaskCostUSD, got.ProbeCostUSD, want.TaskCostUSD, want.ProbeCostUSD)
	}
}

func TestScenarioCorpusDrivenThroughProductionGate(t *testing.T) {
	sc, mf := loadCorpus(t)
	if err := validateCorpus(sc, mf); err != nil {
		t.Fatalf("corpus contract violation: %v", err)
	}
	if len(sc.Scenarios) == 0 {
		t.Fatal("scenarios.json has no scenarios")
	}
	for _, doc := range sc.Scenarios {
		doc := doc
		t.Run(doc.ID, func(t *testing.T) {
			runScenario(t, doc)
		})
	}
}
