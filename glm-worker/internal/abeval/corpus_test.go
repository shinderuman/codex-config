package abeval

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type corpusPair struct {
	Spec                   Spec      `json:"spec"`
	Direct                 RunRecord `json:"direct"`
	Orchestrated           RunRecord `json:"orchestrated"`
	ExpectedCodexReduction string    `json:"expected_codex_reduction"`
}

type corpusFile struct {
	Version     int          `json:"version"`
	Description string       `json:"description"`
	Pairs       []corpusPair `json:"pairs"`
}

func corpusRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join("glm-worker", "go.mod")
	for d := dir; d != string(filepath.Separator); d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, marker)); err == nil {
			return d
		}
	}
	t.Fatalf("corpus root not found from %s", dir)
	return ""
}

func loadCorpus(t *testing.T) corpusFile {
	t.Helper()
	path := filepath.Join(corpusRepoRoot(t), "glm-worker", "scenarios", "ab-eval.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var corpus corpusFile
	if err := decodeStrict(data, "ab-eval.json", &corpus); err != nil {
		t.Fatalf("ab-eval.json parse: %v", err)
	}
	return corpus
}

func validateCorpus(corpus corpusFile) error {
	if corpus.Version != 1 {
		return fmt.Errorf("corpus version must be 1: got %d", corpus.Version)
	}
	if len(corpus.Pairs) == 0 {
		return errors.New("corpus has no pairs")
	}
	seenID := make(map[string]bool, len(corpus.Pairs))
	for i, pair := range corpus.Pairs {
		if err := ValidateSpec(pair.Spec); err != nil {
			return fmt.Errorf("pair %d spec: %w", i, err)
		}
		if seenID[pair.Spec.ID] {
			return fmt.Errorf("duplicate spec id %q", pair.Spec.ID)
		}
		seenID[pair.Spec.ID] = true
		if pair.ExpectedCodexReduction != codexReductionActual && pair.ExpectedCodexReduction != codexReductionUnknown {
			return fmt.Errorf("pair %d unknown expected_codex_reduction %q", i, pair.ExpectedCodexReduction)
		}
		if err := ValidatePair(pair.Spec, pair.Direct, pair.Orchestrated); err != nil {
			return fmt.Errorf("pair %d: %w", i, err)
		}
	}
	return nil
}

func TestABEvalCorpusContract(t *testing.T) {
	corpus := loadCorpus(t)
	if err := validateCorpus(corpus); err != nil {
		t.Fatalf("corpus contract violation: %v", err)
	}
}

func TestABEvalCorpusContractRejectsInvalid(t *testing.T) {
	corpus := loadCorpus(t)
	if err := validateCorpus(corpus); err != nil {
		t.Fatalf("baseline corpus must be valid: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(corpus *corpusFile)
		want   string
	}{
		{"corpus version", func(corpus *corpusFile) { corpus.Version = 2 }, "corpus version"},
		{"no pairs", func(corpus *corpusFile) { corpus.Pairs = nil }, "no pairs"},
		{"duplicate spec id", func(corpus *corpusFile) {
			corpus.Pairs[1].Spec.ID = corpus.Pairs[0].Spec.ID
			corpus.Pairs[1].Direct.SpecID = corpus.Pairs[0].Spec.ID
			corpus.Pairs[1].Orchestrated.SpecID = corpus.Pairs[0].Spec.ID
		}, "duplicate spec id"},
		{"unknown reduction expectation", func(corpus *corpusFile) {
			corpus.Pairs[0].ExpectedCodexReduction = "estimated"
		}, "expected_codex_reduction"},
		{"pair session collision", func(corpus *corpusFile) {
			corpus.Pairs[0].Orchestrated.SessionID = corpus.Pairs[0].Direct.SessionID
		}, "同一session"},
		{"pair spec hash drift", func(corpus *corpusFile) {
			corpus.Pairs[0].Direct.SpecSHA256 = "deadbeef"
		}, "spec_sha256"},
		{"pair estimated codex usage", func(corpus *corpusFile) {
			corpus.Pairs[1].Direct.CodexUsage.InputTokens = 500000
		}, "sourceなしにtoken値"},
		{"pair unknown codex usage source", func(corpus *corpusFile) {
			corpus.Pairs[0].Direct.CodexUsage.Source = "codex-cli-guess"
		}, "codex_usage.sourceは"},
		{"pair transcribed glm source", func(corpus *corpusFile) {
			corpus.Pairs[0].Orchestrated.GLMUsage.Source = "transcribed-telemetry"
		}, "glm-worker-task-statsのみ受理"},
		{"pair negative glm usage", func(corpus *corpusFile) {
			corpus.Pairs[0].Orchestrated.GLMUsage.OutputTokens = -1
		}, "glm_usageの値が負"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			mutated := loadCorpus(t)
			c.mutate(&mutated)
			err := validateCorpus(mutated)
			if err == nil {
				t.Fatal("expected contract violation, got nil")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %q want substring %q", err.Error(), c.want)
			}
		})
	}
}

// TestABEvalCorpusDrivenThroughComparisonはcorpusの全pairを比較・表示まで通し、
// expected_codex_reductionの両経路(actual/unknown)と最重要出力の構成を検証する。
func TestABEvalCorpusDrivenThroughComparison(t *testing.T) {
	corpus := loadCorpus(t)
	if err := validateCorpus(corpus); err != nil {
		t.Fatalf("corpus contract violation: %v", err)
	}
	covered := map[string]bool{}
	for _, pair := range corpus.Pairs {
		out := Format(Compare(pair.Spec, pair.Direct, pair.Orchestrated))
		reductionLine := lineWithKey(t, out, "CODEX_REDUCTION")
		switch pair.ExpectedCodexReduction {
		case codexReductionActual:
			if !strings.Contains(reductionLine, "input=") || !strings.Contains(reductionLine, "(actual usage") {
				t.Fatalf("pair %sのCODEX_REDUCTIONがactual usage基準ではありません: %s", pair.Spec.ID, reductionLine)
			}
		case codexReductionUnknown:
			if !strings.HasPrefix(reductionLine, "CODEX_REDUCTION: unknown (") {
				t.Fatalf("pair %sのCODEX_REDUCTIONがunknown保持ではありません: %s", pair.Spec.ID, reductionLine)
			}
			if strings.Contains(reductionLine, "input=") || strings.Contains(reductionLine, "output=") {
				t.Fatalf("pair %sのunknown経路で削減率percentが出力されています: %s", pair.Spec.ID, reductionLine)
			}
		}
		covered[pair.ExpectedCodexReduction] = true
		for _, key := range []string{"QUALITY_DELTA", "TIME", "CODEX_USAGE", "GLM_USAGE", "PROXY_METRICS", "MEASUREMENT_BOUNDARY", "ISOLATION"} {
			lineWithKey(t, out, key)
		}
		if !strings.Contains(out, "合算値は算出しない") {
			t.Fatalf("pair %sのNOTESへGLM/Codex token非合算方針がありません", pair.Spec.ID)
		}
	}
	if !covered[codexReductionActual] || !covered[codexReductionUnknown] {
		t.Fatalf("corpusはactual/unknown両経路をcoverする必要があります: %v", covered)
	}
}

func lineWithKey(t *testing.T, out string, key string) string {
	t.Helper()
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.HasPrefix(line, key+":") {
			return line
		}
	}
	t.Fatalf("出力へ%s行がありません:\n%s", key, out)
	return ""
}
