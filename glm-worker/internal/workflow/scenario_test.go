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
	// Rawは結果検証違反を再現する生の出力本文。corpus検証を通らない構造(未変換本文による
	// schema-mismatch再現)や意味違反(TARGETS: none等)をscenarioへ入力する。
	Raw string `json:"raw,omitempty"`
	// Usage/CostUSDはRunResult観測値(token/cost telemetry検証用)。実runnerはerror時も
	// 取得できた観測値を返すため、fatal終了stepにも設定して記録欠落を検証できる。
	// 未設定は未観測(0)を表す。
	Usage   scenarioUsage `json:"usage,omitempty"`
	CostUSD float64       `json:"cost_usd,omitempty"`
}

// scenarioArtifactはwrapperのartifact packet検証へ使うtask artifact保存file。
// scriptedRunnerがstep消費時にpacket行の{{ARTIFACT_DIR}}予約tokenを実artifact dirへ
// 置換し、宣言済みfileだけを保存・参照させる。
type scenarioArtifact struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

const scenarioArtifactDirToken = "{{ARTIFACT_DIR}}"

// scenarioPlanSeed/ scenarioPlanMutatedはplan file不変性破壊scenarioのseed(親Codexがcall前に
// 置いた内容の代役)とworker呼出中の書換内容。検証は内容の変化検出のみへ用いる。
const (
	scenarioPlanSeed    = "parent owned plan\n"
	scenarioPlanMutated = "glm edited plan\n"
)

