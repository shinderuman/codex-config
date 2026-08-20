package workflow

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

const planGuardSeed = "parent owned plan\n"

func writePlanFileContent(t *testing.T, repoRoot string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repoRoot, implementationPlanFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newPlanFileWorkflow(
	t *testing.T,
	repoRoot string,
	steps []runnerStep,
	mutatePhase string,
	mutateSkipCalls int,
	mutate func(string) error,
) (*Workflow, *mutatingRunner, *bytes.Buffer, *state.StateStore) {
	t.Helper()
	st := newStateStoreT(t)
	r := &mutatingRunner{
		repoRoot:        repoRoot,
		steps:           steps,
		mutate:          mutate,
		mutatePhase:     mutatePhase,
		mutateSkipCalls: mutateSkipCalls,
	}
	out := &bytes.Buffer{}
	w := newMutationWorkflowShell(t, st)
	w.runner = r
	w.output = out
	w.config.RepoRoot = repoRoot
	w.captureSnapshot = state.CaptureGitSnapshot
	return w, r, out, st
}

func mutatePlanFile(repoRoot string) error {
	return os.WriteFile(filepath.Join(repoRoot, implementationPlanFile), []byte("glm edited plan\n"), 0o644)
}

// requirePlanFileFailClosedはplan file不変性違反のfail closed終端共通の事後条件を検証する。
// reviewerを呼ばず(または次のreviewerを呼ばず)Sol確認へ昇格し、resume可能な停止状態を残さない。
func requirePlanFileFailClosed(t *testing.T, st *state.StateStore, r *mutatingRunner, out *bytes.Buffer, wantReason string, wantCalls int) {
	t.Helper()
	if got, want := len(r.prompts), wantCalls; got != want {
		t.Fatalf("model呼出回数 = %d want %d(次のreviewer開始前に停止すべき): %v", got, want, r.phases)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("task status = %q want waiting-sol-review", st.TaskStatus())
	}
	if _, err := st.LoadResumeCheckpoint(); err == nil {
		t.Fatal("plan file違反時にresume checkpointが残っています")
	}
	pkt := lastPacketFromOutput(t, out.String())
	if pkt.Status() != "NEEDS_SOL_REVIEW" || pkt.Risk() != "HIGH" {
		t.Fatalf("packet = %s/%s want NEEDS_SOL_REVIEW/HIGH:\n%s", pkt.Status(), pkt.Risk(), out.String())
	}
	if !strings.Contains(out.String(), wantReason) {
		t.Fatalf("fail closed理由 %qが出力されていません:\n%s", wantReason, out.String())
	}
}

// TestPlanFileWorkerMutationFailsClosedBeforeReviewは通常worker呼出中のplan file編集を
// 呼出前後の内容比較で検出し、reviewerを呼ばずSol確認へ昇格するproduction因果を固定する。
// GLMの変更内容はbaselineへ自動復元せず、実行されたtask呼出はtelemetryへ残す。
func TestPlanFileWorkerMutationFailsClosedBeforeReview(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, planGuardSeed)
	w, r, out, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}, "worker-new", 0, mutatePlanFile)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	requirePlanFileFailClosed(t, st, r, out, "内容が変化", 1)
	if content, err := os.ReadFile(filepath.Join(repoRoot, implementationPlanFile)); err != nil || string(content) != "glm edited plan\n" {
		t.Fatalf("GLMのplan変更がbaselineへ自動復元されています: %q %v", content, err)
	}
	taskViolations, mismatchEvents := 0, 0
	for _, l := range taskLogs(t, st) {
		if l.CallType == state.CallTypeTask && l.Outcome == "plan_file_violation" {
			taskViolations++
		}
		if l.CallType == state.CallTypeEvent && l.Outcome == "plan_file_mismatch" {
			mismatchEvents++
		}
	}
	if taskViolations != 1 || mismatchEvents != 1 {
		t.Fatalf("telemetry = task violation %d / mismatch event %d want 1/1", taskViolations, mismatchEvents)
	}
}

