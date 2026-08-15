package packet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAcceptsSinglePacketWithSurroundingBlankLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	content := "\nPACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: LOW\nSUMMARY: done\nREQUIREMENT_COVERAGE: covered\nTESTS: pass\nUNVERIFIED: none\nARTIFACTS: none\nPACKET_END\n\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status() != "IMPLEMENTED" || p.Risk() != "LOW" {
		t.Fatalf("status=%q risk=%q", p.Status(), p.Risk())
	}
}

func TestParseRejectsStructuralViolations(t *testing.T) {
	implemented := "PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: LOW\nSUMMARY: s\nREQUIREMENT_COVERAGE: c\nTESTS: t\nUNVERIFIED: none\nARTIFACTS: none\nPACKET_END\n"
	tests := []struct {
		name     string
		content  string
		contains string
		category string
	}{
		{
			name:     "multiple completed packets",
			content:  implemented + implemented,
			contains: "複数検出",
			category: "multiple-packets",
		},
		{
			name:     "packet after closed packet",
			content:  implemented + "\n" + implemented,
			contains: "複数検出",
			category: "multiple-packets",
		},
		{
			name:     "non-empty body before packet",
			content:  "作業を完了しました。\n" + implemented,
			contains: "前に非空の本文",
			category: "stray-body",
		},
		{
			name:     "non-empty body after packet",
			content:  implemented + "以上です。\n",
			contains: "後に非空の本文",
			category: "stray-body",
		},
		{
			name:     "nested begin marker",
			content:  "PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: LOW\nPACKET_BEGIN\nSUMMARY: s\nPACKET_END\n",
			contains: "入れ子",
			category: "nested-marker",
		},
		{
			name:     "end marker without begin",
			content:  "PACKET_END\n" + implemented,
			contains: "対応するPACKET_BEGINがない",
			category: "stray-marker",
		},
		{
			name:     "second end marker after close",
			content:  implemented + "PACKET_END\n",
			contains: "対応するPACKET_BEGINがない",
			category: "stray-marker",
		},
		{
			name:     "unclosed begin marker",
			content:  "PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: LOW\n",
			contains: "閉じられていません",
			category: "unclosed-marker",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "output.txt")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Parse(path)
			if err == nil || !IsConstraintError(err) {
				t.Fatalf("構造違反をpacket制約違反として拒否する必要があります: %v", err)
			}
			if !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("拒否理由が異なります: %v", err)
			}
			if got := RejectCategory(err); got != test.category {
				t.Fatalf("reject category = %q want %q", got, test.category)
			}
		})
	}
}

func TestParseRejectsOversizedPacket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	content := `PACKET_BEGIN
STATUS: IMPLEMENTED
RISK: LOW
SUMMARY: ` + strings.Repeat("x", MaxPacketLineBytes+1) + `
REQUIREMENT_COVERAGE: covered
TESTS: pass
UNVERIFIED: none
ARTIFACTS: none
PACKET_END
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Parse(path)
	if err == nil || !IsConstraintError(err) {
		t.Fatalf("packet constraint errorを期待しました: %v", err)
	}
}

func TestParseRejectsMissingRequiredField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	content := `PACKET_BEGIN
STATUS: IMPLEMENTED
RISK: LOW
SUMMARY: implemented
TESTS: pass
UNVERIFIED: none
ARTIFACTS: none
PACKET_END
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Parse(path); err == nil || !IsConstraintError(err) {
		t.Fatalf("必須field欠落をpacket制約違反として拒否する必要があります: %v", err)
	}
}

