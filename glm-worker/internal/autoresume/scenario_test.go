package autoresume

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type autoresumeVerification struct {
	Outcome            string `json:"outcome"`
	ManualAppConfirmed bool   `json:"manual_app_confirmed"`
}

type autoresumeScenario struct {
	ID                   string                   `json:"id"`
	Behavior             string                   `json:"behavior"`
	ResumeAt             string                   `json:"resume_at"`
	ExistingAutomationID string                   `json:"existing_automation_id"`
	CreateResponses      []string                 `json:"create_responses"`
	UpdateResponses      []string                 `json:"update_responses"`
	DeleteErrors         []string                 `json:"delete_errors"`
	Verifications        []autoresumeVerification `json:"verifications"`
	ExpectedReserved     bool                     `json:"expected_reserved"`
	ExpectedActions      []string                 `json:"expected_actions"`
	ExpectedFallbackID   string                   `json:"expected_fallback_id"`
}

type autoresumeFile struct {
	Version   int                  `json:"version"`
	Scenarios []autoresumeScenario `json:"scenarios"`
}

type autoresumeManifest struct {
	AutoResumeInstructionSHA256 string `json:"auto_resume_instruction_sha256"`
	ScenarioCount               int    `json:"scenario_count"`
}

func scenarioRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join("glm-worker", "scenarios", "autoresume.json")
	for d := dir; d != string(filepath.Separator); d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, marker)); err == nil {
			return d
		}
	}
	t.Fatalf("scenario corpus root not found from %s", dir)
	return ""
}