// TestPlanFileAbsentWorkerCreationFailsClosedはplan欠損時にworkerが同fileを生成した場合を
// 呼出前後の存在比較で検出する。生成禁止契約をwrapper側で機械強制する。
func TestPlanFileAbsentWorkerCreationFailsClosed(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, r, out, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}, "worker-new", 0, mutatePlanFile)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	requirePlanFileFailClosed(t, st, r, out, "存在しない状態から新規作成", 1)
}

// TestPlanFileDeletionFailsClosedはworkerによるplan削除も同じ不変性違反として検出する。
func TestPlanFileDeletionFailsClosed(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, planGuardSeed)
	w, r, out, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}, "worker-new", 0, func(repoRoot string) error {
		return os.Remove(filepath.Join(repoRoot, implementationPlanFile))
	})

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	requirePlanFileFailClosed(t, st, r, out, "削除されました", 1)
}

// TestPlanFileUnchangedProceedsToReviewはplanが親Codex置きの内容のまま変更無ければ
// 通常flowを妨げないことを確認する(false positive回帰)。
func TestPlanFileUnchangedProceedsToReview(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, planGuardSeed)
	w, r, _, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}, "", 0, nil)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("plan不変時は通常reviewを通ってcompleteになるべき: %q", st.TaskStatus())
	}
	if len(r.prompts) != 2 {
		t.Fatalf("worker/reviewer 2呼出が必要: %d", len(r.prompts))
	}
}

// TestPlanFileAbsentThroughoutProceedsはGit repository内でplanが未追跡のまま欠損して
// いる場合、生成検出以外に干渉しないことを確認する。
func TestPlanFileAbsentThroughoutProceeds(t *testing.T) {
	repoRoot := initMutationRepo(t)
	w, r, _, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}, "", 0, nil)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("plan欠損継続時は通常flowを維持すべき: %q", st.TaskStatus())
	}
	if len(r.prompts) != 2 {
		t.Fatalf("worker/reviewer 2呼出が必要: %d", len(r.prompts))
	}
}

// TestPlanFileTrackedMissingFailsClosedBeforeCallはGit indexがplanを追跡するのにworking treeへ
// 欠損している場合、model呼出前にfail closedする境界を固定する。stagedとcommitted両方の
// 追跡形態で等しく扱い、欠損planの再生成・復元は行わない。
func TestPlanFileTrackedMissingFailsClosedBeforeCall(t *testing.T) {
	cases := map[string]func(t *testing.T, repoRoot string){
		"staged index entry": func(t *testing.T, repoRoot string) {
			gitIn(t, repoRoot, "add", implementationPlanFile)
		},
		"committed tracked file": func(t *testing.T, repoRoot string) {
			gitIn(t, repoRoot, "add", implementationPlanFile)
			gitIn(t, repoRoot, "commit", "-q", "-m", "add plan")
		},
	}
	for name, track := range cases {
		t.Run(name, func(t *testing.T) {
			repoRoot := initMutationRepo(t)
			writePlanFileContent(t, repoRoot, planGuardSeed)
			track(t, repoRoot)
			if err := os.Remove(filepath.Join(repoRoot, implementationPlanFile)); err != nil {
				t.Fatal(err)
			}
			w, r, out, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
				{output: implementedPacket("done")},
				{output: passPacket()},
			}, "", 0, nil)

			if err := w.ExecuteNewTask("request"); err != nil {
				t.Fatal(err)
			}
			if len(r.prompts) != 0 {
				t.Fatalf("追跡中plan欠損時はmodel呼出前に停止すべき: %d", len(r.prompts))
			}
			if st.TaskStatus() != state.TaskStatusWaitingSolReview {
				t.Fatalf("task status = %q want waiting-sol-review", st.TaskStatus())
			}
			if _, err := st.LoadResumeCheckpoint(); err == nil {
				t.Fatal("追跡中plan欠損のfail closed後にresume checkpointが残っています")
			}
			pkt := lastPacketFromOutput(t, out.String())
			if pkt.Status() != "NEEDS_SOL_REVIEW" || pkt.Risk() != "HIGH" {
				t.Fatalf("packet = %s/%s want NEEDS_SOL_REVIEW/HIGH:\n%s", pkt.Status(), pkt.Risk(), out.String())
			}
			if !strings.Contains(out.String(), "working treeへ存在しません") {
				t.Fatalf("追跡中plan欠損理由が出力されていません:\n%s", out.String())
			}
			if _, err := os.Stat(filepath.Join(repoRoot, implementationPlanFile)); !os.IsNotExist(err) {
				t.Fatalf("欠損planが復元・生成されています: %v", err)
			}
			events := 0
			for _, l := range taskLogs(t, st) {
				if l.Outcome == "plan_file_missing" && strings.HasSuffix(l.Phase, "plan-file-check") {
					events++
				}
			}
			if events != 1 {
				t.Fatalf("plan_file_missing event = %d want 1", events)
			}
		})
	}
}

