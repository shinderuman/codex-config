package app

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

func convergenceBaseTime() time.Time {
	return time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
}

func matchedTrue() *bool {
	value := true
	return &value
}

// appendConvergenceRoundはseqを採番してround logへrecordを追記するtest helper。
func appendConvergenceRound(t *testing.T, st *state.StateStore, record state.RoundRecord) {
	t.Helper()
	if err := st.AppendRoundRecord(record); err != nil {
		t.Fatal(err)
	}
}

// TestConvergenceRendersRoundsCostsAndSummaryはround log・telemetry・event logだけ
// からround単位の分類・reviewer/worker cost・summaryを表示することを検証する。
func TestConvergenceRendersRoundsCostsAndSummary(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	base := convergenceBaseTime()
	snapshot := state.SnapshotDigest{Head: "head1", IndexDigest: "index1", WorktreeDigest: "worktree1"}
	appendConvergenceRound(t, st, state.RoundRecord{
		TaskID: taskID, WorkerPhase: state.RoundWorkerPhaseBaseline, CapturedAt: base,
		Snapshot: snapshot, Paths: []state.RoundPathState{},
	})
	appendConvergenceRound(t, st, state.RoundRecord{
		TaskID: taskID, ReviewNumber: 1, WorkerPhase: "worker-new", CapturedAt: base.Add(10 * time.Second),
		Snapshot: snapshot, Paths: []state.RoundPathState{},
	})
	st.RecordModelCallLog(state.ModelCallLog{
		TaskID: taskID, CallType: state.CallTypeTask, Role: state.WorkerRole, Phase: "worker-new",
		StartedAt: base, CompletedAt: base.Add(5 * time.Second),
		TreeUsage:      state.TokenUsage{InputTokens: 100, OutputTokens: 40},
		WallDurationMS: 5000, TopLevelTurns: 3,
	})
	st.RecordModelCallLog(state.ModelCallLog{
		TaskID: taskID, CallType: state.CallTypeTask, Role: state.WorkerRole, Phase: "worker-new-packet-compact",
		StartedAt: base.Add(5 * time.Second), CompletedAt: base.Add(6 * time.Second),
		TreeUsage:      state.TokenUsage{InputTokens: 20, OutputTokens: 5},
		WallDurationMS: 1000, TopLevelTurns: 1,
	})
	st.RecordModelCallLog(state.ModelCallLog{
		TaskID: taskID, CallType: state.CallTypeTask, Role: state.ReviewerRole, Phase: "reviewer-1",
		StartedAt: base.Add(20 * time.Second), CompletedAt: base.Add(25 * time.Second),
		TreeUsage:      state.TokenUsage{InputTokens: 200, CacheReadInputTokens: 50, OutputTokens: 10},
		WallDurationMS: 5000, TopLevelTurns: 2, PacketStatus: "PASS",
		EffectiveRisk: "LOW", ReviewerReportedRisk: "LOW",
		Snapshot: &state.SnapshotDiagnostic{Stage: "review-end", Matched: matchedTrue()},
	})
	writeTaskEventLines(t, st, taskID,
		state.TaskEventRecord{TaskID: taskID, CallID: "call-1", Role: "worker", Phase: "worker-new", Timestamp: base.Add(time.Second), Kind: "assistant", Blocks: []state.TaskBlockSummary{
			{Type: "tool_use", Name: "Bash", ToolID: "t1"},
			{Type: "tool_use", Name: "Read", ToolID: "t2"},
		}},
	)

	var out bytes.Buffer
	if err := printConvergence(st, "", &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{
		"TASK_ID: " + taskID,
		"ROUNDS_LOG: " + st.RoundLogPath(taskID),
		"TELEMETRY: ok",
		"EVENT_LOG: ok",
		"BASELINE: captured=2026-08-20T09:00:00Z paths=0 snapshot=ok",
		"ROUNDS: 1",
		"ROUND #1 seq=2 review=1 autofixes=0 worker=worker-new",
		"ROUND #1 DELTA: class=verification-only changed=0 nonsemantic=0",
		"ROUND #1 SNAPSHOT: head=head1 index=index1 worktree=worktree",
		"ROUND #1 REVIEW: calls=1 outcome=PASS risk=LOW reported=LOW reemit=no unresolved=no snapshot=matched",
		"ROUND #1 REVIEWER_COST: calls=1 in=250 out=10 turns=2 dur=5000ms",
		"ROUND #1 WORKER_COST: calls=2 in=120 out=45 turns=4 dur=6000ms",
		"SUMMARY delta=verification-only rounds=1 reviewer_calls=1 reviewer_in=250 reviewer_out=10 reviewer_dur_ms=5000",
		"UNRESOLVED_ISSUE_ROUNDS: 0",
		"HIGH_ROUNDS: 0",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("convergence表示に%qがありません:\n%s", want, body)
		}
	}
}

