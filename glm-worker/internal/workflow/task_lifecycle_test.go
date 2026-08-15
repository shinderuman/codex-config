package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTaskLifecycleContractWiringは親Codex側lifecycle contractのproduction routing配線を
// 決定論検証する。codex/AGENTS.mdのrouting、glm-execution.mdのpacket受理後読込指示、
// task-lifecycle.md本文の必須契約文のいずれかが欠けると失敗する。親Codexが局所終端後に
// 同一taskで継続するかのbehavioral証明ではなく、scenario corpusのtask-lifecycle-*は
// wrapperの局所終端例への終端検証に限定され、親orchestration行動の固定EvalはEVAL.mdの
// 完了条件を待つ。
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
}
