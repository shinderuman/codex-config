package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/packet"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

func writePlanWithActive(t *testing.T, repoRoot string, activeSection string) {
	t.Helper()
	content := "# plan\n\n" + activeSection
	if err := os.WriteFile(filepath.Join(repoRoot, implementationPlanFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTaskFileAtPath(t *testing.T, repoRoot string, relPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repoRoot, filepath.Dir(relPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, relPath), []byte("# task\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// resolveActiveTaskPathはplanのACTIVE欄からACTIVE task fileを一意に解決する。plan欠損は
// 配線なし、配置契約違反・非一意・欠損file参照は要求正本を特定できないためerrorを返す。
func TestResolveActiveTaskPathMatrix(t *testing.T) {
	tests := []struct {
		name        string
		plan        func(t *testing.T, repoRoot string)
		wantPath    string
		wantWired   bool
		wantErrPart string
	}{
		{
			name: "backtick path resolves",
			plan: func(t *testing.T, repoRoot string) {
				writePlanWithActive(t, repoRoot, "## ACTIVE\n\n- `IMPLEMENTATION_TASKS/001-a.md`\n")
				writeTaskFileAtPath(t, repoRoot, "IMPLEMENTATION_TASKS/001-a.md")
			},
			wantPath:  "IMPLEMENTATION_TASKS/001-a.md",
			wantWired: true,
		},
		{
			name: "bare path resolves",
			plan: func(t *testing.T, repoRoot string) {
				writePlanWithActive(t, repoRoot, "## ACTIVE\n\n- IMPLEMENTATION_TASKS/001-a.md\n")
				writeTaskFileAtPath(t, repoRoot, "IMPLEMENTATION_TASKS/001-a.md")
			},
			wantPath:  "IMPLEMENTATION_TASKS/001-a.md",
			wantWired: true,
		},
		{
			name: "entries after next heading are ignored",
			plan: func(t *testing.T, repoRoot string) {
				writePlanWithActive(t, repoRoot, "## ACTIVE\n\n- `IMPLEMENTATION_TASKS/001-a.md`\n\n## NEXT\n\n- `IMPLEMENTATION_TASKS/002-b.md`\n")
				writeTaskFileAtPath(t, repoRoot, "IMPLEMENTATION_TASKS/001-a.md")
				writeTaskFileAtPath(t, repoRoot, "IMPLEMENTATION_TASKS/002-b.md")
			},
			wantPath:  "IMPLEMENTATION_TASKS/001-a.md",
			wantWired: true,
		},
		{
			name: "plan missing means unwired",
			plan: func(t *testing.T, repoRoot string) {},
		},
		{
			name: "active section empty fails",
			plan: func(t *testing.T, repoRoot string) {
				writePlanWithActive(t, repoRoot, "## ACTIVE\n\n(なし)\n")
			},
			wantWired:   true,
			wantErrPart: "ACTIVE欄にtask fileがありません",
		},
		{
			name: "two entries are ambiguous",
			plan: func(t *testing.T, repoRoot string) {
				writePlanWithActive(t, repoRoot, "## ACTIVE\n\n- `IMPLEMENTATION_TASKS/001-a.md`\n- `IMPLEMENTATION_TASKS/002-b.md`\n")
			},
			wantWired:   true,
			wantErrPart: "一意ではありません",
		},
		{
			name: "path escape rejected",
			plan: func(t *testing.T, repoRoot string) {
				writePlanWithActive(t, repoRoot, "## ACTIVE\n\n- `IMPLEMENTATION_TASKS/../AGENTS.md`\n")
			},
			wantWired:   true,
			wantErrPart: "配置契約に違反",
		},
		{
			name: "outside tasks dir rejected",
			plan: func(t *testing.T, repoRoot string) {
				writePlanWithActive(t, repoRoot, "## ACTIVE\n\n- `codex/AGENTS.md`\n")
			},
			wantWired:   true,
			wantErrPart: "IMPLEMENTATION_TASKS配下",
		},
		{
			name: "missing task file fails",
			plan: func(t *testing.T, repoRoot string) {
				writePlanWithActive(t, repoRoot, "## ACTIVE\n\n- `IMPLEMENTATION_TASKS/001-gone.md`\n")
			},
			wantWired:   true,
			wantErrPart: "確認できません",
		},
		{
			name: "semantic filename resolves",
			plan: func(t *testing.T, repoRoot string) {
				writePlanWithActive(t, repoRoot, "## ACTIVE\n\n- `IMPLEMENTATION_TASKS/requirement-task-lifecycle.md`\n")
				writeTaskFileAtPath(t, repoRoot, "IMPLEMENTATION_TASKS/requirement-task-lifecycle.md")
			},
			wantPath:  "IMPLEMENTATION_TASKS/requirement-task-lifecycle.md",
			wantWired: true,
		},
		{
			name: "subdirectory md resolves",
			plan: func(t *testing.T, repoRoot string) {
				writePlanWithActive(t, repoRoot, "## ACTIVE\n\n- `IMPLEMENTATION_TASKS/batch/001-nested.md`\n")
				writeTaskFileAtPath(t, repoRoot, "IMPLEMENTATION_TASKS/batch/001-nested.md")
			},
			wantPath:  "IMPLEMENTATION_TASKS/batch/001-nested.md",
			wantWired: true,
		},
		{
			name: "non-md extension rejected",
			plan: func(t *testing.T, repoRoot string) {
				writePlanWithActive(t, repoRoot, "## ACTIVE\n\n- `IMPLEMENTATION_TASKS/task.txt`\n")
				writeTaskFileAtPath(t, repoRoot, "IMPLEMENTATION_TASKS/task.txt")
			},
			wantWired:   true,
			wantErrPart: "配置契約に違反",
		},
		{
			name: "directory target rejected",
			plan: func(t *testing.T, repoRoot string) {
				writePlanWithActive(t, repoRoot, "## ACTIVE\n\n- `IMPLEMENTATION_TASKS/001-dir.md`\n")
				if err := os.MkdirAll(filepath.Join(repoRoot, "IMPLEMENTATION_TASKS/001-dir.md"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			wantWired:   true,
			wantErrPart: "regular fileではありません",
		},
		{
			name: "symlink target rejected",
			plan: func(t *testing.T, repoRoot string) {
				// repository外の実fileへ向くsymlinkを要求正本にできない。os.Statは辿って
				// 成立してしまうため、Lstatのfile typeで拒否する。
				outside := t.TempDir()
				if err := os.WriteFile(filepath.Join(outside, "outside.md"), []byte("# outside\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(repoRoot, implementationTasksDir), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "outside.md"), filepath.Join(repoRoot, "IMPLEMENTATION_TASKS/link.md")); err != nil {
					t.Fatal(err)
				}
				writePlanWithActive(t, repoRoot, "## ACTIVE\n\n- `IMPLEMENTATION_TASKS/link.md`\n")
			},
			wantWired:   true,
			wantErrPart: "regular fileではありません",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			tt.plan(t, repoRoot)
			path, wired, err := resolveActiveTaskPath(repoRoot)
			if tt.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("error = %v want %qを含む", err, tt.wantErrPart)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if wired != tt.wantWired || path != tt.wantPath {
				t.Fatalf("resolve = (%q,%v) want (%q,%v)", path, wired, tt.wantPath, tt.wantWired)
			}
		})
	}
}

// TestExecuteNewTaskRecordsActiveTaskAndPromptBlockは解決済みACTIVE task pathがtask stateへ
// 固定され、worker/reviewer両promptが同じfileの本文読み込み指示を持つproduction因果を固定する。
// task名や実装者要約ではなくtask file本文を毎回の呼出へ渡す配線の回帰防止。
func TestExecuteNewTaskRecordsActiveTaskAndPromptBlock(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, planGuardSeed)
	w, r, _, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}, "", 0, nil)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if got := st.ReadOr(activeTaskStateKey, ""); got != activeTaskGuardPath {
		t.Fatalf("active-task state = %q want %q", got, activeTaskGuardPath)
	}
	if len(r.prompts) != 2 {
		t.Fatalf("worker/reviewer 2呼出が必要: %d", len(r.prompts))
	}
	for i, prompt := range r.prompts {
		if !strings.Contains(prompt, "ACTIVE_TASK_FILE: "+activeTaskGuardPath) {
			t.Fatalf("prompt %dがACTIVE task file読み込み指示を欠いています:\n%s", i, prompt)
		}
		if !strings.Contains(prompt, "Acceptance criteria") || !strings.Contains(prompt, "task file本文") {
			t.Fatalf("prompt %dがtask file本文確認指示を欠いています:\n%s", i, prompt)
		}
	}
}

// TestPlanWithoutActiveFailsClosedBeforeCallはplanが存在するのにACTIVE欄から要求正本を解決
// できない場合、model呼出前にfail closedすることを固定する。解決できないまま呼ぶとtask名だけの
// 曖昧な要求で作業が始まる。
func TestPlanWithoutActiveFailsClosedBeforeCall(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, "# plan without active\n")
	w, r, out, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
	}, "", 0, nil)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if len(r.prompts) != 0 {
		t.Fatalf("ACTIVE解決失敗時はmodel呼出前に停止すべき: %d", len(r.prompts))
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("task status = %q want waiting-sol-review", st.TaskStatus())
	}
	if _, err := st.LoadResumeCheckpoint(); err == nil {
		t.Fatal("ACTIVE解決失敗のfail closed後にresume checkpointが残っています")
	}
	if !strings.Contains(out.String(), "一意に解決できません") {
		t.Fatalf("ACTIVE解決失敗理由が出力されていません:\n%s", out.String())
	}
	events := 0
	for _, l := range taskLogs(t, st) {
		if l.CallType == state.CallTypeEvent && l.Outcome == parentMetadataGuardSurface.activeUnresolvableOutcome() {
			events++
		}
	}
	if events != 1 {
		t.Fatalf("active_unresolvable event = %d want 1", events)
	}
}

// TestActiveTaskFileDeletionFailsClosedBeforeCallはtask開始後に親Codex以外の何者かがACTIVE
// task fileを削除した場合、次のworker呼出前にfail closedすることを固定する。固定済みpathは
// Plan再解決ではなく実在確認で守る。
func TestActiveTaskFileDeletionFailsClosedBeforeCall(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, planGuardSeed)
	st := newStateStoreT(t)
	if err := st.Write(activeTaskStateKey, activeTaskGuardPath); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("last-request", "request"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusWaitingSolReview); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repoRoot, activeTaskGuardPath)); err != nil {
		t.Fatal(err)
	}
	w, r, out := planFileDecisionWorkflow(t, st, repoRoot, "worker-explicit-fix", nil)

	if err := w.ExecuteExplicitFix("fix"); err != nil {
		t.Fatal(err)
	}
	if len(r.prompts) != 0 {
		t.Fatalf("ACTIVE task file欠損時はmodel呼出前に停止すべき: %d", len(r.prompts))
	}
	if !strings.Contains(out.String(), "ACTIVE task file "+activeTaskGuardPath+"がworking treeへ存在しません") {
		t.Fatalf("ACTIVE task file欠損理由が出力されていません:\n%s", out.String())
	}
}

