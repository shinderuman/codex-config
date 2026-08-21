package packet

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// 必須fieldの意味対応は、旧text protocolのstatus別requiredFields(packet.go@22c1d0b^)を
// typed結果契約へ写したものである。TARGETSを要求した4 STATUS(worker NEEDS_SOL_DECISIONと
// reviewer PASS/FIX_REQUIRED/NEEDS_SOL_REVIEW)すべてで非空を要求し、旧WORKER.mdの
// 「不要ならnone」sentinelは要素"none"として維持する。IMPLEMENTEDだけ旧契約どおり対象なしを許す。
type requiredFieldCase struct {
	key   string
	blank func(*Result)
	want  string
	// mismatchはschema保証違反としてfail closed分類になるfield(STATUS等)を区別する。
	mismatch bool
}

type statusContract struct {
	name     string
	valid    Result
	validate func(Result) error
	required []requiredFieldCase
}

func statusContracts() []statusContract {
	worker := func(result Result) error { return ValidateWorkerResult(result) }
	reviewer := func(result Result) error { return ValidateReviewerResult(result) }
	return []statusContract{
		{
			name:     "worker IMPLEMENTED",
			valid:    implementedResult(),
			validate: worker,
			required: []requiredFieldCase{
				{"STATUS", func(r *Result) { r.Status = "" }, "worker結果のstatus", true},
				{"RISK", func(r *Result) { r.Risk = "" }, "LOWまたはHIGH", false},
				{"SUMMARY", func(r *Result) { r.Summary = " " }, "必須field SUMMARY", false},
				{"REQUIREMENT_COVERAGE", func(r *Result) { r.RequirementCoverage = "" }, "必須field REQUIREMENT_COVERAGE", false},
				{"TESTS", func(r *Result) { r.Tests = "" }, "必須field TESTS", false},
				{"UNVERIFIED", func(r *Result) { r.Unverified = "" }, "必須field UNVERIFIED", false},
			},
		},
		{
			name: "worker NEEDS_SOL_DECISION",
			valid: Result{
				Status:          StatusNeedsSolDecision,
				Risk:            RiskHigh,
				Decision:        "d",
				Evidence:        "e",
				Options:         "o",
				Recommendation:  "r",
				TestObligations: "t",
				Targets:         []string{"glm-worker/internal/packet/validate.go:ValidateWorkerResult"},
			},
			validate: worker,
			required: []requiredFieldCase{
				{"STATUS", func(r *Result) { r.Status = "" }, "worker結果のstatus", true},
				{"RISK", func(r *Result) { r.Risk = RiskLow }, "NEEDS_SOL_DECISIONのrisk", false},
				{"DECISION", func(r *Result) { r.Decision = "" }, "必須field DECISION", false},
				{"EVIDENCE", func(r *Result) { r.Evidence = "" }, "必須field EVIDENCE", false},
				{"OPTIONS", func(r *Result) { r.Options = "" }, "必須field OPTIONS", false},
				{"RECOMMENDATION", func(r *Result) { r.Recommendation = "" }, "必須field RECOMMENDATION", false},
				{"TEST_OBLIGATIONS", func(r *Result) { r.TestObligations = "" }, "必須field TEST_OBLIGATIONS", false},
				{"TARGETS", func(r *Result) { r.Targets = nil }, "NEEDS_SOL_DECISIONのTARGETSは空", false},
			},
		},
		{
			name:     "reviewer PASS",
			valid:    passResult(),
			validate: reviewer,
			required: []requiredFieldCase{
				{"STATUS", func(r *Result) { r.Status = "" }, "reviewer結果のstatus", true},
				{"RISK", func(r *Result) { r.Risk = RiskHigh }, "PASSのrisk", false},
				{"SUMMARY", func(r *Result) { r.Summary = "" }, "必須field SUMMARY", false},
				{"REQUIREMENT_COVERAGE", func(r *Result) { r.RequirementCoverage = "" }, "必須field REQUIREMENT_COVERAGE", false},
				{"INVARIANTS", func(r *Result) { r.Invariants = "" }, "必須field INVARIANTS", false},
				{"TEST_EVIDENCE", func(r *Result) { r.TestEvidence = "" }, "必須field TEST_EVIDENCE", false},
				{"ISSUES", func(r *Result) { r.Issues = "" }, "必須field ISSUES", false},
				{"RESIDUAL_RISK", func(r *Result) { r.ResidualRisk = "" }, "必須field RESIDUAL_RISK", false},
				{"TARGETS", func(r *Result) { r.Targets = nil }, "PASSのTARGETSは空", false},
			},
		},
		{
			name: "reviewer FIX_REQUIRED",
			valid: func() Result {
				fix := passResult()
				fix.Status = StatusFixRequired
				fix.Risk = RiskHigh
				return fix
			}(),
			validate: reviewer,
			required: []requiredFieldCase{
				{"STATUS", func(r *Result) { r.Status = "" }, "reviewer結果のstatus", true},
				{"RISK", func(r *Result) { r.Risk = "" }, "LOWまたはHIGH", false},
				{"SUMMARY", func(r *Result) { r.Summary = "" }, "必須field SUMMARY", false},
				{"REQUIREMENT_COVERAGE", func(r *Result) { r.RequirementCoverage = "" }, "必須field REQUIREMENT_COVERAGE", false},
				{"INVARIANTS", func(r *Result) { r.Invariants = "" }, "必須field INVARIANTS", false},
				{"TEST_EVIDENCE", func(r *Result) { r.TestEvidence = "" }, "必須field TEST_EVIDENCE", false},
				{"ISSUES", func(r *Result) { r.Issues = "" }, "必須field ISSUES", false},
				{"RESIDUAL_RISK", func(r *Result) { r.ResidualRisk = "" }, "必須field RESIDUAL_RISK", false},
				{"TARGETS", func(r *Result) { r.Targets = nil }, "FIX_REQUIREDのTARGETSは空", false},
			},
		},
		{
			name: "reviewer NEEDS_SOL_REVIEW",
			valid: func() Result {
				review := passResult()
				review.Status = StatusNeedsSolReview
				review.Risk = RiskHigh
				review.SolQuestion = "q"
				return review
			}(),
			validate: reviewer,
			required: []requiredFieldCase{
				{"STATUS", func(r *Result) { r.Status = "" }, "reviewer結果のstatus", true},
				{"RISK", func(r *Result) { r.Risk = RiskLow }, "NEEDS_SOL_REVIEWのrisk", false},
				{"SUMMARY", func(r *Result) { r.Summary = "" }, "必須field SUMMARY", false},
				{"REQUIREMENT_COVERAGE", func(r *Result) { r.RequirementCoverage = "" }, "必須field REQUIREMENT_COVERAGE", false},
				{"INVARIANTS", func(r *Result) { r.Invariants = "" }, "必須field INVARIANTS", false},
				{"TEST_EVIDENCE", func(r *Result) { r.TestEvidence = "" }, "必須field TEST_EVIDENCE", false},
				{"ISSUES", func(r *Result) { r.Issues = "" }, "必須field ISSUES", false},
				{"RESIDUAL_RISK", func(r *Result) { r.ResidualRisk = "" }, "必須field RESIDUAL_RISK", false},
				{"TARGETS", func(r *Result) { r.Targets = nil }, "TARGETSはnone", false},
				{"SOL_QUESTION", func(r *Result) { r.SolQuestion = "" }, "必須field SOL_QUESTION", false},
			},
		},
	}
}

