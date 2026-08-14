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

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/packet"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

type scenarioStep struct {
	Lines []string `json:"lines"`
	Error string   `json:"error"`
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
	// ReviewerMutatesWorktreeはreviewerがBash相当でrepositoryを変更するscenarioで有効化する。
	ReviewerMutatesWorktree bool `json:"reviewer_mutates_worktree,omitempty"`
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

	knownEntry := map[string]bool{"new_task": true}
	knownStatus := map[string]bool{"IMPLEMENTED": true, "PASS": true, "FIX_REQUIRED": true, "NEEDS_SOL_DECISION": true, "NEEDS_SOL_REVIEW": true}
	knownRisk := map[string]bool{"LOW": true, "HIGH": true}
	knownTask := map[string]bool{"active": true, "waiting-decision": true, "waiting-sol-review": true, "complete": true, "rate-limited": true}

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
		if len(s.RunnerSteps) != len(s.ExpectedModels) {
			return fmt.Errorf("scenario %s runner_steps/expected_models count mismatch: %d vs %d", s.ID, len(s.RunnerSteps), len(s.ExpectedModels))
		}
		for i, step := range s.RunnerSteps {
			hasLines := len(step.Lines) > 0
			hasErr := step.Error != ""
			if !hasLines && !hasErr {
				return fmt.Errorf("scenario %s step %d empty", s.ID, i)
			}
			if hasLines && hasErr {
				return fmt.Errorf("scenario %s step %d has both packet and error", s.ID, i)
			}
			if hasLines {
				if err := packet.Validate(packet.FromLines(step.Lines)); err != nil {
					return fmt.Errorf("scenario %s step %d invalid packet: %w", s.ID, i, err)
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
		{"unknown entry", func(sc *scenarioFile, _ *manifestFile) { sc.Scenarios[0].Entry = "resume" }, "unknown entry"},
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
		}, "both packet and error"},
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
		steps[i] = runnerStep{output: output, runErr: runErr}
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
	w := newWorkflowT(t, st, r)
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

	switch doc.Entry {
	case "new_task":
		if err := w.ExecuteNewTask(doc.Request); err != nil {
			t.Fatalf("ExecuteNewTask: %v", err)
		}
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
	if len(r.prompts) == 0 {
		t.Fatal("production runner was not invoked")
	}
	for i, p := range r.prompts {
		isReformat := strings.Contains(p, "PACKETだけを再出力")
		hasRequest := strings.Contains(p, doc.Request)
		hasMode := strings.Contains(p, "MODE:") || strings.Contains(p, "REVIEW_MODE:")
		if !(hasMode || isReformat) {
			t.Fatalf("prompt %d is not a production-generated prompt:\n%s", i, p)
		}
		if !isReformat && !hasRequest {
			t.Fatalf("prompt %d does not transmit USER_REQUEST:\n%s", i, p)
		}
	}
	if !strings.Contains(r.prompts[0], "MODE:") || !strings.Contains(r.prompts[0], "USER_REQUEST:") {
		t.Fatalf("worker prompt is not a production NEW_TASK prompt:\n%s", r.prompts[0])
	}

	if got, want := strings.Join(r.models, ","), strings.Join(doc.ExpectedModels, ","); got != want {
		t.Fatalf("model routing = %q want %q", got, want)
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
	if got := string(st.TaskStatus()); got != doc.ExpectedTaskStatus {
		t.Fatalf("task status = %q want %q", got, doc.ExpectedTaskStatus)
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