// TestPlanFileTrackingIndeterminateFailsClosedBeforeCallはGit repository内でplan追跡判定が
// 失敗した場合を未追跡へ畳まず、baseline取得不能としてmodel呼出前にfail closedする境界を
// 固定する。一時的なgit異常でtracked欠損検出を素通りさせない。
func TestPlanFileTrackingIndeterminateFailsClosedBeforeCall(t *testing.T) {
	repoRoot := initMutationRepo(t)
	// .git markerは残し、index参照だけ失敗する状態を作る。
	if err := os.Remove(filepath.Join(repoRoot, ".git", "HEAD")); err != nil {
		t.Fatal(err)
	}
	w, r, out, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}, "", 0, nil)
	w.captureSnapshot = func(string) (state.GitSnapshot, error) {
		return fixedSnapshot, nil
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if len(r.prompts) != 0 {
		t.Fatalf("追跡判定失敗時はmodel呼出前に停止すべき: %d", len(r.prompts))
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("task status = %q want waiting-sol-review", st.TaskStatus())
	}
	if _, err := st.LoadResumeCheckpoint(); err == nil {
		t.Fatal("追跡判定失敗のfail closed後にresume checkpointが残っています")
	}
	pkt := lastPacketFromOutput(t, out.String())
	if pkt.Status() != "NEEDS_SOL_REVIEW" || pkt.Risk() != "HIGH" {
		t.Fatalf("packet = %s/%s want NEEDS_SOL_REVIEW/HIGH:\n%s", pkt.Status(), pkt.Risk(), out.String())
	}
	if !strings.Contains(out.String(), "Git追跡判定に失敗") {
		t.Fatalf("追跡判定失敗理由が出力されていません:\n%s", out.String())
	}
	events := 0
	for _, l := range taskLogs(t, st) {
		if l.Outcome == "plan_file_unavailable" && strings.HasSuffix(l.Phase, "plan-file-check") {
			events++
		}
	}
	if events != 1 {
		t.Fatalf("plan_file_unavailable event = %d want 1", events)
	}
}

// TestPlanFileUntrackedAbsentNonGitRepoProceedsはgit repository外でもplan未追跡欠損を
// 通常作業として扱うことを確認する。glm-workerはplanを置かない他repositoryでも使うため。
// snapshot取得はgit外で失敗するため既定stubへ戻し、plan guard境界だけを検証する。
func TestPlanFileUntrackedAbsentNonGitRepoProceeds(t *testing.T) {
	repoRoot := t.TempDir()
	w, r, _, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}, "", 0, nil)
	w.captureSnapshot = func(string) (state.GitSnapshot, error) {
		return fixedSnapshot, nil
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("git外repoのplan未追跡欠損は通常flowを維持すべき: %q", st.TaskStatus())
	}
	if len(r.prompts) != 2 {
		t.Fatalf("worker/reviewer 2呼出が必要: %d", len(r.prompts))
	}
}

