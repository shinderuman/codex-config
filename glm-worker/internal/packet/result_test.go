package packet

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseStructuredAcceptsTypedResult(t *testing.T) {
	raw := `{"status":"IMPLEMENTED","risk":"LOW","summary":"s","requirement_coverage":"c","tests":"t","unverified":"none","targets":[],"artifacts":[]}`
	result, err := ParseStructured([]byte(raw))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if result.Status != StatusImplemented || result.Risk != RiskLow {
		t.Fatalf("status/risk = %s/%s", result.Status, result.Risk)
	}
	if result.Summary != "s" || result.Tests != "t" {
		t.Fatalf("fields not parsed: %+v", result)
	}
}

func TestParseStructuredRejectsContractBreaks(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"empty", "   "},
		{"null", "null"},
		{"bad json", `{"status":`},
		{"missing status", `{"risk":"LOW","artifacts":[]}`},
		{"wrong type", `{"status":"IMPLEMENTED","risk":"LOW","targets":"not-array","artifacts":[]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, err := ParseStructured([]byte(c.data))
			if err == nil {
				t.Fatalf("expected mismatch error, got %+v", result)
			}
			if !IsMismatchError(err) {
				t.Fatalf("error must be mismatch, got %v", err)
			}
			if IsConstraintError(err) {
				t.Fatalf("mismatch must not be constraint, got %v", err)
			}
		})
	}
}

func implementedResult() Result {
	return Result{
		Status:              StatusImplemented,
		Risk:                RiskLow,
		Summary:             "s",
		RequirementCoverage: "c",
		Tests:               "t",
		Unverified:          "none",
	}
}

func passResult() Result {
	return Result{
		Status:              StatusPass,
		Risk:                RiskLow,
		Summary:             "s",
		RequirementCoverage: "c",
		Invariants:          "i",
		TestEvidence:        "e",
		Issues:              "none",
		ResidualRisk:        "none",
		Targets:             []string{"a.go:f"},
	}
}

func TestValidateWorkerResultStatuses(t *testing.T) {
	decision := Result{
		Status:          StatusNeedsSolDecision,
		Risk:            RiskHigh,
		Decision:        "d",
		Evidence:        "e",
		Options:         "o",
		Recommendation:  "r",
		TestObligations: "t",
		// 旧WORKER.md「不要ならnone」の予約値sentinel。旧protocolの`TARGETS: none`値相当。
		Targets: []string{"none"},
	}
	if err := ValidateWorkerResult(implementedResult()); err != nil {
		t.Fatalf("implemented: %v", err)
	}
	if err := ValidateWorkerResult(decision); err != nil {
		t.Fatalf("decision: %v", err)
	}

	high := implementedResult()
	high.Risk = RiskHigh
	if err := ValidateWorkerResult(high); err != nil {
		t.Fatalf("implemented high risk: %v", err)
	}
}

func TestValidateWorkerResultRejections(t *testing.T) {
	cases := []struct {
		name           string
		mutate         func(*Result)
		want           string
		schemaMismatch bool
	}{
		// status enumはrole別schemaが保証するため、role外statusはschema違反としてfail closed。
		{"reviewer status", func(r *Result) { r.Status = StatusPass }, "worker結果のstatus", true},
		{"decision low risk", func(r *Result) {
			r.Status = StatusNeedsSolDecision
			r.Risk = RiskLow
		}, "NEEDS_SOL_DECISIONのrisk", false},
		{"decision no targets", func(r *Result) {
			r.Status = StatusNeedsSolDecision
			r.Risk = RiskHigh
			r.Decision = "d"
			r.Evidence = "e"
			r.Options = "o"
			r.Recommendation = "r"
			r.TestObligations = "t"
		}, "NEEDS_SOL_DECISIONのTARGETSは空", false},
		{"missing summary", func(r *Result) { r.Summary = " " }, "必須field SUMMARY", false},
		{"multiline field", func(r *Result) { r.Tests = "line1\nline2" }, "改行", false},
		{"oversize field", func(r *Result) { r.Summary = strings.Repeat("x", MaxFieldBytes+1) }, "bytes以内", false},
		{"unknown risk", func(r *Result) { r.Risk = Risk("MEDIUM") }, "LOWまたはHIGH", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := implementedResult()
			c.mutate(&result)
			err := ValidateWorkerResult(result)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if c.schemaMismatch {
				if !IsMismatchError(err) {
					t.Fatalf("error must be mismatch, got %v", err)
				}
			} else if !IsConstraintError(err) {
				t.Fatalf("error must be constraint, got %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %q, want substring %q", err.Error(), c.want)
			}
		})
	}
}

func TestValidateReviewerResultStatuses(t *testing.T) {
	if err := ValidateReviewerResult(passResult()); err != nil {
		t.Fatalf("pass: %v", err)
	}

	fix := passResult()
	fix.Status = StatusFixRequired
	fix.Risk = RiskHigh
	fix.Targets = []string{"a.go:f"}
	if err := ValidateReviewerResult(fix); err != nil {
		t.Fatalf("fix required: %v", err)
	}

	// 旧protocolのPASS/FIX_REQUIREDは`TARGETS: none`値を許容した。typed結果では
	// ["none"]要素がその表示に相当するため、空配列だけを拒否する。
	fixNone := fix
	fixNone.Targets = []string{"none"}
	if err := ValidateReviewerResult(fixNone); err != nil {
		t.Fatalf("fix required with none target: %v", err)
	}

	review := passResult()
	review.Status = StatusNeedsSolReview
	review.Risk = RiskHigh
	review.Targets = []string{"a.go:f"}
	review.SolQuestion = "q"
	if err := ValidateReviewerResult(review); err != nil {
		t.Fatalf("needs sol review: %v", err)
	}
}

func TestValidateReviewerResultRejections(t *testing.T) {
	cases := []struct {
		name           string
		mutate         func(*Result)
		want           string
		schemaMismatch bool
	}{
		{"worker status", func(r *Result) { r.Status = StatusImplemented }, "reviewer結果のstatus", true},
		{"pass high risk", func(r *Result) { r.Risk = RiskHigh }, "PASSのrisk", false},
		{"pass no targets", func(r *Result) { r.Targets = nil }, "PASSのTARGETSは空", false},
		{"fix no targets", func(r *Result) {
			r.Status = StatusFixRequired
			r.Targets = nil
		}, "FIX_REQUIREDのTARGETSは空", false},
		{"sol review low risk", func(r *Result) {
			r.Status = StatusNeedsSolReview
			r.Risk = RiskLow
		}, "NEEDS_SOL_REVIEWのrisk", false},
		{"sol review no targets", func(r *Result) {
			r.Status = StatusNeedsSolReview
			r.Risk = RiskHigh
			r.SolQuestion = "q"
			r.Targets = nil
		}, "TARGETSはnone", false},
		{"sol review none target", func(r *Result) {
			r.Status = StatusNeedsSolReview
			r.Risk = RiskHigh
			r.Targets = []string{"none"}
			r.SolQuestion = "q"
		}, "TARGETSはnone", false},
		{"sol review missing question", func(r *Result) {
			r.Status = StatusNeedsSolReview
			r.Risk = RiskHigh
			r.Targets = []string{"a.go"}
		}, "必須field SOL_QUESTION", false},
		{"missing invariants", func(r *Result) { r.Invariants = "" }, "必須field INVARIANTS", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := passResult()
			c.mutate(&result)
			err := ValidateReviewerResult(result)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if c.schemaMismatch {
				if !IsMismatchError(err) {
					t.Fatalf("error must be mismatch, got %v", err)
				}
			} else if !IsConstraintError(err) {
				t.Fatalf("error must be constraint, got %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %q, want substring %q", err.Error(), c.want)
			}
		})
	}
}

func TestValidateResultRejectsOversizeDisplay(t *testing.T) {
	result := implementedResult()
	result.Summary = strings.Repeat("x", 1520)
	result.RequirementCoverage = strings.Repeat("y", 1520)
	result.Tests = strings.Repeat("z", 1520)
	result.Unverified = strings.Repeat("w", 1520)
	err := ValidateWorkerResult(result)
	if err == nil || !strings.Contains(err.Error(), "結果全体") {
		t.Fatalf("err = %v", err)
	}
}

func TestResultDisplayLines(t *testing.T) {
	implemented := implementedResult()
	want := []string{
		"STATUS: IMPLEMENTED",
		"RISK: LOW",
		"SUMMARY: s",
		"REQUIREMENT_COVERAGE: c",
		"TESTS: t",
		"UNVERIFIED: none",
		"ARTIFACTS: none",
	}
	if got := implemented.DisplayLines(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("display = %v", got)
	}

	implemented.Targets = []string{"a.go", "b.go"}
	if !strings.Contains(implemented.Display(), "TARGETS: a.go;b.go") {
		t.Fatalf("targets not joined: %s", implemented.Display())
	}

	decision := Result{
		Status:          StatusNeedsSolDecision,
		Risk:            RiskHigh,
		Decision:        "d",
		Evidence:        "e",
		Options:         "o",
		Recommendation:  "r",
		TestObligations: "t",
	}
	decisionDisplay := decision.Display()
	for _, want := range []string{"DECISION: d", "TARGETS: none", "ARTIFACTS: none"} {
		if !strings.Contains(decisionDisplay, want) {
			t.Fatalf("decision display missing %q: %s", want, decisionDisplay)
		}
	}

	review := Result{
		Status:       StatusNeedsSolReview,
		Risk:         RiskHigh,
		Summary:      "s",
		TestEvidence: "e",
		Targets:      []string{"a.go"},
		SolQuestion:  "q",
		Invariants:   "i",
		Issues:       "n",
		ResidualRisk: "n",
	}
	reviewDisplay := review.DisplayLines()
	if reviewDisplay[len(reviewDisplay)-1] != "SOL_QUESTION: q" {
		t.Fatalf("SOL_QUESTION must render last: %v", reviewDisplay)
	}
}

func TestFromDisplayLinesRoundTrip(t *testing.T) {
	result := Result{
		Status:              StatusNeedsSolReview,
		Risk:                RiskHigh,
		Summary:             "s",
		RequirementCoverage: "c",
		Invariants:          "i",
		TestEvidence:        "e",
		Issues:              "none",
		ResidualRisk:        "r",
		Targets:             []string{"a.go:f", "b.go"},
		Artifacts:           []string{"/tmp/x"},
		SolQuestion:         "q",
	}
	parsed, err := FromDisplayLines(result.DisplayLines())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	encoded, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != string(want) {
		t.Fatalf("round trip mismatch:\n%s\n%s", encoded, want)
	}
}

func TestFromDisplayLinesRejectsBroken(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"no key", []string{"plain text"}, "KEY: value"},
		{"empty key", []string{": v"}, "KEYが空"},
		{"duplicate", []string{"STATUS: PASS", "STATUS: PASS"}, "重複"},
		{"no status", []string{"RISK: LOW"}, "STATUSがありません"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := FromDisplayLines(c.lines)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want substring %q", err, c.want)
			}
		})
	}
}

func TestIsReportOnlyFix(t *testing.T) {
	fix := Result{Status: StatusFixRequired, Targets: []string{ReportOnlyTargets}}
	if !IsReportOnlyFix(fix) {
		t.Fatal("reserved targets must be report-only")
	}
	fix.Targets = []string{ReportOnlyTargets, "other"}
	if IsReportOnlyFix(fix) {
		t.Fatal("mixed targets must not be report-only")
	}
	normal := Result{Status: StatusFixRequired, Targets: []string{"a.go"}}
	if IsReportOnlyFix(normal) {
		t.Fatal("normal targets must not be report-only")
	}
	pass := Result{Status: StatusPass, Targets: []string{ReportOnlyTargets}}
	if IsReportOnlyFix(pass) {
		t.Fatal("PASS must not be report-only")
	}
}

func TestRejectCategory(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&mismatchError{reason: "structured_outputをResultへ解析できません"}, "schema-mismatch"},
		{&constraintError{reason: "結果に必須field SUMMARYがありません"}, "missing-field"},
		{&constraintError{reason: "NEEDS_SOL_REVIEWのTARGETSはnoneにできません"}, "targets-none"},
		{&constraintError{reason: "FIX_REQUIREDのTARGETSは空にできません"}, "targets-none"},
		{&constraintError{reason: "NEEDS_SOL_DECISIONのTARGETSは空にできません"}, "targets-none"},
		{&constraintError{reason: "PASSのriskはLOWにしてください"}, "risk"},
		{&constraintError{reason: "reviewer結果のstatusとして許容されません"}, "status"},
		{&constraintError{reason: "ARTIFACTSのパスが重複しています"}, "artifacts"},
		{&constraintError{reason: "field SUMMARYに改行を含められません"}, "multiline-field"},
		{&constraintError{reason: "field SUMMARYは1536 bytes以内にしてください"}, "size"},
		{&constraintError{reason: "other"}, "other"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := RejectCategory(c.err); got != c.want {
			t.Fatalf("category = %q, want %q (%v)", got, c.want, c.err)
		}
	}
}