// TestDecisionAndFixPromptsCarryActiveTaskBlockはdecision・explicit fix・auto-fix各worker経路の
// promptも同じ要求正本blockを保つことを固定する。経路ごとに要求源が失われるとreviewerだけが
// task契約を読む非対称状態になる。
func TestDecisionAndFixPromptsCarryActiveTaskBlock(t *testing.T) {
	review := packet.Result{Status: packet.StatusImplemented, Risk: packet.RiskLow, Summary: "done"}
	if got := decisionPrompt("r", "d", activeTaskGuardPath); !strings.Contains(got, "ACTIVE_TASK_FILE: "+activeTaskGuardPath) {
		t.Fatalf("decisionPromptが要求源blockを欠いています:\n%s", got)
	}
	if got := explicitFixPrompt("r", "d", "prev", "fix", activeTaskGuardPath); !strings.Contains(got, "ACTIVE_TASK_FILE: "+activeTaskGuardPath) {
		t.Fatalf("explicitFixPromptが要求源blockを欠いています:\n%s", got)
	}
	if got := automaticFixPrompt("r", "d", review, activeTaskGuardPath); !strings.Contains(got, "ACTIVE_TASK_FILE: "+activeTaskGuardPath) {
		t.Fatalf("automaticFixPromptが要求源blockを欠いています:\n%s", got)
	}
	if got := reportOnlyFixPrompt("r", "d", review, activeTaskGuardPath); !strings.Contains(got, "ACTIVE_TASK_FILE: "+activeTaskGuardPath) {
		t.Fatalf("reportOnlyFixPromptが要求源blockを欠いています:\n%s", got)
	}
	if got := decisionPrompt("r", "d", ""); strings.Contains(got, "ACTIVE_TASK_FILE") {
		t.Fatalf("配線なしrepoのpromptへ要求源blockを付けてはいけません:\n%s", got)
	}
}

