package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFailureEvidenceContractWiringは親Codex側runtime failure evidence contractの
// production routing配線を決定論検証する。codex/AGENTS.mdのrouting、
// glm-execution.mdの委譲前読込指示、glm-packets.mdの受理時指示、
// failure-evidence.md本文の必須契約文のいずれかが欠けると失敗する。
// 親Codexが委譲前にevidence条件を構成し受理時に関係範囲だけ確認する行動の証明ではなく、
// scenario corpusのfailure-evidence-*はwrapperのartifact packet/終端例への検証に限定され、
// 親orchestration行動の固定EvalはEVAL.mdの完了条件を待つ。
// worker/reviewer promptへ一般checklistを追加しない方針も本testで固定する。
func TestFailureEvidenceContractWiring(t *testing.T) {
	root := scenarioRepoRoot(t)

	readContractFile := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}

	cases := []struct {
		file string
		wire string
	}{
		{"codex/AGENTS.md", "原因不明runtime failureの最小evidence保存"},
		{"codex/AGENTS.md", "~/.codex/instructions/failure-evidence.md"},
		{"codex/instructions/glm-execution.md", "外部取得・parser・integration failureの原因診断にstatus・size・error分類だけでは足りない依頼は、`~/.codex/instructions/failure-evidence.md`を読んでから委譲内容を構成する"},
		{"codex/instructions/glm-packets.md", "`ARTIFACTS`参照先を`~/.codex/instructions/failure-evidence.md`の受理条件で必要範囲だけ確認する"},
		{"codex/instructions/failure-evidence.md", "根本原因または再現条件を判定できず、response本文・header・payload断片・parser入力等の実物が診断に必要な場合だけ"},
		{"codex/instructions/failure-evidence.md", "通常の十分診断可能なerror、成功応答、局所bugへ形式的なartifact保存を要求しない"},
		{"codex/instructions/failure-evidence.md", "全response・全成功応答の無条件保存はしない"},
		{"codex/instructions/failure-evidence.md", "再現に必要な最小範囲だけを切り出させ、巨大payloadや診断に不要な部分を保存させない"},
		{"codex/instructions/failure-evidence.md", "credential・token・cookie・session ID・個人情報等を除去または置換させる"},
		{"codex/instructions/failure-evidence.md", "秘密情報を生のまま保存させない"},
		{"codex/instructions/failure-evidence.md", "容量上限・retention/削除時期・access範囲を対象リスクに応じて明示する"},
		{"codex/instructions/failure-evidence.md", "診断に不要な長期保存をさせない"},
		{"codex/instructions/failure-evidence.md", "既存のtask artifact(`REPORT_ARTIFACT_DIR`・`ARTIFACTS`)だけとし、新しいstorageやtelemetry schemaを作らない"},
		{"codex/instructions/failure-evidence.md", "telemetryへ本文を混入させない"},
		{"codex/instructions/failure-evidence.md", "必要証拠・sanitization・保存先・retentionをtask固有条件としてUSER_REQUESTへ構成する"},
		{"codex/instructions/failure-evidence.md", "一般checklistをworker/reviewer promptへ追加しない"},
		{"codex/instructions/failure-evidence.md", "best-effort warningとしてpacketへ残させ、それだけでは本taskを失敗させない"},
		{"codex/instructions/failure-evidence.md", "「判定不能」としてSol/ユーザーへ戻し、推測で修正を重ねさせない"},
		{"codex/instructions/failure-evidence.md", "`ARTIFACTS`参照先を診断に必要な範囲だけ確認し、全内容をpacketや会話へ転載しない"},
	}
	contents := make(map[string]string, 4)
	for _, c := range cases {
		if _, ok := contents[c.file]; !ok {
			contents[c.file] = readContractFile(c.file)
		}
		if !strings.Contains(contents[c.file], c.wire) {
			t.Errorf("%s lacks failure evidence wiring: %q", c.file, c.wire)
		}
	}

	// 本contractは親Codex側の委譲・受理条件であり、常時checklistのworker/reviewer prompt
	// 追加で代替した実装になっていないことを固定する。
	for _, promptFile := range []string{"codex/glm-worker/prompts/WORKER.md", "codex/glm-worker/prompts/REVIEWER.md"} {
		if strings.Contains(readContractFile(promptFile), "failure-evidence") {
			t.Errorf("%s must not add a general failure evidence checklist", promptFile)
		}
	}
}
