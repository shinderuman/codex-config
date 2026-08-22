package packet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// constraintErrorはschemaでは表現できない意味検証不合格。modelが内容を修正して
// 再出力すれば回復できるため、workflowは同一sessionで1回だけ修正再依頼できる。
type constraintError struct {
	reason string
}

func (e *constraintError) Error() string {
	return e.reason
}

// IsConstraintErrorは意味検証不合格(true)と契約ミスマッチ(false)を区別する。
func IsConstraintError(err error) bool {
	var target *constraintError
	return errors.As(err, &target)
}

// RejectCategoryは結果検証不合格のerrorを集計用の安定categoryへ分類する。
// 理由文字列のphrasingに依存するが、これらは検証関数内で固定済み。
func RejectCategory(err error) string {
	if err == nil {
		return ""
	}
	if IsMismatchError(err) {
		return "schema-mismatch"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "artifacts") || strings.Contains(msg, "artifact"):
		return "artifacts"
	case strings.Contains(msg, "改行"):
		return "multiline-field"
	case strings.Contains(msg, "bytes以内"):
		return "size"
	case strings.Contains(msg, "必須field"):
		return "missing-field"
	case strings.Contains(msg, "targets"):
		return "targets-none"
	case strings.Contains(msg, "risk"):
		return "risk"
	case strings.Contains(msg, "status"):
		return "status"
	default:
		return "other"
	}
}

// ValidateWorkerResultはworker role結果の意味契約を検証する。
func ValidateWorkerResult(result Result) error {
	switch result.Status {
	case StatusImplemented:
		if result.Risk != RiskLow && result.Risk != RiskHigh {
			return &constraintError{reason: fmt.Sprintf("riskはLOWまたはHIGHで指定してください: %q", string(result.Risk))}
		}
		if err := validateFields(result, workerImplementedFields); err != nil {
			return err
		}
		return validateTargets(result)
	case StatusNeedsSolDecision:
		if result.Risk != RiskHigh {
			return &constraintError{reason: "NEEDS_SOL_DECISIONのriskはHIGHにしてください"}
		}
		if err := validateFields(result, needsSolDecisionFields); err != nil {
			return err
		}
		return validateTargets(result)
	default:
		// status enumはrole別schemaが保証するため、ここへの到達はschema違反でありfail closed対象。
		return &mismatchError{reason: fmt.Sprintf("worker結果のstatusとして許容されません: %q", string(result.Status))}
	}
}

// ValidateReviewerResultはreviewer role結果の意味契約を検証する。
func ValidateReviewerResult(result Result) error {
	switch result.Status {
	case StatusPass:
		if result.Risk != RiskLow {
			return &constraintError{reason: "PASSのriskはLOWにしてください。高リスクならNEEDS_SOL_REVIEWを返してください"}
		}
		if err := validateFields(result, reviewerFields); err != nil {
			return err
		}
		return validateTargets(result)
	case StatusFixRequired:
		if result.Risk != RiskLow && result.Risk != RiskHigh {
			return &constraintError{reason: fmt.Sprintf("riskはLOWまたはHIGHで指定してください: %q", string(result.Risk))}
		}
		if err := validateFields(result, reviewerFields); err != nil {
			return err
		}
		return validateTargets(result)
	case StatusNeedsSolReview:
		if result.Risk != RiskHigh {
			return &constraintError{reason: "NEEDS_SOL_REVIEWのriskはHIGHにしてください"}
		}
		if err := validateFields(result, reviewerFields); err != nil {
			return err
		}
		if strings.TrimSpace(result.SolQuestion) == "" {
			return &constraintError{reason: "結果に必須field SOL_QUESTIONがありません"}
		}
		return validateTargets(result)
	default:
		// status enumはrole別schemaが保証するため、ここへの到達はschema違反でありfail closed対象。
		return &mismatchError{reason: fmt.Sprintf("reviewer結果のstatusとして許容されません: %q", string(result.Status))}
	}
}