func TestParseRejectsMissingArtifactsField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	content := `PACKET_BEGIN
STATUS: IMPLEMENTED
RISK: LOW
SUMMARY: implemented
REQUIREMENT_COVERAGE: covered
TESTS: pass
UNVERIFIED: none
PACKET_END
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Parse(path)
	if err == nil || !strings.Contains(err.Error(), "ARTIFACTS") {
		t.Fatalf("ARTIFACTS欠落を明示して拒否する必要があります: %v", err)
	}
}

func TestParseRejectsMissingPacket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	if err := os.WriteFile(path, []byte("STATUS: PASS\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Parse(path); err == nil || !IsConstraintError(err) {
		t.Fatalf("packet欠落をpacket制約違反として拒否する必要があります: %v", err)
	}
}

func TestParseRejectsNeedsSolReviewWithoutSolQuestion(t *testing.T) {
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
ARTIFACTS: none
PACKET_END
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Parse(path); err == nil || !IsConstraintError(err) {
		t.Fatalf("NEEDS_SOL_REVIEWのSOL_QUESTION欠落をpacket制約違反として拒否する必要があります: %v", err)
	}
}

func TestParseRejectsNeedsSolReviewWithNoneTargets(t *testing.T) {
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
TARGETS: none
ARTIFACTS: none
SOL_QUESTION: q
PACKET_END
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Parse(path)
	if err == nil || !IsConstraintError(err) {
		t.Fatalf("NEEDS_SOL_REVIEWのTARGETS: noneをpacket制約違反として拒否する必要があります: %v", err)
	}
	if !strings.Contains(err.Error(), "TARGETS") {
		t.Fatalf("拒否理由がTARGETSへ言及していません: %v", err)
	}
	if got := RejectCategory(err); got != "targets-none" {
		t.Fatalf("reject category = %q want targets-none", got)
	}
}

func TestValidateAcceptsNoneTargetsOutsideNeedsSolReview(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{
			name: "pass",
			lines: []string{
				"STATUS: PASS", "RISK: LOW", "SUMMARY: s", "REQUIREMENT_COVERAGE: c",
				"INVARIANTS: i", "TEST_EVIDENCE: e", "ISSUES: none", "RESIDUAL_RISK: none",
				"TARGETS: none", "ARTIFACTS: none",
			},
		},
		{
			name: "fix required",
			lines: []string{
				"STATUS: FIX_REQUIRED", "RISK: HIGH", "SUMMARY: s", "REQUIREMENT_COVERAGE: c",
				"INVARIANTS: i", "TEST_EVIDENCE: e", "ISSUES: i", "RESIDUAL_RISK: r",
				"TARGETS: none", "ARTIFACTS: none",
			},
		},
		{
			name: "decision",
			lines: []string{
				"STATUS: NEEDS_SOL_DECISION", "RISK: HIGH", "DECISION: d", "EVIDENCE: e",
				"OPTIONS: o", "RECOMMENDATION: r", "TEST_OBLIGATIONS: t",
				"TARGETS: none", "ARTIFACTS: none",
			},
		},
		{
			name: "sol review with concrete targets",
			lines: []string{
				"STATUS: NEEDS_SOL_REVIEW", "RISK: HIGH", "SUMMARY: s", "REQUIREMENT_COVERAGE: c",
				"INVARIANTS: i", "TEST_EVIDENCE: e", "ISSUES: i", "RESIDUAL_RISK: r",
				"TARGETS: workflow.go:reviewUntilStable", "ARTIFACTS: none", "SOL_QUESTION: q",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := Validate(FromLines(test.lines)); err != nil {
				t.Fatalf("none TARGETSはNEEDS_SOL_REVIEW以外で許容されるべき: %v", err)
			}
		})
	}
}

func TestRejectCategoryKeepsDuplicateTargetsInMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	content := `PACKET_BEGIN
STATUS: PASS
RISK: LOW
SUMMARY: s
REQUIREMENT_COVERAGE: c
INVARIANTS: i
TEST_EVIDENCE: e
ISSUES: none
RESIDUAL_RISK: none
TARGETS: none
TARGETS: none
ARTIFACTS: none
PACKET_END
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Parse(path)
	if err == nil || !strings.Contains(err.Error(), "TARGETSが重複") {
		t.Fatalf("TARGETS重複を検出する必要があります: %v", err)
	}
	if got := RejectCategory(err); got != "malformed" {
		t.Fatalf("重複TARGETSのreject category = %q want malformed", got)
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
