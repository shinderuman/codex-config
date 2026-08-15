package workflow

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/packet"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

func TestIsCriticalPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		want     bool
		category string
	}{
		{"workflow package drives state machine", "glm-worker/internal/workflow/workflow.go", true, "workflow-package"},
		{"policy file self-protects via workflow package", "glm-worker/internal/workflow/selfprotection.go", true, "workflow-package"},
		{"workflow prompt builder is routing semantics", "glm-worker/internal/workflow/prompts.go", true, "workflow-package"},
		{"packet contract and validation", "glm-worker/internal/packet/packet.go", true, "packet-package"},
		{"packet artifact boundary", "glm-worker/internal/packet/artifacts.go", true, "packet-package"},
		{"runner session/resume/model/safe-mode child process", "glm-worker/internal/runner/runner.go", true, "runner-package"},
		{"runner real model/backend env inflow", "glm-worker/internal/runner/env_override.go", true, "runner-package"},
		{"runner zai 5h retry/fail-closed detection", "glm-worker/internal/runner/zai_limit.go", true, "runner-package"},
		{"app resume/decision/fix dispatch", "glm-worker/internal/app/app.go", true, "app-package"},
		{"app single-execution lock per repo task", "glm-worker/internal/app/lock_unix.go", true, "app-package"},
		{"app single-execution lock other platform", "glm-worker/internal/app/lock_other.go", true, "app-package"},
		{"app status/resume availability and packet output boundary", "glm-worker/internal/app/output.go", true, "app-package"},
		{"config model/risk routing", "glm-worker/internal/config/config.go", true, "config-package"},
		{"state session identity and task lifecycle", "glm-worker/internal/state/store.go", true, "state-critical"},
		{"state checkpoint/resume/retry fail-closed", "glm-worker/internal/state/resume.go", true, "state-critical"},
		{"state baseline snapshot pinning", "glm-worker/internal/state/baseline.go", true, "state-critical"},
		{"state snapshot 3-axis fail-closed", "glm-worker/internal/state/snapshot.go", true, "state-critical"},
		{"state artifact/packet boundary", "glm-worker/internal/state/artifact.go", true, "state-critical"},
		{"state atomic write underpins persistence integrity", "glm-worker/internal/state/store_util.go", true, "state-critical"},
		{"autoresume fail-closed verification of automation", "glm-worker/internal/autoresume/verify.go", true, "autoresume-package"},
		{"autoresume automation TOML entity contract", "glm-worker/internal/autoresume/toml.go", true, "autoresume-package"},
		{"autoresume SQLite row verification", "glm-worker/internal/autoresume/sqlite.go", true, "autoresume-package"},
		{"future internal package defaults critical", "glm-worker/internal/newpkg/engine.go", true, "internal-production"},
		{"nested file under known internal package", "glm-worker/internal/runner/deeper/probe.go", true, "runner-package"},
		{"cmd entrypoint routes CLI flags into app gates", "glm-worker/cmd/glm-worker/main.go", true, "worker-entrypoint"},
		{"future cmd file caught by directory rule", "glm-worker/cmd/glm-worker/wiring.go", true, "worker-entrypoint"},
		{"merge engine for managed settings application", "tools/merge-json/main.go", true, "merge-tool"},
		{"installer applies every managed surface", "install.sh", true, "installer"},
		{"post-merge hook auto-runs installer", ".githooks/post-merge", true, "installer"},
		{"managed claude settings pin model routing and provider", "claude/settings-managed.json", true, "managed-claude-settings"},
		{"managed codex config pins execution envelope", "codex/config-managed.toml", true, "managed-codex-config"},
		{"worker binary dependency boundary", "glm-worker/go.mod", true, "dependency-manifest"},
		{"merge tool dependency boundary", "tools/merge-json/go.mod", true, "dependency-manifest"},
		{"scenario corpus contract is policy", "glm-worker/scenarios/scenarios.json", true, "scenario-corpus"},
		{"scenario manifest hash pin is policy", "glm-worker/scenarios/manifest.json", true, "scenario-corpus"},
		{"future scenario asset caught by directory rule", "glm-worker/scenarios/extra.json", true, "scenario-corpus"},
		{"worker prompt template semantic", "codex/glm-worker/prompts/WORKER.md", true, "managed-prompts"},
		{"future prompt file caught by directory rule", "codex/glm-worker/prompts/AUDITOR.md", true, "managed-prompts"},
		{"worker common-code quality rule", "codex/instructions/worker/common-code.md", true, "managed-instructions"},
		{"glm-packets contract rule", "codex/instructions/glm-packets.md", true, "managed-instructions"},
		{"rules file", "codex/rules/glm-worker.rules", true, "managed-rules"},
		{"managed AGENTS quality gate", "codex/AGENTS.md", true, "managed-agents"},
		{"repository root AGENTS bootstrap contract", "AGENTS.md", true, "repo-agents"},

		{"test files excluded keeps test-only 4.7", "glm-worker/internal/workflow/workflow_test.go", false, "test"},
		{"packet test excluded", "glm-worker/internal/packet/packet_test.go", false, "test"},
		{"selfprotection test excluded", "glm-worker/internal/workflow/selfprotection_test.go", false, "test"},
		{"scenario harness test excluded", "glm-worker/internal/workflow/scenario_test.go", false, "test"},
		{"runner test excluded", "glm-worker/internal/runner/runner_test.go", false, "test"},
		{"app test excluded", "glm-worker/internal/app/command_test.go", false, "test"},
		{"autoresume test excluded", "glm-worker/internal/autoresume/verify_test.go", false, "test"},
		{"merge tool test excluded", "tools/merge-json/main_test.go", false, "test"},
		{"state stats observation only", "glm-worker/internal/state/stats.go", false, "observation"},
		{"state telemetry observation only", "glm-worker/internal/state/telemetry.go", false, "observation"},
		{"README unrelated doc", "README.md", false, "docs"},
		{"EVAL unrelated doc", "EVAL.md", false, "docs"},
		{"license doc", "LICENSE", false, "docs"},
		{"gitignore repo metadata", ".gitignore", false, "repo-metadata"},
		{"install smoke harness", "tests/install_smoke.sh", false, "test-harness"},
		{"isolation smoke harness", "glm-worker/scripts/isolation-smoke.sh", false, "test-harness"},
		{"nested AGENTS outside repo root contract", "docs/AGENTS.md", false, ""},
		{"unclassified future tool stays low until classified", "tools/newtool/main.go", false, ""},
		{"empty path", "", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, cat := IsCriticalPath(tt.path)
			if got != tt.want {
				t.Fatalf("IsCriticalPath(%q)=%v want %v", tt.path, got, tt.want)
			}
			if cat != tt.category {
				t.Fatalf("category=%q want %q", cat, tt.category)
			}
		})
	}
}