// TestPlanFileDecisionWorkerMutationFailsClosedはSol判断後worker経路でも同じ検出を強制する。
func TestPlanFileDecisionWorkerMutationFailsClosed(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, planGuardSeed)
	st := newStateStoreT(t)
	if err := st.Write("last-request", "request"); err != nil {
		t.Fatal(err)
	}
	if err := st.Touch("pending-decision"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusWaitingDecision); err != nil {
		t.Fatal(err)
	}
	w, r, out := planFileDecisionWorkflow(t, st, repoRoot, "worker-decision", mutatePlanFile)

	if err := w.ExecuteDecision("decision"); err != nil {
		t.Fatal(err)
	}
	requirePlanFileFailClosed(t, st, r, out, "内容が変化", 1)
}

// TestPlanFileExplicitFixMutationFailsClosedは明示fix worker経路でも同じ検出を強制する。
func TestPlanFileExplicitFixMutationFailsClosed(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, planGuardSeed)
	st := newStateStoreT(t)
	if err := st.Write("last-request", "request"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusWaitingSolReview); err != nil {
		t.Fatal(err)
	}
	w, r, out := planFileDecisionWorkflow(t, st, repoRoot, "worker-explicit-fix", mutatePlanFile)

	if err := w.ExecuteExplicitFix("fix instruction"); err != nil {
		t.Fatal(err)
	}
	requirePlanFileFailClosed(t, st, r, out, "内容が変化", 1)
}

func planFileDecisionWorkflow(t *testing.T, st *state.StateStore, repoRoot string, mutatePhase string, mutate func(string) error) (*Workflow, *mutatingRunner, *bytes.Buffer) {
	t.Helper()
	r := &mutatingRunner{
		repoRoot:    repoRoot,
		steps:       []runnerStep{{output: implementedPacket("done")}, {output: passPacket()}},
		mutate:      mutate,
		mutatePhase: mutatePhase,
	}
	out := &bytes.Buffer{}
	w := newMutationWorkflowShell(t, st)
	w.runner = r
	w.output = out
	w.config.RepoRoot = repoRoot
	w.captureSnapshot = state.CaptureGitSnapshot
	return w, r, out
}

// TestPlanFileAutoFixMutationFailsClosedはautomatic fix workerの変更を検出し、
// 次のreviewer(round 2)を呼ばずfail closedする。
func TestPlanFileAutoFixMutationFailsClosed(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, planGuardSeed)
	w, r, out, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: fixRequiredPacket()},
		{output: implementedPacket("fixed")},
		{output: passPacket()},
	}, "worker-auto-fix-1", 0, mutatePlanFile)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	requirePlanFileFailClosed(t, st, r, out, "内容が変化", 3)
}

// TestPlanFileResumeWorkerMutationFailsClosedはrate-limit resume後のworker変更を検出し、
// 保存済みcheckpointを復元せずfail closedする。ExecuteResumeはerrorを出さず停止する。
func TestPlanFileResumeWorkerMutationFailsClosed(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, planGuardSeed)
	st := newStateStoreT(t)
	seedRateLimitedWorkerCheckpoint(t, st, "request")
	w, r, out := planFileDecisionWorkflow(t, st, repoRoot, "worker-new", mutatePlanFile)

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	requirePlanFileFailClosed(t, st, r, out, "内容が変化", 1)
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("resume復元logicがfail closed状態を上書きしています: %q", st.TaskStatus())
	}
}

