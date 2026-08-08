package main

import (
	"fmt"
	"os"
	"path/filepath"
)

type workflow struct {
	config appConfig
	state  *stateStore
	runner modelRunner
	temp   string
}

func newWorkflow(config appConfig, state *stateStore, runner modelRunner) *workflow {
	return &workflow{config: config, state: state, runner: runner}
}

func (w *workflow) Execute(command command) error {
	temp, err := os.MkdirTemp("", "glm-worker-*")
	if err != nil {
		return err
	}
	w.temp = temp
	defer os.RemoveAll(temp)

	switch command.Mode {
	case modeNewTask:
		return w.executeNewTask(command.Payload)
	case modeDecision:
		return w.executeDecision(command.Payload)
	case modeFix:
		return w.executeExplicitFix(command.Payload)
	default:
		return fmt.Errorf("unsupported command mode")
	}
}

func (w *workflow) executeNewTask(request string) error {
	if w.state.Exists("pending-decision") {
		return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: previous task is waiting for Sol decision; use --decision or --reset")
	}

	if err := captureGitBaseline(w.config, w.state); err != nil {
		return err
	}
	if err := w.state.Write("last-request", request); err != nil {
		return err
	}
	if err := w.state.Remove("last-decision", "last-review"); err != nil {
		return err
	}

	workerPacket, err := w.runWorker(newTaskPrompt(request), "worker-new")
	if err != nil {
		return err
	}
	return w.handleWorkerResult(request, workerPacket)
}

func (w *workflow) executeDecision(decision string) error {
	if !w.state.Exists("pending-decision") {
		return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: no pending Sol decision for this repository")
	}

	request, err := w.state.Read("last-request")
	if err != nil {
		return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: original request is missing")
	}
	if err := w.state.Write("last-decision", decision); err != nil {
		return err
	}

	workerPacket, err := w.runWorker(decisionPrompt(request, decision), "worker-decision")
	if err != nil {
		return err
	}
	return w.handleWorkerResult(request, workerPacket)
}

func (w *workflow) executeExplicitFix(instruction string) error {
	if w.state.Exists("pending-decision") {
		return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: task is waiting for Sol decision; resolve it before --fix")
	}

	request, err := w.state.Read("last-request")
	if err != nil {
		return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: no previous task for this repository")
	}

	decision := w.state.ReadOr("last-decision", "none")
	review := w.state.ReadOr("last-review", "none")
	prompt := explicitFixPrompt(request, decision, review, instruction)

	workerPacket, err := w.runWorker(prompt, "worker-explicit-fix")
	if err != nil {
		return err
	}
	return w.handleWorkerResult(request, workerPacket)
}

func (w *workflow) handleWorkerResult(request string, workerPacket packet) error {
	switch workerPacket.Status() {
	case "NEEDS_SOL_DECISION":
		if err := w.state.Touch("pending-decision"); err != nil {
			return err
		}
		printPacket(workerPacket)
		return nil
	case "IMPLEMENTED":
		if err := w.state.Remove("pending-decision"); err != nil {
			return err
		}
		return w.reviewUntilStable(request, workerPacket)
	default:
		return fmt.Errorf("STATUS: WORKER_ERROR\nPHASE: worker-format\nERROR: worker did not return a valid STATUS")
	}
}

func (w *workflow) reviewUntilStable(request string, workerPacket packet) error {
	decision := w.state.ReadOr("last-decision", "none")
	autoFixes := 0
	reviewNumber := 1

	for {
		reviewPacket, err := w.runReviewer(
			reviewerPrompt(request, decision, workerPacket, reviewNumber, w.state.BaselineDescription()),
			fmt.Sprintf("reviewer-%d", reviewNumber),
		)
		if err != nil {
			return err
		}
		if err := w.state.Write("last-review", reviewPacket.String()); err != nil {
			return err
		}

		switch reviewPacket.Status() {
		case "PASS", "NEEDS_SOL_REVIEW":
			printPacket(reviewPacket)
			return nil
		case "FIX_REQUIRED":
			if autoFixes >= w.config.MaxAutoFixRounds {
				printNonConvergedPacket(reviewPacket)
				return nil
			}

			autoFixes++
			fixPacket, err := w.runWorker(
				automaticFixPrompt(request, decision, reviewPacket),
				fmt.Sprintf("worker-auto-fix-%d", autoFixes),
			)
			if err != nil {
				return err
			}

			switch fixPacket.Status() {
			case "NEEDS_SOL_DECISION":
				if err := w.state.Touch("pending-decision"); err != nil {
					return err
				}
				printPacket(fixPacket)
				return nil
			case "IMPLEMENTED":
				if err := w.state.Remove("pending-decision"); err != nil {
					return err
				}
				workerPacket = fixPacket
				reviewNumber++
			default:
				return fmt.Errorf("STATUS: WORKER_ERROR\nPHASE: auto-fix-format\nERROR: worker did not return a valid STATUS after review fix")
			}
		default:
			return fmt.Errorf("STATUS: WORKER_ERROR\nPHASE: reviewer-format\nERROR: reviewer did not return a valid STATUS")
		}
	}
}

func (w *workflow) runWorker(prompt string, phase string) (packet, error) {
	return w.runModel(workerRole, false, prompt, phase)
}

func (w *workflow) runReviewer(prompt string, phase string) (packet, error) {
	return w.runModel(reviewerRole, true, prompt, phase)
}

func (w *workflow) runModel(role sessionRole, readOnly bool, prompt string, phase string) (packet, error) {
	outputPath := filepath.Join(w.temp, phase+".log")
	if err := w.runner.Run(role, readOnly, prompt, outputPath); err != nil {
		return packet{}, workerError(phase, outputPath, err)
	}

	result, err := parseLastPacket(outputPath)
	if err != nil {
		return packet{}, fmt.Errorf(
			"STATUS: WORKER_ERROR\nPHASE: %s-format\nERROR: %v\nOUTPUT_TAIL_BEGIN\n%s\nOUTPUT_TAIL_END",
			phase,
			err,
			tailLines(outputPath, 20),
		)
	}
	return result, nil
}

func workerError(phase string, outputPath string, runErr error) error {
	exitCode := 1
	if value, ok := runErr.(interface{ ExitCode() int }); ok {
		exitCode = value.ExitCode()
	}

	return fmt.Errorf(
		"STATUS: WORKER_ERROR\nPHASE: %s\nEXIT_CODE: %d\nERROR_TAIL_BEGIN\n%s\nERROR_TAIL_END",
		phase,
		exitCode,
		tailLines(outputPath, 30),
	)
}

func printNonConvergedPacket(reviewPacket packet) {
	fmt.Println("STATUS: NEEDS_SOL_REVIEW")
	fmt.Println("RISK: HIGH")
	fmt.Println("SUMMARY: GLM workerと独立reviewerの自動修正が規定回数内に収束しなかった")
	fmt.Println("REQUIREMENT_COVERAGE: 最終状態をSol Highで確認する必要あり")
	fmt.Println("INVARIANTS: 未確定")
	fmt.Println("TEST_EVIDENCE: 直近worker/reviewerで検証実施")
	fmt.Printf("ISSUES: %s\n", reviewPacket.Fields["ISSUES"])
	fmt.Println("RESIDUAL_RISK: reviewer指摘が残っている可能性")
	fmt.Println("TARGETS: 最終diffと直近reviewer指摘に限定")
	fmt.Println("SOL_QUESTION: 未解決問題の修正方針を判断する")
}
