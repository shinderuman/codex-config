package packet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLastUsesLastCompletePacket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	content := `noise
PACKET_BEGIN
STATUS: IMPLEMENTED
RISK: LOW
SUMMARY: first
REQUIREMENT_COVERAGE: covered
TESTS: pass
UNVERIFIED: none
PACKET_END
more noise
PACKET_BEGIN
STATUS: NEEDS_SOL_REVIEW
RISK: HIGH
SUMMARY: review
REQUIREMENT_COVERAGE: covered
INVARIANTS: preserved
TEST_EVIDENCE: pass
ISSUES: none
RESIDUAL_RISK: review required
TARGETS: foo.go:Run
SOL_QUESTION: architecture direction
PACKET_END
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := ParseLast(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status() != "NEEDS_SOL_REVIEW" {
		t.Fatalf("status = %q", p.Status())
	}
	if p.Risk() != "HIGH" {
		t.Fatalf("risk = %q", p.Risk())
	}
}

func TestParseLastRejectsOversizedPacket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	content := `PACKET_BEGIN
STATUS: IMPLEMENTED
RISK: LOW
SUMMARY: ` + strings.Repeat("x", MaxPacketLineBytes+1) + `
REQUIREMENT_COVERAGE: covered
TESTS: pass
UNVERIFIED: none
PACKET_END
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ParseLast(path)
	if err == nil || !IsConstraintError(err) {
		t.Fatalf("packet constraint errorを期待しました: %v", err)
	}
}

func TestParseLastRejectsMissingRequiredField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	content := `PACKET_BEGIN
STATUS: IMPLEMENTED
RISK: LOW
SUMMARY: implemented
TESTS: pass
UNVERIFIED: none
PACKET_END
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ParseLast(path); err == nil || !IsConstraintError(err) {
		t.Fatalf("必須field欠落をpacket制約違反として拒否する必要があります: %v", err)
	}
}

func TestParseLastRejectsMissingPacket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	if err := os.WriteFile(path, []byte("STATUS: PASS\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ParseLast(path); err == nil || !IsConstraintError(err) {
		t.Fatalf("packet欠落をpacket制約違反として拒否する必要があります: %v", err)
	}
}

func TestParseLastRejectsNeedsSolReviewWithoutSolQuestion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	content := `PACKET_BEGIN
STATUS: NEEDS_SOL_REVIEW
RISK: HIGH
SUMMARY: review
REQUIREMENT_COVERAGE: covered
INVARIANTS: preserved
TEST_EVIDENCE: pass
ISSUES: none
RESIDUAL_RISK: review required
TARGETS: foo.go:Run
PACKET_END
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ParseLast(path); err == nil || !IsConstraintError(err) {
		t.Fatalf("NEEDS_SOL_REVIEWのSOL_QUESTION欠落をpacket制約違反として拒否する必要があります: %v", err)
	}
}

func TestTailReturnsRequestedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := Tail(path, 2); got != "three\nfour" {
		t.Fatalf("tail = %q", got)
	}
	if got := Tail(path, 0); got != "" {
		t.Fatalf("zero tail = %q", got)
	}
	if got := Tail(filepath.Join(t.TempDir(), "missing"), 2); got != "" {
		t.Fatalf("missing tail = %q", got)
	}
}

func TestConstraintErrorIncludesReason(t *testing.T) {
	err := &constraintError{reason: "reason"}
	if err.Error() != "reason" {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestValidateRejectsDuplicateFieldsAndInvalidRiskStatusPair(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{name: "duplicate", lines: []string{"STATUS: IMPLEMENTED", "STATUS: IMPLEMENTED", "RISK: LOW"}},
		{name: "decision low", lines: []string{"STATUS: NEEDS_SOL_DECISION", "RISK: LOW"}},
		{name: "pass high", lines: []string{"STATUS: PASS", "RISK: HIGH"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := Validate(FromLines(test.lines)); err == nil || !IsConstraintError(err) {
				t.Fatalf("constraint errorを期待: %v", err)
			}
		})
	}
}

func TestTailCapsDiagnosticBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("長い診断行\n", MaxDiagnosticBytes)), 0o600); err != nil {
		t.Fatal(err)
	}
	got := Tail(path, MaxDiagnosticBytes)
	if len(got) > MaxDiagnosticBytes || !strings.HasPrefix(got, "[前方を省略]\n") {
		t.Fatalf("diagnostic tail length=%d prefix=%t", len(got), strings.HasPrefix(got, "[前方を省略]\n"))
	}
}

func TestPromptTemplatesDeclareRequiredPacketFields(t *testing.T) {
	workerPrompt, err := os.ReadFile(filepath.Join("..", "..", "..", "codex", "glm-worker", "prompts", "WORKER.md"))
	if err != nil {
		t.Fatal(err)
	}
	reviewerPrompt, err := os.ReadFile(filepath.Join("..", "..", "..", "codex", "glm-worker", "prompts", "REVIEWER.md"))
	if err != nil {
		t.Fatal(err)
	}

	promptByStatus := map[string][]byte{
		"NEEDS_SOL_DECISION": workerPrompt,
		"IMPLEMENTED":        workerPrompt,
		"PASS":               reviewerPrompt,
		"FIX_REQUIRED":       reviewerPrompt,
		"NEEDS_SOL_REVIEW":   reviewerPrompt,
	}
	for status, fields := range requiredFields {
		prompt := string(promptByStatus[status])
		for _, field := range fields {
			if !strings.Contains(prompt, field+":") {
				t.Errorf("%s promptに必須field %sの出力定義がありません", status, field)
			}
		}
	}
}