// TestPlanFileResumeAdoptsParentUpdateAsBaselineは停止中に親Codexが更新したplan内容を
// call開始時baselineとして採用し、変更無しを通常flowとして扱う。
func TestPlanFileResumeAdoptsParentUpdateAsBaseline(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, planGuardSeed)
	st := newStateStoreT(t)
	seedRateLimitedWorkerCheckpoint(t, st, "request")
	writePlanFileContent(t, repoRoot, "parent updated plan\n")
	w, r, _ := planFileDecisionWorkflow(t, st, repoRoot, "", nil)
	r.steps = []runnerStep{{output: implementedPacket("resumed")}, {output: passPacket()}}

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if len(r.prompts) != 2 {
		t.Fatalf("親更新後のresumeは通常どおりworker/reviewerへ進むべき: %d", len(r.prompts))
	}
	if content, err := os.ReadFile(filepath.Join(repoRoot, implementationPlanFile)); err != nil || string(content) != "parent updated plan\n" {
		t.Fatalf("親Codex更新内容が保持されていません: %q %v", content, err)
	}
}

func seedRateLimitedWorkerCheckpoint(t *testing.T, st *state.StateStore, request string) {
	t.Helper()
	if err := st.Write("last-request", request); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:          state.ResumeStageWorker,
		Phase:          "worker-new",
		Role:           state.WorkerRole,
		Model:          "opus",
		Effort:         "high",
		Prompt:         "p",
		OriginalPrompt: "p",
		Request:        request,
		RateLimited:    true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}
}

// TestPlanFileTransientRecoveryResumedTaskMutationFailsClosedはtransient復帰のresumed task
// 呼出中の変更も検出する。provider-unavailable停止やfatal扱いへ上書きせずfail closedする。
func TestPlanFileTransientRecoveryResumedTaskMutationFailsClosed(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, planGuardSeed)
	w, r, out, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: "API Error: 503 Service Unavailable", runErr: errors.New("exit status 1")},
		{output: implementedPacket("resumed")},
		{output: passPacket()},
	}, "worker-new", 0, mutatePlanFile)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	requirePlanFileFailClosed(t, st, r, out, "内容が変化", 2)
	if len(r.probes) != 1 {
		t.Fatalf("probe 1回の成功後にresumed taskで停止すべき: %d", len(r.probes))
	}
}

// TestPlanFileReadErrorFailsClosedBeforeCallはbaseline取得に失敗すると不変性の基準が
// 確認できないため、model呼出を実行せずfail closedすることを固定する。
func TestPlanFileReadErrorFailsClosedBeforeCall(t *testing.T) {
	repoRoot := initMutationRepo(t)
	if err := os.Mkdir(filepath.Join(repoRoot, implementationPlanFile), 0o755); err != nil {
		t.Fatal(err)
	}
	w, r, out, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
	}, "", 0, nil)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if len(r.prompts) != 0 {
		t.Fatalf("baseline取得失敗時はmodel呼出前に停止すべき: %d", len(r.prompts))
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("task status = %q want waiting-sol-review", st.TaskStatus())
	}
	pkt := lastPacketFromOutput(t, out.String())
	if pkt.Status() != "NEEDS_SOL_REVIEW" || pkt.Risk() != "HIGH" {
		t.Fatalf("packet = %s/%s want NEEDS_SOL_REVIEW/HIGH:\n%s", pkt.Status(), pkt.Risk(), out.String())
	}
	if !strings.Contains(out.String(), "baseline取得失敗") {
		t.Fatalf("baseline取得失敗理由が出力されていません:\n%s", out.String())
	}
}

// TestPlanFileReviewerMutationUsesExistingSnapshotInvariantはreviewer呼出をplan guardの
// 対象外とし、reviewerのplan変更は既存review-end snapshot不変性で検出することを固定する。
func TestPlanFileReviewerMutationUsesExistingSnapshotInvariant(t *testing.T) {
	repoRoot := initMutationRepo(t)
	writePlanFileContent(t, repoRoot, planGuardSeed)
	w, r, out, st := newPlanFileWorkflow(t, repoRoot, []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}, "", 0, mutatePlanFile)

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if len(r.prompts) != 2 {
		t.Fatalf("reviewer呼出まで進み既存invariantで停止すべき: %d", len(r.prompts))
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("task status = %q want waiting-sol-review", st.TaskStatus())
	}
	if !strings.Contains(out.String(), "reviewer実行中にrepository状態が変化") {
		t.Fatalf("reviewer変更はreview-end snapshot検出によるべきです:\n%s", out.String())
	}
	for _, l := range taskLogs(t, st) {
		if l.Outcome == "plan_file_mismatch" || l.Outcome == "plan_file_violation" {
			t.Fatalf("reviewer経路へplan guardが適用されています: %+v", l)
		}
	}
}