// TestConvergenceMutatingToolUseStaysSameSnapshotはfile変更toolが観測された
// same-snapshot roundをverification-onlyへ細分化しないことを検証する。
func TestConvergenceMutatingToolUseStaysSameSnapshot(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	base := convergenceBaseTime()
	snapshot := state.SnapshotDigest{Head: "h", IndexDigest: "i", WorktreeDigest: "w"}
	appendConvergenceRound(t, st, state.RoundRecord{
		TaskID: taskID, WorkerPhase: state.RoundWorkerPhaseBaseline, CapturedAt: base, Snapshot: snapshot,
	})
	appendConvergenceRound(t, st, state.RoundRecord{
		TaskID: taskID, ReviewNumber: 1, WorkerPhase: "worker-new", CapturedAt: base.Add(10 * time.Second),
		Snapshot: snapshot,
	})
	writeTaskEventLines(t, st, taskID,
		state.TaskEventRecord{TaskID: taskID, CallID: "call-1", Role: "worker", Phase: "worker-new", Timestamp: base.Add(time.Second), Kind: "assistant", Blocks: []state.TaskBlockSummary{
			{Type: "tool_use", Name: "Edit", ToolID: "t1"},
		}},
	)

	var out bytes.Buffer
	if err := printConvergence(st, "", &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, "DELTA: class=same-snapshot") {
		t.Fatalf("file変更tool観測roundがverification-onlyへ細分化されています:\n%s", body)
	}
	if strings.Contains(body, "class=verification-only") {
		t.Fatalf("verification-only表示が残っています:\n%s", body)
	}
}

