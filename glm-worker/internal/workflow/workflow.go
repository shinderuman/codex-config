// Package workflowはworker→reviewer→auto-fixの状態機械を駆動する。
package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/packet"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/runner"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// interfaceは実装側ではなく利用側に置き、テストでは偽装実装へ差し替える。
type ModelRunner interface {
	Run(role state.SessionRole, model string, readOnly bool, effort string, prompt string, outputPath string) (runner.RunResult, error)
}

type Workflow struct {
	config              config.AppConfig
	state               *state.StateStore
	runner              ModelRunner
	output              io.Writer
	temp                string
	captureSnapshot     func(repoRoot string) (state.GitSnapshot, error)
	collectChangedPaths func(repoRoot, baselineHead string) ([]string, error)
}

func NewWorkflow(cfg config.AppConfig, st *state.StateStore, r ModelRunner, output io.Writer) *Workflow {
	return &Workflow{
		config:              cfg,
		state:               st,
		runner:              r,
		output:              output,
		captureSnapshot:     state.CaptureGitSnapshot,
		collectChangedPaths: collectChangedPaths,
	}
}

func (w *Workflow) withTemp(fn func() error) error {
	temp, err := os.MkdirTemp("", "glm-worker-*")
	if err != nil {
		return err
	}
	w.temp = temp
	defer os.RemoveAll(temp)
	return fn()
}