// TestPlanFileGuardScenarioPinnedInEscapedCorpusはescaped corpusがplan file不変性破壊の
// production検出scenarioを保持することを固定する。corpus契約testは存在するscenarioの妥当性
// だけを検証するため、当該scenarioの削除は本pin検証だけが検知する。
func TestPlanFileGuardScenarioPinnedInEscapedCorpus(t *testing.T) {
	sc, mf := loadCorpus(t)
	found := ""
	for _, s := range sc.Scenarios {
		if s.WorkerMutatesPlanFile {
			found = s.ID
			break
		}
	}
	if found == "" {
		t.Fatal("escaped corpusにplan file不変性破壊scenarioがありません")
	}
	trackedAbsent := ""
	for _, s := range sc.Scenarios {
		if s.PlanFileTrackedAbsent {
			trackedAbsent = s.ID
			break
		}
	}
	if trackedAbsent == "" {
		t.Fatal("escaped corpusに追跡中plan欠損scenarioがありません")
	}
	for _, path := range []string{"AGENTS.md", "codex/glm-worker/prompts/WORKER.md"} {
		listed := false
		for _, e := range mf.InstructionFiles {
			if e.Path != path {
				continue
			}
			for _, sid := range e.Scenarios {
				if sid == found {
					listed = true
				}
			}
		}
		if !listed {
			t.Fatalf("manifestの%sが%sをpinしていません", path, found)
		}
	}
	for _, e := range mf.InstructionFiles {
		if e.Path != "AGENTS.md" {
			continue
		}
		listed := false
		for _, sid := range e.Scenarios {
			if sid == trackedAbsent {
				listed = true
			}
		}
		if !listed {
			t.Fatalf("manifestのAGENTS.mdが%sをpinしていません", trackedAbsent)
		}
	}
}

// TestPlanFileContractWiringはroot AGENTS.md・EVAL.md・plan guard実装の契約文と実装の
// 対応を固定する。文面の並記だけへ依存させないため、guard sentinelと読み取り専用契約の
// 両方が現物へ存在することを要求する。
func TestPlanFileContractWiring(t *testing.T) {
	root := scenarioRepoRoot(t)
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"tracked canonical source",
		"親Codexだけ",
		"読み取り専用",
		"更新候補と根拠をPACKETへ記載して親Codexへ報告",
		"Git indexで追跡されているのにworking treeへ存在しない場合と",
		"Git repository内で追跡判定自体ができない場合",
		"未追跡で最初から存在しない他repositoryおよびGit管理外directoryでは通常作業を許可",
	} {
		if !strings.Contains(string(agents), want) {
			t.Errorf("root AGENTS.mdにplan契約文 %qがありません", want)
		}
	}
	eval, err := os.ReadFile(filepath.Join(root, "EVAL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"tracked canonical source",
		"更新できるのは親Codexだけ",
		"reviewer開始前にfail closed検出",
		"git ls-files",
		"未追跡で最初から存在しないrepositoryでは",
		"index現物",
		"追跡判定不能なGit異常は呼出前fail closed",
		"Git管理外directoryだけを未追跡欠損の許可枠",
	} {
		if !strings.Contains(string(eval), want) {
			t.Errorf("EVAL.mdの計画file bootstrap節に契約文 %qがありません", want)
		}
	}
	managed, err := os.ReadFile(filepath.Join(root, "codex", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(managed), implementationPlanFile) {
		t.Error("global配布用codex/AGENTS.mdへrepository固有のplan規則を入れない")
	}
}