func workerImplementedFields(result Result) []displayField {
	return []displayField{
		{key: "SUMMARY", value: result.Summary},
		{key: "REQUIREMENT_COVERAGE", value: result.RequirementCoverage},
		{key: "TESTS", value: result.Tests},
		{key: "UNVERIFIED", value: result.Unverified},
	}
}

func needsSolDecisionFields(result Result) []displayField {
	return []displayField{
		{key: "DECISION", value: result.Decision},
		{key: "EVIDENCE", value: result.Evidence},
		{key: "OPTIONS", value: result.Options},
		{key: "RECOMMENDATION", value: result.Recommendation},
		{key: "TEST_OBLIGATIONS", value: result.TestObligations},
	}
}

func reviewerFields(result Result) []displayField {
	return []displayField{
		{key: "SUMMARY", value: result.Summary},
		{key: "REQUIREMENT_COVERAGE", value: result.RequirementCoverage},
		{key: "INVARIANTS", value: result.Invariants},
		{key: "TEST_EVIDENCE", value: result.TestEvidence},
		{key: "ISSUES", value: result.Issues},
		{key: "RESIDUAL_RISK", value: result.ResidualRisk},
	}
}

// validateTargetsはTARGETS要素の正規形を強制する唯一のpredicateで、worker/reviewer
// 両roleの全statusが共有する。schema(array of string)で表現できない意味契約をここへ集約する:
//   - TARGETSを要求するstatus(worker NEEDS_SOL_DECISION・reviewer PASS/FIX_REQUIRED/
//     NEEDS_SOL_REVIEW)は配列長1以上。IMPLEMENTEDだけ旧契約どおり空配列を許す
//   - 各要素はTrimSpace後に空ではない(空要素・空白のみ要素の拒否)。具体対象の
//     外側空白自体は正規形違反としない
//   - 予約値noneは小文字厳密表現"none"の単独要素だけを対象なしsentinelの正規形とし、
//     NONE等の大小文字・前後空白variantは全statusで拒否し、具体対象との混在も
//     全statusで拒否する
//   - NEEDS_SOL_REVIEWはnone要素を1つでも含めたら拒否する(Solが読む対象を実質失う)
//   - 予約値PACKETはreviewer FIX_REQUIREDの報告再出力専用(IsReportOnlyFix)で、
//     大小文字・前後空白variantごと拒否し、厳密表現"PACKET"の単独要素としてだけ許す
//   - TrimSpace後に同一の要素重複は拒否する
func validateTargets(result Result) error {
	if len(result.Targets) == 0 {
		if result.Status == StatusImplemented {
			return nil
		}
		return &constraintError{reason: fmt.Sprintf("%sのTARGETSは空にできません: Solが読むべき最小対象をfile:symbol/行範囲で指定してください", string(result.Status))}
	}
	seen := make(map[string]struct{}, len(result.Targets))
	hasNone := false
	for _, element := range result.Targets {
		trimmed := strings.TrimSpace(element)
		if trimmed == "" {
			return &constraintError{reason: "TARGETSの要素は空・空白のみにできません: 具体対象または予約値none/PACKETを指定してください"}
		}
		if _, duplicate := seen[trimmed]; duplicate {
			return &constraintError{reason: "TARGETSの要素が重複しています: 各対象は1回だけ指定してください"}
		}
		seen[trimmed] = struct{}{}
		if strings.EqualFold(trimmed, noneTargetsSentinel) {
			if element != noneTargetsSentinel {
				return &constraintError{reason: "TARGETSの予約値noneは小文字厳密表現のnoneだけを要素にできます: 大小文字・空白の変形は使えません"}
			}
			hasNone = true
		}
		if strings.EqualFold(trimmed, ReportOnlyTargets) &&
			!(result.Status == StatusFixRequired && element == ReportOnlyTargets && len(result.Targets) == 1) {
			return &constraintError{reason: "TARGETSの予約値PACKETはFIX_REQUIREDの報告再出力専用です: 実装修正では具体対象を指定してください"}
		}
	}
	if hasNone {
		if len(result.Targets) > 1 {
			return &constraintError{reason: "TARGETSの予約値noneは具体対象と混在できません: 対象が概念的なときはnoneだけを要素にしてください"}
		}
		if result.Status == StatusNeedsSolReview {
			return &constraintError{reason: "NEEDS_SOL_REVIEWのTARGETSはnoneにできません: Solが読むべき最小対象をfile:symbol/行範囲で指定してください"}
		}
	}
	return nil
}