// TestSelfProtectionClassifiesEveryTrackedFileはrepoの全tracked fileがcritical・非対象
// いずれかの意味分類を持つことを強制する。将来file追加時に分類を決めないまま放置すると
// 本testが失敗し、HIGH/LOWの意味判断を強制する。
func TestSelfProtectionClassifiesEveryTrackedFile(t *testing.T) {
	root := scenarioRepoRoot(t)
	if _, err := exec.Command("git", "-C", root, "rev-parse", "--git-dir").Output(); err != nil {
		t.Skipf("git metadata absent under %s: tracked-file completeness unverifiable", root)
	}
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	paths := splitNul(out)
	if len(paths) == 0 {
		t.Fatal("git ls-files returned no tracked files")
	}
	critical, nonCritical := 0, 0
	for _, p := range paths {
		isCritical, category := IsCriticalPath(p)
		if category == "" {
			t.Errorf("tracked file %q has no self-protection classification; decide critical or non-critical in selfprotection.go", p)
			continue
		}
		if isCritical {
			critical++
		} else {
			nonCritical++
		}
	}
	if critical == 0 || nonCritical == 0 {
		t.Fatalf("classification universe collapsed: critical=%d non-critical=%d", critical, nonCritical)
	}
}

func TestClassifySelfProtectionAggregatesCategories(t *testing.T) {
	dec := classifySelfProtection([]string{
		"README.md",
		"glm-worker/internal/workflow/workflow.go",
		"codex/glm-worker/prompts/WORKER.md",
		"glm-worker/internal/workflow/workflow_test.go",
	})
	if !dec.High {
		t.Fatal("critical pathを含むならHIGH")
	}
	if dec.Source != "managed-prompts,workflow-package" {
		t.Fatalf("Source=%q", dec.Source)
	}
	if dec.HitPath != "glm-worker/internal/workflow/workflow.go" {
		t.Fatalf("HitPath=%q", dec.HitPath)
	}
}

