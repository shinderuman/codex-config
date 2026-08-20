package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTaskLifecycleContractWiringは親Codex側lifecycle contractのproduction routing配線と、
// 親behavioral Eval入力・期待判断との因果を決定論検証する。codex/AGENTS.mdのrouting、
// glm-execution.mdのpacket受理後読込指示、task-lifecycle.md本文の必須契約文のいずれかが
// 欠けると失敗する。EVAL.mdの親behavioral Evalはscripted scenarioの終端検証とは異なり
// 親Codexの再評価・継続行動の証明ではないため、その入力・期待判断がinstruction本文の
// どの契約文へ根拠を持つかを対で固定する。worker/reviewer promptへlifecycle checklistを
// 追加しない方針も本testで固定する。
func TestTaskLifecycleContractWiring(t *testing.T) {
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
		{"codex/AGENTS.md", "安全停止・子task終端と親USER_REQUEST完了の区別"},
		{"codex/AGENTS.md", "~/.codex/instructions/task-lifecycle.md"},
		{"codex/instructions/glm-execution.md", "packet受理・個別commit・install完了は局所終端であり、親USER_REQUESTの完了か次の継続操作かは`~/.codex/instructions/task-lifecycle.md`を読んで判断する"},
		{"codex/instructions/task-lifecycle.md", "scheduler停止・queue/checkpoint保全・alarm報告の完了は局所終端である"},
		{"codex/instructions/task-lifecycle.md", "task・review・commit・installの個別完了は局所終端である"},
		{"codex/instructions/task-lifecycle.md", "親依頼本体と、ユーザー・automationが明示的に継続対象とした実装計画範囲の未解決作業がすべて解消した時だけ"},
		{"codex/instructions/task-lifecycle.md", "原因修正・再開確認・後続改善等が残るなら、同じCodexタスクで次の操作へ継続する"},
		{"codex/instructions/task-lifecycle.md", "monitorがscheduler停止・queue保全・alarm報告を完了しても、元依頼に診断・修正・再開確認が残る場合は親USER_REQUESTを完了扱いしない"},
		{"codex/instructions/task-lifecycle.md", "個別commit・installが完了しても、明示的に継続対象とした計画範囲が残る場合は親USER_REQUESTを完了扱いしない"},
		{"codex/instructions/task-lifecycle.md", "新しい権限、Codexの外で変わる外部状態、意味のあるユーザー判断が本当に必要な場合だけ停止する"},
		{"codex/instructions/task-lifecycle.md", "checkpoint・session・working treeを保持し、残作業とblockerを報告する"},
		{"codex/instructions/task-lifecycle.md", "実装計画に長期roadmapが存在するだけで、現在の親依頼範囲へ作業を勝手に拡張しない"},
		{"codex/instructions/task-lifecycle.md", "「後続へ継続」「停止しない」と明示した範囲を、直近subtaskの局所終端で打ち切らない"},
	}
	contents := make(map[string]string, 3)
	for _, c := range cases {
		if _, ok := contents[c.file]; !ok {
			contents[c.file] = readContractFile(c.file)
		}
		if !strings.Contains(contents[c.file], c.wire) {
			t.Errorf("%s lacks task lifecycle wiring: %q", c.file, c.wire)
		}
	}

	// 親behavioral Evalの期待判断(EVAL.md本節)がproduction guidanceのどの契約文へ根拠を
	// 持つかを対で検証する。EVAL.md側の文面だけ、instruction側の契約文だけの片側存在は通さない。
	section := evalLifecycleSection(t, readContractFile("EVAL.md"))
	instruction := contents["codex/instructions/task-lifecycle.md"]
	evalGrounds := []struct {
		eval     string
		guidance string
	}{
		{"局所終端の完了報告(monitorのscheduler停止・queue/checkpoint保全・alarm報告、GLM child taskのreview・個別commit・install)", "scheduler停止・queue/checkpoint保全・alarm報告の完了は局所終端である"},
		{"局所終端の完了報告(monitorのscheduler停止・queue/checkpoint保全・alarm報告、GLM child taskのreview・個別commit・install)", "task・review・commit・installの個別完了は局所終端である"},
		{"monitorのscheduler停止・queue/checkpoint保全・alarm報告の完了だけが得られても元依頼に診断・原因修正・再開確認が残る場合、安全停止・状態保全の成功報告を親USER_REQUESTの完了として受領せず", "monitorがscheduler停止・queue保全・alarm報告を完了しても、元依頼に診断・修正・再開確認が残る場合は親USER_REQUESTを完了扱いしない"},
		{"同じCodex taskで次の安全なin-scope操作(診断・原因修正・再開確認)へ継続する", "原因修正・再開確認・後続改善等が残るなら、同じCodexタスクで次の操作へ継続する"},
		{"同じCodex taskで次の安全なin-scope操作(診断・原因修正・再開確認)へ継続する", "各局所終端の直後に、親依頼と明示継続対象計画の未解決作業と次の安全なin-scope操作を再評価する"},
		{"child taskのreview・個別commit・install完了後も明示継続対象計画範囲が残る場合は完了扱いせず次項の安全な操作へ継続する", "個別commit・installが完了しても、明示的に継続対象とした計画範囲が残る場合は親USER_REQUESTを完了扱いしない"},
		{"局所終端の成功報告で親USER_REQUESTの完了報告を代用しない", "局所終端の成功報告で親USER_REQUESTの完了報告を代用しない"},
		{"親依頼本体と明示継続対象計画範囲の未解決作業がすべて解消した場合だけ親USER_REQUESTを完了扱う", "親依頼本体と、ユーザー・automationが明示的に継続対象とした実装計画範囲の未解決作業がすべて解消した時だけを指す"},
		{"依頼が単一局所成果物へ明示限定される場合は長期roadmapや依頼外診断へ範囲を拡張せず通常完遂する", "実装計画に長期roadmapが存在するだけで、現在の親依頼範囲へ作業を勝手に拡張しない"},
		{"継続に新しい権限・Codexの外で変わる外部状態・意味のあるユーザー判断が本当に必要な場合だけ停止し", "新しい権限、Codexの外で変わる外部状態、意味のあるユーザー判断が本当に必要な場合だけ停止する"},
		{"checkpoint・session・working treeを保持して残作業とblockerを報告する", "停止時はcheckpoint・session・working treeを保持し、残作業とblockerを報告する"},
		{"明示継続範囲を直近subtaskの局所終端で打ち切らない", "「後続へ継続」「停止しない」と明示した範囲を、直近subtaskの局所終端で打ち切らない"},
		{"親Codexの局所終端後の再評価・継続/停止/完了判断・次の操作選択・完了報告内容をraw telemetry・task log等の一次証拠で照合する", "各局所終端の直後に、親依頼と明示継続対象計画の未解決作業と次の安全なin-scope操作を再評価する"},
	}
	for _, g := range evalGrounds {
		if !strings.Contains(instruction, g.guidance) {
			t.Errorf("task-lifecycle.md lacks guidance grounding %q", g.guidance)
		}
		if !strings.Contains(section, g.eval) {
			t.Errorf("EVAL.md lifecycle section lacks behavioral eval judgment grounded in guidance: %q", g.eval)
		}
	}

	// behavioral Eval・corpus参照の管理文面。scripted packetの局所終端宣言を親Codexの再評価・
	// 継続行動の証明としない限定と、未実行Evalの一次証拠・完了条件・実行条件をEVAL.mdへ残す。
	for _, wire := range []string{
		"TestTaskLifecycleContractWiring",
		"task-lifecycle-monitor-safe-stop-local-terminal-returns-to-sol",
		"task-lifecycle-external-judgment-blocker-stops-with-state",
		"task-lifecycle-explicitly-limited-deliverable-completes",
		"scripted packetの局所終端宣言だけを親Codexの再評価・継続行動の証明として採用しない",
		"親behavioral Evalの代替として重複scenarioをcorpusへ追加しない",
		"親Codexの局所終端後の再評価・継続/停止/完了判断・次の操作選択・完了報告内容をraw telemetry・task log等の一次証拠で照合",
		"live model呼出しを要するためユーザーの明示指示後だけ実行し",
		"EVAL.md本節のpositive/negative caseと期待判断を`task-lifecycle.md`の終端3分類・局所終端後再評価・停止条件・範囲規律の契約文へ直接突き合わせて検証",
	} {
		if !strings.Contains(section, wire) {
			t.Errorf("EVAL.md lifecycle section lacks task lifecycle eval wiring: %q", wire)
		}
	}

	// EVAL.mdが参照するcorpus entryとmanifest pinが実在すること。文面参照だけの自己充足を防ぐと
	// ともに、親behavioral Evalの代替へtask-lifecycle-*の重複scenarioがcorpusへ追加された場合へ
	// 失敗させる。
	expectedIDs := []string{
		"task-lifecycle-monitor-safe-stop-local-terminal-returns-to-sol",
		"task-lifecycle-external-judgment-blocker-stops-with-state",
		"task-lifecycle-explicitly-limited-deliverable-completes",
	}
	sc, mf := loadCorpus(t)
	expectedSet := make(map[string]bool, len(expectedIDs))
	for _, id := range expectedIDs {
		expectedSet[id] = true
	}
	for _, s := range sc.Scenarios {
		if !strings.HasPrefix(s.ID, "task-lifecycle-") {
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
		if e.Path == "codex/instructions/task-lifecycle.md" {
			pinned = true
		}
	}
	if !pinned {
		t.Error("manifest.json must pin codex/instructions/task-lifecycle.md")
	}

	// 本contractは親Codex側の再評価・継続条件であり、常時checklistのworker/reviewer prompt
	// 追加で代替した実装になっていないことを固定する。
	for _, promptFile := range []string{"codex/glm-worker/prompts/WORKER.md", "codex/glm-worker/prompts/REVIEWER.md"} {
		prompt := readContractFile(promptFile)
		for _, keyword := range []string{"task-lifecycle", "親USER_REQUEST", "局所終端"} {
			if strings.Contains(prompt, keyword) {
				t.Errorf("%s must not add a task lifecycle checklist (%s)", promptFile, keyword)
			}
		}
	}
}

func evalLifecycleSection(t *testing.T, evalDoc string) string {
	t.Helper()
	const header = "## 親USER_REQUEST lifecycle contract"
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