// TestResumePromptCarriesOriginalActiveTaskBlockはrate-limit/provider停止のresumeが保存済み
// 前回指示を再送するため、compaction後もACTIVE task file指示が失われないことを固定する。
func TestResumePromptCarriesOriginalActiveTaskBlock(t *testing.T) {
	original := newTaskPrompt("request", activeTaskGuardPath)
	got := resumePrompt(state.ResumeCheckpoint{OriginalPrompt: original})
	if !strings.Contains(got, "ACTIVE_TASK_FILE: "+activeTaskGuardPath) {
		t.Fatalf("resumePromptが元指示の要求源blockを失っています:\n%s", got)
	}
}

// TestExecuteNewTaskClearsStaleActiveTaskStateは新task開始時に前taskのACTIVE固定を除去し、
// planの無いrepoでは初回解決成功として空値を設定済みにすることを固定する。設定済み空値と
// 未設定を区別しないと、ACTIVE解決fail closed後の修復再開で要求正本なし・旧task参照のまま
// modelを呼べてしまう。
func TestExecuteNewTaskClearsStaleActiveTaskState(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, r, _, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}, "", 0, nil)
	if err := st.Write(activeTaskStateKey, "IMPLEMENTATION_TASKS/999-stale.md"); err != nil {
		t.Fatal(err)
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if !st.Exists(activeTaskStateKey) {
		t.Fatal("planの無いrepoでも初回解決成功として空値を設定済みにする必要があります")
	}
	if got, err := st.Read(activeTaskStateKey); err != nil || got != "" {
		t.Fatalf("active-task state = (%q,%v) want 設定済み空値", got, err)
	}
	for i, prompt := range r.prompts {
		if strings.Contains(prompt, "ACTIVE_TASK_FILE") {
			t.Fatalf("planの無いrepoのprompt %dへ要求源blockを付けてはいけません:\n%s", i, prompt)
		}
	}
}

