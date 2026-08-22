package workflow

import (
	"os"
	"os/exec"
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
		// final HEAD postcondition gateの契約文。手順instructionの文言pinだけに依存しない
		// 機械強制としてinstall.sh gateが存在し、その契約がgit.mdへ固定されていることを検査する。
		{"codex/instructions/git.md", "### final HEAD postconditionの機械強制"},
		{"codex/instructions/git.md", "install.shがmanaged配置の前段階としてfinal HEAD postconditionを機械検証する"},
		{"codex/instructions/git.md", "順序は「実装・commit-ready planの初回commit → 親CodexによるPlan・IMPLEMENTATION_TASKS・Historyの完了同期 → 同一commitへのamend → install.shのgate通過と本配置 → 次task・handoff」であり、installをamendより先に行わない"},
		{"codex/instructions/git.md", "gateは`git show HEAD:IMPLEMENTATION_PLAN.local.md`の内容だけを判定し、dirty working treeのplanを判定に使わない"},
		{"codex/instructions/git.md", "PlanのACTIVE欄が`IMPLEMENTATION_TASKS/`配下の`.md` task fileへ一意に解決できること"},
		{"codex/instructions/git.md", "ACTIVE/NEXT/BLOCKEDが参照するtask fileがすべてHEAD treeへregular fileとして存在すること"},
		{"codex/instructions/git.md", "ACTIVE task fileがNEXT/BLOCKEDへ重複記載されていないこと"},
		{"codex/instructions/git.md", "Git境界のbranchがHEADの実際のbranchと一致すること"},
		{"codex/instructions/git.md", "現在のGit境界・停止理由・次の親Codex操作が完了済みcommitの操作をamend直前・install前・amendの前等の未実施として記述していないこと"},
		// Sol review修正の契約面。NEXT/BLOCKED全bulletのfail closed検証、runtimeと同じ
		// 共通task path契約・bullet構文、過渡表現のamend後除外とidentifier境界をgit.mdへ固定する。
		{"codex/instructions/git.md", "ACTIVE/NEXT/BLOCKEDの各欄はbulletが存在するならすべてがbullet構文およびtask path契約へ解決されること(NEXT/BLOCKEDの空欄は許容する)"},
		{"codex/instructions/git.md", "task path契約はruntime配置契約(`validateActiveTaskPath`)と同じである"},
		{"codex/instructions/git.md", "bullet構文はruntime ACTIVE解決(`activeSectionEntries`/`activeEntryPath`)と同じである"},
		{"codex/instructions/git.md", "閉じbacktick欠損・前後の余分なtext・複数backtick組はmalformedとしてACTIVE/NEXT/BLOCKEDすべてでfail closedに拒否する"},
		{"codex/instructions/git.md", "`TestPlanFinalHeadTaskPathValidatorMatchesRuntime`が固定する"},
		{"codex/instructions/git.md", "`TestPlanFinalHeadBulletExtractionMatchesRuntime`が固定する"},
		{"codex/instructions/git.md", "完了済み操作に続く正当な現在task記述(「amend後のpostconditionを実装する」等)で使う「amend後」は対象外とする"},
		{"codex/instructions/git.md", "判定はbyte志向の`LC_ALL=C`で行い"},
		{"codex/instructions/git.md", "gate失敗時はinstall・次task・handoffへ進まず、Plan・IMPLEMENTATION_TASKS・Historyを完了同期して同一commitへamendする"},
		{"codex/instructions/git.md", "amend失敗でobsolete HEADが残っている間もgateは拒否し続ける"},
		{"codex/instructions/git.md", "gateの適用外は非Git directory・commitが存在しないrepository・planがGit indexで未追跡のrepository・planがHEADへ未収録のrepositoryだけとし"},
		{"codex/instructions/git.md", "worker-start guard・worker/reviewer個別checklist・全repository共通hookは追加しない"},
		{"install.sh", "verify_plan_final_head || exit $?"},
		{"install.sh", "plan_transitional_pattern='(^|[^[:alnum:]_])(amend|install)(する)?(の|直)?前'"},
		{"install.sh", "require grep"},
		{"install.sh", "validate_plan_task_path"},
		{"install.sh", "LC_ALL=C grep -E \"$plan_transitional_pattern\""},
		{"install.sh", "skipped (IMPLEMENTATION_PLAN.local.md is untracked)"},
		{"install.sh", "skipped (IMPLEMENTATION_PLAN.local.md is not in HEAD yet)"},
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
		{"gateはHEAD収録planのACTIVE一意解決・ACTIVE/NEXT/BLOCKED参照task fileのHEAD tree存在・ACTIVE重複記載拒否・Git境界branch一致・現在状態節の過渡表現(amend直前・install前等)拒否を機械検証する", "ACTIVE/NEXT/BLOCKEDが参照するtask fileがすべてHEAD treeへregular fileとして存在すること"},
		{"NEXT/BLOCKED欄の全unordered bulletをvalid task path解決へfail closed検証し", "ACTIVE/NEXT/BLOCKEDの各欄はbulletが存在するならすべてがbullet構文およびtask path契約へ解決されること(NEXT/BLOCKEDの空欄は許容する)"},
		{"`TestPlanFinalHeadTaskPathValidatorMatchesRuntime`がshell/runtimeの受理集合差分を固定する", "`TestPlanFinalHeadTaskPathValidatorMatchesRuntime`が固定する"},
		{"`TestPlanFinalHeadBulletExtractionMatchesRuntime`がshell/runtimeの抽出規約差分を", "`TestPlanFinalHeadBulletExtractionMatchesRuntime`が固定する"},
		{"runtime側ACTIVE解決も同じbullet構文違反をerrorに強めた", "閉じbacktick欠損・前後の余分なtext・複数backtick組はmalformedとしてACTIVE/NEXT/BLOCKEDすべてでfail closedに拒否する"},
		{"過渡表現から`amend後`を除外し正当な現在task記述を許容し", "完了済み操作に続く正当な現在task記述(「amend後のpostconditionを実装する」等)で使う「amend後」は対象外とする"},
		{"非Git・commitなし・untracked plan・HEAD未収録planはskipし、dirty working treeではなくHEADだけを判定する", "gateは`git show HEAD:IMPLEMENTATION_PLAN.local.md`の内容だけを判定し、dirty working treeのplanを判定に使わない"},
		{"amend失敗後の同一commit復旧をproduction install.sh経由で固定する", "amend失敗でobsolete HEADが残っている間もgateは拒否し続ける"},
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
		"final HEAD postconditionの機械強制(2026-08-22)",
		"final HEAD postcondition gateのSol review修正(2026-08-22)",
		"final HEAD postcondition gateのbullet抽出同一化(2026-08-22 independent review fix)",
		"認識できないbulletを黙って無視するfail openを廃止した",
		"byte志向`LC_ALL=C`判定でBSD grepのmultibyte bracket欠陥を回避する",
		"実Git repository scenarioによる検証は`tests/install_smoke.sh`が担い",
		"`TestPlanCommitSyncContractWiring`は本gateの`git.md`契約文・EVAL本節・install.sh wiringを直接突き合わせて検証する",
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

	// gateはpreflight・binary build・managed files配置など全配置の前に実行される。呼出位置が
	// 配置段階より後へ動くとstale HEADのまま配置が完了するため、install.sh本文内の呼出順を固定する。
	// gateはgrepへ依存するため、require grepがgate呼出より前であることも固定する。
	installer := readContractFile("install.sh")
	gateCall := strings.Index(installer, "verify_plan_final_head || exit $?")
	if gateCall < 0 {
		t.Fatalf("install.sh lacks verify_plan_final_head call")
	}
	requireGrep := strings.Index(installer, "\nrequire grep\n")
	if requireGrep < 0 {
		t.Fatalf("install.sh lacks require grep dependency for the gate")
	}
	if requireGrep > gateCall {
		t.Errorf("require grep must precede verify_plan_final_head so grep absence fails explicitly")
	}
	for _, placementCall := range []string{"\npreflight || exit $?\n", "\nbuild_glm_worker\n", "\ninstall_codex_files\n"} {
		at := strings.Index(installer, placementCall)
		if at < 0 {
			t.Fatalf("install.sh lacks placement call %q", strings.TrimSpace(placementCall))
		}
		if gateCall > at {
			t.Errorf("verify_plan_final_head must run before %s", strings.TrimSpace(placementCall))
		}
	}
}

