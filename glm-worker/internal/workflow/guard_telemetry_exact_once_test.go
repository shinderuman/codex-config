package workflow

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// 実行済みTask Work Callはrunnerを実際に呼んだ全terminal pathでraw telemetryへexactly once
// 記録される。本testはinitial/resumed/provider recovery各経路とcall前停止・mismatch・
// after-read失敗・通常success/errorの全終端を横断し、runner実行回数・task記録数・stats計上の
// 3者一致とprobe記録の分離をproduction flow全体で強制する。guard検出以外の終端
// (rate_limited・state_error等)の記録は既存のprovider/packet testが固定している。
type exactOnceCase struct {
	name string
	// setupはrepoとstate storeへplan/history・resume checkpoint等を用意する。
	setup       func(t *testing.T, repoRoot string, st *state.StateStore)
	steps       []runnerStep
	entry       func(w *Workflow) error
	mutatePhase string
	mutate      func(string) error
	// mutateStateはstate store側fileを破壊する終端(5h上限保存失敗)で使う。
	mutateState       func(st *state.StateStore) error
	mutateOnErr       bool
	wantEntryErr      string
	wantRunnerCalls   int
	wantTaskOutcomes  []string
	wantProbes        int
	wantTransient     int
	wantEventOutcomes []string
}

func (c exactOnceCase) run(t *testing.T) {
	t.Helper()
	repoRoot := initMutationRepo(t)
	st := newStateStoreT(t)
	if c.setup != nil {
		c.setup(t, repoRoot, st)
	}
	r := &mutatingRunner{
		repoRoot:         repoRoot,
		steps:            c.steps,
		mutate:           c.mutate,
		mutatePhase:      c.mutatePhase,
		mutateOnRunError: c.mutateOnErr,
	}
	if c.mutateState != nil {
		r.mutate = func(string) error { return c.mutateState(st) }
	}
	w := newMutationWorkflowShell(t, st)
	w.runner = r
	w.output = &strings.Builder{}
	w.config.RepoRoot = repoRoot
	w.captureSnapshot = state.CaptureGitSnapshot

	err := c.entry(w)
	switch {
	case c.wantEntryErr != "":
		if err == nil || !strings.Contains(err.Error(), c.wantEntryErr) {
			t.Fatalf("entry error = %v want %qを含む", err, c.wantEntryErr)
		}
	case err != nil:
		t.Fatal(err)
	}

	if got := len(r.prompts); got != c.wantRunnerCalls {
		t.Fatalf("runner実行回数 = %d want %d: %v", got, c.wantRunnerCalls, r.phases)
	}
	logs := taskLogs(t, st)
	taskOutcomes := make([]string, 0, len(logs))
	probeRecords, eventOutcomes := 0, make([]string, 0, len(logs))
	for _, l := range logs {
		switch l.CallType {
		case state.CallTypeTask:
			taskOutcomes = append(taskOutcomes, l.Outcome)
		case state.CallTypeProbe:
			probeRecords++
		case state.CallTypeEvent:
			eventOutcomes = append(eventOutcomes, l.Outcome)
		}
	}
	if len(taskOutcomes) != c.wantRunnerCalls {
		t.Fatalf("task記録数 = %d(%v) want %d: telemetryが実行回数と不一致", len(taskOutcomes), taskOutcomes, c.wantRunnerCalls)
	}
	if len(c.wantTaskOutcomes) != 0 && strings.Join(taskOutcomes, ",") != strings.Join(c.wantTaskOutcomes, ",") {
		t.Fatalf("task outcomes = %v want %v", taskOutcomes, c.wantTaskOutcomes)
	}
	if probeRecords != c.wantProbes {
		t.Fatalf("probe記録数 = %d want %d", probeRecords, c.wantProbes)
	}
	for _, want := range c.wantEventOutcomes {
		if !strings.Contains(strings.Join(eventOutcomes, ","), want) {
			t.Fatalf("event outcomes %vに%qがない", eventOutcomes, want)
		}
	}
	stats := currentStats(t, st)
	if stats.ModelCalls != c.wantRunnerCalls {
		t.Fatalf("stats ModelCalls = %d want %d", stats.ModelCalls, c.wantRunnerCalls)
	}
	if stats.WorkerCalls+stats.ReviewerCalls != stats.ModelCalls {
		t.Fatalf("role別計上の和がModelCallsと不一致: worker=%d reviewer=%d total=%d", stats.WorkerCalls, stats.ReviewerCalls, stats.ModelCalls)
	}
	probeStats := 0
	for _, n := range stats.ProbeOutcome {
		probeStats += n
	}
	if probeStats != c.wantProbes {
		t.Fatalf("stats probe計上 = %d want %d", probeStats, c.wantProbes)
	}
	if stats.TransientRetries != c.wantTransient {
		t.Fatalf("stats TransientRetries = %d want %d", stats.TransientRetries, c.wantTransient)
	}
}

