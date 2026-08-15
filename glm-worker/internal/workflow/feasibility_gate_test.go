package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFeasibilityGateContractWiringは親Codex側feasibility gateのproduction routing配線を
// 決定論検証する。codex/AGENTS.mdの条件付きrouting・品質gate項目、glm-execution.mdの委譲前
// 読込指示、feasibility-gate.md本文の必須契約文のいずれかが欠けると失敗する。
// 親Codexが規則へ従うかのbehavioral証明ではなく、scenario corpusのfeasibility-gate-*は
// wrapper終端検証に限定され、親orchestration行動の固定EvalはEVAL.mdの完了条件を待つ。
func TestFeasibilityGateContractWiring(t *testing.T) {
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
		{"codex/AGENTS.md", "未検証の外部成立性を本番設計の前提へ進める変更のGo/No-Goと撤退判断"},
		{"codex/AGENTS.md", "~/.codex/instructions/feasibility-gate.md"},
		{"codex/instructions/glm-execution.md", "未検証成立性が本番設計の前提になる依頼は、`~/.codex/instructions/feasibility-gate.md`を読んでから委譲内容を構成する"},
		{"codex/instructions/feasibility-gate.md", "未検証のcritical assumptionの列挙"},
		{"codex/instructions/feasibility-gate.md", "assumptionごとの最小PoCと代表case"},
		{"codex/instructions/feasibility-gate.md", "transport成功だけを成立性の証明にしない"},
		{"codex/instructions/feasibility-gate.md", "Amazon取得PoCの48〜72時間はその対象固有の観測条件であり一般contractへ固定しない"},
		{"codex/instructions/feasibility-gate.md", "短時間の意味的検証で足りる対象へ長時間試験を要求しない"},
		{"codex/instructions/feasibility-gate.md", "形式的なPoCや固定の観測期間を要求しない"},
		{"codex/instructions/feasibility-gate.md", "Go/No-Go基準と撤退条件"},
		{"codex/instructions/feasibility-gate.md", "workaroundの追加実装をさせず観測事実をSol/ユーザー判断へ戻す"},
		{"codex/instructions/feasibility-gate.md", "PoC・観測taskとproduction実装taskを分離する"},
	}
	contents := make(map[string]string, 3)
	for _, c := range cases {
		if _, ok := contents[c.file]; !ok {
			contents[c.file] = readContractFile(c.file)
		}
		if !strings.Contains(contents[c.file], c.wire) {
			t.Errorf("%s lacks feasibility gate wiring: %q", c.file, c.wire)
		}
	}
}