// TestStatusRequiredFieldsCorrespondenceは旧status別requiredFieldsの意味対応を
// table-drivenのnegative/positiveで固定する。ARTIFACTSの行存在契約はschema requiredが担うため
// TestProducerSchemaConsumerAcceptance側で検証する。
func TestStatusRequiredFieldsCorrespondence(t *testing.T) {
	for _, contract := range statusContracts() {
		t.Run(contract.name, func(t *testing.T) {
			if err := contract.validate(contract.valid); err != nil {
				t.Fatalf("positive result rejected: %v", err)
			}
			for _, field := range contract.required {
				t.Run(field.key, func(t *testing.T) {
					blanked := contract.valid
					field.blank(&blanked)
					err := contract.validate(blanked)
					if err == nil {
						t.Fatalf("%sを空にしても受理されました", field.key)
					}
					if field.mismatch && !IsMismatchError(err) {
						t.Fatalf("error must be mismatch, got %v", err)
					}
					if !field.mismatch && !IsConstraintError(err) {
						t.Fatalf("error must be constraint, got %v", err)
					}
					if !strings.Contains(err.Error(), field.want) {
						t.Fatalf("err = %q, want substring %q", err.Error(), field.want)
					}
				})
			}
		})
	}
}

// TestWorkerDecisionTargetsNoneSentinelは旧WORKER.mdのTARGETS契約
// 「現物確認が必要ならfile:symbol等。不要ならnone」の予約値noneを要素として維持する。
// 対象が概念的でfile targetを持たないNEEDS_SOL_DECISIONもこの既存sentinelだけで表現し、
// 新しい自由文fallbackは追加しない。
func TestWorkerDecisionTargetsNoneSentinel(t *testing.T) {
	decision := Result{
		Status:          StatusNeedsSolDecision,
		Risk:            RiskHigh,
		Decision:        "d",
		Evidence:        "e",
		Options:         "o",
		Recommendation:  "r",
		TestObligations: "t",
		Targets:         []string{"none"},
	}
	if err := ValidateWorkerResult(decision); err != nil {
		t.Fatalf("予約値none要素のNEEDS_SOL_DECISIONは旧契約どおり有効: %v", err)
	}
}