func loadAutoresumeCorpus(t *testing.T) (autoresumeFile, autoresumeManifest) {
	t.Helper()
	base := filepath.Join(scenarioRepoRoot(t), "glm-worker", "scenarios")

	var corpus autoresumeFile
	corpusBytes, err := os.ReadFile(filepath.Join(base, "autoresume.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(corpusBytes, &corpus); err != nil {
		t.Fatalf("autoresume.json parse: %v", err)
	}

	var manifest autoresumeManifest
	manifestBytes, err := os.ReadFile(filepath.Join(base, "autoresume-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("autoresume-manifest.json parse: %v", err)
	}
	return corpus, manifest
}

func validateAutoresumeCorpus(corpus autoresumeFile, manifest autoresumeManifest) error {
	if corpus.Version != 1 {
		return fmt.Errorf("corpus version must be 1: got %d", corpus.Version)
	}
	required := map[string]bool{
		"autoresume-error-response-not-misread-as-success":                false,
		"autoresume-empty-response-not-misread-as-success":                false,
		"autoresume-suggestion-card-response-not-success":                 false,
		"autoresume-success-response-without-id-not-success":              false,
		"autoresume-id-only-response-without-success-marker-not-success":  false,
		"autoresume-new-automation-two-stage-no-immediate-dtstart-create": false,
		"autoresume-update-failure-cleans-up-placeholder":                 false,
		"autoresume-verify-fail-fail-closed-with-cleanup":                 false,
		"autoresume-verify-unavailable-unconfirmed-is-failure":            false,
		"autoresume-existing-automation-updates-same-id-no-placeholder":   false,
	}
	seenID := make(map[string]bool, len(corpus.Scenarios))
	for _, s := range corpus.Scenarios {
		if s.ID == "" {
			return errors.New("autoresume scenario ID empty")
		}
		if seenID[s.ID] {
			return fmt.Errorf("duplicate autoresume scenario ID %q", s.ID)
		}
		seenID[s.ID] = true
		if s.Behavior == "" {
			return fmt.Errorf("scenario %s behavior empty", s.ID)
		}
		if s.ResumeAt == "" {
			return fmt.Errorf("scenario %s resume_at empty", s.ID)
		}
		if _, ok := required[s.ID]; ok {
			required[s.ID] = true
		}
		if len(s.ExpectedActions) == 0 {
			return fmt.Errorf("scenario %s expected_actions empty", s.ID)
		}
		joined := strings.Join(s.ExpectedActions, "\n")
		last := s.ExpectedActions[len(s.ExpectedActions)-1]
		if s.ExpectedReserved && last != "report_reserved" {
			return fmt.Errorf("scenario %s reserved scenario must end with report_reserved", s.ID)
		}
		if !s.ExpectedReserved && last != "report_failure" {
			return fmt.Errorf("scenario %s failure scenario must end with report_failure", s.ID)
		}
		if !s.ExpectedReserved && strings.Contains(joined, "report_reserved") {
			return fmt.Errorf("scenario %s must not report reservation success", s.ID)
		}
		if s.ExistingAutomationID != "" && strings.Contains(joined, "create_placeholder") {
			return fmt.Errorf("scenario %s updates an existing automation but creates a placeholder", s.ID)
		}
	}
	for id, present := range required {
		if !present {
			return fmt.Errorf("escaped bug corpus missing required scenario %q", id)
		}
	}
	if manifest.ScenarioCount != len(corpus.Scenarios) {
		return fmt.Errorf("manifest scenario_count = %d want %d", manifest.ScenarioCount, len(corpus.Scenarios))
	}
	if manifest.AutoResumeInstructionSHA256 == "" {
		return errors.New("manifest auto_resume_instruction_sha256 empty")
	}
	return nil
}

type scriptedEnv struct {
	createResponses []string
	updateResponses []string
	deleteErrors    []string
	verifications   []verification
	createCalls     []string
	updateCalls     []string
	deleteCalls     []string
	verifyCalls     int
	t               *testing.T
}

func (e *scriptedEnv) CreatePlaceholder(key string) string {
	e.t.Helper()
	e.createCalls = append(e.createCalls, key)
	if len(e.createCalls) > len(e.createResponses) {
		e.t.Fatalf("unexpected CreatePlaceholder call %d", len(e.createCalls))
	}
	return e.createResponses[len(e.createCalls)-1]
}

func (e *scriptedEnv) UpdateActive(id string) string {
	e.t.Helper()
	e.updateCalls = append(e.updateCalls, id)
	if len(e.updateCalls) > len(e.updateResponses) {
		e.t.Fatalf("unexpected UpdateActive call %d", len(e.updateCalls))
	}
	return e.updateResponses[len(e.updateCalls)-1]
}

func (e *scriptedEnv) DeleteAutomation(id string) error {
	e.t.Helper()
	e.deleteCalls = append(e.deleteCalls, id)
	if len(e.deleteCalls) > len(e.deleteErrors) {
		e.t.Fatalf("unexpected DeleteAutomation call %d", len(e.deleteCalls))
	}
	if text := e.deleteErrors[len(e.deleteCalls)-1]; text != "" {
		return errors.New(text)
	}
	return nil
}

func (e *scriptedEnv) VerifyReservation() verification {
	e.t.Helper()
	e.verifyCalls++
	if e.verifyCalls > len(e.verifications) {
		e.t.Fatalf("unexpected VerifyReservation call %d", e.verifyCalls)
	}
	return e.verifications[e.verifyCalls-1]
}

func TestAutoresumeScenarioCorpusContract(t *testing.T) {
	corpus, manifest := loadAutoresumeCorpus(t)
	if err := validateAutoresumeCorpus(corpus, manifest); err != nil {
		t.Fatalf("autoresume corpus contract violation: %v", err)
	}
	sum := sha256FileForAutoresume(t, filepath.Join(scenarioRepoRoot(t), "codex", "instructions", "glm-auto-resume.md"))
	if sum != manifest.AutoResumeInstructionSHA256 {
		t.Fatalf("glm-auto-resume.md changed: expected %s got %s; re-confirm autoresume scenarios", manifest.AutoResumeInstructionSHA256, sum)
	}
}

func TestAutoresumeScenarioCorpusDrivenThroughReservationContract(t *testing.T) {
	corpus, manifest := loadAutoresumeCorpus(t)
	if err := validateAutoresumeCorpus(corpus, manifest); err != nil {
		t.Fatalf("autoresume corpus contract violation: %v", err)
	}
	for _, doc := range corpus.Scenarios {
		doc := doc
		t.Run(doc.ID, func(t *testing.T) {
			verifications := make([]verification, len(doc.Verifications))
			for i, v := range doc.Verifications {
				var outcome Outcome
				switch v.Outcome {
				case "PASS":
					outcome = Pass
				case "FAIL":
					outcome = Fail
				case "UNAVAILABLE":
					outcome = Unavailable
				default:
					t.Fatalf("scenario %s unknown verification outcome %q", doc.ID, v.Outcome)
				}
				verifications[i] = verification{Outcome: outcome, ManualAppConfirmed: v.ManualAppConfirmed}
			}
			env := &scriptedEnv{
				createResponses: doc.CreateResponses,
				updateResponses: doc.UpdateResponses,
				deleteErrors:    doc.DeleteErrors,
				verifications:   verifications,
				t:               t,
			}
			result := orchestrateReservation(env, "glm-worker-resume-testrepo12-12345678", doc.ResumeAt, doc.ExistingAutomationID)

			got := make([]string, len(result.Actions))
			for i, a := range result.Actions {
				got[i] = a.String()
			}
			if strings.Join(got, "\n") != strings.Join(doc.ExpectedActions, "\n") {
				t.Fatalf("scenario %s actions = %v want %v (failure reason: %s)", doc.ID, got, doc.ExpectedActions, result.FailureReason)
			}
			if result.Reserved != doc.ExpectedReserved {
				t.Fatalf("scenario %s reserved = %v want %v (failure reason: %s)", doc.ID, result.Reserved, doc.ExpectedReserved, result.FailureReason)
			}
			if !doc.ExpectedReserved && result.FailureReason == "" {
				t.Fatalf("scenario %s reports failure without reason", doc.ID)
			}
			if result.ManualFallbackID != doc.ExpectedFallbackID {
				t.Fatalf("scenario %s manual fallback ID = %q want %q", doc.ID, result.ManualFallbackID, doc.ExpectedFallbackID)
			}
			if doc.ExpectedFallbackID != "" && len(env.deleteCalls) == 0 {
				t.Fatalf("scenario %s expects fallback ID but no delete was attempted", doc.ID)
			}
		})
	}
}

func TestAutoresumeEscapedBugCases(t *testing.T) {
	cases := []struct {
		name string
		text string
		want responseClass
	}{
		{"invalid arguments", "Error: invalid arguments supplied to automation_update", responseFailure},
		{"empty after trim", "   \n", responseFailure},
		{"rendered suggestion", "Rendered suggestion card for automation creation", responseFailure},
		{"suggested create", "suggested_create: some-id", responseFailure},
		{"failed update", "failed to update automation", responseFailure},
		{"id only without success marker", "automation_id: auto-1234", responseFailure},
		{"created in the app with id", "Created automation in the app. Automation ID: auto-1234", responseSuccess},
		{"updated in the app with id", "Updated automation in the app. Automation ID: auto-1234", responseSuccess},
		{"success with id", "Automation created successfully. Automation ID: auto-1234", responseSuccess},
		{"success without id", "Automation created successfully", responseFailure},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			class, _ := classifyResponse(c.text)
			if class != c.want {
				t.Fatalf("classifyResponse(%q) = %d want %d", c.text, class, c.want)
			}
		})
	}
}

func sha256FileForAutoresume(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
