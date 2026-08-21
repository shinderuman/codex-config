package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlanCommitSyncContractWiringは親Codex側plan commit同期contractのproduction wiringを
// 決定論検証する。codex/AGENTS.mdのcommit時読込routing、git.md本文の必須契約文、
// root AGENTS.mdのparent-only plan規則、git.mdの既存commit承認・push禁止規則の存続の
// いずれかが欠けると失敗する。EVAL.md本節の親behavioral Eval入力・期待判断がinstruction
// 本文のどの契約文へ根拠を持つかを対で固定する。本contractのcommit・amendは親Codexが
// 実行するためwrapper終端が存在せず、corpusへの重複scenario追加とworker/reviewer prompt
// へのchecklist追加で代替した実装になっていないことも固定する。
func TestPlanCommitSyncContractWiring(t *testing.T) {
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
		{"codex/AGENTS.md", "commit・Git履歴操作 → `~/.codex/instructions/git.md`"},
		{"codex/instructions/git.md", "## tracked canonical planのcommit同期"},
		{"codex/instructions/git.md", "repository rootに親Codex管理のtracked canonical plan(`IMPLEMENTATION_PLAN.local.md`)が存在するrepositoryのcommitだけに適用する親Codex orchestration contractである"},
		{"codex/instructions/git.md", "HEADのplanが現在作業より一世代古いstale-by-oneになる"},
		{"codex/instructions/git.md", "初回commitと同一commitへのamendからなる二段階で解消する"},
		{"codex/instructions/git.md", "実装・test・独立review・必要なSol品質gate完了後も未完了項目を`[x]`にせず、planを作業実態と次task内容へ同期したcommit-ready状態へ更新する"},
		{"codex/instructions/git.md", "実装とcommit-ready planを初回commitへ含める"},
		{"codex/instructions/git.md", "親Codexが直ちにplanと`IMPLEMENTATION_HISTORY.md`を完了証跡(`[x]`)・次task・実working tree状態へ同期する"},
		{"codex/instructions/git.md", "同期済みplan/historyだけを初回commitと同じcommitへamendする"},
		{"codex/instructions/git.md", "final HEADとclean working treeを確認してからinstall・次task・handoffへ進む"},
		{"codex/instructions/git.md", "初回commitとamendの間に停止・ユーザー報告でのturn終了・別task開始・GLM起動・install・handoffを行わず、amendまでを同じturnの連続操作とする"},
		{"codex/instructions/git.md", "amend失敗時はobsolete HEADのままinstall・次task・handoffへ進まず、同じcommitへのplan/history同期を復旧して再度amendする"},
		{"codex/instructions/git.md", "追加commitの連鎖でplan同期を先送りしない"},
		{"codex/instructions/git.md", "大規模ledger・別status DB・追加commitの連鎖・worker/reviewer個別checklistは追加しない"},
		{"codex/instructions/git.md", "worker/reviewerへの個別checklist追加で代替しない"},
		{"codex/instructions/git.md", "plan本文・`[x]`・優先順・現在状態の更新権限が親Codex専有であること、commit実行の承認条件、Gitリモートへの書込禁止、wrapperのplan file不変guardは本契約で変更しない"},
		// 既存規則の存続確認。contract追加に伴い既存のcommit承認・push禁止・parent-only
		// plan/history規則が削除・弱体化していないことを検査する。
		{"codex/instructions/git.md", "明示的な依頼がない限り`git commit`しない"},
		{"codex/instructions/git.md", "`git push`等Gitリモートへの書き込みは禁止"},
		{"AGENTS.md", "このplanの本文・`[x]`・優先順・現在状態を更新できるのは親Codexだけである"},
		{"AGENTS.md", "GLM worker/reviewerはこのfileを読み取り専用で参照し、編集・生成・復元・削除を行わない"},
		{"AGENTS.md", "同historyは親Codex専有のtracked archiveであり、GLM worker/reviewerは編集・生成・削除を行わず"},
	}
	contents := make(map[string]string, 3)
	for _, c := range cases {
		if _, ok := contents[c.file]; !ok {
			contents[c.file] = readContractFile(c.file)
		}
		if !strings.Contains(contents[c.file], c.wire) {
			t.Errorf("%s lacks plan commit sync wiring: %q", c.file, c.wire)
		}
	}

	// 親behavioral Evalの期待判断(EVAL.md本節)がproduction guidanceのどの契約文へ根拠を
	// 持つかを対で検証する。EVAL.md側の文面だけ、instruction側の契約文だけの片側存在は通さない。
	section := evalPlanCommitSyncSection(t, readContractFile("EVAL.md"))
	instruction := contents["codex/instructions/git.md"]
	evalGrounds := []struct {
		eval     string
		guidance string
	}{
		{"未完了項目を`[x]`にせずplanを作業実態と次task内容へ同期したcommit-ready状態へ更新し", "未完了項目を`[x]`にせず、planを作業実態と次task内容へ同期したcommit-ready状態へ更新する"},
		{"実装とcommit-ready planを初回commitへ含める", "実装とcommit-ready planを初回commitへ含める"},
		{"完了証跡(`[x]`)・次task・実working tree状態へ同期し", "完了証跡(`[x]`)・次task・実working tree状態へ同期する"},
		{"同期済みplan/historyだけを同じcommitへamendし", "同期済みplan/historyだけを初回commitと同じcommitへamendする"},
		{"final HEADとclean working treeを確認してからinstall・次task・handoffへ進む", "final HEADとclean working treeを確認してからinstall・次task・handoffへ進む"},
		{"初回commitとamendの間に停止・ユーザー報告でのturn終了・別task開始・GLM起動・install・handoffを行わない", "初回commitとamendの間に停止・ユーザー報告でのturn終了・別task開始・GLM起動・install・handoffを行わず、amendまでを同じturnの連続操作とする"},
		{"amend失敗時はobsolete HEADのままinstall・次task・handoffへ進まず、同じcommitへのplan/history同期を復旧する", "amend失敗時はobsolete HEADのままinstall・次task・handoffへ進まず、同じcommitへのplan/history同期を復旧して再度amendする"},
		{"planが存在しないrepositoryの通常commitへ本契約の手順を適用せず", "repository rootに親Codex管理のtracked canonical plan(`IMPLEMENTATION_PLAN.local.md`)が存在するrepositoryのcommitだけに適用する親Codex orchestration contractである"},
		{"大規模ledger・別status DB・追加commitの連鎖・worker/reviewer個別checklistは追加しない", "大規模ledger・別status DB・追加commitの連鎖・worker/reviewer個別checklistは追加しない"},
		{"commit前後のplan本文・`git show`によるHEAD収録内容・`git status`によるworking tree状態を一次証拠で照合する", "final HEADとclean working treeを確認してからinstall・次task・handoffへ進む"},
	}
	for _, g := range evalGrounds {
		if !strings.Contains(instruction, g.guidance) {
			t.Errorf("git.md lacks guidance grounding %q", g.guidance)
		}
		if !strings.Contains(section, g.eval) {
			t.Errorf("EVAL.md plan commit sync section lacks behavioral eval judgment grounded in guidance: %q", g.eval)
		}
	}

	// behavioral Eval・管理文面。scripted packetやcorpus scenarioを親Codexのcommit同期
	// 行動の証明としない限定と、未実行Evalの一次証拠・完了条件・実行条件をEVAL.mdへ残す。
	for _, wire := range []string{
		"TestPlanCommitSyncContractWiring",
		"scripted packetで表現できるwrapper終端を持たず",
		"`plan-commit-sync-*`",
		"親behavioral Evalの代替として重複scenarioをcorpusへ追加しない方針も本testが固定する",
		"corpus scenarioもscripted packetも親Codexのcommit・amend・同期復旧行動の証明にならない",
		"live model呼出しを要するためユーザーの明示指示後だけ実行し",
		"EVAL.md本節のpositive/negative caseと期待判断を`git.md`の二段階契約・初回commitとamendの間の停止禁止・amend失敗復旧の契約文へ直接突き合わせて検証",
	} {
		if !strings.Contains(section, wire) {
			t.Errorf("EVAL.md plan commit sync section lacks eval wiring: %q", wire)
		}
	}

	// 本contractは親Codexがcommit・amendを実行するorchestration契約であり、wrapper終端
	// scenarioを持たない。親behavioral Evalの代替へplan-commit-sync-*のscenarioがcorpusへ
	// 追加された場合へ失敗させる。
	sc, _ := loadCorpus(t)
	for _, s := range sc.Scenarios {
		if strings.HasPrefix(s.ID, "plan-commit-sync-") {
			t.Errorf("scenario %s must not duplicate the parent behavioral eval into the corpus", s.ID)
		}
	}

	// worker/reviewer promptへの個別checklist追加で代替した実装になっていないことを固定する。
	for _, promptFile := range []string{"codex/glm-worker/prompts/WORKER.md", "codex/glm-worker/prompts/REVIEWER.md"} {
		prompt := readContractFile(promptFile)
		for _, keyword := range []string{"commit-ready", "stale-by-one", "amend", "commit同期"} {
			if strings.Contains(prompt, keyword) {
				t.Errorf("%s must not add a plan commit sync checklist (%s)", promptFile, keyword)
			}
		}
	}
}

func evalPlanCommitSyncSection(t *testing.T, evalDoc string) string {
	t.Helper()
	const header = "## tracked canonical planのcommit同期contract"
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