func resultJSONFieldNames(t *testing.T) map[string]bool {
	t.Helper()
	typ := reflect.TypeOf(Result{})
	fields := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			fields[name] = true
		}
	}
	return fields
}

func schemaObjectNodes(node map[string]any) []map[string]any {
	nodes := []map[string]any{node}
	if properties, ok := node["properties"].(map[string]any); ok {
		for _, raw := range properties {
			if child, ok := raw.(map[string]any); ok && child["type"] == "object" {
				nodes = append(nodes, schemaObjectNodes(child)...)
			}
		}
	}
	return nodes
}

func schemaPropertyNames(node map[string]any) []string {
	properties, _ := node["properties"].(map[string]any)
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	return names
}

func schemaRequiredNames(t *testing.T, node map[string]any) []string {
	t.Helper()
	rawRequired, _ := node["required"].([]any)
	names := make([]string, 0, len(rawRequired))
	for _, raw := range rawRequired {
		name, ok := raw.(string)
		if !ok {
			t.Fatalf("requiredに非string要素: %v", raw)
		}
		names = append(names, name)
	}
	return names
}

// TestProducerSchemaConsumerAcceptanceはproducer JSON Schemaとconsumer
// (ParseStructured+Validate*)の受理集合の合成を固定する。schemaは未検証語彙の
// additionalPropertiesを使わないため未知propertyを許容し、consumerは未知fieldを
// 無害に無視する。既知fieldの型・必須・status別意味制約は厳格に維持する。
func TestProducerSchemaConsumerAcceptance(t *testing.T) {
	knownFields := resultJSONFieldNames(t)
	for name, build := range map[string]func() (string, error){
		"worker":   WorkerSchemaJSON,
		"reviewer": ReviewerSchemaJSON,
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := build()
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			var schema map[string]any
			if err := json.Unmarshal([]byte(encoded), &schema); err != nil {
				t.Fatalf("schema json: %v", err)
			}
			for _, node := range schemaObjectNodes(schema) {
				if _, ok := node["additionalProperties"]; ok {
					t.Fatal("schemaは未検証語彙のadditionalPropertiesを使ってはいけません")
				}
				for _, property := range schemaPropertyNames(node) {
					if !knownFields[property] {
						t.Fatalf("schema property %qはconsumerのResult fieldへ対応していません", property)
					}
				}
			}
		})
	}

	t.Run("reviewer required mirrors old presence contract", func(t *testing.T) {
		encoded, err := ReviewerSchemaJSON()
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		var schema map[string]any
		if err := json.Unmarshal([]byte(encoded), &schema); err != nil {
			t.Fatalf("schema json: %v", err)
		}
		required := strings.Join(schemaRequiredNames(t, schema), ",")
		if required != "status,risk,targets,artifacts" {
			t.Fatalf("reviewer required = %q, want status,risk,targets,artifacts", required)
		}
	})
	t.Run("worker required mirrors old presence contract", func(t *testing.T) {
		encoded, err := WorkerSchemaJSON()
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		var schema map[string]any
		if err := json.Unmarshal([]byte(encoded), &schema); err != nil {
			t.Fatalf("schema json: %v", err)
		}
		required := strings.Join(schemaRequiredNames(t, schema), ",")
		if required != "status,risk,targets,artifacts" {
			t.Fatalf("worker required = %q, want status,risk,targets,artifacts", required)
		}
	})
}

