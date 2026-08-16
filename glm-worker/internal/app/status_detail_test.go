package app

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// TestExecuteStatusShowsDetailFromEventLogAndTelemetryは--statusが既存event logと
// telemetryだけからcurrent phase/role/model・開始経過・最終event・session aging・
// probe観測を表示し、AI callを足さないことを検証する。
func TestExecuteStatusShowsDetailFromEventLogAndTelemetry(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}

	writeTaskEventLines(t, st, taskID,
		state.TaskEventRecord{TaskID: taskID, CallID: "call-1", Role: "worker", Phase: "worker-new", ModelAlias: "opus", Kind: "system", Subtype: "init", MessageModel: "glm-5.3"},
		state.TaskEventRecord{
			TaskID:     taskID,
			CallID:     "call-1",
			Role:       "worker",
			Phase:      "worker-new",
			ModelAlias: "opus",
			Kind:       "user",
			Timestamp:  time.Now().UTC(),
			Blocks:     []state.TaskBlockSummary{{Type: "tool_result", Name: "Bash", ToolID: "toolu_1", Bytes: 123, DurationMS: 456}},
		},
	)

	startedAt := time.Now().UTC().Add(-time.Hour)
	st.RecordModelCallLog(state.ModelCallLog{
		TaskID:         taskID,
		CallType:       state.CallTypeTask,
		SessionID:      "sess-worker",
		Role:           state.WorkerRole,
		ModelAlias:     "opus",
		StartedAt:      startedAt,
		CompletedAt:    startedAt.Add(8 * time.Second),
		TopLevelTurns:  10,
		TreeUsage:      state.TokenUsage{InputTokens: 100, CacheReadInputTokens: 50, OutputTokens: 20},
		WallDurationMS: 8000,
	})
	st.RecordModelCallLog(state.ModelCallLog{
		TaskID:         taskID,
		CallType:       state.CallTypeTask,
		SessionID:      "sess-worker",
		Role:           state.WorkerRole,
		ModelAlias:     "opus",
		Resumed:        true,
		StartedAt:      startedAt.Add(10 * time.Minute),
		CompletedAt:    startedAt.Add(10*time.Minute + 9*time.Second),
		TopLevelTurns:  12,
		TreeUsage:      state.TokenUsage{InputTokens: 300, OutputTokens: 30},
		WallDurationMS: 9000,
	})
	st.RecordModelCallLog(state.ModelCallLog{
		TaskID:         taskID,
		CallType:       state.CallTypeProbe,
		SessionID:      "none",
		Role:           state.WorkerRole,
		ModelAlias:     "opus",
		StartedAt:      startedAt.Add(20 * time.Minute),
		CompletedAt:    startedAt.Add(20 * time.Minute),
		Outcome:        "probe_failure",
		ProbeAttempt:   2,
		WallDurationMS: 1500,
	})

	var out bytes.Buffer
	if err := Execute(Command{Mode: ModeStatus}, cfg, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{
		"CURRENT_PHASE: worker-new",
		"CURRENT_ROLE: worker",
		"CURRENT_MODEL: opus",
		"LAST_EVENT: ",
		"tool_result(Bash):123b/456ms",
		"SESSION_AGING: role=worker model=opus id=sess-worker calls=2 resumed=1 turns=22 in=450 out=50 lat_ms=8000,9000",
		"PROBES: 1",
		"PROBE_LAST_OUTCOME: probe_failure",
		"PROBE_LAST_ATTEMPT: 2",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("status出力に%qがありません:\n%s", want, body)
		}
	}
	if strings.Contains(body, "TASK_STARTED_AT: unknown") || strings.Contains(body, "TASK_ELAPSED: unknown") || strings.Contains(body, "LAST_EVENT_AGE: unknown") {
		t.Fatalf("観測できる値がunknown表示です:\n%s", body)
	}
}

// TestExecuteStatusDetailFallsBackToCheckpointはevent logがないtaskでresume checkpoint
// からcurrent表示を補い、停止理由ごとの再開情報(RFC3339 reset・probe gate resume plan)を
// 既存stateだけから表示することを検証する。
func TestExecuteStatusDetailFallsBackToCheckpoint(t *testing.T) {
	cases := []struct {
		name   string
		seed   func(st *state.StateStore)
		status state.TaskStatus
		wants  []string
	}{
		{
			name: "provider unavailable",
			seed: func(st *state.StateStore) {
				if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
					Stage:                             state.ResumeStageWorker,
					Phase:                             "worker-new",
					Role:                              state.WorkerRole,
					Model:                             "opus",
					Prompt:                            "p",
					Request:                           "req",
					ProviderUnavailable:               true,
					ProviderUnavailableClassification: "http-503",
					ProviderUnavailableProbes:         3,
					ProviderUnavailableStartedAt:      time.Now().UTC().Add(-20 * time.Minute),
				}); err != nil {
					t.Fatal(err)
				}
			},
			status: state.TaskStatusProviderUnavailable,
			wants: []string{
				"PROVIDER_RESUME_PLAN: --resume re-probes the provider before continuing this phase",
				"PROVIDER_PROBES: 3",
			},
		},
		{
			name: "rate limited",
			seed: func(st *state.StateStore) {
				if err := st.SaveResumeCheckpoint(state.ResumeCheckpoint{
					Stage:          state.ResumeStageReview,
					Phase:          "reviewer-1",
					Role:           state.ReviewerRole,
					Model:          "haiku",
					Prompt:         "p",
					Request:        "req",
					RateLimited:    true,
					ResetAtCST:     "2026-08-16 14:06:34",
					ResetAtRFC3339: "2026-08-16T14:06:34+08:00",
				}); err != nil {
					t.Fatal(err)
				}
			},
			status: state.TaskStatusRateLimited,
			wants: []string{
				"RESET_AT_RFC3339: 2026-08-16T14:06:34+08:00",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := newAppConfig(t)
			st, err := state.NewStateStore(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.StartNewTask(); err != nil {
				t.Fatal(err)
			}
			c.seed(st)
			if err := st.SetTaskStatus(c.status); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			if err := Execute(Command{Mode: ModeStatus}, cfg, nil, &out, io.Discard); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			wants := append([]string{"LAST_EVENT: none", "SESSION_AGING: none"}, c.wants...)
			for _, want := range wants {
				if !strings.Contains(body, want) {
					t.Fatalf("status出力に%qがありません:\n%s", want, body)
				}
			}
			if strings.Contains(body, "CURRENT_PHASE: unknown") {
				t.Fatalf("checkpointがあるのにcurrent表示がunknownです:\n%s", body)
			}
		})
	}
}

// TestExecuteStatusEmptyTaskDetailIsExplicitUnknownはtaskがない状態で観測できない項目を
// unknown/none表示し、probe/session表示を出さないことを検証する。
func TestExecuteStatusEmptyTaskDetailIsExplicitUnknown(t *testing.T) {
	cfg := newAppConfig(t)
	var out bytes.Buffer
	if err := Execute(Command{Mode: ModeStatus}, cfg, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{
		"TASK_STARTED_AT: unknown",
		"TASK_ELAPSED: unknown",
		"CURRENT_PHASE: unknown",
		"CURRENT_ROLE: unknown",
		"CURRENT_MODEL: unknown",
		"LAST_EVENT: none",
		"SESSION_AGING: none",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("空状態のstatus出力に%qがありません:\n%s", want, body)
		}
	}
	if strings.Contains(body, "PROBES:") || strings.Contains(body, "PROBE_LAST") {
		t.Fatalf("probe記録がないのにprobe表示が出ています:\n%s", body)
	}
}