// TestGuardTerminalPathsRecordExecutedCallsExactlyOnceは親Codex専有file guardとprovider
// recoveryを横断するterminal path行列でexactly once invariantを強制する。
func TestGuardTerminalPathsRecordExecutedCallsExactlyOnce(t *testing.T) {
	seedPlan := func(t *testing.T, repoRoot string, st *state.StateStore) {
		writePlanFileContent(t, repoRoot, planGuardSeed)
	}
	seedPlanAndHistory := func(t *testing.T, repoRoot string, st *state.StateStore) {
		writePlanFileContent(t, repoRoot, planGuardSeed)
		writeHistoryFileContent(t, repoRoot, historyGuardSeed)
	}
	newTask := func(w *Workflow) error { return w.ExecuteNewTask("request") }
	transientFirstStep := runnerStep{output: "API Error: 503 Service Unavailable", runErr: errors.New("exit status 1")}

	cases := []exactOnceCase{
		{
			name: "plan tracked missing stops before call without phantom telemetry",
			setup: func(t *testing.T, repoRoot string, st *state.StateStore) {
				writePlanFileContent(t, repoRoot, planGuardSeed)
				gitIn(t, repoRoot, "add", implementationPlanFile)
				if err := removeFile(t, repoRoot, implementationPlanFile); err != nil {
					t.Fatal(err)
				}
			},
			steps:             []runnerStep{{output: implementedPacket("done")}, {output: passPacket()}},
			entry:             newTask,
			wantRunnerCalls:   0,
			wantTaskOutcomes:  nil,
			wantEventOutcomes: []string{"parent_metadata_missing"},
		},
		{
			name: "history tracked missing stops before call without phantom telemetry",
			setup: func(t *testing.T, repoRoot string, st *state.StateStore) {
				seedPlanAndHistory(t, repoRoot, st)
				gitIn(t, repoRoot, "add", implementationHistoryFile)
				if err := removeFile(t, repoRoot, implementationHistoryFile); err != nil {
					t.Fatal(err)
				}
			},
			steps:             []runnerStep{{output: implementedPacket("done")}, {output: passPacket()}},
			entry:             newTask,
			wantRunnerCalls:   0,
			wantTaskOutcomes:  nil,
			wantEventOutcomes: []string{"parent_metadata_missing"},
		},
		{
			name:              "plan mismatch on initial call records executed call once",
			setup:             seedPlan,
			steps:             []runnerStep{{output: implementedPacket("done")}, {output: passPacket()}},
			entry:             newTask,
			mutatePhase:       "worker-new",
			mutate:            mutatePlanFile,
			wantRunnerCalls:   1,
			wantTaskOutcomes:  []string{"parent_metadata_violation"},
			wantEventOutcomes: []string{"parent_metadata_mismatch"},
		},
		{
			name:              "history mismatch on initial call records executed call once",
			setup:             seedPlanAndHistory,
			steps:             []runnerStep{{output: implementedPacket("done")}, {output: passPacket()}},
			entry:             newTask,
			mutatePhase:       "worker-new",
			mutate:            mutateHistoryFile,
			wantRunnerCalls:   1,
			wantTaskOutcomes:  []string{"parent_metadata_violation"},
			wantEventOutcomes: []string{"parent_metadata_mismatch"},
		},
		{
			name:              "plan after-read failure on initial call records executed call once",
			setup:             seedPlan,
			steps:             []runnerStep{{output: implementedPacket("done")}, {output: passPacket()}},
			entry:             newTask,
			mutatePhase:       "worker-new",
			mutate:            removeAndDirGuardFile(implementationPlanFile),
			wantRunnerCalls:   1,
			wantTaskOutcomes:  []string{"parent_metadata_unavailable"},
			wantEventOutcomes: []string{"parent_metadata_unavailable"},
		},
		{
			name:              "history after-read failure on initial call records executed call once",
			setup:             seedPlanAndHistory,
			steps:             []runnerStep{{output: implementedPacket("done")}, {output: passPacket()}},
			entry:             newTask,
			mutatePhase:       "worker-new",
			mutate:            removeAndDirGuardFile(implementationHistoryFile),
			wantRunnerCalls:   1,
			wantTaskOutcomes:  []string{"parent_metadata_unavailable"},
			wantEventOutcomes: []string{"parent_metadata_unavailable"},
		},
		{
			name:             "ordinary success records worker and reviewer once each",
			setup:            seedPlan,
			steps:            []runnerStep{{output: implementedPacket("done")}, {output: passPacket()}},
			entry:            newTask,
			wantRunnerCalls:  2,
			wantTaskOutcomes: []string{"success", "success"},
		},
		{
			name:             "ordinary nontransient error records executed call once",
			setup:            seedPlan,
			steps:            []runnerStep{{runErr: errors.New("exit status 1")}},
			entry:            newTask,
			wantEntryErr:     "WORKER_ERROR",
			wantRunnerCalls:  1,
			wantTaskOutcomes: []string{"error"},
		},
		{
			name: "rate-limit resume success records resumed call once",
			setup: func(t *testing.T, repoRoot string, st *state.StateStore) {
				seedPlan(t, repoRoot, st)
				seedRateLimitedWorkerCheckpoint(t, st, "request")
			},
			steps:            []runnerStep{{output: implementedPacket("resumed")}, {output: passPacket()}},
			entry:            func(w *Workflow) error { return w.ExecuteResume() },
			wantRunnerCalls:  2,
			wantTaskOutcomes: []string{"success", "success"},
		},
		{
			name:             "provider recovery records initial transient and resumed success separately",
			setup:            seedPlan,
			steps:            []runnerStep{transientFirstStep, {output: implementedPacket("resumed")}, {output: passPacket()}},
			entry:            newTask,
			wantRunnerCalls:  3,
			wantTaskOutcomes: []string{"transient_error", "success", "success"},
			wantProbes:       1,
			wantTransient:    1,
		},
		{
			name:              "provider recovery after-read failure on resumed task records both calls",
			setup:             seedPlan,
			steps:             []runnerStep{transientFirstStep, {output: implementedPacket("resumed")}, {output: passPacket()}},
			entry:             newTask,
			mutatePhase:       "worker-new",
			mutate:            removeAndDirGuardFile(implementationPlanFile),
			wantRunnerCalls:   2,
			wantTaskOutcomes:  []string{"transient_error", "parent_metadata_unavailable"},
			wantProbes:        1,
			wantTransient:     1,
			wantEventOutcomes: []string{"parent_metadata_unavailable"},
		},
		{
			// 5h上限でMarkReadyに失敗しても実行済み呼出はstate_errorとして1回だけ残る。
			// resume経路で起こすためready markerの中身有りdirectoryは事前掃除されない。
			name: "rate limit ready-state failure records executed call once",
			setup: func(t *testing.T, repoRoot string, st *state.StateStore) {
				seedPlan(t, repoRoot, st)
				seedRateLimitedWorkerCheckpoint(t, st, "request")
				readyDir := st.Path("worker.ready")
				if err := os.Mkdir(readyDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(readyDir, "blocker"), []byte("x\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			steps:            []runnerStep{{output: zaiFiveHourLog, runErr: errors.New("exit status 1")}},
			entry:            func(w *Workflow) error { return w.ExecuteResume() },
			wantEntryErr:     "worker.ready",
			wantRunnerCalls:  1,
			wantTaskOutcomes: []string{"state_error"},
		},
		{
			// 5h上限で停止状態の保存に失敗しても実行済み呼出はstate_errorとして1回だけ残る。
			name:  "rate limit checkpoint persist failure records executed call once",
			setup: seedPlan,
			steps: []runnerStep{{output: zaiFiveHourLog, runErr: errors.New("exit status 1")}},
			entry: newTask,
			mutateState: func(st *state.StateStore) error {
				// run前のresume checkpoint書込みは成功させ、run後の保存だけを壊す。
				if err := os.Remove(st.Path("resume-state.json")); err != nil {
					return err
				}
				return os.Mkdir(st.Path("resume-state.json"), 0o755)
			},
			mutateOnErr:      true,
			mutatePhase:      "worker-new",
			wantEntryErr:     "resume stateを書き込めません",
			wantRunnerCalls:  1,
			wantTaskOutcomes: []string{"state_error"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.run(t)
		})
	}
}

func removeFile(t *testing.T, repoRoot string, name string) error {
	t.Helper()
	return os.Remove(filepath.Join(repoRoot, name))
}