func TestClassifySelfProtectionEmptyIsLow(t *testing.T) {
	dec := classifySelfProtection([]string{"README.md", "tests/install_smoke.sh", "glm-worker/internal/state/stats.go"})
	if dec.High {
		t.Fatalf("非対象pathのみはLOWであるべき: %#v", dec)
	}
}

func TestClassifySelfProtectionPolicyFileSelfProtects(t *testing.T) {
	ok, _ := IsCriticalPath("glm-worker/internal/workflow/selfprotection.go")
	if !ok {
		t.Fatal("policy file自身(selfprotection.go)はworkflow package対象でHIGHにならなければならない")
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "tester")
	return dir
}

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func writeRepoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectChangedPathsDetectsWorktreeUntrackedAndCommitted(t *testing.T) {
	dir := initGitRepo(t)
	writeRepoFile(t, dir, "README.md", "init")
	writeRepoFile(t, dir, "codex/AGENTS.md", "rules")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")
	baseline := gitCmd(t, dir, "rev-parse", "HEAD")

	writeRepoFile(t, dir, "codex/AGENTS.md", "rules changed")
	writeRepoFile(t, dir, "codex/glm-worker/prompts/WORKER.md", "prompt")
	writeRepoFile(t, dir, "glm-worker/internal/workflow/workflow.go", "package workflow")
	gitCmd(t, dir, "add", "glm-worker/internal/workflow/workflow.go")
	gitCmd(t, dir, "commit", "-m", "workflow")

	paths, err := collectChangedPaths(dir, baseline)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range paths {
		got[p] = true
	}
	for _, want := range []string{
		"codex/AGENTS.md",
		"codex/glm-worker/prompts/WORKER.md",
		"glm-worker/internal/workflow/workflow.go",
	} {
		if !got[want] {
			t.Fatalf("changed pathsに%sがない: %#v", want, paths)
		}
	}
	dec := classifySelfProtection(paths)
	if !dec.High {
		t.Fatalf("worktree/untracked/commitのcritical変更をHIGHと判定すべき: %#v", dec)
	}
}

func TestCollectChangedPathsEmptyTreeFallbackIsConservative(t *testing.T) {
	dir := initGitRepo(t)
	writeRepoFile(t, dir, "glm-worker/internal/packet/packet.go", "package packet")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "critical")

	paths, err := collectChangedPaths(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	dec := classifySelfProtection(paths)
	if !dec.High {
		t.Fatalf("baseline-head欠落時は空tree fall backで保守的HIGHになるべき: %#v", dec)
	}
}

func TestSelfProtectionEscalatesLowWorkerToHighRiskReviewer(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.collectChangedPaths = func(string, string) ([]string, error) {
		return []string{"glm-worker/internal/workflow/workflow.go"}, nil
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("LOW自己申告でもcritical path変更はPASSを拒否すべき: status=%q", st.TaskStatus())
	}
	if strings.Join(r.models, ",") != "opus,sonnet,sonnet" {
		t.Fatalf("初回reviewerはHighRiskReviewerModelへ昇格すべき: %#v", r.models)
	}
	review := st.ReadOr("last-review", "")
	if !strings.Contains(review, "STATUS: NEEDS_SOL_REVIEW") || !strings.Contains(review, "RISK: HIGH") {
		t.Fatalf("risk floor強制packetでない: %s", review)
	}
}

func TestSelfProtectionManagedPromptMarkdownIsHigh(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.collectChangedPaths = func(string, string) ([]string, error) {
		return []string{"codex/glm-worker/prompts/REVIEWER.md"}, nil
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("prompt template .md意味変更は拡張子でLOWにせずHIGH: status=%q", st.TaskStatus())
	}
	if strings.Join(r.models, ",") != "opus,sonnet,sonnet" {
		t.Fatalf("models=%#v", r.models)
	}
}

func TestSelfProtectionCmdEntrypointChangeIsHigh(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.collectChangedPaths = func(string, string) ([]string, error) {
		return []string{"glm-worker/cmd/glm-worker/main.go"}, nil
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("entrypoint変更は現状薄くてもCLI routing・gate呼出の境界としてHIGH: status=%q", st.TaskStatus())
	}
	if strings.Join(r.models, ",") != "opus,sonnet,sonnet" {
		t.Fatalf("models=%#v", r.models)
	}
	risk := w.computeEffectiveRisk(packetOfRisk("LOW"), 0, false, false)
	if !strings.Contains(risk.source, "self-protection:worker-entrypoint") {
		t.Fatalf("self-protection sourceにentrypoint分類がない: %s", risk.source)
	}
}