// worker/reviewer schema双方のrequired keyを過不足なく含む正例JSON。schema適合出力が
// consumerへ受理されることだけを検証するため、omitemptyで省略されうる空配列も明示する。
const (
	workerStructuredFixture   = `{"status":"IMPLEMENTED","risk":"LOW","summary":"s","requirement_coverage":"c","tests":"t","unverified":"none","targets":[],"artifacts":[]}`
	reviewerStructuredFixture = `{"status":"PASS","risk":"LOW","summary":"s","requirement_coverage":"c","invariants":"i","test_evidence":"e","issues":"none","residual_risk":"none","targets":["a.go:f"],"artifacts":[]}`
)

// TestParseStructuredIgnoresSchemaPermittedUnknownFieldsはproducer schemaが許容する
// 未知fieldをconsumerが無害に無視し、表示へ伝播しないことを検証する。
func TestParseStructuredIgnoresSchemaPermittedUnknownFields(t *testing.T) {
	cases := []struct {
		name     string
		base     string
		validate func(Result) error
	}{
		{"worker top level", workerStructuredFixture, ValidateWorkerResult},
		{"reviewer top level", reviewerStructuredFixture, ValidateReviewerResult},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := strings.Replace(c.base, "{", `{"untracked_field":"value",`, 1)
			result, err := ParseStructured([]byte(data))
			if err != nil {
				t.Fatalf("schema許容の未知fieldで拒否されました: %v", err)
			}
			if err := c.validate(result); err != nil {
				t.Fatalf("意味検証不合格: %v", err)
			}
			if strings.Contains(result.Display(), "untracked_field") || strings.Contains(result.Display(), "value") {
				t.Fatalf("未知fieldが表示へ伝播しています: %s", result.Display())
			}
		})
	}
}

// TestParseStructuredKeepsKnownFieldStrictnessは未知fieldの無視と引き換えに
// 既知fieldの構造検証を緩めないことを検証する。
func TestParseStructuredKeepsKnownFieldStrictness(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"string field as number", `{"status":"IMPLEMENTED","risk":"LOW","summary":3,"artifacts":[]}`},
		{"array field as string", `{"status":"IMPLEMENTED","risk":"LOW","targets":"a.go","artifacts":[]}`},
		{"array item as object", `{"status":"IMPLEMENTED","risk":"LOW","targets":[{"file":"a.go"}],"artifacts":[]}`},
		{"risk as number", `{"status":"IMPLEMENTED","risk":1,"artifacts":[]}`},
		{"trailing garbage", `{"status":"IMPLEMENTED","risk":"LOW","artifacts":[]} trailing`},
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
		})
	}
}

