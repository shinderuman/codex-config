package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestActiveTaskContractWiringはACTIVE task requirement契約をAGENTS.md・caller側instruction・
// EVAL.md・dynamic prompt配線へ分散した対応を決定論検証する。ルール文面・caller契約・Eval記述・
// prompt builderのいずれかが欠けると失敗する。常時checklistとして静的role prompt
// (WORKER.md/REVIEWER.md)へ条文を複製せず、毎呼出で動的に ACTIVE task pathを載せる方針を固定する。
func TestActiveTaskContractWiring(t *testing.T) {
	root := scenarioRepoRoot(t)

	readFile := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}

	agents := readFile("AGENTS.md")
	for _, wire := range []string{
		"parent-managed implementation metadataの単一集合",
		"IMPLEMENTATION_RULES.md`・`IMPLEMENTATION_PLAN.local.md`・`IMPLEMENTATION_TASKS/`配下全file・`IMPLEMENTATION_HISTORY.md",
		"pathごとの分岐を増やさず",
		"`## ACTIVE`節は実行中taskの要求正本を`IMPLEMENTATION_TASKS/`配下へ1件だけ指す",
		"workerとreviewerがそれぞれ独立にtask file本文から読む",
		"USER_REQUEST・会話要約・過去session記憶を要求定義の代わりにしない",
		"一意に解決できない場合(未記載・複数記載・配置契約外・参照file欠損)はmodel呼出前にfail closed",
		"task完了時のfile削除・history移行・plan昇格はTask 002の完了flowで行う",
	} {
		if !strings.Contains(agents, wire) {
			t.Errorf("root AGENTS.md lacks ACTIVE task contract wiring: %q", wire)
		}
	}

	execution := readFile("codex/instructions/glm-execution.md")
	for _, wire := range []string{
		"Planの`## ACTIVE`節から1件だけ指す",
		"USER_REQUESTへtask詳細を複製せず",
		"次の呼出前にtask fileのAmendmentsへ追記してから委譲する",
		"task完了前のtask file削除・history移行・plan昇格は行わない",
		"ACTIVE task解決失敗(`parent_metadata_active_unresolvable`)",
		"親Codexが直接確認・修復してから同じtaskを再開する",
	} {
		if !strings.Contains(execution, wire) {
			t.Errorf("codex/instructions/glm-execution.md lacks caller-side ACTIVE task wiring: %q", wire)
		}
	}

	eval := readFile("EVAL.md")
	for _, wire := range []string{
		"parent-managed implementation metadataの単一集合",
		"path名ごとの分岐実装を持たず",
		"critical分類(`implementation-rules`)",
		"critical分類(`implementation-tasks`)",
		"ACTIVE task fileを`IMPLEMENTATION_TASKS/`配下へ一意に解決する",
		"`.md` regular fileに限定し",
		"non-regular file(固定後のsymlink差し替え含む)は同じ解決失敗として扱う",
		"pathはtask開始時に一度だけstateへ固定し",
		"新task開始時は前taskの固定を除去し",
		"解決fail closed後のstateは未設定のまま残し",
		"未設定を検出してACTIVEを再解決・固定してからpromptを作る",
		"再解決失敗は0 model callで再度fail closedする",
		"停止期間中に同じfileがさらに再変更されていてもcurrent値に関係なくfile単位で拒否し",
		"毎回の呼出promptへ同じACTIVE task file pathと本文読み込み指示(ACTIVE_TASK_FILE block)",
		"resumeは保存済み前回指示を再送するためblockが失われない",
		"TestActiveTaskContractWiring",
		"型不一致でfail closedし、migration・fallbackは置かない",
	} {
		if !strings.Contains(eval, wire) {
			t.Errorf("EVAL.md lacks ACTIVE task contract wiring: %q", wire)
		}
	}

	// 静的role promptへ契約を複製しない。ACTIVE task pathはtask毎に変わるためdynamic prompt側
	// (prompts.go)だけが持ち、常時checklist化によるtoken固定消費と文言衝突を避ける。
	for _, promptFile := range []string{"codex/glm-worker/prompts/WORKER.md", "codex/glm-worker/prompts/REVIEWER.md"} {
		prompt := readFile(promptFile)
		for _, keyword := range []string{"ACTIVE_TASK_FILE", "IMPLEMENTATION_TASKS", "IMPLEMENTATION_RULES.md"} {
			if strings.Contains(prompt, keyword) {
				t.Errorf("%s must not hard-code repository-specific ACTIVE task wiring (%s)", promptFile, keyword)
			}
		}
	}

	// 配線対象fileはmanifest hash pinが守る。変更時は該当scenarioの期待結果を現物へ再照合する。
	sc, mf := loadCorpus(t)
	_ = sc
	pinned := map[string]bool{}
	for _, e := range mf.InstructionFiles {
		pinned[e.Path] = true
	}
	for _, path := range []string{"AGENTS.md", "codex/instructions/glm-execution.md"} {
		if !pinned[path] {
			t.Errorf("manifest.json must pin %s for the ACTIVE task contract wiring", path)
		}
	}
}
