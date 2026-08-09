package main

import (
	"os"
	"strings"
	"testing"
)

type scriptedRunner struct {
	outputs []string
	prompts []string
}

func (r *scriptedRunner) Run(
	_ sessionRole,
	_ bool,
	_ string,
	prompt string,
	outputPath string,
) error {
	r.prompts = append(r.prompts, prompt)
	index := len(r.prompts) - 1
	return os.WriteFile(outputPath, []byte(r.outputs[index]), 0o600)
}

func TestRunModelRecompactsInvalidPacketInSameRunner(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}
	if _, err := state.StartNewTask(); err != nil {
		t.Fatal(err)
	}

	runner := &scriptedRunner{outputs: []string{
		"PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: LOW\nSUMMARY: " + strings.Repeat("x", maxPacketLineBytes+1) + "\nREQUIREMENT_COVERAGE: covered\nTESTS: pass\nUNVERIFIED: none\nPACKET_END\n",
		"PACKET_BEGIN\nSTATUS: IMPLEMENTED\nRISK: LOW\nSUMMARY: implemented\nREQUIREMENT_COVERAGE: covered\nTESTS: pass\nUNVERIFIED: none\nPACKET_END\n",
	}}
	w := newWorkflow(appConfig{RoutineEffort: "high"}, state, runner)
	w.temp = t.TempDir()

	result, err := w.runModel(resumeCheckpoint{
		Stage:   resumeStageWorker,
		Phase:   "worker-new",
		Role:    workerRole,
		Effort:  "high",
		Prompt:  "original",
		Request: "request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status() != "IMPLEMENTED" {
		t.Fatalf("status = %q", result.Status())
	}
	if len(runner.prompts) != 2 || !strings.Contains(runner.prompts[1], "再圧縮") {
		t.Fatalf("same runnerで再圧縮されていません: %#v", runner.prompts)
	}

	stats, err := state.loadTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.ModelCalls != 2 || stats.PacketCompactions != 1 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestExplicitFixRejectsCompletedTask(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}
	if _, err := state.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	if err := state.Write("last-request", "request"); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskStatus(taskStatusComplete); err != nil {
		t.Fatal(err)
	}

	w := newWorkflow(appConfig{}, state, &scriptedRunner{})
	err := w.Execute(command{Mode: modeFix, Payload: "fix"})
	if err == nil || !strings.Contains(err.Error(), "only available after NEEDS_SOL_REVIEW") {
		t.Fatalf("completed taskの--fixを拒否する必要があります: %v", err)
	}
}