// TestActiveResolutionFailureClearsStaleActiveTaskStateはACTIVE解決fail closed時に前taskの
// 固定値が残らないことを固定する。残ると修復後の継続呼出が旧taskを読み込める。
func TestActiveResolutionFailureClearsStaleActiveTaskState(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, "# plan without active\n")
	w, r, _, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
	}, "", 0, nil)
	if err := st.Write(activeTaskStateKey, "IMPLEMENTATION_TASKS/999-stale.md"); err != nil {
		t.Fatal(err)
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.Exists(activeTaskStateKey) {
		t.Fatalf("ACTIVE解決失敗後もactive-task stateが残っています: %q", st.ReadOr(activeTaskStateKey, ""))
	}
	if len(r.prompts) != 0 {
		t.Fatalf("ACTIVE解決失敗時はmodel呼出前に停止すべき: %d", len(r.prompts))
	}
}

// TestActiveTaskNonRegularFileFailsClosedBeforeCallは`.md`以外・directory・symlink等の
// 配置契約外対象をACTIVE解決が受理したままmodelを呼ばないことをproduction pathで固定する。
// symlinkはrepository外の実fileへ向いても解決時に拒否される。
func TestActiveTaskNonRegularFileFailsClosedBeforeCall(t *testing.T) {
	repoRoot := initMutationRepo(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "outside.md"), []byte("# outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, implementationTasksDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "outside.md"), filepath.Join(repoRoot, "IMPLEMENTATION_TASKS/link.md")); err != nil {
		t.Fatal(err)
	}
	writePlanFileContent(t, repoRoot, "# plan\n\n## ACTIVE\n\n- `IMPLEMENTATION_TASKS/link.md`\n")
	w, r, out, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
	}, "", 0, nil)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if len(r.prompts) != 0 {
		t.Fatalf("配置契約外対象はmodel呼出前に停止すべき: %d", len(r.prompts))
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("task status = %q want waiting-sol-review", st.TaskStatus())
	}
	if !strings.Contains(out.String(), "regular fileではありません") {
		t.Fatalf("non-regular拒否理由が出力されていません:\n%s", out.String())
	}
	events := 0
	for _, l := range taskLogs(t, st) {
		if l.CallType == state.CallTypeEvent && l.Outcome == parentMetadataGuardSurface.activeUnresolvableOutcome() {
			events++
		}
	}
	if events != 1 {
		t.Fatalf("active_unresolvable event = %d want 1", events)
	}
}