// telemetryClockInjectedStartはscenario終端後の全telemetry record時刻が注入fake clockの
// 開始時刻と一致することを期待するexpected_telemetry_clock値。
const telemetryClockInjectedStart = "injected-start"

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
	// ReportOnlyMutatesWorktreeはreport-only PACKET再出力workerが期待packetを返しながら
	// 開始前後でrepositoryを変更するscenarioで有効化する。scripted packetの成功宣言と
	// 無関係にreport-only不変性のproduction検証へ失敗させるescaped review固定のhook。
	ReportOnlyMutatesWorktree bool `json:"report_only_mutates_worktree,omitempty"`
	// WorkerMutatesPlanFileはworkerがtask呼出中にIMPLEMENTATION_PLAN.local.mdへ書き込む
	// scenarioで有効化する。scripted packetの成功宣言と無関係にplan file不変性のproduction
	// 検証がreviewer開始前へfail closedする escaped review固定のhook。
	WorkerMutatesPlanFile bool `json:"worker_mutates_plan_file,omitempty"`
	// PlanFileInitiallyAbsentはWorkerMutatesPlanFile対象repoのplan fileを初期欠損にする。
	// workerによる新規作成(plan生成禁止契約)を再現する。
	PlanFileInitiallyAbsent bool `json:"plan_file_initially_absent,omitempty"`
	// PlanFileTrackedAbsentはGit indexがplan fileを追跡するのにworking treeへ欠損した
	// repoを再現する。追跡中canonical source欠損のmodel呼出前fail closedを固定する。
	PlanFileTrackedAbsent bool `json:"plan_file_tracked_absent,omitempty"`
	// ExpectedOutputContainsはwrapper最終出力へ含めるべき本文。終端STATUS期待だけでは
	// 観測できない「どのgateが発火したか」をfail closed理由文へ直接束ねる。
	ExpectedOutputContains []string `json:"expected_output_contains,omitempty"`
	// ExpectedStatsは終端後のtask/probe呼出数集計の期待値。
	ExpectedStats *scenarioStats `json:"expected_stats,omitempty"`
	// ExpectedTelemetryは終端後のJSONL telemetryの種別別記録数とtoken/costの期待値。
	ExpectedTelemetry *scenarioTelemetry `json:"expected_telemetry,omitempty"`
	// ExpectedTelemetryClockは終端後の全telemetry record時刻・durationが注入fake clockの
	// 開始時刻だけから導出されていることを検証する。production codeへwall clock取得が
	// 再導入されるとrecord時刻が実時間へ分岐し、scripted packet終端期待とは独立に失敗する。
	ExpectedTelemetryClock string `json:"expected_telemetry_clock,omitempty"`
	// ExpectedCheckpointは終端時のresume checkpoint状態(none/rate-limited/provider-unavailable)。
	ExpectedCheckpoint string `json:"expected_checkpoint,omitempty"`
	// ExpectedProviderClassificationはprovider-unavailable停止時のcheckpoint分類期待値。
	ExpectedProviderClassification string `json:"expected_provider_classification,omitempty"`
	// ArtifactFilesはscriptedRunnerがstep消費時に現在taskのartifact dirへ保存するfile。
	// packet行の{{ARTIFACT_DIR}}tokenと組み合わせ、ARTIFACTS参照の実在file検証を通る例を作る。
	ArtifactFiles []scenarioArtifact `json:"artifact_files,omitempty"`
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
		if s.ReviewerMutatesWorktree && s.ReportOnlyMutatesWorktree ||
			s.ReviewerMutatesWorktree && s.WorkerMutatesPlanFile ||
			s.ReportOnlyMutatesWorktree && s.WorkerMutatesPlanFile {
			return fmt.Errorf("scenario %s declares multiple mutation hooks", s.ID)
		}
		// 追跡中plan欠損scenarioはmutation無しでmodel呼出前に停止する。呼出中の変化を
		// 扱うhookや0呼出以外の終端と併存した場合、期待内容の再確認を強制する。
		if s.PlanFileTrackedAbsent {
			if s.WorkerMutatesPlanFile || s.ReviewerMutatesWorktree || s.ReportOnlyMutatesWorktree {
				return fmt.Errorf("scenario %s plan_file_tracked_absent with mutation hook", s.ID)
			}
			if s.ExpectedPacketStatus != "NEEDS_SOL_REVIEW" {
				return fmt.Errorf("scenario %s plan file tracked absent must expect fail-closed NEEDS_SOL_REVIEW", s.ID)
			}
			if s.ExpectedRunCount == nil || *s.ExpectedRunCount != 0 {
				return fmt.Errorf("scenario %s plan file tracked absent must expect zero model runs", s.ID)
			}
		}
		// report-only不変性破壊scenarioは、scripted packet成功宣言と無関係にproduction gateが
		// fail closed終端へ至ること自体が期待値。他終端への書換えは再確認を強制する。
		if s.ReportOnlyMutatesWorktree && s.ExpectedPacketStatus != "NEEDS_SOL_REVIEW" {
			return fmt.Errorf("scenario %s report-only mutation must expect fail-closed NEEDS_SOL_REVIEW", s.ID)
		}
		// plan file不変性破壊scenarioも同じくfail closed終端が期待値。
		if s.WorkerMutatesPlanFile && s.ExpectedPacketStatus != "NEEDS_SOL_REVIEW" {
			return fmt.Errorf("scenario %s plan file mutation must expect fail-closed NEEDS_SOL_REVIEW", s.ID)
		}
		if s.PlanFileInitiallyAbsent && !s.WorkerMutatesPlanFile {
			return fmt.Errorf("scenario %s plan_file_initially_absent without worker_mutates_plan_file", s.ID)
		}
		for i, want := range s.ExpectedOutputContains {
			if want == "" {
				return fmt.Errorf("scenario %s empty expected_output_contains entry %d", s.ID, i)
			}
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
		knownTelemetryClock := map[string]bool{"": true, telemetryClockInjectedStart: true}
		if !knownTelemetryClock[s.ExpectedTelemetryClock] {
			return fmt.Errorf("scenario %s unknown expected telemetry clock %q", s.ID, s.ExpectedTelemetryClock)
		}
		if s.ExpectedTelemetryClock == telemetryClockInjectedStart && s.SleepAdvanceMinutes > 0 {
			return fmt.Errorf("scenario %s injected-start telemetry clock conflicts with sleep_advance_minutes", s.ID)
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
		artifactNames := make(map[string]bool, len(s.ArtifactFiles))
		for _, af := range s.ArtifactFiles {
			if af.Name == "" || af.Name == "." || af.Name == ".." || filepath.IsAbs(af.Name) || strings.ContainsAny(af.Name, `/\`) {
				return fmt.Errorf("scenario %s invalid artifact file name %q", s.ID, af.Name)
			}
			if af.Content == "" {
				return fmt.Errorf("scenario %s artifact %s content empty", s.ID, af.Name)
			}
			artifactNames[af.Name] = true
		}
		artifactTokenUsed := false
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
				result, convErr := packet.FromDisplayLines(step.Lines)
				if convErr != nil {
					return fmt.Errorf("scenario %s step %d invalid result: %w", s.ID, i, convErr)
				}
				if err := validateTypedResult(result); err != nil {
					return fmt.Errorf("scenario %s step %d invalid result: %v", s.ID, i, err)
				}
				for _, ln := range step.Lines {
					if err := validateScenarioArtifactToken(s.ID, ln, artifactNames); err != nil {
						return err
					}
					if strings.Contains(ln, scenarioArtifactDirToken) {
						artifactTokenUsed = true
					}
				}
			}
		}
		if len(s.ArtifactFiles) > 0 && !artifactTokenUsed {
			return fmt.Errorf("scenario %s declares artifact_files but no runner step line references %s", s.ID, scenarioArtifactDirToken)
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

// validateScenarioArtifactTokenはpacket行内の{{ARTIFACT_DIR}}予約tokenが宣言済み
// artifact file名だけを参照していることを検証する。未宣言fileや素のdir参照は
// scriptedRunnerでの置換後に実在file検証へ失敗するため、corpus段階で拒否する。
func validateScenarioArtifactToken(id string, line string, artifactNames map[string]bool) error {
	rest := line
	for {
		i := strings.Index(rest, scenarioArtifactDirToken)
		if i < 0 {
			return nil
		}
		after := rest[i+len(scenarioArtifactDirToken):]
		matched := false
		for name := range artifactNames {
			if strings.HasPrefix(after, "/"+name) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("scenario %s line references %s without matching artifact_files entry: %q", id, scenarioArtifactDirToken, line)
		}
		rest = after
	}
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
				{Lines: []string{"STATUS: PASS", "RISK: LOW", "SUMMARY: s", "REQUIREMENT_COVERAGE: c", "INVARIANTS: i", "TEST_EVIDENCE: e", "ISSUES: none", "RESIDUAL_RISK: none", "TARGETS: final diff", "ARTIFACTS: none"}},
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
		{"multiple mutation hooks", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ReviewerMutatesWorktree = true
			sc.Scenarios[0].ReportOnlyMutatesWorktree = true
		}, "multiple mutation hooks"},
		{"report-only mutation without fail-closed terminal", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ReportOnlyMutatesWorktree = true
		}, "must expect fail-closed NEEDS_SOL_REVIEW"},
		{"plan file mutation without fail-closed terminal", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].WorkerMutatesPlanFile = true
		}, "plan file mutation must expect fail-closed NEEDS_SOL_REVIEW"},
		{"plan file initially absent without mutation hook", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].PlanFileInitiallyAbsent = true
		}, "without worker_mutates_plan_file"},
		{"plan file tracked absent with mutation hook", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].PlanFileTrackedAbsent = true
			sc.Scenarios[0].WorkerMutatesPlanFile = true
			sc.Scenarios[0].ExpectedPacketStatus = "NEEDS_SOL_REVIEW"
		}, "with mutation hook"},
		{"plan file tracked absent without zero-run expectation", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].PlanFileTrackedAbsent = true
			sc.Scenarios[0].ExpectedPacketStatus = "NEEDS_SOL_REVIEW"
		}, "must expect zero model runs"},
		{"empty expected output contains", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ExpectedOutputContains = []string{""}
		}, "empty expected_output_contains"},
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
		}, "invalid result"},
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
		{"unknown expected telemetry clock", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ExpectedTelemetryClock = "wall"
		}, "unknown expected telemetry clock"},
		{"telemetry clock with sleep advance", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ExpectedTelemetryClock = telemetryClockInjectedStart
			sc.Scenarios[0].SleepAdvanceMinutes = 90
		}, "conflicts with sleep_advance_minutes"},
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
		{"artifact file name with separator", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ArtifactFiles = []scenarioArtifact{{Name: "nested/evidence.md", Content: "x"}}
		}, "invalid artifact file name"},
		{"artifact content empty", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ArtifactFiles = []scenarioArtifact{{Name: "evidence.md"}}
		}, "content empty"},
		{"artifact files unused", func(sc *scenarioFile, _ *manifestFile) {
			sc.Scenarios[0].ArtifactFiles = []scenarioArtifact{{Name: "evidence.md", Content: "x"}}
		}, "no runner step line references"},
		{"artifact token without declared file", func(sc *scenarioFile, _ *manifestFile) {
			lines := sc.Scenarios[0].RunnerSteps[0].Lines
			lines[len(lines)-1] = strings.Replace(lines[len(lines)-1], "ARTIFACTS: none", "ARTIFACTS: "+scenarioArtifactDirToken+"/evidence.md", 1)
		}, "without matching artifact_files entry"},
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

// validateTypedResultは表示行から復元した結果がworker/reviewerどちらかの契約を満たすかを
// 検証する。roleは実行時のrunModelで確定するため、corpus検証はどちらか一方の通過を要求する。
func validateTypedResult(result packet.Result) error {
	if err := packet.ValidateWorkerResult(result); err == nil {
		return nil
	}
	return packet.ValidateReviewerResult(result)
}

// lastPacketFromOutputはwrapper stdoutの表示行(KEY: value)からtyped結果を復元する。
// 表示の`TARGETS: none`は空配列と予約値none sentinel(旧protocolの`TARGETS: none`値)の
// どちらにも同じ文字列でrenderされるため、契約検証ではsentinelへ読み替える。
// sentinel不許容のNEEDS_SOL_REVIEWはこの表示で受理されないため読み替えは無害。
func lastPacketFromOutput(t *testing.T, out string) packet.Result {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	emitted := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			emitted = append(emitted, ln)
		}
	}
	if len(emitted) == 0 {
		t.Fatalf("no emitted result in output:\n%s", out)
	}
	value, err := packet.FromDisplayLines(emitted)
	if err != nil {
		t.Fatalf("emitted result is not display lines: %v:\n%s", err, out)
	}
	for _, ln := range emitted {
		if ln == "TARGETS: none" && value.Targets == nil {
			value.Targets = []string{"none"}
		}
	}
	return value
}

func runScenario(t *testing.T, doc scenarioDoc) {
	t.Helper()
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: stepsFromScenario(doc)}
	if len(doc.ArtifactFiles) > 0 {
		// ExecuteNewTaskは新規task IDへ切り替わるため、artifact fileと{{ARTIFACT_DIR}}の
		// 置換はstep消費時の現在taskへ行う。事前配置では旧task配下になり検証が通らない。
		r.artifactFiles = doc.ArtifactFiles
		r.taskArtifactDir = st.PrepareArtifactDir
	}
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
	if doc.ReviewerMutatesWorktree || doc.ReportOnlyMutatesWorktree || doc.WorkerMutatesPlanFile || doc.PlanFileTrackedAbsent {
		mutationRepoRoot = initMutationRepo(t)
		mutate := func(root string) error {
			return os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("mutated\n"), 0o644)
		}
		if doc.WorkerMutatesPlanFile {
			// seedは親Codexがcall前に置いたworking tree内容の代役。初期欠損指定だけ省く。
			if !doc.PlanFileInitiallyAbsent {
				if err := os.WriteFile(filepath.Join(mutationRepoRoot, implementationPlanFile), []byte(scenarioPlanSeed), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			mutate = func(root string) error {
				return os.WriteFile(filepath.Join(root, implementationPlanFile), []byte(scenarioPlanMutated), 0o644)
			}
		}
		if doc.PlanFileTrackedAbsent {
			// Git indexへ追加済み(追跡中)なのにworking treeへ無い状態をseedする。
			// このrepoのstaged planと同じくindex追跡がcanonical、working tree欠損を再現する。
			if err := os.WriteFile(filepath.Join(mutationRepoRoot, implementationPlanFile), []byte(scenarioPlanSeed), 0o644); err != nil {
				t.Fatal(err)
			}
			gitIn(t, mutationRepoRoot, "add", implementationPlanFile)
			if err := os.Remove(filepath.Join(mutationRepoRoot, implementationPlanFile)); err != nil {
				t.Fatal(err)
			}
			mutate = nil
		}
		mr := &mutatingRunner{
			repoRoot: mutationRepoRoot,
			steps:    stepsFromScenario(doc),
			mutate:   mutate,
		}
		switch {
		case doc.ReportOnlyMutatesWorktree:
			mr.mutatePhase = "worker-report-only-1"
		case doc.WorkerMutatesPlanFile:
			mr.mutatePhase = "worker-new"
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

	if doc.ReviewerMutatesWorktree || doc.ReportOnlyMutatesWorktree || doc.WorkerMutatesPlanFile || doc.PlanFileTrackedAbsent {
		mr := w.runner.(*mutatingRunner)
		r.prompts = mr.prompts
		r.models = mr.models
		if doc.WorkerMutatesPlanFile {
			// orchestratorがGLMのplan変更をbaselineへ自動復元していないことを検証する。
			if content, err := os.ReadFile(filepath.Join(mutationRepoRoot, implementationPlanFile)); err != nil || string(content) != scenarioPlanMutated {
				t.Fatalf("workerによるplan file変更が自動復元・編集されています: %q %v", content, err)
			}
		} else if doc.PlanFileTrackedAbsent {
			// orchestratorが追跡中plan欠損を検出してもfileを再生成・復元しないことを検証する。
			if _, err := os.Stat(filepath.Join(mutationRepoRoot, implementationPlanFile)); !os.IsNotExist(err) {
				t.Fatalf("追跡中plan欠損が復元・生成されています: %v", err)
			}
			if out := gitIn(t, mutationRepoRoot, "ls-files", "--", implementationPlanFile); out == "" {
				t.Fatal("seed失敗: plan fileがindexへ追跡されていません")
			}
		} else if content, err := os.ReadFile(filepath.Join(mutationRepoRoot, "tracked.txt")); err != nil || string(content) != "mutated\n" {
			t.Fatalf("mutation hook対象呼出でrepositoryが変更されていません: %q %v", content, err)
		}
	}
	// expected_run_count=0はmodel呼出前に停止する終端を明示する期待値であるため、
	// この検証から除外する。それ以外はproduction codeへ一度も到達しないscenarioを検出する。
	if len(r.prompts) == 0 && doc.ExpectedProbeCount == 0 && doc.ExpectedRunCount == nil {
		t.Fatal("production runner was not invoked")
	}
	for i, p := range r.prompts {
		// 再出力系prompt(risk floor再出力・意味検証不合格の修正再依頼)はMODEを持たない。
		isReformat := strings.Contains(p, "PACKETだけを再出力") || strings.Contains(p, "結果を再出力") || strings.Contains(p, "結果だけを再出力")
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
		if err := validateTypedResult(pkt); err != nil {
			t.Fatalf("emitted result fails production contract: %v", err)
		}
		if string(pkt.Status) != doc.ExpectedPacketStatus {
			t.Fatalf("packet status = %q want %q", pkt.Status, doc.ExpectedPacketStatus)
		}
		if string(pkt.Risk) != doc.ExpectedPacketRisk {
			t.Fatalf("packet risk = %q want %q", pkt.Risk, doc.ExpectedPacketRisk)
		}
		for _, want := range doc.ExpectedOutputContains {
			if !strings.Contains(buf.String(), want) {
				t.Fatalf("wrapper output does not contain %q:\n%s", want, buf.String())
			}
		}
		if doc.MustNotPass && pkt.Status == packet.StatusPass {
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
	if doc.ExpectedTelemetryClock == telemetryClockInjectedStart {
		if err := checkTelemetryClock(taskLogs(t, st)); err != nil {
			t.Fatalf("scenario %s telemetry clock: %v", doc.ID, err)
		}
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

// checkTelemetryClockは全telemetry recordの時刻とdurationが注入fake clockだけから導出
// されていることを検証する。sleepでclockを進めないscenarioでは全recordが開始時刻に一致し、
// workflow production codeへ直接wall clock取得が再導入されると実時間へ分岐したrecordで失敗する。
func checkTelemetryClock(logs []state.ModelCallLog) error {
	if len(logs) == 0 {
		return errors.New("telemetry recordが無いためclock検証が空で通過")
	}
	for i, l := range logs {
		if l.StartedAt != testFixedTime || l.CompletedAt != testFixedTime {
			return fmt.Errorf("record %d (%s %s)時刻 = %s/%s want %s: wall clock取得の再導入", i, l.CallType, l.Phase, l.StartedAt, l.CompletedAt, testFixedTime)
		}
		if l.WallDurationMS != 0 {
			return fmt.Errorf("record %d (%s %s)wall duration = %d: wall clock由来のduration", i, l.CallType, l.Phase, l.WallDurationMS)
		}
	}
	return nil
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

// TestTelemetryClockCheckRejectsWallClockRecordsはclock検証がwall clock由来のrecordを
// 実際に拒否することを確認する。空recordでの無条件通過も拒否する。
func TestTelemetryClockCheckRejectsWallClockRecords(t *testing.T) {
	injected := state.ModelCallLog{CallType: state.CallTypeTask, Phase: "worker-new", StartedAt: testFixedTime, CompletedAt: testFixedTime}
	if err := checkTelemetryClock([]state.ModelCallLog{injected}); err != nil {
		t.Fatalf("注入clock由来recordが拒否されました: %v", err)
	}
	wallNow := time.Now().UTC()
	cases := []struct {
		name string
		logs []state.ModelCallLog
		want string
	}{
		{"wall clock timestamps", []state.ModelCallLog{{
			CallType:    state.CallTypeTask,
			Phase:       "worker-new",
			StartedAt:   wallNow,
			CompletedAt: wallNow,
		}}, "wall clock取得の再導入"},
		{"wall clock duration", []state.ModelCallLog{injected, {
			CallType:       state.CallTypeTask,
			Phase:          "review",
			StartedAt:      testFixedTime,
			CompletedAt:    testFixedTime,
			WallDurationMS: wallNow.Sub(testFixedTime).Milliseconds(),
		}}, "wall clock由来のduration"},
		{"no records", nil, "clock検証が空で通過"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			err := checkTelemetryClock(c.logs)
			if err == nil {
				t.Fatal("wall clock由来recordが検出されません")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %q want substring %q", err.Error(), c.want)
			}
		})
	}
}