// TestConsumerBackstopForReviewerTargetsKeyはschema requiredをproviderが破った
// structured_output(targets key欠落)でもconsumer側意味検証が拒否することを検証する。
func TestConsumerBackstopForReviewerTargetsKey(t *testing.T) {
	data := strings.Replace(reviewerStructuredFixture, `"targets":["a.go:f"],`, "", 1)
	result, err := ParseStructured([]byte(data))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	err = ValidateReviewerResult(result)
	if err == nil || !IsConstraintError(err) || !strings.Contains(err.Error(), "PASSのTARGETSは空") {
		t.Fatalf("err = %v, want constraint TARGETS拒否", err)
	}
}

// TestConsumerBackstopForWorkerTargetsKeyはworker側でもtargets key欠落・空配列の
// NEEDS_SOL_DECISIONをconsumer側意味検証が拒否することを検証する(schemaはrole共通のため
// IMPLEMENTEDの空配列は引き続き受理する)。
func TestConsumerBackstopForWorkerTargetsKey(t *testing.T) {
	decision := `{"status":"NEEDS_SOL_DECISION","risk":"HIGH","decision":"d","evidence":"e","options":"o","recommendation":"r","test_obligations":"t","artifacts":[]}`
	result, err := ParseStructured([]byte(decision))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	err = ValidateWorkerResult(result)
	if err == nil || !IsConstraintError(err) || !strings.Contains(err.Error(), "NEEDS_SOL_DECISIONのTARGETSは空") {
		t.Fatalf("err = %v, want constraint TARGETS拒否", err)
	}
	empty := `{"status":"NEEDS_SOL_DECISION","risk":"HIGH","decision":"d","evidence":"e","options":"o","recommendation":"r","test_obligations":"t","targets":[],"artifacts":[]}`
	parsed, err := ParseStructured([]byte(empty))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	err = ValidateWorkerResult(parsed)
	if err == nil || !IsConstraintError(err) {
		t.Fatalf("err = %v, want constraint TARGETS拒否", err)
	}
	implemented, err := ParseStructured([]byte(workerStructuredFixture))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if err := ValidateWorkerResult(implemented); err != nil {
		t.Fatalf("IMPLEMENTEDの空targetsは旧契約どおり有効: %v", err)
	}
}

// TestStructuredStatusPositivesは全statusのschema適合かつ意味検証合格の正例を
// producer出力から最終受理まで通す。schemaが要求する必須keyを全て含む形で与える。
func TestStructuredStatusPositives(t *testing.T) {
	cases := []struct {
		name     string
		data     string
		validate func(Result) error
	}{
		{"IMPLEMENTED", workerStructuredFixture, ValidateWorkerResult},
		{"NEEDS_SOL_DECISION", `{"status":"NEEDS_SOL_DECISION","risk":"HIGH","decision":"d","evidence":"e","options":"o","recommendation":"r","test_obligations":"t","targets":["none"],"artifacts":[]}`, ValidateWorkerResult},
		{"PASS", reviewerStructuredFixture, ValidateReviewerResult},
		{"FIX_REQUIRED", `{"status":"FIX_REQUIRED","risk":"HIGH","summary":"s","requirement_coverage":"c","invariants":"i","test_evidence":"e","issues":"i","residual_risk":"r","targets":["a.go:f"],"artifacts":[]}`, ValidateReviewerResult},
		{"NEEDS_SOL_REVIEW", `{"status":"NEEDS_SOL_REVIEW","risk":"HIGH","summary":"s","requirement_coverage":"c","invariants":"i","test_evidence":"e","issues":"i","residual_risk":"r","targets":["a.go:f"],"artifacts":[],"sol_question":"q"}`, ValidateReviewerResult},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			parsed, err := ParseStructured([]byte(c.data))
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if err := c.validate(parsed); err != nil {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
