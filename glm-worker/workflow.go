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
	case modeResume:
		return w.executeResume()
	default:
		return fmt.Errorf("unsupported command mode")
	}
}

func (w *workflow) executeNewTask(request string) error {
	if w.state.Exists("pending-decision") {
		return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: previous task is waiting for Sol decision; use --decision or --reset")
	}
	if checkpoint, err := w.state.LoadResumeCheckpoint(); err == nil && checkpoint.RateLimited {
		return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: previous task is rate-limited; use --resume or --reset")
	}

	if _, err := w.state.StartNewTask(); err != nil {
		return err
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

	prompt := newTaskPrompt(request)
	checkpoint := resumeCheckpoint{
		Stage:          resumeStageWorker,
		Phase:          "worker-new",
		Role:           workerRole,
		ReadOnly:       false,
		Effort:         w.config.RoutineEffort,
		Prompt:         prompt,
		OriginalPrompt: prompt,
		Request:        request,
	}

	workerPacket, err := w.runModel(checkpoint)
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

	prompt := decisionPrompt(request, decision)
	checkpoint := resumeCheckpoint{
		Stage:          resumeStageWorker,
		Phase:          "worker-decision",
		Role:           workerRole,
		ReadOnly:       false,
		Effort:         w.config.EscalatedEffort,
		Prompt:         prompt,
		OriginalPrompt: prompt,
		Request:        request,
		Decision:       decision,
	}

	workerPacket, err := w.runModel(checkpoint)
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
	checkpoint := resumeCheckpoint{
		Stage:          resumeStageWorker,
		Phase:          "worker-explicit-fix",
		Role:           workerRole,
		ReadOnly:       false,
		Effort:         w.config.EscalatedEffort,
		Prompt:         prompt,
		OriginalPrompt: prompt,
		Request:        request,
		Decision:       decision,
	}

	workerPacket, err := w.runModel(checkpoint)
	if err != nil {
		return err
	}
	return w.handleWorkerResult(request, workerPacket)
}

func (w *workflow) executeResume() error {
	checkpoint, err := w.state.LoadResumeCheckpoint()
	if err != nil {
		return err
	}
	if !checkpoint.RateLimited {
		return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: saved task is not stopped by Z.ai 5h limit")
	}

	previousCheckpoint := checkpoint
	checkpoint.Prompt = resumePrompt(checkpoint)
	checkpoint.RateLimited = false
	checkpoint.ResetAtCST = ""
	checkpoint.ResetAtRFC3339 = ""

	result, err := w.runModel(checkpoint)
	if err != nil {
		saved, loadErr := w.state.LoadResumeCheckpoint()
		if loadErr != nil || !saved.RateLimited {
			_ = w.state.SaveResumeCheckpoint(previousCheckpoint)
		}
		return err
	}

	switch checkpoint.Stage {
	case resumeStageWorker:
		return w.handleWorkerResult(checkpoint.Request, result)
	case resumeStageReview:
		workerPacket := packetFromLines(checkpoint.WorkerPacket)
		if err := w.state.Write("last-review", result.String()); err != nil {
			return err
		}
		return w.handleReviewResult(
			checkpoint.Request,
			workerPacket,
			result,
			checkpoint.ReviewNumber,
			checkpoint.AutoFixes,
		)
	case resumeStageAutoFix:
		return w.handleAutoFixResult(
			checkpoint.Request,
			result,
			checkpoint.ReviewNumber,
			checkpoint.AutoFixes,
		)
	default:
		return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: unknown resume stage: %s", checkpoint.Stage)
	}
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
		return w.reviewUntilStable(request, workerPacket, 1, 0)
	default:
		return fmt.Errorf("STATUS: WORKER_ERROR\nPHASE: worker-format\nERROR: worker did not return a valid STATUS")
	}
}

func (w *workflow) reviewUntilStable(
	request string,
	workerPacket packet,
	reviewNumber int,
	autoFixes int,
) error {
	decision := w.state.ReadOr("last-decision", "none")
	prompt := reviewerPrompt(
		request,
		decision,
		workerPacket,
		reviewNumber,
		w.state.BaselineDescription(),
	)
	checkpoint := resumeCheckpoint{
		Stage:          resumeStageReview,
		Phase:          fmt.Sprintf("reviewer-%d", reviewNumber),
		Role:           reviewerRole,
		ReadOnly:       true,
		Effort:         w.config.RoutineEffort,
		Prompt:         prompt,
		OriginalPrompt: prompt,
		Request:        request,
		Decision:       decision,
		WorkerPacket:   append([]string(nil), workerPacket.Lines...),
		ReviewNumber:   reviewNumber,
		AutoFixes:      autoFixes,
	}

	reviewPacket, err := w.runModel(checkpoint)
	if err != nil {
		return err
	}
	if err := w.state.Write("last-review", reviewPacket.String()); err != nil {
		return err
	}

	return w.handleReviewResult(
		request,
		workerPacket,
		reviewPacket,
		reviewNumber,
		autoFixes,
	)
}