func TestSelfProtectionNonCriticalKeepsLowRiskPass(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.collectChangedPaths = func(string, string) ([]string, error) {
		return []string{"README.md", "EVAL.md", "tests/install_smoke.sh", "glm-worker/scripts/isolation-smoke.sh"}, nil
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("非対象doc/config-only変更は通常PASSを維持すべき: status=%q", st.TaskStatus())
	}
	if strings.Join(r.models, ",") != "opus,haiku" {
		t.Fatalf("非対象変更は4.7 reviewerを使うべき: %#v", r.models)
	}
}

func TestSelfProtectionTestOnlyChangeStaysLow(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.collectChangedPaths = func(string, string) ([]string, error) {
		return []string{
			"glm-worker/internal/workflow/workflow_test.go",
			"glm-worker/internal/workflow/selfprotection_test.go",
		}, nil
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusComplete {
		t.Fatalf("test-only変更は4.7/PASSを維持すべき: status=%q", st.TaskStatus())
	}
	if strings.Join(r.models, ",") != "opus,haiku" {
		t.Fatalf("test-only変更は4.7 reviewerを使うべき: %#v", r.models)
	}
}

func TestSelfProtectionClassifyErrorFailsSafeHigh(t *testing.T) {
	st := newStateStoreT(t)
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacket("done")},
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.collectChangedPaths = func(string, string) ([]string, error) {
		return nil, errSelfProtectionSentinel
	}

	if err := w.ExecuteNewTask("request"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("classify失敗時は保守的HIGHでPASSを拒否すべき: status=%q", st.TaskStatus())
	}
	if strings.Join(r.models, ",") != "opus,sonnet,sonnet" {
		t.Fatalf("classify失敗時はHighRiskReviewerModelを使うべき: %#v", r.models)
	}
}

func TestSelfProtectionEffectiveRiskDistinguishesWorkerDeclaredFromWrapper(t *testing.T) {
	st := newStateStoreT(t)

	t.Run("low worker plus critical path escalates with self-protection source only", func(t *testing.T) {
		w := newWorkflowT(t, st, &scriptedRunner{})
		w.collectChangedPaths = func(string, string) ([]string, error) {
			return []string{"glm-worker/internal/packet/packet.go"}, nil
		}
		risk := w.computeEffectiveRisk(packetOfRisk("LOW"), 0, false, false)
		if !risk.high {
			t.Fatal("LOW worker + critical path はeffective HIGHへ昇格すべき")
		}
		if !strings.Contains(risk.source, "self-protection:") {
			t.Fatalf("sourceにself-protectionがない: %s", risk.source)
		}
		if strings.Contains(risk.source, "worker-declared") {
			t.Fatalf("workerがLOW申告時、effective sourceにworker-declaredは入らない: %s", risk.source)
		}
	})

	t.Run("high worker records both worker-declared and self-protection", func(t *testing.T) {
		w := newWorkflowT(t, st, &scriptedRunner{})
		w.collectChangedPaths = func(string, string) ([]string, error) {
			return []string{"glm-worker/internal/workflow/workflow.go"}, nil
		}
		risk := w.computeEffectiveRisk(packetOfRisk("HIGH"), 0, false, false)
		if !risk.high {
			t.Fatal("HIGH")
		}
		if !strings.Contains(risk.source, "worker-declared") || !strings.Contains(risk.source, "self-protection:") {
			t.Fatalf("HIGH申告+critical pathは両方のsourceを記録すべき: %s", risk.source)
		}
	})
}

