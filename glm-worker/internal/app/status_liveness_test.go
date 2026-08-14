package app

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// statusは対象repo lockと独立に、別repoのstate/lockの影響を受けない。
func TestExecuteStatusShowsRepositoryLockFreeByDefault(t *testing.T) {
	cfg := newAppConfig(t)
	var out bytes.Buffer
	if err := Execute(Command{Mode: ModeStatus}, cfg, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, "REPOSITORY_LOCK: free") {
		t.Fatalf("空状態でREPOSITORY_LOCK: freeが必要です:\n%s", body)
	}
	if strings.Contains(body, "TASK_LIVENESS") {
		t.Fatalf("TASK_STATUSがactiveでない状態でTASK_LIVENESSが出てはいけません:\n%s", body)
	}
}

func TestExecuteStatusActiveWithoutLockIsStaleCandidate(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Execute(Command{Mode: ModeStatus}, cfg, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{
		"TASK_STATUS: active",
		"REPOSITORY_LOCK: free",
		"TASK_LIVENESS: stale",
		"RESUME_AVAILABLE: no",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("status出力に%qがありません:\n%s", want, body)
		}
	}
}

func TestExecuteStatusActiveWithLockHeldIsRunning(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}

	lock, err := AcquireRepoLock(st.LockPath())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	var out bytes.Buffer
	if err := Execute(Command{Mode: ModeStatus}, cfg, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{
		"REPOSITORY_LOCK: held",
		"TASK_LIVENESS: running",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("status出力に%qがありません:\n%s", want, body)
		}
	}
}

// status観測後に同じrepoで次commandを実行すると、lock取得成否だけで安全に収束する。
// 別repoのlockは同一commandの可否に影響しない。
func TestExecuteStatusRaceConvergesOnNextCommandLock(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var statusOut bytes.Buffer
	if err := Execute(Command{Mode: ModeStatus}, cfg, nil, &statusOut, io.Discard); err != nil {
		t.Fatal(err)
	}

	otherCfg := cfg
	otherCfg.RepoHash = cfg.RepoHash + "-other"
	otherCfg.RepoShort = cfg.RepoShort + "-oth"
	otherSt, err := state.NewStateStore(otherCfg)
	if err != nil {
		t.Fatal(err)
	}
	otherLock, err := AcquireRepoLock(otherSt.LockPath())
	if err != nil {
		t.Fatal(err)
	}
	defer otherLock.Close()

	var resetOut bytes.Buffer
	if err := Execute(Command{Mode: ModeReset}, cfg, nil, &resetOut, io.Discard); err != nil {
		t.Fatalf("別repo lock保持中に対象repoの次commandが失敗しました: %v", err)
	}
	if !strings.Contains(resetOut.String(), "STATUS: RESET") {
		t.Fatalf("reset出力が想定外です: %q", resetOut.String())
	}
	_ = st
}

// TASK_STATUSがactive以外ではTASK_LIVENESSを表示しない。
func TestExecuteStatusHidesLivenessForNonActiveTask(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusWaitingDecision); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Execute(Command{Mode: ModeStatus}, cfg, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "TASK_LIVENESS") {
		t.Fatalf("非active taskでTASK_LIVENESSが出ています:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "REPOSITORY_LOCK: free") {
		t.Fatalf("REPOSITORY_LOCKは常に表示が必要です:\n%s", out.String())
	}
}

// checkpointを持つrate-limited taskのresume表示はliveness追加後も不変。
func TestExecuteStatusRateLimitedResumeFieldsUnchanged(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
		Stage:       state.ResumeStageWorker,
		Phase:       "worker-new",
		Role:        state.WorkerRole,
		Model:       "opus",
		Effort:      "high",
		Prompt:      "p",
		Request:     "req",
		RateLimited: true,
		ResetAtCST:  "2026-08-15 10:00:00",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Execute(Command{Mode: ModeStatus}, cfg, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{
		"TASK_STATUS: rate-limited",
		"RATE_LIMITED: yes",
		"RESUME_AVAILABLE: yes",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rate-limited status出力に%qがありません:\n%s", want, body)
		}
	}
	if strings.Contains(body, "TASK_LIVENESS") {
		t.Fatalf("rate-limited taskでTASK_LIVENESSが出ています:\n%s", body)
	}
}