// TestPlanFinalHeadTaskPathValidatorMatchesRuntimeはinstall.shのvalidate_plan_task_pathが
// runtime配置契約validateActiveTaskPathと同じ受理集合を持つことを固定する。install.shから
// 関数本体だけを抽出してshで実行し、境界caseも含めた候補path群の判定をruntime側と突き合わせる。
// shell側だけ緩いpathを通すとgateがruntime配置契約と異なるHEADを許可するため、差分はfailする。
func TestPlanFinalHeadTaskPathValidatorMatchesRuntime(t *testing.T) {
	root := scenarioRepoRoot(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "install.sh"))
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	installer := string(installerBytes)
	const marker = "validate_plan_task_path() {"
	start := strings.Index(installer, marker)
	if start < 0 {
		t.Fatalf("install.sh lacks validate_plan_task_path definition")
	}
	bodyEnd := strings.Index(installer[start:], "\n}\n")
	if bodyEnd < 0 {
		t.Fatalf("install.sh validate_plan_task_path body is not terminated")
	}
	script := installer[start:start+bodyEnd+2] + "\nvalidate_plan_task_path \"$1\"\n"

	candidates := []struct {
		path string
		note string
	}{
		{"IMPLEMENTATION_TASKS/002-plan-final-head-postcondition.md", "標準形"},
		{"IMPLEMENTATION_TASKS/sub/deep/task.md", "subdirectory許容"},
		{"IMPLEMENTATION_TASKS/.hidden/task.md", "dotfile segment許容"},
		{"IMPLEMENTATION_TASKS/.md", "prefix/suffixだけの最小形"},
		{"IMPLEMENTATION_TASKS/..md", "`.`/`..`でないdot segment"},
		{"tasks/task.md", "prefix違反"},
		{"implementation_tasks/task.md", "prefix大文字小文字"},
		{"IMPLEMENTATION_TASKS/task.txt", "suffix違反"},
		{"IMPLEMENTATION_TASKS", "directoryではなくfile契約"},
		{"IMPLEMENTATION_TASKS/", "空rest"},
		{"/IMPLEMENTATION_TASKS/task.md", "絶対path"},
		{"IMPLEMENTATION_TASKS//task.md", "空segment"},
		{"IMPLEMENTATION_TASKS/./task.md", "`.` segment"},
		{"IMPLEMENTATION_TASKS/../task.md", "`..` segment"},
		{"IMPLEMENTATION_TASKS/a/./b.md", "中間`.` segment"},
		{"IMPLEMENTATION_TASKS/a/../b.md", "中間`..` segment"},
		{"IMPLEMENTATION_TASKS\\task.md", "backslash混在"},
		{"IMPLEMENTATION_TASKS/task.md/", "末尾slash"},
		{"IMPLEMENTATION_TASKS/task.md/..", "末尾`..`"},
		{"IMPLEMENTATION_TASKS/..", "上方脱出"},
	}
	for _, c := range candidates {
		shellErr := exec.Command("sh", "-c", script, "validate_plan_task_path", c.path).Run()
		runtimeErr := validateActiveTaskPath(c.path)
		if (runtimeErr == nil) != (shellErr == nil) {
			t.Errorf("validate_plan_task_path(%q %s) = %v, runtime validateActiveTaskPath = %v", c.path, c.note, shellErr, runtimeErr)
		}
	}
}