func TestSelfProtectionResumePreservesSavedHighFloor(t *testing.T) {
	st := newStateStoreT(t)
	seedReviewStartSnapshot(t, st)
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:               state.ResumeStageReview,
		Phase:               "reviewer-1",
		Role:                state.ReviewerRole,
		Model:               "sonnet",
		ReadOnly:            true,
		Effort:              "high",
		Prompt:              "review",
		OriginalPrompt:      "review",
		Request:             "request",
		WorkerPacket:        []string{"STATUS: IMPLEMENTED", "RISK: LOW", "SUMMARY: done", "REQUIREMENT_COVERAGE: covered", "TESTS: pass", "UNVERIFIED: none", "ARTIFACTS: none"},
		ReviewNumber:        1,
		RateLimited:         true,
		EffectiveRisk:       "HIGH",
		EffectiveRiskSource: "self-protection:workflow-package",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.collectChangedPaths = func(string, string) ([]string, error) { return nil, nil }

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("保存HIGHはresume後もPASSを拒否すべき: status=%q", st.TaskStatus())
	}
	review := st.ReadOr("last-review", "")
	if !strings.Contains(review, "STATUS: NEEDS_SOL_REVIEW") || !strings.Contains(review, "RISK: HIGH") {
		t.Fatalf("risk floor強制packetでない: %s", review)
	}
	if strings.Join(r.models, ",") != "sonnet,sonnet" {
		t.Fatalf("resume model = %#v", r.models)
	}
}

func TestSelfProtectionResumeSavedLowReEscalatesOnCriticalChange(t *testing.T) {
	st := newStateStoreT(t)
	seedReviewStartSnapshot(t, st)
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:               state.ResumeStageReview,
		Phase:               "reviewer-1",
		Role:                state.ReviewerRole,
		Model:               "haiku",
		ReadOnly:            true,
		Effort:              "high",
		Prompt:              "review",
		OriginalPrompt:      "review",
		Request:             "request",
		WorkerPacket:        []string{"STATUS: IMPLEMENTED", "RISK: LOW", "SUMMARY: done", "REQUIREMENT_COVERAGE: covered", "TESTS: pass", "UNVERIFIED: none", "ARTIFACTS: none"},
		ReviewNumber:        1,
		RateLimited:         true,
		EffectiveRisk:       "LOW",
		EffectiveRiskSource: "",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.collectChangedPaths = func(string, string) ([]string, error) {
		return []string{"codex/instructions/worker/common-code.md"}, nil
	}

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("保存LOWでも現在critical pathがあればHIGHへ再昇格しPASSを拒否すべき: status=%q", st.TaskStatus())
	}
	if strings.Join(r.models, ",") != "haiku,sonnet" {
		t.Fatalf("review受信時は保存model(haiku)を使い、PASS却下後のreemitはsonnetになるべき: %#v", r.models)
	}
}

func TestSelfProtectionResumeLegacyCheckpointReconstructsToSafeSide(t *testing.T) {
	st := newStateStoreT(t)
	seedReviewStartSnapshot(t, st)
	if err := st.Write("last-request", "req"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:          state.ResumeStageReview,
		Phase:          "reviewer-1",
		Role:           state.ReviewerRole,
		Model:          "sonnet",
		ReadOnly:       true,
		Effort:         "high",
		Prompt:         "review",
		OriginalPrompt: "review",
		Request:        "request",
		WorkerPacket:   []string{"STATUS: IMPLEMENTED", "RISK: LOW", "SUMMARY: done", "REQUIREMENT_COVERAGE: covered", "TESTS: pass", "UNVERIFIED: none", "ARTIFACTS: none"},
		ReviewNumber:   1,
		RateLimited:    true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}
	r := &scriptedRunner{steps: []runnerStep{
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.collectChangedPaths = func(string, string) ([]string, error) {
		return []string{"codex/instructions/worker/common-code.md"}, nil
	}

	if err := w.ExecuteResume(); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("旧checkpointは現在stateから安全側HIGHへ再構成すべき: status=%q", st.TaskStatus())
	}
}

func TestSelfProtectionDecisionFloorStaysHighWithoutCriticalPath(t *testing.T) {
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
	r := &scriptedRunner{steps: []runnerStep{
		{output: implementedPacketWithRisk("decision applied", "LOW")},
		{output: passPacket()},
		{output: needsSolReviewPacket()},
	}}
	w := newWorkflowT(t, st, r)
	w.collectChangedPaths = func(string, string) ([]string, error) {
		return []string{"README.md"}, nil
	}

	if err := w.ExecuteDecision("A案"); err != nil {
		t.Fatal(err)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("decision floorは自己保護非triggerでもPASSを拒否すべき: status=%q", st.TaskStatus())
	}
	if strings.Join(r.models, ",") != "opus,sonnet,sonnet" {
		t.Fatalf("models=%#v", r.models)
	}
}

var errSelfProtectionSentinel = errors.New("classify io failure")

func packetOfRisk(risk string) packet.Packet {
	return packet.FromLines([]string{"STATUS: IMPLEMENTED", "RISK: " + risk})
}