// TestExplicitFixAfterParentRepairResolvesActiveTaskはACTIVE解決fail closed後に親Codexが
// Plan・task fileを修復して同じtaskを--fixで再開した場合、state未設定を検出してACTIVEを
// 再解決・固定してからpromptを作るproduction経路を固定する。
func TestExplicitFixAfterParentRepairResolvesActiveTask(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, "# plan without active\n")
	w, r, _, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("fixed")},
		{output: passPacket()},
	}, "", 0, nil)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if len(r.prompts) != 0 {
		t.Fatalf("ACTIVE解決失敗時はmodel呼出前に停止すべき: %d", len(r.prompts))
	}
	// 親Codexが停止期間中にPlanとACTIVE task fileを修復した状態を再現する。
	writePlanFileContent(t, repoRoot, planGuardSeed)

	if err := w.ExecuteExplicitFix("修復後の継続"); err != nil {
		t.Fatal(err)
	}
	if got := st.ReadOr(activeTaskStateKey, ""); got != activeTaskGuardPath {
		t.Fatalf("修復再開後のactive-task state = %q want %q", got, activeTaskGuardPath)
	}
	if len(r.prompts) != 2 {
		t.Fatalf("修復再開後はworker fixとreviewerの2呼出が必要: %d", len(r.prompts))
	}
	for i, prompt := range r.prompts {
		if !strings.Contains(prompt, "ACTIVE_TASK_FILE: "+activeTaskGuardPath) {
			t.Fatalf("修復再開後のprompt %dが要求源blockを欠いています:\n%s", i, prompt)
		}
	}
}

// TestExplicitFixStillUnresolvableFailsClosedAgainは修復が不十分なまま--fixされた場合、
// ACTIVE再解決失敗を0 model callで再度fail closedすることを固定する。
func TestExplicitFixStillUnresolvableFailsClosedAgain(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, "# plan without active\n")
	w, r, out, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
	}, "", 0, nil)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if err := w.ExecuteExplicitFix("まだ修復されていない"); err != nil {
		t.Fatal(err)
	}
	if len(r.prompts) != 0 {
		t.Fatalf("再解決失敗時はmodel呼出前に停止すべき: %d", len(r.prompts))
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("task status = %q want waiting-sol-review", st.TaskStatus())
	}
	if _, err := st.LoadResumeCheckpoint(); err == nil {
		t.Fatal("再解決失敗のfail closed後にresume checkpointが残っています")
	}
	if !strings.Contains(out.String(), "一意に解決できません") {
		t.Fatalf("再解決失敗理由が出力されていません:\n%s", out.String())
	}
	events := 0
	for _, l := range taskLogs(t, st) {
		if l.CallType == state.CallTypeEvent && l.Outcome == parentMetadataGuardSurface.activeUnresolvableOutcome() {
			events++
		}
	}
	if events != 2 {
		t.Fatalf("active_unresolvable event = %d want 2 (new task + fix)", events)
	}
}

// TestEnsureActiveTaskPathDoesNotSwapFixedTaskは一度固定したACTIVE task pathをPlanのACTIVE欄
// 変更で別taskへすり替えないことを固定する。停止中のPlan切り替えは次task開始時にだけ反映される。
func TestEnsureActiveTaskPathDoesNotSwapFixedTask(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, planGuardSeed)
	w, _, _, st := newPlanFileWorkflow(t, repoRoot, nil, "", 0, nil)
	fixedPath := "IMPLEMENTATION_TASKS/001-fixed.md"
	if err := st.Write(activeTaskStateKey, fixedPath); err != nil {
		t.Fatal(err)
	}

	got, err := w.ensureActiveTaskPath("worker-explicit-fix")
	if err != nil {
		t.Fatal(err)
	}
	if got != fixedPath {
		t.Fatalf("ensureActiveTaskPath = %q want 固定済みの %q", got, fixedPath)
	}
}
