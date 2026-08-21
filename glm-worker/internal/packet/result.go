// Package packetはstructured outputのtyped結果と、Sol表示への変換・意味検証を担う。
// model呼出の唯一のprotocolは--json-schemaで強制されるtyped structured outputであり、
// marker抽出・KEY行parser・重複/迷子marker検出のようなtext構造検査は持たない。
package packet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 表示・検証契約の上限。structured outputでは物理行数は意味を持たないため、
// Solが読む表示全体のbyte上限と1 field本文のbyte上限だけを圧縮規律として残す。
const (
	MaxPacketBytes     = 6 * 1024
	MaxFieldBytes      = 1536
	MaxDiagnosticBytes = 6 * 1024
)

// Statusはworkflow終端の種別。worker roleとreviewer roleで許容集合が異なる。
type Status string

const (
	StatusImplemented      Status = "IMPLEMENTED"
	StatusNeedsSolDecision Status = "NEEDS_SOL_DECISION"
	StatusPass             Status = "PASS"
	StatusFixRequired      Status = "FIX_REQUIRED"
	StatusNeedsSolReview   Status = "NEEDS_SOL_REVIEW"
)

// RiskはSol判断へ昇格すべき変更かの申告。意味整合はValidate*Resultが強制する。
type Risk string

const (
	RiskLow  Risk = "LOW"
	RiskHigh Risk = "HIGH"
)

// ReportOnlyTargetsはFIX_REQUIREDのTARGETS予約値。reviewerがコード・diffを正しいと
// 確認し報告の意味情報だけを不足と指摘するときに使い、productionは実装修正と
// 報告再出力をこの値だけで機械識別する。
const ReportOnlyTargets = "PACKET"

// Resultは1回のmodel呼出が返すtyped結果。worker/reviewer両roleで同じ構造を持ち、
// statusenumと意味検証でrole契約を強制する。未知の意味問題は各free text fieldへ
// 残り、構造はschemaとこの型が固定する。
type Result struct {
	Status              Status   `json:"status"`
	Risk                Risk     `json:"risk"`
	Summary             string   `json:"summary,omitempty"`
	RequirementCoverage string   `json:"requirement_coverage,omitempty"`
	Tests               string   `json:"tests,omitempty"`
	Unverified          string   `json:"unverified,omitempty"`
	Decision            string   `json:"decision,omitempty"`
	Evidence            string   `json:"evidence,omitempty"`
	Options             string   `json:"options,omitempty"`
	Recommendation      string   `json:"recommendation,omitempty"`
	TestObligations     string   `json:"test_obligations,omitempty"`
	Invariants          string   `json:"invariants,omitempty"`
	TestEvidence        string   `json:"test_evidence,omitempty"`
	Issues              string   `json:"issues,omitempty"`
	ResidualRisk        string   `json:"residual_risk,omitempty"`
	SolQuestion         string   `json:"sol_question,omitempty"`
	Targets             []string `json:"targets,omitempty"`
	Artifacts           []string `json:"artifacts,omitempty"`
}

// mismatchErrorはresult event契約・schema適合の破綻。modelの内容修正で回復できない
// 経路のため再依頼せずfail closedする。
type mismatchError struct {
	reason string
}

func (e *mismatchError) Error() string {
	return e.reason
}

// IsMismatchErrorはschema/result契約ミスマッチ(true)と意味検証不合格(false)を区別する。
func IsMismatchError(err error) bool {
	var target *mismatchError
	return errors.As(err, &target)
}

// ParseStructuredはresult eventのauthoritative structured_outputをtyped結果へ変換する。
// producer schemaはadditionalProperties未検証の語彙制限から未知propertyを許容するため、
// decoderも未知fieldを無害に無視して表示・stateへ伝播させない。既知fieldの型不一致と
// status欠落だけを契約ミスマッチとしてfail closedに分類し、必須性・status別意味制約は
// Validate*Resultが厳格に強制する。
func ParseStructured(data []byte) (Result, error) {
	if len(bytes.TrimSpace(data)) == 0 || string(bytes.TrimSpace(data)) == "null" {
		return Result{}, &mismatchError{reason: "result eventにstructured_outputがありません"}
	}
	var result Result
	if err := json.Unmarshal(data, &result); err != nil {
		return Result{}, &mismatchError{reason: fmt.Sprintf("structured_outputをResultへ解析できません: %v", err)}
	}
	if result.Status == "" {
		return Result{}, &mismatchError{reason: "structured_outputのstatusが空です"}
	}
	return result, nil
}

// displayKeyはtyped field名をSol表示のKEYへ対応付ける。表示順はrender順で固定する。
type displayField struct {
	key   string
	value string
}