// TestConvergenceGapAndMismatchFallToUnknownはseq不連続とreviewer番号不一致が
// 該当roundの分類をunknownへ倒し、mismatch/gap表示へ出ることを検証する。
func TestConvergenceGapAndMismatchFallToUnknown(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	base := convergenceBaseTime()
	snapshot := state.SnapshotDigest{Head: "h", IndexDigest: "i", WorktreeDigest: "w"}
	changed := state.SnapshotDigest{Head: "h", IndexDigest: "i", WorktreeDigest: "w2"}
	appendConvergenceRound(t, st, state.RoundRecord{
		TaskID: taskID, WorkerPhase: state.RoundWorkerPhaseBaseline, CapturedAt: base, Snapshot: snapshot,
	})
	// round 1: worker-new。telemetry reviewerはreviewer-2を返す(番号不一致)。
	appendConvergenceRound(t, st, state.RoundRecord{
		TaskID: taskID, ReviewNumber: 1, WorkerPhase: "worker-new", CapturedAt: base.Add(10 * time.Second),
		Snapshot: snapshot, Paths: []state.RoundPathState{},
	})
	// round 2: seqを手書きで5へ飛ばしrecord欠落(不連続)を作る。
	jumped := state.RoundRecord{
		Version: 1, TaskID: taskID, Seq: 5, ReviewNumber: 2, WorkerPhase: "worker-auto-fix-1",
		CapturedAt: base.Add(30 * time.Second), Snapshot: changed,
		Paths: []state.RoundPathState{
			{Path: "main.go", Class: state.RoundPathClassCode, FullDigest: "f2", SemanticDigest: "s1"},
		},
	}
	jumpedData, err := json.Marshal(jumped)
	if err != nil {
		t.Fatal(err)
	}
	roundFile, err := os.OpenFile(st.RoundLogPath(taskID), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := roundFile.Write(append(jumpedData, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := roundFile.Close(); err != nil {
		t.Fatal(err)
	}
	st.RecordModelCallLog(state.ModelCallLog{
		TaskID: taskID, CallType: state.CallTypeTask, Role: state.ReviewerRole, Phase: "reviewer-2",
		StartedAt: base.Add(20 * time.Second), CompletedAt: base.Add(21 * time.Second),
		PacketStatus: "PASS", WallDurationMS: 1000,
	})

	var out bytes.Buffer
	if err := printConvergence(st, "", &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, "ROUND #1 DELTA: class=same-snapshot changed=0 nonsemantic=0 mismatched_reviewer=yes") {
		t.Fatalf("round 1のmismatch表示がありません:\n%s", body)
	}
	if !strings.Contains(body, "ROUND #2 seq=5") || !strings.Contains(body, "ROUND #2 DELTA: class=unknown") || !strings.Contains(body, "gap=yes") {
		t.Fatalf("不連続roundがunknown/gap表示になっていません:\n%s", body)
	}
	if strings.Contains(body, "class=comment-doc-format-only") {
		t.Fatalf("欠落疑いroundの分類がunknownへ倒されていません:\n%s", body)
	}
	if !strings.Contains(body, "SUMMARY delta=unknown rounds=1") {
		t.Fatalf("unknown summaryがありません:\n%s", body)
	}
}

// TestConvergenceUnresolvedAndHighCountersはFIX_REQUIRED outcomeとHIGH riskの
// round集計を検証する。
func TestConvergenceUnresolvedAndHighCounters(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	base := convergenceBaseTime()
	// baseline recordを意図的に欠かせround 1をinitial分類にする。
	appendConvergenceRound(t, st, state.RoundRecord{
		TaskID: taskID, ReviewNumber: 1, WorkerPhase: "worker-new", CapturedAt: base.Add(10 * time.Second),
		Snapshot: state.SnapshotDigest{Head: "h2", IndexDigest: "i2", WorktreeDigest: "w2"},
		Paths: []state.RoundPathState{
			{Path: "main.go", Class: state.RoundPathClassCode, FullDigest: "f1", SemanticDigest: "s1"},
		},
	})
	st.RecordModelCallLog(state.ModelCallLog{
		TaskID: taskID, CallType: state.CallTypeTask, Role: state.ReviewerRole, Phase: "reviewer-1",
		StartedAt: base.Add(20 * time.Second), PacketStatus: "FIX_REQUIRED",
		EffectiveRisk: "HIGH", ReviewerReportedRisk: "LOW", WallDurationMS: 1000,
	})

	var out bytes.Buffer
	if err := printConvergence(st, "", &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{
		"ROUND #1 DELTA: class=initial changed=0 nonsemantic=0",
		"ROUND #1 REVIEW: calls=1 outcome=FIX_REQUIRED risk=HIGH reported=LOW reemit=no unresolved=yes snapshot=unknown",
		"SUMMARY delta=initial rounds=1 reviewer_calls=1",
		"UNRESOLVED_ISSUE_ROUNDS: 1",
		"HIGH_ROUNDS: 1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("convergence表示に%qがありません:\n%s", want, body)
		}
	}
}

// TestConvergenceSkipsCorruptRoundLinesはround logの部分破損行をskip件数として
// 報告し、以後のrecord表示へ波及させない。
func TestConvergenceSkipsCorruptRoundLines(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}
	base := convergenceBaseTime()
	snapshot := state.SnapshotDigest{Head: "h", IndexDigest: "i", WorktreeDigest: "w"}
	appendConvergenceRound(t, st, state.RoundRecord{
		TaskID: taskID, WorkerPhase: state.RoundWorkerPhaseBaseline, CapturedAt: base, Snapshot: snapshot,
	})
	appendConvergenceRound(t, st, state.RoundRecord{
		TaskID: taskID, ReviewNumber: 1, WorkerPhase: "worker-new", CapturedAt: base.Add(10 * time.Second),
		Snapshot: snapshot,
	})
	roundFile, err := os.OpenFile(st.RoundLogPath(taskID), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := roundFile.WriteString("{\"version\":1,\"kind\":\"brokencorrupt\n"); err != nil {
		t.Fatal(err)
	}
	if err := roundFile.Close(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := printConvergence(st, "", &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, "SKIPPED_ROUNDS: 1") {
		t.Fatalf("skip件数表示がありません:\n%s", body)
	}
	if !strings.Contains(body, "ROUND #1 seq=2 review=1 autofixes=0 worker=worker-new") {
		t.Fatalf("破損行以前のrecord表示がありません:\n%s", body)
	}
}

// TestConvergenceCurrentTaskWithoutRecordsはround logがまだない現在taskを
// 正常終了する。
func TestConvergenceCurrentTaskWithoutRecords(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := printConvergence(st, "", &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, "ROUNDS_LOG: none") {
		t.Fatalf("無round log表示 = %q", body)
	}
	if strings.Contains(body, "ROUND #") {
		t.Fatalf("round logがないのにround表示が出ています:\n%s", body)
	}
}

// TestConvergenceExplicitTaskMissingRoundLogは明示指定taskのround log不在を
// errorにする。
func TestConvergenceExplicitTaskMissingRoundLog(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}

	out := &bytes.Buffer{}
	if err := printConvergence(st, "12345678-1234-4234-8123-123456789abc", out); err == nil {
		t.Fatalf("不在task IDがerrorになりません: %s", out.String())
	}
}

// TestConvergenceRejectsTaskIDOutsideGeneratedFormは--timelineと同じ生成形式
// 検証がfilesystem probeより先に働くことを検証する。
func TestConvergenceRejectsTaskIDOutsideGeneratedForm(t *testing.T) {
	cfg := newAppConfig(t)
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeTimelineSentinel(t, cfg)

	for _, taskID := range []string{"../../evil", "/etc/hostname", "12345678-1234-1234-8123-123456789abc", "none"} {
		out := &bytes.Buffer{}
		if err := printConvergence(st, taskID, out); err == nil {
			t.Fatalf("不正task ID %qがerrorになりません: %s", taskID, out.String())
		}
		if body := out.String(); body != "" {
			t.Fatalf("不正task ID %qが出力しました: %s", taskID, body)
		}
	}
}

func TestParseCommandConvergence(t *testing.T) {
	cmd, err := ParseCommand([]string{"--convergence", "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Mode != ModeConvergence || cmd.Payload != "task-1" {
		t.Fatalf("command = %+v", cmd)
	}
	cmd, err = ParseCommand([]string{"--convergence"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Mode != ModeConvergence || cmd.Payload != "" {
		t.Fatalf("command = %+v", cmd)
	}
	if _, err := ParseCommand([]string{"--convergence", "task-1", "extra"}); err == nil {
		t.Fatal("余分な引数が受け入れられています")
	}
}

// TestExecuteConvergenceDoesNotCreateStateは--convergence実行がstate dirを
// 一切作成・書換しない。
func TestExecuteConvergenceDoesNotCreateState(t *testing.T) {
	base := t.TempDir()
	cfg := config.AppConfig{StateBase: base, RepoHash: "convergencehash", RepoRoot: "/repo"}
	cmd, err := ParseCommand([]string{"--convergence"})
	if err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	if err := Execute(cmd, cfg, nil, out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "TASK_ID: none") || !strings.Contains(out.String(), "ROUNDS_LOG: none") {
		t.Fatalf("convergence出力 = %q", out.String())
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("--convergenceがstate dirを作成しました: %v", entries)
	}
}