// TestPlanFinalHeadBulletExtractionMatchesRuntimeはinstall.shのplan_bullet_pathsが
// runtime activeSectionEntries/activeEntryPathと同じbullet抽出規則を持つことを固定する。
// 閉じbacktick欠損・前後の余分なtext・複数backtick組をshellだけ緩く受けると、runtimeが
// 拒否するACTIVE欄をinstaller gateが通すfail openになるため、path validatorだけでなく
// 抽出規約の差分もfailする。
func TestPlanFinalHeadBulletExtractionMatchesRuntime(t *testing.T) {
	root := scenarioRepoRoot(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "install.sh"))
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	installer := string(installerBytes)
	const marker = "plan_bullet_paths() {"
	start := strings.Index(installer, marker)
	if start < 0 {
		t.Fatalf("install.sh lacks plan_bullet_paths definition")
	}
	bodyEnd := strings.Index(installer[start:], "\n}\n")
	if bodyEnd < 0 {
		t.Fatalf("install.sh plan_bullet_paths body is not terminated")
	}
	script := installer[start:start+bodyEnd+2] + "\nplan_bullet_paths\n"

	bullets := []struct {
		line string
		note string
	}{
		{"- `IMPLEMENTATION_TASKS/x.md`", "標準形式"},
		{"- IMPLEMENTATION_TASKS/x.md", "逆引用符なし直書き"},
		{"- `IMPLEMENTATION_TASKS/x.md", "閉じbacktick欠損"},
		{"- `IMPLEMENTATION_TASKS/x.md` suffix", "閉じbacktick後に余分なtext"},
		{"- prefix `IMPLEMENTATION_TASKS/x.md`", "開始backtick前に余分なtext"},
		{"- `a.md` `b.md`", "複数backtick組"},
		{"- ``", "空pathのbacktick組"},
		{"-   `IMPLEMENTATION_TASKS/x.md`", "marker直後の余分な空白"},
		{"- `IMPLEMENTATION_TASKS/x.md`   ", "行末空白"},
		{"- garbage", "非task path項目"},
		{"- ", "空項目"},
		{"plain text", "bullet以外の行"},
		{"  - `IMPLEMENTATION_TASKS/x.md`", "字下げbullet"},
	}
	for _, b := range bullets {
		cmd := exec.Command("sh", "-c", script)
		cmd.Stdin = strings.NewReader(b.line + "\n")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("plan_bullet_paths(%q %s): %v", b.line, b.note, err)
		}
		shellLine := strings.TrimSuffix(string(out), "\n")

		runtimeEntries, runtimeErr := activeSectionEntries("## ACTIVE\n\n" + b.line + "\n\n## NEXT\n")
		switch {
		case shellLine == "":
			if runtimeErr != nil || len(runtimeEntries) != 0 {
				t.Errorf("plan_bullet_paths(%q %s)はbulletを無視しましたがruntimeは扱います: entries=%v err=%v", b.line, b.note, runtimeEntries, runtimeErr)
			}
		case strings.HasPrefix(shellLine, "!"):
			if runtimeErr == nil {
				t.Errorf("plan_bullet_paths(%q %s)はmalformed扱いですがruntimeは受理します: %q", b.line, b.note, shellLine)
			}
		case strings.HasPrefix(shellLine, "+"):
			path := strings.TrimPrefix(shellLine, "+")
			if runtimeErr != nil {
				t.Errorf("plan_bullet_paths(%q %s)はpath候補 %qを返しましたがruntimeはmalformed扱いです: %v", b.line, b.note, path, runtimeErr)
				continue
			}
			if len(runtimeEntries) != 1 || runtimeEntries[0] != path {
				t.Errorf("plan_bullet_paths(%q %s) = %q, runtime = %v", b.line, b.note, path, runtimeEntries)
			}
		default:
			t.Fatalf("plan_bullet_paths(%q %s)が未知の出力を返しました: %q", b.line, b.note, shellLine)
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