func (w *workflow) handleReviewResult(
	request string,
	workerPacket packet,
	reviewPacket packet,
	reviewNumber int,
	autoFixes int,
) error {
	switch reviewPacket.Status() {
	case "PASS", "NEEDS_SOL_REVIEW":
		printPacket(reviewPacket)
		return nil

	case "FIX_REQUIRED":
		if autoFixes >= w.config.MaxAutoFixRounds {
			printNonConvergedPacket(reviewPacket)
			return nil
		}

		nextAutoFixes := autoFixes + 1
		decision := w.state.ReadOr("last-decision", "none")
		prompt := automaticFixPrompt(request, decision, reviewPacket)
		checkpoint := resumeCheckpoint{
			Stage:          resumeStageAutoFix,
			Phase:          fmt.Sprintf("worker-auto-fix-%d", nextAutoFixes),
			Role:           workerRole,
			ReadOnly:       false,
			Effort:         w.config.RoutineEffort,
			Prompt:         prompt,
			OriginalPrompt: prompt,
			Request:        request,
			Decision:       decision,
			ReviewNumber:   reviewNumber,
			AutoFixes:      nextAutoFixes,
		}

		fixPacket, err := w.runModel(checkpoint)
		if err != nil {
			return err
		}

		return w.handleAutoFixResult(
			request,
			fixPacket,
			reviewNumber,
			nextAutoFixes,
		)

	default:
		return fmt.Errorf("STATUS: WORKER_ERROR\nPHASE: reviewer-format\nERROR: reviewer did not return a valid STATUS")
	}
}

func (w *workflow) handleAutoFixResult(
	request string,
	fixPacket packet,
	reviewNumber int,
	autoFixes int,
) error {
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
		return w.reviewUntilStable(
			request,
			fixPacket,
			reviewNumber+1,
			autoFixes,
		)

	default:
		return fmt.Errorf("STATUS: WORKER_ERROR\nPHASE: auto-fix-format\nERROR: worker did not return a valid STATUS after review fix")
	}
}

func (w *workflow) runModel(checkpoint resumeCheckpoint) (packet, error) {
	outputPath := filepath.Join(w.temp, checkpoint.Phase+".log")

	if checkpoint.OriginalPrompt == "" {
		checkpoint.OriginalPrompt = checkpoint.Prompt
	}
	if checkpoint.Effort == "" {
		checkpoint.Effort = w.config.RoutineEffort
	}

	if err := w.state.SaveResumeCheckpoint(checkpoint); err != nil {
		return packet{}, err
	}

	runErr := w.runner.Run(
		checkpoint.Role,
		checkpoint.ReadOnly,
		checkpoint.Effort,
		checkpoint.Prompt,
		outputPath,
	)
	if runErr != nil {
		if limit, ok := detectZaiFiveHourLimit(outputPath); ok {
			// 5h上限ではsession IDを破棄しない。
			// Claude Codeの同一sessionへ--resumeして作業途中から継続する。
			if err := w.state.MarkReady(checkpoint.Role); err != nil {
				return packet{}, err
			}

			checkpoint.RateLimited = true
			checkpoint.ResetAtCST = limit.ResetAtCST
			checkpoint.ResetAtRFC3339 = limit.ResetAtRFC3339
			if err := w.state.SaveResumeCheckpoint(checkpoint); err != nil {
				return packet{}, err
			}

			return packet{}, zaiRateLimitError{
				Phase: checkpoint.Phase,
				Limit: limit,
			}
		}

		_ = w.state.ClearResumeCheckpoint()
		_ = w.state.RemoveUnreadySession(checkpoint.Role)
		return packet{}, workerError(
			checkpoint.Phase,
			outputPath,
			runErr,
		)
	}

	if err := w.state.ClearResumeCheckpoint(); err != nil {
		return packet{}, err
	}

	result, err := parseLastPacket(outputPath)
	if err != nil {
		return packet{}, fmt.Errorf(
			"STATUS: WORKER_ERROR\nPHASE: %s-format\nERROR: %v\nOUTPUT_TAIL_BEGIN\n%s\nOUTPUT_TAIL_END",
			checkpoint.Phase,
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