// validateFieldsはstatus別必須fieldの非空・改行なし・byte上限を検証する。
// 改行は表示の1 field 1行契約を壊すため意味検証で拒否する。
func validateFields(result Result, fields func(Result) []displayField) error {
	for _, field := range fields(result) {
		if strings.TrimSpace(field.value) == "" {
			return &constraintError{reason: fmt.Sprintf("結果に必須field %sがありません", field.key)}
		}
		if strings.ContainsAny(field.value, "\n\r") {
			return &constraintError{reason: fmt.Sprintf("field %sに改行を含められません: 複数事項は同じvalue内でセミコロン区切りにしてください", field.key)}
		}
		if len(field.value) > MaxFieldBytes {
			return &constraintError{reason: fmt.Sprintf("field %sは%d bytes以内にしてください", field.key, MaxFieldBytes)}
		}
	}
	for _, value := range append(append([]string(nil), result.Targets...), result.Artifacts...) {
		if strings.ContainsAny(value, "\n\r") {
			return &constraintError{reason: "TARGETS/ARTIFACTSの各要素に改行を含められません"}
		}
		if len(value) > MaxFieldBytes {
			return &constraintError{reason: fmt.Sprintf("TARGETS/ARTIFACTSの各要素は%d bytes以内にしてください", MaxFieldBytes)}
		}
	}
	if size := result.ByteSize(); size > MaxPacketBytes {
		return &constraintError{reason: fmt.Sprintf("結果全体は%d bytes以内にしてください: %d bytes", MaxPacketBytes, size)}
	}
	return nil
}

// IsReportOnlyFixはreviewer結果が報告再出力専用のTARGETS予約値かを判定する。
func IsReportOnlyFix(result Result) bool {
	return result.Status == StatusFixRequired && len(result.Targets) == 1 && result.Targets[0] == ReportOnlyTargets
}

// ValidateArtifactsはartifacts参照がtask専用root配下の実在通常ファイルだけを
// 指していることを検証する。空配列(none)は検証不要。
func ValidateArtifacts(artifacts []string, root string) error {
	if len(artifacts) == 0 {
		return nil
	}

	root = filepath.Clean(root)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return &constraintError{reason: fmt.Sprintf("artifact rootを確認できません: %v", err)}
	}
	seen := make(map[string]struct{})
	for _, path := range artifacts {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return &constraintError{reason: fmt.Sprintf("ARTIFACTSは正規化済み絶対パスを指定してください: %q", path)}
		}
		if !pathWithinRoot(root, path) {
			return &constraintError{reason: fmt.Sprintf("ARTIFACTSは現在taskのartifact dir配下だけを指定してください: %s", path)}
		}
		if _, exists := seen[path]; exists {
			return &constraintError{reason: fmt.Sprintf("ARTIFACTSのパスが重複しています: %s", path)}
		}
		seen[path] = struct{}{}

		info, err := os.Lstat(path)
		if err != nil {
			return &constraintError{reason: fmt.Sprintf("ARTIFACTSのファイルを確認できません: %s: %v", path, err)}
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return &constraintError{reason: fmt.Sprintf("ARTIFACTSは実在する通常ファイルだけを指定してください: %s", path)}
		}
		resolvedPath, err := filepath.EvalSymlinks(path)
		if err != nil || !pathWithinRoot(resolvedRoot, resolvedPath) {
			return &constraintError{reason: fmt.Sprintf("ARTIFACTSの解決先がartifact dir外です: %s", path)}
		}
	}
	return nil
}

func pathWithinRoot(root string, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
