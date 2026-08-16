package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEscapedCauseLayerContractWiringはescaped bug/review原因層分類contractの
// production routing配線と、親behavioral Eval入力・期待判断との因果を決定論検証する。
// codex/AGENTS.mdのrouting、glm-execution.mdの委譲前読込指示、escaped-cause-layer.md本文の
// 必須契約文のいずれかが欠けると失敗する。EVAL.mdの親behavioral Evalはscripted scenarioの
// 終端検証とは異なり親Codexの分類行動の証明ではないため、その入力・期待判断が
// instruction本文のどの契約文へ根拠を持つかを対で固定する。
// worker/reviewer promptへ本checklistを追加しない方針も本testで固定する。
func TestEscapedCauseLayerContractWiring(t *testing.T) {
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
		{"codex/AGENTS.md", "escaped bug/reviewの原因層分類"},
		{"codex/AGENTS.md", "~/.codex/instructions/escaped-cause-layer.md"},
		{"codex/instructions/glm-execution.md", "外部review・実運用で見つかったescaped bug・escaped reviewの原因分析を委譲する場合は、`~/.codex/instructions/escaped-cause-layer.md`を読んでから委譲内容を構成する"},
		{"codex/instructions/escaped-cause-layer.md", "escaped bug・escaped reviewの原因分析を開始する場合だけ適用する"},
		{"codex/instructions/escaped-cause-layer.md", "通常の実装・調査task、review通過時の通常確認、新規依頼の受け付けへこの分類を要求しない"},
		{"codex/instructions/escaped-cause-layer.md", "対策・prompt変更・gate追加の検討より先に"},
		{"codex/instructions/escaped-cause-layer.md", "production code・prompt・PACKET契約・raw telemetry/log・Git履歴等の一次証拠から"},
		{"codex/instructions/escaped-cause-layer.md", "内部のworker/reviewer pipeline失敗"},
		{"codex/instructions/escaped-cause-layer.md", "親Codex orchestration失敗: critical assumptionの確定、親USER_REQUEST lifecycle、runtime evidence管理、semantic deltaに基づくreview invocation"},
		{"codex/instructions/escaped-cause-layer.md", "一次証拠で層が確定できない場合は推測で確定させず"},
		{"codex/instructions/escaped-cause-layer.md", "worker/reviewer promptへの個別checklist追加や個別gate追加だけで解決扱いにしない"},
		{"codex/instructions/escaped-cause-layer.md", "worker/reviewer prompt・個別gate・新しい対策を追加する前に、原因が本当にその層で発生したかを分類結果と照合する"},
		{"codex/instructions/escaped-cause-layer.md", "既存対策が直接対応している原因へ、重複する新しい対策を追加しない"},
		{"codex/instructions/escaped-cause-layer.md", "期待終端の再現だけでは採用根拠としない"},
		{"codex/instructions/escaped-cause-layer.md", "productionのprompt・dispatch分岐と実際に渡す内容・期待判断の因果を別testで固定する"},
		{"codex/instructions/escaped-cause-layer.md", "原因層の分類と対策方向の最終判断は親Codexが行い、GLMだけで確定させない"},
	}
	contents := make(map[string]string, 3)
	for _, c := range cases {
		if _, ok := contents[c.file]; !ok {
			contents[c.file] = readContractFile(c.file)
		}
		if !strings.Contains(contents[c.file], c.wire) {
			t.Errorf("%s lacks escaped cause layer wiring: %q", c.file, c.wire)
		}
	}

	// 親behavioral Evalの期待判断(EVAL.md)がproduction guidanceのどの契約文へ根拠を持つかを
	// 対で検証する。EVAL.md側の文面だけ、instruction側の契約文だけの片側存在は通さない。
	evalDoc := readContractFile("EVAL.md")
	instruction := contents["codex/instructions/escaped-cause-layer.md"]
	evalGrounds := []struct {
		eval     string
		guidance string
	}{
		{"escaped bug・escaped reviewの原因分析を開始する場合だけ適用する親Codex orchestration contract", "escaped bug・escaped reviewの原因分析を開始する場合だけ適用する"},
		{"通常task・全reviewへ常時要求する重いgateにせず", "通常の実装・調査task、review通過時の通常確認、新規依頼の受け付けへこの分類を要求しない"},
		{"内部のworker/reviewer pipeline失敗か親Codex orchestration失敗かを先に分類する", "対策・prompt変更・gate追加の検討より先に"},
		{"親側contractで対策し、worker/reviewerチェック増殖や既存対策へ重複する第5・第6対策で解決扱いにしない", "worker/reviewer promptへの個別checklist追加や個別gate追加だけで解決扱いにしない"},
		{"受理した層分類に基づき親側原因なら親contract対策を選択してworker/reviewer prompt・個別gate・重複する新対策の追加を拒否する", "worker/reviewer prompt・個別gate・新しい対策を追加する前に、原因が本当にその層で発生したかを分類結果と照合する"},
		{"`glm-worker`内部pipeline失敗なら通常の直接修正pathへ移す", "内部のworker/reviewer pipeline失敗"},
		{"通常task・review通過確認へ層分類を要求しない", "通常の実装・調査task、review通過時の通常確認、新規依頼の受け付けへこの分類を要求しない"},
	}
	for _, g := range evalGrounds {
		if !strings.Contains(instruction, g.guidance) {
			t.Errorf("escaped-cause-layer.md lacks guidance grounding %q", g.guidance)
		}
		if !strings.Contains(evalDoc, g.eval) {
			t.Errorf("EVAL.md lacks behavioral eval judgment grounded in guidance: %q", g.eval)
		}
	}

	// behavioral Eval・corpus参照の管理文面。scripted終端検証を親行動の証明としない限定と、
	// 未実行Evalの完了条件・実行条件をEVAL.mdへ残す。
	for _, wire := range []string{
		"TestEscapedCauseLayerContractWiring",
		"escaped-cause-layer-parent-orchestration-cause-returns-to-sol",
		"escaped-cause-layer-worker-pipeline-cause-fix-returns-to-sol-review",
		"escaped-cause-layer-unrelated-normal-task-completes",
		"親Codexが分類を行う行動の証明ではない",
		"親Codexの分類判断・委譲内容・対策選択をraw telemetry・task log等の一次証拠で照合できる検証形態が整備されること",
		"live model呼出しを要するためユーザーの明示指示後だけ実行し",
		"EVAL.md本節のpositive/negative caseと期待判断を`escaped-cause-layer.md`の適用条件・層定義・対策層整合の契約文へ直接突き合わせて検証する",
	} {
		if !strings.Contains(evalDoc, wire) {
			t.Errorf("EVAL.md lacks escaped cause layer eval wiring: %q", wire)
		}
	}

	// EVAL.mdが参照するcorpus entryとmanifest pinが実在すること。文面参照だけの
	// 自己充足を防ぎ、参照先がvalidateCorpusの対象へ入っていることを固定する。
	sc, mf := loadCorpus(t)
	corpusIDs := make(map[string]bool, len(sc.Scenarios))
	for _, s := range sc.Scenarios {
		corpusIDs[s.ID] = true
	}
	for _, id := range []string{
		"escaped-cause-layer-parent-orchestration-cause-returns-to-sol",
		"escaped-cause-layer-worker-pipeline-cause-fix-returns-to-sol-review",
		"escaped-cause-layer-unrelated-normal-task-completes",
	} {
		if !corpusIDs[id] {
			t.Errorf("scenario corpus lacks %s referenced by EVAL.md", id)
		}
	}
	pinned := false
	for _, e := range mf.InstructionFiles {
		if e.Path == "codex/instructions/escaped-cause-layer.md" {
			pinned = true
		}
	}
	if !pinned {
		t.Error("manifest.json must pin codex/instructions/escaped-cause-layer.md")
	}

	// 本contractは親Codex側の分類・委譲条件であり、常時checklistのworker/reviewer prompt
	// 追加で代替した実装になっていないことを固定する。
	for _, promptFile := range []string{"codex/glm-worker/prompts/WORKER.md", "codex/glm-worker/prompts/REVIEWER.md"} {
		if strings.Contains(readContractFile(promptFile), "escaped-cause-layer") {
			t.Errorf("%s must not add an escaped cause layer checklist", promptFile)
		}
	}
}