func (w *Workflow) ExecuteNewTask(request string) error {
	return w.withTemp(func() error {
		if w.state.Exists("pending-decision") {
			return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: previous task is waiting for Sol decision; use --decision or --reset")
		}
		if checkpoint, err := w.state.LoadResumeCheckpoint(); err == nil && checkpoint.RateLimited {
			return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: previous task is rate-limited; use --resume or --reset")
		}

		if _, err := w.state.StartNewTask(); err != nil {
			return err
		}

		if err := state.CaptureGitBaseline(w.config, w.state); err != nil {
			return err
		}
		if err := w.state.Write("last-request", request); err != nil {
			return err
		}
		if err := w.state.Remove("last-decision", "last-review"); err != nil {
			return err
		}

		prompt := newTaskPrompt(request)
		checkpoint := state.ResumeCheckpoint{
			Stage:          state.ResumeStageWorker,
			Phase:          "worker-new",
			Role:           state.WorkerRole,
			Model:          w.config.WorkerModel,
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
	})
}

func (w *Workflow) ExecuteDecision(decision string) error {
	return w.withTemp(func() error {
		if w.state.TaskStatus() != state.TaskStatusWaitingDecision || !w.state.Exists("pending-decision") {
			return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: no pending Sol decision for this repository")
		}

		request, err := w.state.Read("last-request")
		if err != nil {
			return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: original request is missing")
		}
		if err := w.state.Write("last-decision", decision); err != nil {
			return err
		}
		if err := w.state.SetTaskStatus(state.TaskStatusActive); err != nil {
			return err
		}
		w.state.RecordDecision()

		prompt := decisionPrompt(request, decision)
		checkpoint := state.ResumeCheckpoint{
			Stage:          state.ResumeStageWorker,
			Phase:          "worker-decision",
			Role:           state.WorkerRole,
			Model:          w.config.WorkerModel,
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
	})
}

func (w *Workflow) ExecuteExplicitFix(instruction string) error {
	return w.withTemp(func() error {
		if w.state.Exists("pending-decision") {
			return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: task is waiting for Sol decision; resolve it before --fix")
		}
		if w.state.TaskStatus() != state.TaskStatusWaitingSolReview {
			return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: --fix is only available after NEEDS_SOL_REVIEW; start a new task after PASS")
		}

		request, err := w.state.Read("last-request")
		if err != nil {
			return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: no previous task for this repository")
		}

		decision := w.state.ReadOr("last-decision", "none")
		review := w.state.ReadOr("last-review", "none")
		if err := w.state.SetTaskStatus(state.TaskStatusActive); err != nil {
			return err
		}
		w.state.RecordFix()
		prompt := explicitFixPrompt(request, decision, review, instruction)
		checkpoint := state.ResumeCheckpoint{
			Stage:          state.ResumeStageWorker,
			Phase:          "worker-explicit-fix",
			Role:           state.WorkerRole,
			Model:          w.config.WorkerModel,
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
	})
}

func (w *Workflow) ExecuteResume() error {
	return w.withTemp(func() error {
		checkpoint, err := w.state.LoadResumeCheckpoint()
		if err != nil {
			return err
		}
		if !checkpoint.RateLimited {
			return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: saved task is not stopped by Z.ai 5h limit")
		}
		if !isKnownResumeStage(checkpoint.Stage) {
			return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: unknown resume stage: %s", checkpoint.Stage)
		}

		previousCheckpoint := checkpoint
		if err := w.state.SetTaskStatus(state.TaskStatusActive); err != nil {
			return err
		}
		w.state.RecordResume()
		// review工程resume時は5h上限の時間経過を挟んでreview-start snapshotと現在状態を再照合する。
		if checkpoint.Stage == state.ResumeStageReview {
			if stopped, err := w.verifyReviewResumeSnapshot(); err != nil {
				return err
			} else if stopped {
				return nil
			}
		}
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
			_ = w.state.SetTaskStatus(state.TaskStatusRateLimited)
			return err
		}

		switch checkpoint.Stage {
		case state.ResumeStageWorker:
			return w.handleWorkerResult(checkpoint.Request, result)
		case state.ResumeStageReview:
			workerPacket := packet.FromLines(checkpoint.WorkerPacket)
			decision := w.state.ReadOr("last-decision", "none")
			if checkpoint.RiskFloorReemit {
				reviewPacket := resolveRiskFloorReemit(result)
				if err := w.state.Write("last-review", reviewPacket.String()); err != nil {
					return err
				}
				return w.handleReviewResult(
					checkpoint.Request,
					workerPacket,
					reviewPacket,
					checkpoint.ReviewNumber,
					checkpoint.AutoFixes,
				)
			}
			highRiskFloor := w.resolveReviewResumeRisk(workerPacket, checkpoint).high
			reviewPacket, err := w.enforceRiskFloor(
				checkpoint.Request,
				workerPacket,
				checkpoint.ReviewNumber,
				checkpoint.AutoFixes,
				decision,
				highRiskFloor,
				result,
			)
			if err != nil {
				return err
			}
			if err := w.state.Write("last-review", reviewPacket.String()); err != nil {
				return err
			}
			return w.handleReviewResult(
				checkpoint.Request,
				workerPacket,
				reviewPacket,
				checkpoint.ReviewNumber,
				checkpoint.AutoFixes,
			)
		case state.ResumeStageAutoFix:
			return w.handleAutoFixResult(
				checkpoint.Request,
				result,
				checkpoint.ReviewNumber,
				checkpoint.AutoFixes,
			)
		default:
			return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: unknown resume stage: %s", checkpoint.Stage)
		}
	})
}

func isKnownResumeStage(stage state.ResumeStage) bool {
	switch stage {
	case state.ResumeStageWorker, state.ResumeStageReview, state.ResumeStageAutoFix:
		return true
	default:
		return false
	}
}

func (w *Workflow) handleWorkerResult(request string, workerPacket packet.Packet) error {
	switch workerPacket.Status() {
	case "NEEDS_SOL_DECISION":
		if err := w.state.Touch("pending-decision"); err != nil {
			return err
		}
		if err := w.state.SetTaskStatus(state.TaskStatusWaitingDecision); err != nil {
			return err
		}
		return w.emitPacket(workerPacket)
	case "IMPLEMENTED":
		if err := w.state.Remove("pending-decision"); err != nil {
			return err
		}
		if err := w.state.SetTaskStatus(state.TaskStatusActive); err != nil {
			return err
		}
		return w.reviewUntilStable(request, workerPacket, 1, 0)
	default:
		return fmt.Errorf("STATUS: WORKER_ERROR\nPHASE: worker-format\nERROR: worker did not return a valid STATUS")
	}
}

func (w *Workflow) reviewUntilStable(
	request string,
	workerPacket packet.Packet,
	reviewNumber int,
	autoFixes int,
) error {
	if stopped, err := w.captureWorkerEndSnapshot(); err != nil {
		return err
	} else if stopped {
		return nil
	}

	decision := w.state.ReadOr("last-decision", "none")
	hasDecision := w.state.Exists("last-decision")
	risk := w.computeEffectiveRisk(workerPacket, autoFixes, hasDecision, w.state.Exists("last-review"))
	prompt := reviewerPrompt(
		request,
		decision,
		workerPacket,
		reviewNumber,
		w.state.BaselineDescription(),
	)
	checkpoint := state.ResumeCheckpoint{
		Stage:               state.ResumeStageReview,
		Phase:               fmt.Sprintf("reviewer-%d", reviewNumber),
		Role:                state.ReviewerRole,
		Model:               w.reviewerModel(risk),
		ReadOnly:            true,
		Effort:              w.config.RoutineEffort,
		Prompt:              prompt,
		OriginalPrompt:      prompt,
		Request:             request,
		Decision:            decision,
		WorkerPacket:        append([]string(nil), workerPacket.Lines...),
		ReviewNumber:        reviewNumber,
		AutoFixes:           autoFixes,
		EffectiveRisk:       riskLabel(risk.high),
		EffectiveRiskSource: risk.source,
	}

	if stopped, err := w.verifyReviewStartSnapshot(); err != nil {
		return err
	} else if stopped {
		return nil
	}

	reviewPacket, err := w.runModel(checkpoint)
	if err != nil {
		return err
	}
	reviewPacket, err = w.enforceRiskFloor(
		request,
		workerPacket,
		reviewNumber,
		autoFixes,
		decision,
		risk.high,
		reviewPacket,
	)
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

func (w *Workflow) handleReviewResult(
	request string,
	workerPacket packet.Packet,
	reviewPacket packet.Packet,
	reviewNumber int,
	autoFixes int,
) error {
	switch reviewPacket.Status() {
	case "PASS":
		if err := w.state.SetTaskStatus(state.TaskStatusComplete); err != nil {
			return err
		}
		return w.emitPacket(reviewPacket)

	case "NEEDS_SOL_REVIEW":
		if err := w.state.SetTaskStatus(state.TaskStatusWaitingSolReview); err != nil {
			return err
		}
		return w.emitPacket(reviewPacket)

	case "FIX_REQUIRED":
		if autoFixes >= w.config.MaxAutoFixRounds {
			if err := w.state.SetTaskStatus(state.TaskStatusWaitingSolReview); err != nil {
				return err
			}
			return w.emitPacket(nonConvergedPacket(reviewPacket))
		}

		nextAutoFixes := autoFixes + 1
		decision := w.state.ReadOr("last-decision", "none")
		prompt := automaticFixPrompt(request, decision, reviewPacket)
		checkpoint := state.ResumeCheckpoint{
			Stage:          state.ResumeStageAutoFix,
			Phase:          fmt.Sprintf("worker-auto-fix-%d", nextAutoFixes),
			Role:           state.WorkerRole,
			Model:          w.config.WorkerModel,
			ReadOnly:       false,
			Effort:         w.config.RoutineEffort,
			Prompt:         prompt,
			OriginalPrompt: prompt,
			Request:        request,
			Decision:       decision,
			ReviewNumber:   reviewNumber,
			AutoFixes:      nextAutoFixes,
		}
		w.state.RecordAutoFix()

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

func reviewNeedsHighRiskFloor(workerPacket packet.Packet, autoFixes int, hasDecision bool, hasPriorReview bool) bool {
	return workerPacket.Risk() == "HIGH" || autoFixes > 0 || hasDecision || hasPriorReview
}

// effectiveRiskはworker原文risk・既存floor信号(auto-fix/decision/prior-review)・自己保護を統合したwrapperの実効risk。
// workerのLOW自己申告を実際の変更対象でHIGHへ昇格できる。
type effectiveRisk struct {
	high   bool
	source string
}

func riskLabel(high bool) string {
	if high {
		return "HIGH"
	}
	return "LOW"
}

func (w *Workflow) computeEffectiveRisk(workerPacket packet.Packet, autoFixes int, hasDecision bool, hasPriorReview bool) effectiveRisk {
	sp := w.selfProtectionNow()
	if !reviewNeedsHighRiskFloor(workerPacket, autoFixes, hasDecision, hasPriorReview) && !sp.High {
		return effectiveRisk{high: false}
	}
	var sources []string
	if workerPacket.Risk() == "HIGH" {
		sources = append(sources, "worker-declared")
	}
	if autoFixes > 0 {
		sources = append(sources, "auto-fix")
	}
	if hasDecision {
		sources = append(sources, "decision")
	}
	if hasPriorReview {
		sources = append(sources, "prior-review")
	}
	if sp.High {
		sources = append(sources, "self-protection:"+sp.Source)
	}
	return effectiveRisk{high: true, source: strings.Join(sources, ";")}
}

// selfProtectionNowはpath取得失敗時をclassify-error HIGHへ倒し、silent LOWによるfail-openを防ぐ。
func (w *Workflow) selfProtectionNow() selfProtectionDecision {
	baselineHead, _ := w.state.Read("baseline-head")
	paths, err := w.collectChangedPaths(w.config.RepoRoot, baselineHead)
	if err != nil {
		return selfProtectionDecision{High: true, Source: "classify-error", HitPath: err.Error()}
	}
	return classifySelfProtection(paths)
}

// resolveReviewResumeRiskはresume時の実効riskを決定する。保存HIGHはfloor保持のため無条件で維持し、
// 保存LOW・未計算(旧checkpoint)は現在の自己保護を再評価してHIGHへ昇格できる。これによりpolicy更新後に
// 保存LOWがcritical変更をLOWへ固定するfail-openを防ぐ。永続policy versionは持たず、LOW再評価が常に
// 現行policyへ照合するためversion陳腐化は起きない。
func (w *Workflow) resolveReviewResumeRisk(workerPacket packet.Packet, checkpoint state.ResumeCheckpoint) effectiveRisk {
	if checkpoint.EffectiveRisk == "HIGH" {
		return effectiveRisk{high: true, source: checkpoint.EffectiveRiskSource}
	}
	hasDecision := w.state.Exists("last-decision")
	return w.computeEffectiveRisk(workerPacket, checkpoint.AutoFixes, hasDecision, w.state.Exists("last-review"))
}

func (w *Workflow) reviewerModel(risk effectiveRisk) string {
	if risk.high {
		return w.config.HighRiskReviewerModel
	}
	return w.config.ReviewerModel
}

func (w *Workflow) handleAutoFixResult(
	request string,
	fixPacket packet.Packet,
	reviewNumber int,
	autoFixes int,
) error {
	switch fixPacket.Status() {
	case "NEEDS_SOL_DECISION":
		if err := w.state.Touch("pending-decision"); err != nil {
			return err
		}
		if err := w.state.SetTaskStatus(state.TaskStatusWaitingDecision); err != nil {
			return err
		}
		return w.emitPacket(fixPacket)

	case "IMPLEMENTED":
		if err := w.state.Remove("pending-decision"); err != nil {
			return err
		}
		if err := w.state.SetTaskStatus(state.TaskStatusActive); err != nil {
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

func (w *Workflow) runModel(checkpoint state.ResumeCheckpoint) (packet.Packet, error) {
	outputPath := filepath.Join(w.temp, checkpoint.Phase+".log")
	if checkpoint.Role == state.WorkerRole {
		artifactDir, err := w.state.PrepareArtifactDir()
		if err != nil {
			return packet.Packet{}, err
		}
		checkpoint.Prompt = withArtifactContext(checkpoint.Prompt, artifactDir)
		if checkpoint.OriginalPrompt != "" {
			checkpoint.OriginalPrompt = withArtifactContext(checkpoint.OriginalPrompt, artifactDir)
		}
	}

	if checkpoint.OriginalPrompt == "" {
		checkpoint.OriginalPrompt = checkpoint.Prompt
	}
	if checkpoint.Model == "" {
		return packet.Packet{}, fmt.Errorf("STATUS: WORKER_ERROR\nPHASE: %s\nERROR: checkpoint model is missing", checkpoint.Phase)
	}
	if checkpoint.Effort == "" {
		checkpoint.Effort = w.config.RoutineEffort
	}

	if err := w.state.SaveResumeCheckpoint(checkpoint); err != nil {
		return packet.Packet{}, err
	}
	w.state.RecordModelCall(checkpoint.Role, checkpoint.Model)

	startedAt := time.Now().UTC()
	runResult, runErr := w.runner.Run(
		checkpoint.Role,
		checkpoint.Model,
		checkpoint.ReadOnly,
		checkpoint.Effort,
		checkpoint.Prompt,
		outputPath,
	)
	completedAt := time.Now().UTC()
	w.state.RecordModelDuration(checkpoint.Model, completedAt.Sub(startedAt))
	if runErr != nil {
		if limit, ok := runner.DetectZaiFiveHourLimit(outputPath); ok {
			// 5h上限ではsession IDを破棄しない。
			// Claude Codeの同一sessionへ--resumeして作業途中から継続する。
			if err := w.state.MarkReady(checkpoint.Role); err != nil {
				return packet.Packet{}, err
			}

			checkpoint.RateLimited = true
			checkpoint.ResetAtCST = limit.ResetAtCST
			checkpoint.ResetAtRFC3339 = limit.ResetAtRFC3339
			if err := w.state.SaveResumeCheckpoint(checkpoint); err != nil {
				return packet.Packet{}, err
			}
			if err := w.state.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
				return packet.Packet{}, err
			}
			w.state.RecordRateLimit(checkpoint.Model)

			taskID, err := w.state.TaskID()
			if err != nil {
				return packet.Packet{}, err
			}
			artifactErr := w.state.SecureArtifactDir()
			telemetryErr := runErr
			artifactWarning := ""
			if artifactErr != nil {
				artifactWarning = artifactErr.Error()
				telemetryErr = fmt.Errorf("%v; %w", runErr, artifactErr)
			}
			w.recordModelCall(checkpoint, runResult, startedAt, completedAt, "rate_limited", "", telemetryErr, outputPath)
			return packet.Packet{}, runner.ZaiRateLimitError{
				Phase:           checkpoint.Phase,
				Limit:           limit,
				TaskID:          taskID,
				RepoRoot:        w.config.RepoRoot,
				RepoShort:       w.config.RepoShort,
				ArtifactWarning: artifactWarning,
			}
		}
	}

	if err := w.state.SecureArtifactDir(); err != nil {
		w.recordModelCall(checkpoint, runResult, startedAt, completedAt, "state_error", "", err, outputPath)
		return packet.Packet{}, err
	}
	if runErr != nil {
		w.recordModelCall(checkpoint, runResult, startedAt, completedAt, "error", "", runErr, outputPath)
		_ = w.state.ClearResumeCheckpoint()
		_ = w.state.RemoveUnreadySession(checkpoint.Role)
		return packet.Packet{}, workerError(
			checkpoint.Phase,
			outputPath,
			runErr,
		)
	}

	if err := w.state.ClearResumeCheckpoint(); err != nil {
		w.recordModelCall(checkpoint, runResult, startedAt, completedAt, "state_error", "", err, outputPath)
		return packet.Packet{}, err
	}

	result, err := packet.ParseLast(outputPath)
	if err == nil {
		taskID, taskErr := w.state.TaskID()
		if taskErr != nil {
			w.recordModelCall(checkpoint, runResult, startedAt, completedAt, "state_error", "", taskErr, outputPath)
			return packet.Packet{}, taskErr
		}
		err = packet.ValidateArtifacts(result, w.state.ArtifactDir(taskID))
	}
	if err != nil {
		w.recordModelCall(checkpoint, runResult, startedAt, completedAt, "invalid_packet", "", err, outputPath)
		if packet.IsConstraintError(err) && !checkpoint.PacketCompacted {
			w.state.RecordPacketCompaction()
			compactCheckpoint := checkpoint
			compactCheckpoint.Phase += "-packet-compact"
			compactPrompt := packetCompressionPrompt(err.Error())
			compactCheckpoint.Prompt = compactPrompt
			compactCheckpoint.OriginalPrompt = compactPrompt
			compactCheckpoint.PacketCompacted = true
			return w.runModel(compactCheckpoint)
		}
		return packet.Packet{}, fmt.Errorf(
			"STATUS: WORKER_ERROR\nPHASE: %s-format\nERROR: %v\nOUTPUT_TAIL_BEGIN\n%s\nOUTPUT_TAIL_END",
			checkpoint.Phase,
			err,
			packet.Tail(outputPath, 20),
		)
	}
	w.recordModelCall(checkpoint, runResult, startedAt, completedAt, "success", result.Status(), nil, outputPath)
	return result, nil
}

func (w *Workflow) recordModelCall(
	checkpoint state.ResumeCheckpoint,
	runResult runner.RunResult,
	startedAt time.Time,
	completedAt time.Time,
	outcome string,
	packetStatus string,
	callErr error,
	outputPath string,
) {
	response := runResult.Response
	if response == "" {
		response = packet.Tail(outputPath, packet.MaxDiagnosticBytes)
	}
	promptHash := sha256.Sum256([]byte(checkpoint.Prompt))
	responseHash := sha256.Sum256([]byte(response))
	resolvedUsage := make(map[string]state.ResolvedModelUsage, len(runResult.ModelUsage))
	for model, usage := range runResult.ModelUsage {
		resolvedUsage[model] = state.ResolvedModelUsage{
			InputTokens:              usage.InputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
			OutputTokens:             usage.OutputTokens,
			CostUSD:                  usage.CostUSD,
		}
	}
	errorText := ""
	if callErr != nil {
		errorText = boundedText(callErr.Error(), packet.MaxDiagnosticBytes)
	}
	promptContent := checkpoint.Prompt
	systemPromptContent := runResult.SystemPrompt
	responseContent := response
	if !w.config.TelemetryContent {
		promptContent = ""
		systemPromptContent = ""
		responseContent = ""
	}
	w.state.RecordModelCallLog(state.ModelCallLog{
		TaskID:             w.state.ReadOr("task.id", "unknown"),
		SessionID:          modelSessionID(w.state, checkpoint.Role, runResult.SessionID),
		StartedAt:          startedAt,
		CompletedAt:        completedAt,
		Phase:              checkpoint.Phase,
		Role:               checkpoint.Role,
		ModelAlias:         checkpoint.Model,
		ResolvedModelUsage: resolvedUsage,
		Effort:             checkpoint.Effort,
		ReadOnly:           checkpoint.ReadOnly,
		Resumed:            runResult.Resumed,
		Outcome:            outcome,
		PacketStatus:       packetStatus,
		Prompt:             promptContent,
		PromptBytes:        len([]byte(checkpoint.Prompt)),
		PromptSHA256:       hex.EncodeToString(promptHash[:]),
		SystemPromptBytes:  runResult.SystemPromptBytes,
		SystemPromptSHA256: runResult.SystemPromptSHA256,
		SystemPrompt:       systemPromptContent,
		Response:           responseContent,
		ResponseBytes:      len([]byte(response)),
		ResponseSHA256:     hex.EncodeToString(responseHash[:]),
		Error:              errorText,
		TopLevelUsage: state.TokenUsage{
			InputTokens:              runResult.TopLevelUsage.InputTokens,
			CacheCreationInputTokens: runResult.TopLevelUsage.CacheCreationInputTokens,
			CacheReadInputTokens:     runResult.TopLevelUsage.CacheReadInputTokens,
			OutputTokens:             runResult.TopLevelUsage.OutputTokens,
		},
		WallDurationMS:      completedAt.Sub(startedAt).Milliseconds(),
		ClaudeDurationMS:    runResult.DurationMS,
		ClaudeAPIDurationMS: runResult.DurationAPIMS,
		TopLevelTurns:       runResult.TopLevelTurns,
		TotalCostUSD:        runResult.TotalCostUSD,
	})
}

func modelSessionID(st *state.StateStore, role state.SessionRole, fromRunner string) string {
	if fromRunner != "" {
		return fromRunner
	}
	return st.ReadOr(string(role)+".id", "unknown")
}

func boundedText(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	prefix := "[前方を省略]\n"
	return prefix + value[len(value)-(maxBytes-len(prefix)):]
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
		packet.Tail(outputPath, 30),
	)
}

func (w *Workflow) emitPacket(value packet.Packet) error {
	w.state.RecordSolPacket(value)
	fmt.Fprintln(w.output, value.String())
	return nil
}

func (w *Workflow) enforceRiskFloor(
	request string,
	workerPacket packet.Packet,
	reviewNumber int,
	autoFixes int,
	decision string,
	effectiveHigh bool,
	reviewPacket packet.Packet,
) (packet.Packet, error) {
	if !effectiveHigh || reviewPacket.Status() != "PASS" {
		return reviewPacket, nil
	}
	return w.riskFloorReemit(request, workerPacket, reviewNumber, autoFixes, decision)
}

func (w *Workflow) riskFloorReemit(
	request string,
	workerPacket packet.Packet,
	reviewNumber int,
	autoFixes int,
	decision string,
) (packet.Packet, error) {
	prompt := riskFloorReemitPrompt()
	checkpoint := state.ResumeCheckpoint{
		Stage:           state.ResumeStageReview,
		Phase:           fmt.Sprintf("reviewer-%d-risk-floor", reviewNumber),
		Role:            state.ReviewerRole,
		Model:           w.config.HighRiskReviewerModel,
		ReadOnly:        true,
		Effort:          w.config.RoutineEffort,
		Prompt:          prompt,
		OriginalPrompt:  prompt,
		Request:         request,
		Decision:        decision,
		WorkerPacket:    append([]string(nil), workerPacket.Lines...),
		ReviewNumber:    reviewNumber,
		AutoFixes:       autoFixes,
		RiskFloorReemit: true,
	}
	reemitPacket, err := w.runModel(checkpoint)
	if err != nil {
		return packet.Packet{}, err
	}
	return resolveRiskFloorReemit(reemitPacket), nil
}

func resolveRiskFloorReemit(reemitPacket packet.Packet) packet.Packet {
	if reemitPacket.Status() == "NEEDS_SOL_REVIEW" {
		return reemitPacket
	}
	return riskFloorFailClosedPacket(reemitPacket)
}

func riskFloorFailClosedPacket(reemitPacket packet.Packet) packet.Packet {
	return packet.FromLines([]string{
		"STATUS: NEEDS_SOL_REVIEW",
		"RISK: HIGH",
		fmt.Sprintf("SUMMARY: reviewerがrisk floor再出力要求へ従わず%sを返したためSol確認へ昇格", reemitPacket.Status()),
		"REQUIREMENT_COVERAGE: reviewer再出力が非準拠のためSolが直接確認する必要あり",
		"INVARIANTS: wrapper risk floorはHIGH RISK経路のreviewer PASSを許容しない",
		"TEST_EVIDENCE: reviewer同一sessionへNEEDS_SOL_REVIEW/HIGH再出力を依頼済み",
		fmt.Sprintf("ISSUES: reviewer再出力が非許容STATUS(%s)を返却", reemitPacket.Status()),
		"RESIDUAL_RISK: reviewer判断だけでHIGH RISK経路を完了扱いできない",
		"TARGETS: 直近reviewer出力と最終diff",
		fmt.Sprintf("ARTIFACTS: %s", reemitPacket.Fields["ARTIFACTS"]),
		"SOL_QUESTION: reviewer非準拠時の最終確認・修正方針をSolが判断する",
	})
}

func (w *Workflow) captureWorkerEndSnapshot() (bool, error) {
	workerEnd, err := w.captureSnapshot(w.config.RepoRoot)
	if err != nil {
		return true, w.failClosedSnapshot(state.SnapshotStageWorkerEnd, workerEnd, state.GitSnapshot{}, "worker-end snapshot取得失敗", err)
	}
	if err := w.state.SaveWorkerEndSnapshot(workerEnd); err != nil {
		return true, w.failClosedSnapshot(state.SnapshotStageWorkerEnd, workerEnd, state.GitSnapshot{}, "worker-end snapshot保存失敗", err)
	}
	return false, nil
}

func (w *Workflow) verifyReviewStartSnapshot() (bool, error) {
	workerEnd, err := w.state.LoadWorkerEndSnapshot()
	if err != nil {
		return true, w.failClosedSnapshot(state.SnapshotStageReviewStart, state.GitSnapshot{}, state.GitSnapshot{}, "worker-end snapshot読込失敗", err)
	}
	reviewStart, err := w.captureSnapshot(w.config.RepoRoot)
	if err != nil {
		return true, w.failClosedSnapshot(state.SnapshotStageReviewStart, workerEnd, state.GitSnapshot{}, "review-start snapshot取得失敗", err)
	}
	if err := w.state.SaveReviewStartSnapshot(reviewStart); err != nil {
		return true, w.failClosedSnapshot(state.SnapshotStageReviewStart, workerEnd, reviewStart, "review-start snapshot保存失敗", err)
	}
	comparison := state.CompareGitSnapshot(workerEnd, reviewStart, state.SnapshotStageReviewStart, "")
	if err := w.state.SaveSnapshotComparison(comparison); err != nil {
		return true, w.failClosedSnapshot(state.SnapshotStageReviewStart, workerEnd, reviewStart, "snapshot comparison保存失敗", err)
	}
	if !comparison.Matched {
		return true, w.failClosedSnapshot(state.SnapshotStageReviewStart, workerEnd, reviewStart, "worker終了状態とreview開始状態が一致しません", nil)
	}
	return false, nil
}

func (w *Workflow) verifyReviewResumeSnapshot() (bool, error) {
	saved, err := w.state.LoadReviewStartSnapshot()
	if err != nil {
		return true, w.failClosedSnapshot(state.SnapshotStageReviewResume, state.GitSnapshot{}, state.GitSnapshot{}, "review-start snapshot読込失敗", err)
	}
	current, err := w.captureSnapshot(w.config.RepoRoot)
	if err != nil {
		return true, w.failClosedSnapshot(state.SnapshotStageReviewResume, saved, state.GitSnapshot{}, "resume時snapshot取得失敗", err)
	}
	comparison := state.CompareGitSnapshot(saved, current, state.SnapshotStageReviewResume, "")
	if err := w.state.SaveSnapshotComparison(comparison); err != nil {
		return true, w.failClosedSnapshot(state.SnapshotStageReviewResume, saved, current, "snapshot comparison保存失敗", err)
	}
	if !comparison.Matched {
		return true, w.failClosedSnapshot(state.SnapshotStageReviewResume, saved, current, "review開始時から状態が変化しています", nil)
	}
	return false, nil
}

// checkpointを先に消すことでstatus更新失敗時もresumeさせず安全方向へ収束させ、
// WaitingSolReview移行中に残存checkpointがresume復元情報と矛盾するのを防ぐ。
func (w *Workflow) failClosedSnapshot(stage state.SnapshotStage, workerEnd, reviewStart state.GitSnapshot, reason string, cause error) error {
	if err := w.state.ClearResumeCheckpoint(); err != nil {
		return err
	}
	if err := w.state.SetTaskStatus(state.TaskStatusWaitingSolReview); err != nil {
		return err
	}
	if cause != nil {
		reason = fmt.Sprintf("%s: %v", reason, cause)
	}
	return w.emitPacket(snapshotFailClosedPacket(stage, workerEnd, reviewStart, reason))
}

func snapshotFailClosedPacket(stage state.SnapshotStage, workerEnd, reviewStart state.GitSnapshot, reason string) packet.Packet {
	return packet.FromLines([]string{
		"STATUS: NEEDS_SOL_REVIEW",
		"RISK: HIGH",
		fmt.Sprintf("SUMMARY: worker終了状態とreview開始状態の同一性確認に失敗しreviewerを呼ばずSol確認へ昇格(%s)", stage),
		"REQUIREMENT_COVERAGE: reviewerへ状態を引き渡す前にSolが直接確認する必要あり",
		"INVARIANTS: wrapperはworker-endとreview-start snapshotの3軸一致を確認するまでreviewerを呼ばない",
		"TEST_EVIDENCE: HEAD/index/worktree snapshotの比較・取得結果で不一致または失敗を検出",
		fmt.Sprintf("ISSUES: %s", reason),
		"RESIDUAL_RISK: reviewerがworkerと別の状態をreviewする可能性を排除できなかった",
		"TARGETS: repository HEAD/index/worktreeの現在状態と保存済みsnapshot state file",
		"ARTIFACTS: none",
		"SOL_QUESTION: worker終了状態とreview開始状態の差異・外部変更の有無をSolが判断する",
	})
}

func nonConvergedPacket(reviewPacket packet.Packet) packet.Packet {
	return packet.FromLines([]string{
		"STATUS: NEEDS_SOL_REVIEW",
		"RISK: HIGH",
		"SUMMARY: GLM workerと独立reviewerの自動修正が規定回数内に収束しなかった",
		"REQUIREMENT_COVERAGE: 最終状態をSol Highで確認する必要あり",
		"INVARIANTS: 未確定",
		"TEST_EVIDENCE: 直近worker/reviewerで検証実施",
		fmt.Sprintf("ISSUES: %s", reviewPacket.Fields["ISSUES"]),
		"RESIDUAL_RISK: reviewer指摘が残っている可能性",
		"TARGETS: 最終diffと直近reviewer指摘に限定",
		fmt.Sprintf("ARTIFACTS: %s", reviewPacket.Fields["ARTIFACTS"]),
		"SOL_QUESTION: 未解決問題の修正方針を判断する",
	})
}
