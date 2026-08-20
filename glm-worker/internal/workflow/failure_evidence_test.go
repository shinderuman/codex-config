package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFailureEvidenceContractWiringは親Codex側runtime failure evidence contractの
// production routing配線と、親behavioral Eval入力・期待判断との因果を決定論検証する。
// codex/AGENTS.mdのrouting、glm-execution.mdの委譲前読込指示、glm-packets.mdの受理時指示、
// failure-evidence.md本文の必須契約文のいずれかが欠けると失敗する。EVAL.mdの親behavioral Evalは
// scripted scenarioの終端検証とは異なり親Codexの委譲/受理/差戻し行動の証明ではないため、
// その入力・期待判断がinstruction本文のどの契約文へ根拠を持つかを対で固定する。
// 親behavioral Evalの代替へfailure-evidence-*の重複scenarioがcorpusへ追加された場合へも失敗する。
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

	// 親behavioral Evalの期待判断(EVAL.md本節)がproduction guidanceのどの契約文へ根拠を
	// 持つかを対で検証する。EVAL.md側の文面だけ、instruction側の契約文だけの片側存在は通さない。
	section := evalFailureEvidenceSection(t, readContractFile("EVAL.md"))
	instruction := contents["codex/instructions/failure-evidence.md"]
	evalGrounds := []struct {
		eval     string
		guidance string
	}{
		{"status/size/error分類だけでは原因判定不能な外部取得・parser・integration failure依頼へ、委譲前に必要証拠・sanitization・保存先・retentionをtask固有条件としてUSER_REQUESTへ構成する", "委譲前に、必要証拠・sanitization・保存先・retentionをtask固有条件としてUSER_REQUESTへ構成する"},
		{"受理時に`ARTIFACTS`参照先を診断に必要な範囲だけ確認する", "packet受理時は`ARTIFACTS`参照先を診断に必要な範囲だけ確認し"},
		{"原因判定に本文等が必要なのにstatus/sizeだけを残して推測修正を重ねる完了報告を成立の根拠として受領せず、必要evidenceの保存または「判定不能」への差戻しを要求する", "原因判定に証拠が必須なのに取得不能な場合は「判定不能」としてSol/ユーザーへ戻し、推測で修正を重ねさせない"},
		{"evidence取得不能時は判定不能としてSol/ユーザーへ戻し、推測修正を続けさせない", "原因判定に証拠が必須なのに取得不能な場合は「判定不能」としてSol/ユーザーへ戻し、推測で修正を重ねさせない"},
		{"十分診断可能なerror・成功応答・局所bugへ形式的artifact保存を要求せず", "通常の十分診断可能なerror、成功応答、局所bugへ形式的なartifact保存を要求しない"},
		{"全responseの無条件保存も要求しない", "全response・全成功応答の無条件保存はしない"},
		{"親Codexの委譲内容・受理確認・差戻し判断をraw telemetry・task log・artifact実体等の一次証拠で照合する", "委譲前に、必要証拠・sanitization・保存先・retentionをtask固有条件としてUSER_REQUESTへ構成する"},
	}
	for _, g := range evalGrounds {
		if !strings.Contains(instruction, g.guidance) {
			t.Errorf("failure-evidence.md lacks guidance grounding %q", g.guidance)
		}
		if !strings.Contains(section, g.eval) {
			t.Errorf("EVAL.md failure evidence section lacks behavioral eval judgment grounded in guidance: %q", g.eval)
		}
	}

	// behavioral Eval・corpus参照の管理文面。scripted packetのARTIFACTS宣言を親Codexの委譲/受理/
	// 差戻し行動の証明としない限定と、未実行Evalの一次証拠・完了条件・実行条件をEVAL.mdへ残す。
	for _, wire := range []string{
		"TestFailureEvidenceContractWiring",
		"failure-evidence-minimal-sanitized-evidence-packet-returns-to-sol",
		"failure-evidence-unobtainable-evidence-returns-undecidable-to-sol",
		"failure-evidence-sufficient-classification-completes-without-artifact",
		"scripted packetのARTIFACTS宣言だけを親Codexの委譲/受理/差戻し行動の証明として採用しない",
		"親behavioral Evalの代替として重複scenarioをcorpusへ追加しない",
		"親Codexの委譲内容・受理確認・差戻し判断をraw telemetry・task log・artifact実体等の一次証拠で照合",
		"live model呼出しを要するためユーザーの明示指示後だけ実行し",
		"EVAL.md本節のpositive/negative caseと期待判断を`failure-evidence.md`の適用条件・保存契約・orchestration契約文へ直接突き合わせて検証",
	} {
		if !strings.Contains(section, wire) {
			t.Errorf("EVAL.md failure evidence section lacks eval wiring: %q", wire)
		}
	}

	// EVAL.mdが参照するcorpus entryとmanifest pinが実在すること。文面参照だけの自己充足を防ぐと
	// ともに、親behavioral Evalの代替へfailure-evidence-*の重複scenarioがcorpusへ追加された
	// 場合へ失敗させる。
	expectedIDs := []string{
		"failure-evidence-minimal-sanitized-evidence-packet-returns-to-sol",
		"failure-evidence-unobtainable-evidence-returns-undecidable-to-sol",
		"failure-evidence-sufficient-classification-completes-without-artifact",
	}
	sc, mf := loadCorpus(t)
	expectedSet := make(map[string]bool, len(expectedIDs))
	for _, id := range expectedIDs {
		expectedSet[id] = true
	}
	for _, s := range sc.Scenarios {
		if !strings.HasPrefix(s.ID, "failure-evidence-") {
			continue
		}
		if !expectedSet[s.ID] {
			t.Errorf("scenario %s must not duplicate the parent behavioral eval into the corpus", s.ID)
		}
	}
	for _, id := range expectedIDs {
		found := false
		for _, s := range sc.Scenarios {
			if s.ID == id {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("scenario corpus lacks %s referenced by EVAL.md", id)
		}
	}
	pinned := false
	for _, e := range mf.InstructionFiles {
		if e.Path == "codex/instructions/failure-evidence.md" {
			pinned = true
		}
	}
	if !pinned {
		t.Error("manifest.json must pin codex/instructions/failure-evidence.md")
	}

	// 本contractは親Codex側の委譲・受理条件であり、常時checklistのworker/reviewer prompt
	// 追加で代替した実装になっていないことを固定する。
	for _, promptFile := range []string{"codex/glm-worker/prompts/WORKER.md", "codex/glm-worker/prompts/REVIEWER.md"} {
		if strings.Contains(readContractFile(promptFile), "failure-evidence") {
			t.Errorf("%s must not add a general failure evidence checklist", promptFile)
		}
	}
}

func evalFailureEvidenceSection(t *testing.T, evalDoc string) string {
	t.Helper()
	const header = "## 原因不明runtime failureの最小evidence管理"
	start := strings.Index(evalDoc, header)
	if start < 0 {
		t.Fatalf("EVAL.md lacks section header %q", header)
	}
	rest := evalDoc[start+len(header):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}