// displayFieldsはstatus別の表示field順。旧PACKET表示の順序と意味を保持し、
// 空fieldは行を出さず、targets/artifactsは配列をセミコロン区切りへ直す。
func (r Result) displayFields() []displayField {
	text := func(key string, value string) displayField {
		return displayField{key: key, value: value}
	}
	switch r.Status {
	case StatusNeedsSolDecision:
		fields := []displayField{
			text("STATUS", string(r.Status)),
			text("RISK", string(r.Risk)),
			text("DECISION", r.Decision),
			text("EVIDENCE", r.Evidence),
			text("OPTIONS", r.Options),
			text("RECOMMENDATION", r.Recommendation),
			text("TEST_OBLIGATIONS", r.TestObligations),
		}
		fields = append(fields, r.targetsField())
		return append(fields, r.artifactsField())
	case StatusImplemented:
		fields := []displayField{
			text("STATUS", string(r.Status)),
			text("RISK", string(r.Risk)),
			text("SUMMARY", r.Summary),
			text("REQUIREMENT_COVERAGE", r.RequirementCoverage),
			text("TESTS", r.Tests),
			text("UNVERIFIED", r.Unverified),
		}
		if len(r.Targets) > 0 {
			fields = append(fields, r.targetsField())
		}
		return append(fields, r.artifactsField())
	default:
		fields := []displayField{
			text("STATUS", string(r.Status)),
			text("RISK", string(r.Risk)),
			text("SUMMARY", r.Summary),
			text("REQUIREMENT_COVERAGE", r.RequirementCoverage),
			text("INVARIANTS", r.Invariants),
			text("TEST_EVIDENCE", r.TestEvidence),
			text("ISSUES", r.Issues),
			text("RESIDUAL_RISK", r.ResidualRisk),
			r.targetsField(),
			r.artifactsField(),
		}
		if r.SolQuestion != "" {
			fields = append(fields, text("SOL_QUESTION", r.SolQuestion))
		}
		return fields
	}
}

func (r Result) targetsField() displayField {
	return displayField{key: "TARGETS", value: joinDisplayList(r.Targets)}
}

func (r Result) artifactsField() displayField {
	return displayField{key: "ARTIFACTS", value: joinDisplayList(r.Artifacts)}
}

func joinDisplayList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ";")
}

// DisplayLinesはSolへ出力する表示行を返す。
func (r Result) DisplayLines() []string {
	fields := r.displayFields()
	lines := make([]string, 0, len(fields))
	for _, field := range fields {
		lines = append(lines, field.key+": "+field.value)
	}
	return lines
}

// Displayは表示行を改行接続した文字列を返す。wrapperの最終stdoutと
// prompt埋め込み・state保存で使う単一の表示表現である。
func (r Result) Display() string {
	return strings.Join(r.DisplayLines(), "\n")
}

// ByteSizeはSol表示全体のbyte数。圧縮規律の検証に使う。
func (r Result) ByteSize() int {
	return len(r.Display())
}

// FromDisplayLinesは旧text PACKET形式で保存されたresume checkpointのworker報告を
// typed結果へ変換する。v2 checkpointのupgrade互換のためだけに存在し、
// model出力の受理経路には使わない。
func FromDisplayLines(lines []string) (Result, error) {
	fields := make(map[string]string, len(lines))
	for _, line := range lines {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return Result{}, fmt.Errorf("旧packet行をKEY: value形式へ解析できません: %q", line)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return Result{}, fmt.Errorf("旧packet行のKEYが空です: %q", line)
		}
		if _, exists := fields[key]; exists {
			return Result{}, fmt.Errorf("旧packet field %sが重複しています", key)
		}
		fields[key] = strings.TrimSpace(value)
	}
	if fields["STATUS"] == "" {
		return Result{}, fmt.Errorf("旧packetにSTATUSがありません")
	}
	result := Result{
		Status:              Status(fields["STATUS"]),
		Risk:                Risk(fields["RISK"]),
		Summary:             fields["SUMMARY"],
		RequirementCoverage: fields["REQUIREMENT_COVERAGE"],
		Tests:               fields["TESTS"],
		Unverified:          fields["UNVERIFIED"],
		Decision:            fields["DECISION"],
		Evidence:            fields["EVIDENCE"],
		Options:             fields["OPTIONS"],
		Recommendation:      fields["RECOMMENDATION"],
		TestObligations:     fields["TEST_OBLIGATIONS"],
		Invariants:          fields["INVARIANTS"],
		TestEvidence:        fields["TEST_EVIDENCE"],
		Issues:              fields["ISSUES"],
		ResidualRisk:        fields["RESIDUAL_RISK"],
		SolQuestion:         fields["SOL_QUESTION"],
	}
	if targets := splitDisplayList(fields["TARGETS"]); len(targets) > 0 {
		result.Targets = targets
	}
	if artifacts := splitDisplayList(fields["ARTIFACTS"]); len(artifacts) > 0 {
		result.Artifacts = artifacts
	}
	return result, nil
}

// splitDisplayListは表示のセミコロン区切りを配列へ戻す。"none"・空は要素なし扱い。
func splitDisplayList(value string) []string {
	if value == "" || value == "none" {
		return nil
	}
	parts := strings.Split(value, ";")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
