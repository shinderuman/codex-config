// Package workflowはworker→reviewer→auto-fixの状態機械を駆動する。
package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
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
// phaseは受動event logへ記録するcall識別metadataで、実行内容へは影響しない。
type ModelRunner interface {
	Run(role state.SessionRole, phase string, model string, readOnly bool, effort string, prompt string, outputPath string) (runner.RunResult, error)
	Probe(model string) (runner.ProbeResult, error)
}

type Workflow struct {
	config              config.AppConfig
	state               *state.StateStore
	runner              ModelRunner
	output              io.Writer
	temp                string
	captureSnapshot     func(repoRoot string) (state.GitSnapshot, error)
	collectChangedPaths func(repoRoot, baselineHead string) ([]string, error)
	now                 func() time.Time
	sleep               func(time.Duration)
	jitter              func(base time.Duration) time.Duration
	// pendingSnapshotはverifyReviewStart/ResumeSnapshotが一致判定した直近snapshot診断。
	// reviewer呼出成功時にそのcallのtelemetryへ付与して消費する。reemit呼出には付与しない。
	pendingSnapshot *state.SnapshotDiagnostic
	// currentResumeSourceはExecuteResumeが設定する再開理由(rate-limit/provider-unavailable)。
	// resume直後のrunModel記録へ付与して使う。1コマンド実行で1回だけ設定される。
	currentResumeSource string
}

// callDiagnosticsは1回のmodel呼出記録へ付与する診断情報。recordModelCallへ渡す。
// reportedRiskは成功時のpacket RISK。providerClassificationはtransient障害分類。
type callDiagnostics struct {
	reportedRisk           string
	providerClassification string
}

func NewWorkflow(cfg config.AppConfig, st *state.StateStore, r ModelRunner, output io.Writer) *Workflow {
	return &Workflow{
		config:              cfg,
		state:               st,
		runner:              r,
		output:              output,
		captureSnapshot:     state.CaptureGitSnapshot,
		collectChangedPaths: collectChangedPaths,
		now:                 time.Now,
		sleep:               time.Sleep,
		jitter:              boundedBackoffJitter,
	}
}

// providerUnavailableDeadlineは一時障害回復のhard deadline。backoffは各待機後にprobe 1回だけ送り、
// この上限とprobe回数上限の先に到達した側で停止する。deadlineを超えるsleepはbackoffWaitが禁止する。
const providerUnavailableDeadline = 3 * time.Hour

const maxTransientProbes = 4

// transientBackoffScheduleは各probe前のbase待機時間。合計155分で、jitter込みでも通常は
// 2.5〜3時間以内にdeadlineへ収まるよう選んだ。
var transientBackoffSchedule = []time.Duration{
	5 * time.Minute,
	15 * time.Minute,
	45 * time.Minute,
	90 * time.Minute,
}

// boundedBackoffJitterは固定間隔pollingを避けるためbaseに0〜25%を加える。testへ差し替え可能。
func boundedBackoffJitter(base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}
	return base + time.Duration(rand.Int63n(int64(base)/4+1))
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
		if checkpoint, err := w.state.LoadResumeCheckpoint(); err == nil {
			switch {
			case checkpoint.RateLimited:
				return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: previous task is rate-limited; use --resume or --reset")
			case checkpoint.ProviderUnavailable:
				return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: previous task is provider-unavailable; use --resume or --reset")
			}
		}

		if _, err := w.state.StartNewTask(); err != nil {
			return err
		}

		if err := state.CaptureGitBaseline(w.config, w.state); err != nil {
			return err
		}
		w.recordBaselineRound()
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
		return w.handleWorkerResult(request, workerPacket, checkpoint.Phase)
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
		return w.handleWorkerResult(request, workerPacket, checkpoint.Phase)
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
		return w.handleWorkerResult(request, workerPacket, checkpoint.Phase)
	})
}

func (w *Workflow) ExecuteResume() error {
	return w.withTemp(func() error {
		checkpoint, err := w.state.LoadResumeCheckpoint()
		if err != nil {
			return err
		}
		if !checkpoint.RateLimited && !checkpoint.ProviderUnavailable {
			return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: saved task is not stopped by Z.ai 5h limit or provider unavailability")
		}
		if !isKnownResumeStage(checkpoint.Stage) {
			return fmt.Errorf("STATUS: WORKER_ERROR\nERROR: unknown resume stage: %s", checkpoint.Stage)
		}

		previousCheckpoint := checkpoint
		if err := w.state.SetTaskStatus(state.TaskStatusActive); err != nil {
			return err
		}
		w.state.RecordResume()
		w.currentResumeSource = resumeSourceOf(checkpoint)
		// 旧binary保存のreport-only checkpoint(ReportOnly field無し)は厳格なphase形式から
		// report-onlyを推定する。基準snapshotが無ければ不変性を確認する基準自体が存在しないため、
		// probe・worker呼出を1件も実行する前にfail closedし、新baseline撮影で欠損を隠さない。
		if checkpoint.Stage == state.ResumeStageAutoFix && isReportOnlyResume(checkpoint) {
			if stopped, err := w.gateReportOnlyResumeSnapshot(); err != nil {
				return err
			} else if stopped {
				return nil
			}
		}
		// provider-unavailable resumeは本taskの前にprobeで疎通確認する。未回復のまま重い実requestを
		// 浪費しないため。transient失敗でbackoffに入り、上限で同じprovider-unavailable状態を再保存、
		// 明確な非transient errorはfail closedへ復帰する。
		if checkpoint.ProviderUnavailable {
			if err := w.gateResumeOnProbe(checkpoint); err != nil {
				var pErr *runner.ProviderUnavailableError
				if errors.As(err, &pErr) {
					return err
				}
				var limitErr runner.ZaiRateLimitError
				if errors.As(err, &limitErr) {
					return err
				}
				_ = w.state.ClearResumeCheckpoint()
				_ = w.state.RemoveUnreadySession(checkpoint.Role)
				// gate上のfatal(auth/config・state異常等)は本task再開せず従来のWORKER_ERROR終端へ出す。
				return fmt.Errorf("STATUS: WORKER_ERROR\nPHASE: %s\nERROR: %v", checkpoint.Phase, err)
			}
		}
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
		checkpoint.ProviderUnavailable = false
		checkpoint.ProviderUnavailableClassification = ""
		checkpoint.ProviderUnavailableProbes = 0
		checkpoint.ProviderUnavailableStartedAt = time.Time{}

		result, err := w.runModel(checkpoint)
		if err != nil {
			var pErr *runner.ProviderUnavailableError
			if errors.As(err, &pErr) {
				return err
			}
			var limitErr runner.ZaiRateLimitError
			if errors.As(err, &limitErr) {
				return err
			}
			saved, loadErr := w.state.LoadResumeCheckpoint()
			if loadErr != nil || (!saved.RateLimited && !saved.ProviderUnavailable) {
				_ = w.state.SaveResumeCheckpoint(previousCheckpoint)
			}
			// 誤resume防止のためstatusは保存済みcheckpointの停止理由と一致させる。
			restoredStatus := state.TaskStatusActive
			if loadErr == nil {
				switch {
				case saved.ProviderUnavailable:
					restoredStatus = state.TaskStatusProviderUnavailable
				case saved.RateLimited:
					restoredStatus = state.TaskStatusRateLimited
				}
			}
			if restoredStatus == state.TaskStatusActive {
				switch {
				case previousCheckpoint.ProviderUnavailable:
					restoredStatus = state.TaskStatusProviderUnavailable
				case previousCheckpoint.RateLimited:
					restoredStatus = state.TaskStatusRateLimited
				}
			}
			_ = w.state.SetTaskStatus(restoredStatus)
			return err
		}

		switch checkpoint.Stage {
		case state.ResumeStageWorker:
			return w.handleWorkerResult(checkpoint.Request, result, checkpoint.Phase)
		case state.ResumeStageReview:
			workerPacket := packet.FromLines(checkpoint.WorkerPacket)
			decision := w.state.ReadOr("last-decision", "none")
			if checkpoint.RiskFloorReemit {
				if stopped, err := w.verifyReviewEndSnapshot(); err != nil {
					return err
				} else if stopped {
					return nil
				}
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
			if stopped, err := w.verifyReviewEndSnapshot(); err != nil {
				return err
			} else if stopped {
				return nil
			}
			highRiskFloor := w.resolveReviewResumeRisk(workerPacket, checkpoint).high
			reviewPacket, reemitStopped, err := w.enforceRiskFloor(
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
			if reemitStopped {
				return nil
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
			// report-only resumeは初回開始前に保存した基準snapshotへ再照合する。停止期間中の
			// 変化も含めて同一性を強制し、resume時に新baselineを取り直して隠さない。
			// 旧checkpointもphase推定で同じ検証へ載せる。
			if isReportOnlyResume(checkpoint) {
				if stopped, err := w.verifyReportOnlyEndSnapshot(); err != nil {
					return err
				} else if stopped {
					return nil
				}
			}
			return w.handleAutoFixResult(
				checkpoint.Request,
				result,
				checkpoint.ReviewNumber,
				checkpoint.AutoFixes,
				checkpoint.Phase,
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

// resumeSourceOfは再開理由をrate-limit/provider-unavailable/空(非resume)へ分類する。
func resumeSourceOf(checkpoint state.ResumeCheckpoint) string {
	switch {
	case checkpoint.ProviderUnavailable:
		return "provider-unavailable"
	case checkpoint.RateLimited:
		return "rate-limit"
	default:
		return ""
	}
}

func (w *Workflow) handleWorkerResult(request string, workerPacket packet.Packet, workerPhase string) error {
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
		return w.reviewUntilStable(request, workerPacket, 1, 0, workerPhase)
	default:
		return fmt.Errorf("STATUS: WORKER_ERROR\nPHASE: worker-format\nERROR: worker did not return a valid STATUS")
	}
}

func (w *Workflow) reviewUntilStable(
	request string,
	workerPacket packet.Packet,
	reviewNumber int,
	autoFixes int,
	workerPhase string,
) error {
	workerEnd, stopped, err := w.captureWorkerEndSnapshot()
	if err != nil {
		return err
	}
	if stopped {
		return nil
	}
	w.recordConvergenceRound(reviewNumber, autoFixes, workerPhase, workerEnd)

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
	if stopped, err := w.verifyReviewEndSnapshot(); err != nil {
		return err
	} else if stopped {
		return nil
	}
	reviewPacket, reemitStopped, err := w.enforceRiskFloor(
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
	if reemitStopped {
		return nil
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
		phase := fmt.Sprintf("worker-auto-fix-%d", nextAutoFixes)
		reportOnly := isReportOnlyFix(reviewPacket)
		// TARGETS: PACKETはreviewerがコード・diffを正しいと確認しPACKET/reportの意味情報だけを
		// 不足と指摘した予約marker。実装修正へ流さず報告再出力専用promptへ分岐する。
		// 収束上限・risk floor・session/model routingは通常auto-fixと同じ枠で数える。
		// report-only workerはReadOnly capabilityで実行し、開始前3軸snapshotを基準とした
		// 前後同一性をprompt遵守ではなくwrapper側で強制する。
		if reportOnly {
			prompt = reportOnlyFixPrompt(request, decision, reviewPacket)
			phase = fmt.Sprintf("worker-report-only-%d", nextAutoFixes)
		}
		checkpoint := state.ResumeCheckpoint{
			Stage:          state.ResumeStageAutoFix,
			Phase:          phase,
			Role:           state.WorkerRole,
			Model:          w.config.WorkerModel,
			ReadOnly:       reportOnly,
			ReportOnly:     reportOnly,
			Effort:         w.config.RoutineEffort,
			Prompt:         prompt,
			OriginalPrompt: prompt,
			Request:        request,
			Decision:       decision,
			ReviewNumber:   reviewNumber,
			AutoFixes:      nextAutoFixes,
		}
		w.state.RecordAutoFix()

		if reportOnly {
			if stopped, err := w.saveReportOnlyStartSnapshot(); err != nil {
				return err
			} else if stopped {
				return nil
			}
		}

		fixPacket, err := w.runModel(checkpoint)
		if err != nil {
			return err
		}

		if reportOnly {
			if stopped, err := w.verifyReportOnlyEndSnapshot(); err != nil {
				return err
			} else if stopped {
				return nil
			}
		}

		return w.handleAutoFixResult(
			request,
			fixPacket,
			reviewNumber,
			nextAutoFixes,
			phase,
		)

	default:
		return fmt.Errorf("STATUS: WORKER_ERROR\nPHASE: reviewer-format\nERROR: reviewer did not return a valid STATUS")
	}
}

func reviewNeedsHighRiskFloor(workerPacket packet.Packet, autoFixes int, hasDecision bool, hasPriorReview bool) bool {
	return workerPacket.Risk() == "HIGH" || autoFixes > 0 || hasDecision || hasPriorReview
}

// reportOnlyFixTargetsはFIX_REQUIREDのTARGETS予約値。reviewerがコード・diffを正しいと確認し
// workerのPACKET/reportの意味情報だけを不足と指摘するときに使い、productionは実装修正と
// 報告再出力をこの値だけで機械識別する。自然言語の解釈は行わない。
const reportOnlyFixTargets = "PACKET"

func isReportOnlyFix(reviewPacket packet.Packet) bool {
	return reviewPacket.Status() == "FIX_REQUIRED" && reviewPacket.Fields["TARGETS"] == reportOnlyFixTargets
}

// packetCompactPhaseSuffixはpacket圧縮再実行時にrunModelがphase末尾へ付与する固定suffix。
// reportOnlyFromPhaseが圧縮済み旧checkpointもreport-only推定へ含めるため、生成側と共有する。
const packetCompactPhaseSuffix = "-packet-compact"

// isReportOnlyResumeはresume checkpointがreport-only PACKET再出力工程かを判定する。
// 新checkpointはReportOnly fieldを第一判定とし、旧binaryが保存したcheckpointは
// ReportOnly fieldを持たないため厳格なphase生成形式"worker-report-only-<十進数>"(packet
// 圧縮suffix付きを含む)の全体一致から推定する。部分一致や類似phase(worker-auto-fix-N等)
// へ誤適用しない。
func isReportOnlyResume(checkpoint state.ResumeCheckpoint) bool {
	return checkpoint.ReportOnly || reportOnlyFromPhase(checkpoint.Phase)
}

func reportOnlyFromPhase(phase string) bool {
	if !strings.HasPrefix(phase, "worker-report-only-") {
		return false
	}
	suffix := strings.TrimPrefix(phase, "worker-report-only-")
	// 圧縮再実行は一度だけしか付与されない(PacketCompactedで再圧縮を禁止)ため、
	// この固定suffix 1回だけを除去して残りが十進数かを見る。
	suffix = strings.TrimSuffix(suffix, packetCompactPhaseSuffix)
	if suffix == "" {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
	fixPhase string,
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
			fixPhase,
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

	startedAt := w.now().UTC()
	runResult, runErr := w.runner.Run(
		checkpoint.Role,
		checkpoint.Phase,
		checkpoint.Model,
		checkpoint.ReadOnly,
		checkpoint.Effort,
		checkpoint.Prompt,
		outputPath,
	)
	completedAt := w.now().UTC()
	w.state.RecordModelDuration(checkpoint.Model, completedAt.Sub(startedAt))
	failureClass := runner.ProviderFailureClass{}
	if runErr != nil {
		failureClass = mergePlainFailureClass(
			runner.ClassifyProviderFailureText(runner.ReadTransientSignal(outputPath)),
			runResult.PlainFailure,
		)
		if failureClass.Kind == runner.ProviderFailureZaiFiveHour {
			return packet.Packet{}, w.saveRateLimitedState(checkpoint, failureClass.FiveHourLimit, runResult, startedAt, completedAt, runErr, outputPath)
		}
	}

	// Z.ai 5h上限以外の一時障害(502/503/504/529・明確な一時network障害)は同じsession/checkpointと
	// Git snapshotを保持した上限付きbackoffで回復する。auth/invalid request/session破損/不明errorは
	// 非transientのためここへ入らず、従来どおり下段のWORKER_ERROR分岐へ進む。
	recoveryFatal := false
	if failureClass.Kind == runner.ProviderFailureTransient {
		recovered, resumeResult, resumeStartedAt, resumeCompletedAt, recErr := w.recoverTransient(
			checkpoint, outputPath, failureClass.Detail, runResult, startedAt, completedAt,
		)
		if recovered {
			runResult = resumeResult
			startedAt = resumeStartedAt
			completedAt = resumeCompletedAt
			runErr = nil
		} else {
			var pErr *runner.ProviderUnavailableError
			if errors.As(recErr, &pErr) {
				_ = w.state.SecureArtifactDir()
				w.state.RecordProviderUnavailable(checkpoint.Model)
				return packet.Packet{}, recErr
			}
			var limitErr runner.ZaiRateLimitError
			if errors.As(recErr, &limitErr) {
				// 5h上限checkpointは保存済みのため、WORKER_ERROR分岐でcheckpointを壊させない。
				return packet.Packet{}, recErr
			}
			runResult = resumeResult
			startedAt = resumeStartedAt
			completedAt = resumeCompletedAt
			runErr = recErr
			// recovery終端のfatal分類と実call記録は分離する。実行された再開task呼出は
			// runResumedTaskが各終端で記録済みで、probe段階fatalは本task呼出を実行していない。
			// どちらもここへの追加記録はphantom/二重計上になるため抑止する。
			recoveryFatal = true
		}
	}

	if err := w.state.SecureArtifactDir(); err != nil {
		if !recoveryFatal {
			w.recordModelCall(checkpoint, runResult, startedAt, completedAt, "state_error", "", err, outputPath, callDiagnostics{})
		}
		return packet.Packet{}, err
	}
	if runErr != nil {
		if !recoveryFatal {
			w.recordModelCall(checkpoint, runResult, startedAt, completedAt, "error", "", runErr, outputPath, callDiagnostics{})
		}
		_ = w.state.ClearResumeCheckpoint()
		_ = w.state.RemoveUnreadySession(checkpoint.Role)
		return packet.Packet{}, workerError(
			checkpoint.Phase,
			outputPath,
			runErr,
		)
	}

	if err := w.state.ClearResumeCheckpoint(); err != nil {
		w.recordModelCall(checkpoint, runResult, startedAt, completedAt, "state_error", "", err, outputPath, callDiagnostics{})
		return packet.Packet{}, err
	}

	result, err := packet.Parse(outputPath)
	if err == nil {
		taskID, taskErr := w.state.TaskID()
		if taskErr != nil {
			w.recordModelCall(checkpoint, runResult, startedAt, completedAt, "state_error", "", taskErr, outputPath, callDiagnostics{})
			return packet.Packet{}, taskErr
		}
		err = packet.ValidateArtifacts(result, w.state.ArtifactDir(taskID))
	}
	if err != nil {
		w.recordModelCall(checkpoint, runResult, startedAt, completedAt, "invalid_packet", "", err, outputPath, callDiagnostics{})
		if packet.IsConstraintError(err) && !checkpoint.PacketCompacted {
			w.state.RecordPacketCompaction()
			compactCheckpoint := checkpoint
			compactCheckpoint.Phase += packetCompactPhaseSuffix
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
	w.recordModelCall(checkpoint, runResult, startedAt, completedAt, "success", result.Status(), nil, outputPath, callDiagnostics{reportedRisk: result.Risk()})
	return result, nil
}

// saveRateLimitedStateは5h上限時もsessionを破棄せず再開可能な状態へ保存し、ZaiRateLimitErrorを返す。
func (w *Workflow) saveRateLimitedState(
	checkpoint state.ResumeCheckpoint,
	limit runner.ZaiFiveHourLimit,
	runResult runner.RunResult,
	startedAt time.Time,
	completedAt time.Time,
	runErr error,
	outputPath string,
) error {
	if err := w.state.MarkReady(checkpoint.Role); err != nil {
		return err
	}
	taskID, err := w.persistRateLimitedStop(checkpoint, limit)
	if err != nil {
		return err
	}

	artifactErr := w.state.SecureArtifactDir()
	telemetryErr := runErr
	artifactWarning := ""
	if artifactErr != nil {
		artifactWarning = artifactErr.Error()
		telemetryErr = fmt.Errorf("%v; %w", runErr, artifactErr)
	}
	w.recordModelCall(checkpoint, runResult, startedAt, completedAt, "rate_limited", "", telemetryErr, outputPath, callDiagnostics{})
	return runner.ZaiRateLimitError{
		Phase:           checkpoint.Phase,
		Limit:           limit,
		TaskID:          taskID,
		RepoRoot:        w.config.RepoRoot,
		RepoShort:       w.config.RepoShort,
		ArtifactWarning: artifactWarning,
	}
}

// persistRateLimitedStopは5h上限の再開可能停止へcheckpointとtask statusを保存する。
// provider-unavailable停止fieldは排他させてクリアし、resume理由の誤分類を防ぐ。
func (w *Workflow) persistRateLimitedStop(checkpoint state.ResumeCheckpoint, limit runner.ZaiFiveHourLimit) (string, error) {
	checkpoint.RateLimited = true
	checkpoint.ResetAtCST = limit.ResetAtCST
	checkpoint.ResetAtRFC3339 = limit.ResetAtRFC3339
	checkpoint.ProviderUnavailable = false
	checkpoint.ProviderUnavailableClassification = ""
	checkpoint.ProviderUnavailableProbes = 0
	checkpoint.ProviderUnavailableStartedAt = time.Time{}
	if err := w.state.SaveResumeCheckpoint(checkpoint); err != nil {
		return "", err
	}
	if err := w.state.SetTaskStatus(state.TaskStatusRateLimited); err != nil {
		return "", err
	}
	w.state.RecordRateLimit(checkpoint.Model)
	taskID, err := w.state.TaskID()
	if err != nil {
		return "", err
	}
	return taskID, nil
}

// saveProbeRateLimitedはprobe応答から5h上限を検出した時点でrate-limited停止へ保存する。
// probe呼出はrecordProbeCallで記録済みのため、task呼出のtelemetry記録は追加しない。
func (w *Workflow) saveProbeRateLimited(checkpoint state.ResumeCheckpoint, limit runner.ZaiFiveHourLimit) error {
	taskID, err := w.persistRateLimitedStop(checkpoint, limit)
	if err != nil {
		return err
	}
	_ = w.state.SecureArtifactDir()
	return runner.ZaiRateLimitError{
		Phase:     checkpoint.Phase,
		Limit:     limit,
		TaskID:    taskID,
		RepoRoot:  w.config.RepoRoot,
		RepoShort: w.config.RepoShort,
	}
}

// recoverTransientは一時provider障害を同じsession/checkpoint/Git snapshot保持で回復する。
// 5h上限のような自動wakeは設定せず、短周期polling・新task/sessionでの再実行は行わない。
func (w *Workflow) recoverTransient(
	checkpoint state.ResumeCheckpoint,
	outputPath string,
	classification string,
	initialResult runner.RunResult,
	initialStartedAt time.Time,
	initialCompletedAt time.Time,
) (bool, runner.RunResult, time.Time, time.Time, error) {
	w.recordModelCall(checkpoint, initialResult, initialStartedAt, initialCompletedAt, "transient_error", "", fmt.Errorf("transient provider failure: %s", classification), outputPath, callDiagnostics{providerClassification: classification})
	if err := w.state.MarkReady(checkpoint.Role); err != nil {
		return false, runner.RunResult{}, time.Time{}, time.Time{}, err
	}
	return w.recoveryLoop(checkpoint, classification, false, func() (bool, runner.RunResult, time.Time, time.Time, error) {
		return w.runResumedTask(checkpoint, outputPath)
	})
}

// gateResumeOnProbeはprovider-unavailableからの--resumeで本task送出前にprobeで疎通を確認する。
// 手動resume直後は既に時間経過しているため最初のprobeは即時に行う。
func (w *Workflow) gateResumeOnProbe(checkpoint state.ResumeCheckpoint) error {
	_, _, _, _, err := w.recoveryLoop(checkpoint, checkpoint.ProviderUnavailableClassification, true, func() (bool, runner.RunResult, time.Time, time.Time, error) {
		return true, runner.RunResult{}, time.Time{}, time.Time{}, nil
	})
	return err
}

func (w *Workflow) runResumedTask(checkpoint state.ResumeCheckpoint, outputPath string) (bool, runner.RunResult, time.Time, time.Time, error) {
	startedAt := w.now().UTC()
	// probe成功後の再実行も本taskのTask Work Callとしてrole別に数える。
	w.state.RecordTransientRetry()
	w.state.RecordModelCall(checkpoint.Role, checkpoint.Model)
	result, runErr := w.runner.Run(
		checkpoint.Role,
		checkpoint.Phase,
		checkpoint.Model,
		checkpoint.ReadOnly,
		checkpoint.Effort,
		checkpoint.Prompt,
		outputPath,
	)
	completedAt := w.now().UTC()
	w.state.RecordModelDuration(checkpoint.Model, completedAt.Sub(startedAt))
	if runErr == nil {
		return true, result, startedAt, completedAt, nil
	}
	class := mergePlainFailureClass(
		runner.ClassifyProviderFailureText(runner.ReadTransientSignal(outputPath)),
		result.PlainFailure,
	)
	if class.Kind == runner.ProviderFailureZaiFiveHour {
		err := w.saveRateLimitedState(checkpoint, class.FiveHourLimit, result, startedAt, completedAt, runErr, outputPath)
		return false, result, startedAt, completedAt, err
	}
	if class.Kind != runner.ProviderFailureTransient {
		// 実行した再開task呼出がordinary nontransient fatalで終わった場合も、初回transient runと
		// 同じく実callの記録を残す。runModel側のrecovery fatal抑止と対で、call実行事実と
		// recovery終端分類を独立に扱う。
		w.recordModelCall(checkpoint, result, startedAt, completedAt, "error", "", runErr, outputPath, callDiagnostics{})
		return false, result, startedAt, completedAt, runErr
	}
	w.recordModelCall(checkpoint, result, startedAt, completedAt, "transient_error", "", runErr, outputPath, callDiagnostics{providerClassification: class.Detail})
	return false, runner.RunResult{}, startedAt, completedAt, nil
}

// mergePlainFailureClassはoutputPath本文の分類に、result本文が無い経路だけrunnerが
// plain stdoutから検出した5h上限・transient分類を統合する。旧raw fallbackは全本文を
// 連結して5h→transientの順で一致させていたため、どちらかの5hを最優先にし、
// file由来とplain由来が同種のときはfile由来のdetailを保つ。
func mergePlainFailureClass(base runner.ProviderFailureClass, plain runner.ProviderFailureClass) runner.ProviderFailureClass {
	switch {
	case plain.Kind == runner.ProviderFailureZaiFiveHour:
		return plain
	case base.Kind == runner.ProviderFailureZaiFiveHour:
		return base
	case base.Kind == runner.ProviderFailureTransient:
		return base
	case plain.Kind == runner.ProviderFailureTransient:
		return plain
	default:
		return base
	}
}

// recoveryLoopは上限付きbackoffでprobeを繰り返し、transientだけ再試行する。
// probe応答の契約違反(semantic invalid)もsaved taskのresumeへ進めない通常のprobe失敗として
// 同じbackoff/retryへ戻し、probe上限・hard deadlineの先に到達した側でprovider-unavailableへ保存する。
// probe応答の分類優先度は5h→transient→明示fatalの順でtask呼出と共通の既存classifierと一致させ、
// 明示的なauth/config信号のときだけfatal経路へ即時にfail closedする。
func (w *Workflow) recoveryLoop(
	checkpoint state.ResumeCheckpoint,
	classification string,
	firstProbeImmediate bool,
	onProbeSuccess func() (bool, runner.RunResult, time.Time, time.Time, error),
) (bool, runner.RunResult, time.Time, time.Time, error) {
	recoveryStart := w.now().UTC()
	deadline := recoveryStart.Add(providerUnavailableDeadline)
	probes := 0
	sleeps := 0
	// 停止時分類は初期障害分類で開始し、probe応答の契約違反観測で上書きする。
	exhaustClassification := classification

	for {
		if probes >= maxTransientProbes {
			break
		}
		if !(firstProbeImmediate && probes == 0) {
			wait, ok := w.backoffWait(sleeps, deadline)
			if !ok {
				break
			}
			w.sleep(wait)
			sleeps++
			if w.now().After(deadline) {
				break
			}
		}

		probes++
		probeStartedAt := w.now().UTC()
		probeResult, probeErr := w.runner.Probe(checkpoint.Model)
		probeCompletedAt := w.now().UTC()
		// fake runnerはreal runnerの応答検証を通らないため、gate側でも契約を強制する。
		if probeErr == nil {
			if contractErr := runner.ValidateProbeResult(probeResult); contractErr != nil {
				probeErr = &runner.ProbeInvalidResponseError{
					Model:  checkpoint.Model,
					Reason: contractErr,
				}
			}
		}
		w.recordProbeCall(checkpoint, probeResult, probes, probeStartedAt, probeCompletedAt, probeErr)

		if probeErr != nil {
			class := runner.ClassifyProviderFailureText(probeErr.Error())
			// 5h上限signatureはprobe応答でも他分類へより優先しrate-limited停止へ保存する。
			if class.Kind == runner.ProviderFailureZaiFiveHour {
				err := w.saveProbeRateLimited(checkpoint, class.FiveHourLimit)
				return false, runner.RunResult{}, probeStartedAt, probeCompletedAt, err
			}
			// transient信号は明示fatal検出より優先し、既存classifierと同じbackoff/retryへ戻す。
			// 503等とauth語の混在応答もここでtransientとして扱われる。
			if class.Kind == runner.ProviderFailureTransient {
				continue
			}
			var probeInvalid *runner.ProbeInvalidResponseError
			if errors.As(probeErr, &probeInvalid) {
				// 応答本文中の明示的なauth/config信号はsemantic invalidと区別し、
				// 既存classifierと同じfatal経路へ直ちにfail closedする。
				if runner.DetectProbeFatalSignal(probeErr.Error()) {
					return false, runner.RunResult{}, probeStartedAt, probeCompletedAt, probeErr
				}
				exhaustClassification = runner.ProbeContractFailure
				continue
			}
			return false, runner.RunResult{}, probeStartedAt, probeCompletedAt, probeErr
		}

		recovered, result, startedAt, completedAt, err := onProbeSuccess()
		if recovered {
			return true, result, startedAt, completedAt, nil
		}
		if err != nil {
			return false, result, startedAt, completedAt, err
		}
	}

	pErr, saveErr := w.saveProviderUnavailable(checkpoint, exhaustClassification, probes, recoveryStart)
	if saveErr != nil {
		return false, runner.RunResult{}, time.Time{}, time.Time{}, saveErr
	}
	return false, runner.RunResult{}, time.Time{}, time.Time{}, pErr
}

// backoffWaitはscheduleにjitterを加えた待機時間を返す。deadline残り時間を超えないよう切り詰め、
// schedule外か残り0のときはok=falseを返す。
func (w *Workflow) backoffWait(sleeps int, deadline time.Time) (time.Duration, bool) {
	if sleeps >= len(transientBackoffSchedule) {
		return 0, false
	}
	remaining := deadline.Sub(w.now())
	if remaining <= 0 {
		return 0, false
	}
	wait := w.jitter(transientBackoffSchedule[sleeps])
	if wait > remaining {
		wait = remaining
	}
	return wait, true
}

func (w *Workflow) saveProviderUnavailable(checkpoint state.ResumeCheckpoint, classification string, probes int, recoveryStart time.Time) (*runner.ProviderUnavailableError, error) {
	checkpoint.ProviderUnavailable = true
	checkpoint.ProviderUnavailableClassification = classification
	checkpoint.ProviderUnavailableProbes = probes
	checkpoint.ProviderUnavailableStartedAt = recoveryStart
	checkpoint.RateLimited = false
	if err := w.state.SaveResumeCheckpoint(checkpoint); err != nil {
		return nil, err
	}
	if err := w.state.SetTaskStatus(state.TaskStatusProviderUnavailable); err != nil {
		return nil, err
	}
	elapsed := w.now().Sub(recoveryStart)
	w.recordProviderUnavailableEvent(checkpoint, classification, probes, elapsed)
	taskID, _ := w.state.TaskID()
	return &runner.ProviderUnavailableError{
		Phase:          checkpoint.Phase,
		Classification: classification,
		Probes:         probes,
		Elapsed:        elapsed,
		TaskID:         taskID,
		RepoRoot:       w.config.RepoRoot,
		RepoShort:      w.config.RepoShort,
	}, nil
}

// recordProviderUnavailableEventは回復が上限/deadlineに到達し再開可能停止状態へ移行した事実を
// telemetryへ記録する。classification・probe回数・累積経過時間を残し、token消費は持たない(best-effort)。
func (w *Workflow) recordProviderUnavailableEvent(checkpoint state.ResumeCheckpoint, classification string, probes int, elapsed time.Duration) {
	now := w.now().UTC()
	w.state.RecordModelCallLog(state.ModelCallLog{
		TaskID:                 w.state.ReadOr("task.id", "unknown"),
		CallType:               state.CallTypeEvent,
		StartedAt:              now,
		CompletedAt:            now,
		Phase:                  checkpoint.Phase + "-provider-unavailable",
		Role:                   checkpoint.Role,
		ModelAlias:             checkpoint.Model,
		Outcome:                "provider_unavailable",
		ProviderClassification: classification,
		ProbeAttempt:           probes,
		RetryElapsedMS:         elapsed.Milliseconds(),
	})
}

func (w *Workflow) recordProbeCall(
	checkpoint state.ResumeCheckpoint,
	probe runner.ProbeResult,
	attempt int,
	startedAt time.Time,
	completedAt time.Time,
	probeErr error,
) {
	outcome := "probe_success"
	errorText := ""
	if probeErr != nil {
		outcome = "probe_failure"
		errorText = boundedText(probeErr.Error(), packet.MaxDiagnosticBytes)
	}
	w.state.RecordProbeOutcome(outcome)
	promptHash := sha256.Sum256([]byte(runner.ProbePrompt))
	response := probe.Response
	if !w.config.TelemetryContent {
		response = ""
	}
	resolvedUsage := make(map[string]state.ResolvedModelUsage, len(probe.ModelUsage))
	for model, usage := range probe.ModelUsage {
		resolvedUsage[model] = state.ResolvedModelUsage{
			InputTokens:              usage.InputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
			OutputTokens:             usage.OutputTokens,
			CostUSD:                  usage.CostUSD,
		}
	}
	w.state.RecordModelCallLog(state.ModelCallLog{
		TaskID:              w.state.ReadOr("task.id", "unknown"),
		CallType:            state.CallTypeProbe,
		SessionID:           "none",
		StartedAt:           startedAt,
		CompletedAt:         completedAt,
		Phase:               fmt.Sprintf("%s-probe-%d", checkpoint.Phase, attempt),
		Role:                checkpoint.Role,
		ModelAlias:          checkpoint.Model,
		ResolvedModelUsage:  resolvedUsage,
		Effort:              "low",
		ReadOnly:            true,
		Outcome:             outcome,
		ProbeAttempt:        attempt,
		PromptBytes:         len(runner.ProbePrompt),
		PromptSHA256:        hex.EncodeToString(promptHash[:]),
		Response:            response,
		ResponseBytes:       len(probe.Response),
		Error:               errorText,
		TopLevelUsage:       state.TokenUsage(probe.Usage),
		WallDurationMS:      completedAt.Sub(startedAt).Milliseconds(),
		ClaudeDurationMS:    probe.DurationMS,
		ClaudeAPIDurationMS: probe.DurationAPIMS,
		TotalCostUSD:        probe.TotalCostUSD,
	})
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
	diag callDiagnostics,
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
	entry := state.ModelCallLog{
		TaskID:             w.state.ReadOr("task.id", "unknown"),
		CallType:           state.CallTypeTask,
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
	}
	w.applyCallDiagnostics(&entry, checkpoint, outcome, callErr, diag)
	w.state.RecordModelCallLog(entry)
}

// applyCallDiagnosticsは診断fieldと集計をentryへ反映する。報告risk・実効risk・risk floor・
// snapshot・packet reject・provider分類・resume sourceを、当該callで観測されたときだけ設定する。
// 未観測は零値(空文字/nil)のままで、意味値(HIGH/LOW/一致)とは区別される。
func (w *Workflow) applyCallDiagnostics(entry *state.ModelCallLog, checkpoint state.ResumeCheckpoint, outcome string, callErr error, diag callDiagnostics) {
	if diag.reportedRisk != "" {
		if checkpoint.Role == state.ReviewerRole {
			entry.ReviewerReportedRisk = diag.reportedRisk
		} else {
			entry.WorkerReportedRisk = diag.reportedRisk
		}
	}
	if checkpoint.EffectiveRisk != "" {
		entry.EffectiveRisk = checkpoint.EffectiveRisk
		entry.RiskFloorSource = checkpoint.EffectiveRiskSource
		if checkpoint.Role == state.ReviewerRole && checkpoint.EffectiveRisk == "HIGH" {
			category := riskFloorCategory(checkpoint.EffectiveRiskSource)
			entry.RiskFloorCategory = category
			w.state.RecordRiskFloor(category)
		}
	}
	if diag.providerClassification != "" {
		entry.ProviderClassification = diag.providerClassification
	}
	if w.currentResumeSource != "" {
		entry.ResumeSource = w.currentResumeSource
		w.currentResumeSource = ""
	}
	if outcome == "invalid_packet" && callErr != nil {
		category := packet.RejectCategory(callErr)
		entry.PacketRejectReason = category
		w.state.RecordPacketReject(category)
	}
	if checkpoint.Role == state.ReviewerRole && outcome == "success" && w.pendingSnapshot != nil {
		entry.Snapshot = w.pendingSnapshot
		w.pendingSnapshot = nil
	}
}

// riskFloorSourceは"worker-declared;auto-fix;self-protection:critical-path"等の詳細文字列。
// riskFloorCategoryは集計用へself-protectionのpath詳細を落として安定bucket化する。
func riskFloorCategory(source string) string {
	if source == "" {
		return ""
	}
	var categories []string
	for _, raw := range strings.Split(source, ";") {
		name := strings.SplitN(raw, ":", 2)[0]
		if name != "" {
			categories = append(categories, name)
		}
	}
	return strings.Join(categories, ",")
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
) (packet.Packet, bool, error) {
	if !effectiveHigh || reviewPacket.Status() != "PASS" {
		return reviewPacket, false, nil
	}
	reemitPacket, stopped, err := w.riskFloorReemit(request, workerPacket, reviewNumber, autoFixes, decision)
	if err != nil || stopped {
		return packet.Packet{}, stopped, err
	}
	return reemitPacket, false, nil
}

func (w *Workflow) riskFloorReemit(
	request string,
	workerPacket packet.Packet,
	reviewNumber int,
	autoFixes int,
	decision string,
) (packet.Packet, bool, error) {
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
		return packet.Packet{}, false, err
	}
	if stopped, err := w.verifyReviewEndSnapshot(); err != nil {
		return packet.Packet{}, false, err
	} else if stopped {
		return packet.Packet{}, true, nil
	}
	return resolveRiskFloorReemit(reemitPacket), false, nil
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

func (w *Workflow) captureWorkerEndSnapshot() (state.GitSnapshot, bool, error) {
	workerEnd, err := w.captureSnapshot(w.config.RepoRoot)
	if err != nil {
		return workerEnd, true, w.failClosedSnapshot(state.SnapshotStageWorkerEnd, workerEnd, state.GitSnapshot{}, "worker-end snapshot取得失敗", err)
	}
	if err := w.state.SaveWorkerEndSnapshot(workerEnd); err != nil {
		return workerEnd, true, w.failClosedSnapshot(state.SnapshotStageWorkerEnd, workerEnd, state.GitSnapshot{}, "worker-end snapshot保存失敗", err)
	}
	return workerEnd, false, nil
}

// saveReportOnlyStartSnapshotはreport-only worker開始直前のHEAD/index/worktreeを基準として
// 保存する。基準を確保できないときはworkerを実行せずfail closedする。resumeはこの保存済み
// snapshotを基準に再利用し、再撮影して停止期間中の変化を隠さない。
func (w *Workflow) saveReportOnlyStartSnapshot() (bool, error) {
	start, err := w.captureSnapshot(w.config.RepoRoot)
	if err != nil {
		return true, w.failClosedReportOnlySnapshot(state.SnapshotStageReportOnlyStart, start, state.GitSnapshot{}, "report-only開始前snapshot取得失敗", err)
	}
	if err := w.state.SaveReportOnlyStartSnapshot(start); err != nil {
		return true, w.failClosedReportOnlySnapshot(state.SnapshotStageReportOnlyStart, start, state.GitSnapshot{}, "report-only開始前snapshot保存失敗", err)
	}
	return false, nil
}

// gateReportOnlyResumeSnapshotはreport-only resumeを実行前に基準snapshotの存在だけを確認する。
// 基準が無ければprobeもworker呼出も1件も行わずfail closedする。resume時に新baselineを撮って
// 欠損を隠さないため、存在する基準は必ず初回開始直前に保存されたものである。
func (w *Workflow) gateReportOnlyResumeSnapshot() (bool, error) {
	if _, err := w.state.LoadReportOnlyStartSnapshot(); err != nil {
		return true, w.failClosedReportOnlySnapshot(
			state.SnapshotStageReportOnlyStart,
			state.GitSnapshot{},
			state.GitSnapshot{},
			"resume再開前にreport-only開始前snapshotが欠損しているため不変性の基準を確認できません(旧形式checkpointを含む)",
			err,
		)
	}
	return false, nil
}

// verifyReportOnlyEndSnapshotはreport-only worker終了後、開始直前の保存snapshotへ現在状態を
// 再照合する。通常auto-fixがworker-end snapshotを新基準として撮り直すreviewUntilStableへ
// 入る前に強制し、1軸でも変化すれば通常reviewへ進めずfail closedする。rate-limit・provider
// 障害のresume後も同じ基準を使うため、停止期間中の変化も検出から逃れない。
func (w *Workflow) verifyReportOnlyEndSnapshot() (bool, error) {
	start, err := w.state.LoadReportOnlyStartSnapshot()
	if err != nil {
		return true, w.failClosedReportOnlySnapshot(state.SnapshotStageReportOnlyEnd, state.GitSnapshot{}, state.GitSnapshot{}, "report-only開始前snapshot読込失敗", err)
	}
	current, err := w.captureSnapshot(w.config.RepoRoot)
	if err != nil {
		return true, w.failClosedReportOnlySnapshot(state.SnapshotStageReportOnlyEnd, start, state.GitSnapshot{}, "report-only終了後snapshot取得失敗", err)
	}
	comparison := state.CompareGitSnapshot(start, current, state.SnapshotStageReportOnlyEnd, "")
	if err := w.state.SaveSnapshotComparison(comparison); err != nil {
		return true, w.failClosedReportOnlySnapshot(state.SnapshotStageReportOnlyEnd, start, current, "snapshot comparison保存失敗", err)
	}
	if !comparison.Matched {
		return true, w.failClosedReportOnlySnapshot(state.SnapshotStageReportOnlyEnd, start, current, "report-only worker開始前から終了後までの間にrepository状態が変化しています", nil)
	}
	return false, nil
}

// recordConvergenceRoundはreview round開始境界のrepo状態観測をround logへbest-effort
// 記録する。reviewer呼出・token・durationはtelemetryが正であるためここへ持たず、
// 表示側でCapturedAtの時間窓とWorkerPhaseで対応付ける。失敗はround log側のwarning
// だけとし、review flowへ影響させない。
func (w *Workflow) recordConvergenceRound(reviewNumber int, autoFixes int, workerPhase string, snap state.GitSnapshot) {
	record := state.RoundRecord{
		TaskID:       w.state.ReadOr("task.id", "unknown"),
		ReviewNumber: reviewNumber,
		AutoFixes:    autoFixes,
		WorkerPhase:  workerPhase,
		CapturedAt:   w.now().UTC(),
		Snapshot:     state.SnapshotDigest{Head: snap.Head, IndexDigest: snap.IndexDigest, WorktreeDigest: snap.WorktreeDigest},
	}
	record.Paths, record.CaptureError = w.classifyRoundPaths()
	_ = w.state.AppendRoundRecord(record)
}

// recordBaselineRoundはtask開始時(worker実行前)の境界recordを書き、round 1の差分
// 分類にtask開始前状態を与える。baseline観測はreview進行へ影響しないため、snapshot
// 取得失敗もCaptureErrorへ記録するだけとする。
func (w *Workflow) recordBaselineRound() {
	record := state.RoundRecord{
		TaskID:      w.state.ReadOr("task.id", "unknown"),
		WorkerPhase: state.RoundWorkerPhaseBaseline,
		CapturedAt:  w.now().UTC(),
	}
	snap, err := w.captureSnapshot(w.config.RepoRoot)
	if err != nil {
		record.CaptureError = boundedText(err.Error(), packet.MaxDiagnosticBytes)
	} else {
		record.Snapshot = state.SnapshotDigest{Head: snap.Head, IndexDigest: snap.IndexDigest, WorktreeDigest: snap.WorktreeDigest}
	}
	paths, classErr := w.classifyRoundPaths()
	if record.CaptureError == "" {
		record.CaptureError = classErr
	}
	record.Paths = paths
	_ = w.state.AppendRoundRecord(record)
}

// classifyRoundPathsはself-protectionと同じ変更対象集合をworktree観測へ変換する。
// 取得失敗はerror文字列へ返し、観測を部分続行しない(欠落pathの誤分類を防ぐ)。
func (w *Workflow) classifyRoundPaths() ([]state.RoundPathState, string) {
	baselineHead, _ := w.state.Read("baseline-head")
	paths, err := w.collectChangedPaths(w.config.RepoRoot, baselineHead)
	if err != nil {
		return nil, boundedText(err.Error(), packet.MaxDiagnosticBytes)
	}
	return state.ClassifyRoundPaths(w.config.RepoRoot, paths), ""
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
	w.pendingSnapshot = snapshotDiagnosticPtr(state.BuildSnapshotDiagnostic(state.SnapshotStageReviewStart, workerEnd, reviewStart, comparison, ""))
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
	w.pendingSnapshot = snapshotDiagnosticPtr(state.BuildSnapshotDiagnostic(state.SnapshotStageReviewResume, saved, current, comparison, ""))
	return false, nil
}

// verifyReviewEndSnapshotはreviewer呼出成功直後・結果採用前にもreview-start snapshotへ再照合する。
// reviewerはEdit/Write禁止でもBash等でrepositoryを変更でき、review-start時点では検出できないため。
func (w *Workflow) verifyReviewEndSnapshot() (bool, error) {
	saved, err := w.state.LoadReviewStartSnapshot()
	if err != nil {
		return true, w.failClosedSnapshot(state.SnapshotStageReviewEnd, state.GitSnapshot{}, state.GitSnapshot{}, "review-start snapshot読込失敗", err)
	}
	current, err := w.captureSnapshot(w.config.RepoRoot)
	if err != nil {
		return true, w.failClosedSnapshot(state.SnapshotStageReviewEnd, saved, state.GitSnapshot{}, "review-end snapshot取得失敗", err)
	}
	comparison := state.CompareGitSnapshot(saved, current, state.SnapshotStageReviewEnd, "")
	if err := w.state.SaveSnapshotComparison(comparison); err != nil {
		return true, w.failClosedSnapshot(state.SnapshotStageReviewEnd, saved, current, "snapshot comparison保存失敗", err)
	}
	if !comparison.Matched {
		return true, w.failClosedSnapshot(state.SnapshotStageReviewEnd, saved, current, "reviewer実行中にrepository状態が変化しています", nil)
	}
	return false, nil
}

// checkpointを先に消すことでstatus更新失敗時もresumeさせず安全方向へ収束させ、
// WaitingSolReview移行中に残存checkpointがresume復元情報と矛盾するのを防ぐ。
// 併せてsnapshot診断をtelemetry/statsへbest-effort記録する(本flowは止めない)。
func (w *Workflow) failClosedSnapshot(stage state.SnapshotStage, workerEnd, reviewStart state.GitSnapshot, reason string, cause error) error {
	w.recordSnapshotEvent(state.ReviewerRole, stage, workerEnd, reviewStart, reason, cause)
	return w.failClosedStopped(stage, reason, cause, snapshotFailClosedPacket)
}

// failClosedReportOnlySnapshotはreport-only worker前後の不変性確認失敗を同じ停止semantics
// (checkpoint清除・WaitingSolReview・診断記録)へ載せる。検出主体はreviewerではなくworkerの
// 前後invariantのため、event roleとpacket文言をreview-start/end検証と区別する。
func (w *Workflow) failClosedReportOnlySnapshot(stage state.SnapshotStage, start, current state.GitSnapshot, reason string, cause error) error {
	w.recordSnapshotEvent(state.WorkerRole, stage, start, current, reason, cause)
	return w.failClosedStopped(stage, reason, cause, reportOnlySnapshotFailClosedPacket)
}

func (w *Workflow) failClosedStopped(stage state.SnapshotStage, reason string, cause error, build func(state.SnapshotStage, string) packet.Packet) error {
	if err := w.state.ClearResumeCheckpoint(); err != nil {
		return err
	}
	if err := w.state.SetTaskStatus(state.TaskStatusWaitingSolReview); err != nil {
		return err
	}
	if cause != nil {
		reason = fmt.Sprintf("%s: %v", reason, cause)
	}
	return w.emitPacket(build(stage, reason))
}

// recordSnapshotEventはsnapshot同一性確認失敗をtelemetryへ記録する。比較結果に応じて
// Outcomeを切り替える: 両snapshot揃って不一致→snapshot_mismatch(mismatch軸を集計)、
// 揃ったが一致(保存失敗等でfail-closed)→snapshot_save_failed、一方が空(取得失敗等)で
// 比較未実施→snapshot_unavailable。token消費は持たない(best-effort)。
func (w *Workflow) recordSnapshotEvent(role state.SessionRole, stage state.SnapshotStage, previous, current state.GitSnapshot, reason string, cause error) {
	comparison := state.CompareGitSnapshot(previous, current, stage, "")
	diag := state.BuildSnapshotDiagnostic(stage, previous, current, comparison, reason)
	outcome := "snapshot_unavailable"
	switch {
	case diag.Matched != nil && !*diag.Matched:
		outcome = "snapshot_mismatch"
		w.state.RecordSnapshotMismatch(diag.MismatchAxis)
	case diag.Matched != nil && *diag.Matched:
		outcome = "snapshot_save_failed"
	}
	now := w.now().UTC()
	entry := state.ModelCallLog{
		TaskID:      w.state.ReadOr("task.id", "unknown"),
		CallType:    state.CallTypeEvent,
		StartedAt:   now,
		CompletedAt: now,
		Phase:       fmt.Sprintf("%s-snapshot-check", stage),
		Role:        role,
		Outcome:     outcome,
		Snapshot:    snapshotDiagnosticPtr(diag),
	}
	if cause != nil {
		entry.Error = boundedText(cause.Error(), packet.MaxDiagnosticBytes)
	}
	w.state.RecordModelCallLog(entry)
}

func snapshotDiagnosticPtr(diag state.SnapshotDiagnostic) *state.SnapshotDiagnostic {
	return &diag
}

func snapshotFailClosedPacket(stage state.SnapshotStage, reason string) packet.Packet {
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

// reportOnlySnapshotFailClosedPacketはreport-only PACKET再出力workerの前後同一性確認失敗時の
// Sol確認packet。reviewer自身のreview-start/end mutation検出とは主体が違い、report-only workerの
// 開始前snapshot基準であることをSolへ区別可能にする。
func reportOnlySnapshotFailClosedPacket(stage state.SnapshotStage, reason string) packet.Packet {
	return packet.FromLines([]string{
		"STATUS: NEEDS_SOL_REVIEW",
		"RISK: HIGH",
		fmt.Sprintf("SUMMARY: report-only PACKET再出力workerの開始前後でHEAD/index/worktree同一性を確認できず(%s)、通常reviewへ進めずSol確認へ昇格", stage),
		"REQUIREMENT_COVERAGE: report-only workerのrepo不変postconditionを機械強制できなかったためSolが直接確認する必要あり",
		"INVARIANTS: wrapperはreport-only worker開始前snapshotと終了後状態の3軸一致を確認するまで通常reviewへ進まない",
		"TEST_EVIDENCE: 開始前保存snapshotと終了後snapshotの比較で不一致または取得失敗を検出",
		fmt.Sprintf("ISSUES: %s", reason),
		"RESIDUAL_RISK: report-only workerがrepositoryを変更した可能性とその意図を排除できなかった",
		"TARGETS: repository HEAD/index/worktreeの現在状態とreport-only開始前snapshot・telemetry記録",
		"ARTIFACTS: none",
		"SOL_QUESTION: report-only workerによる変更の意図有無と追跡・修正方針をSolが判断する",
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
