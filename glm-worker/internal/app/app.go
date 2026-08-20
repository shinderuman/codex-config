// Package appはCLI引数解析・実行調停・プロセス間ロック・コマンド出力を担う。
package app

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/runner"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/workflow"
)

type CommandMode int

const (
	ModeNewTask CommandMode = iota
	ModeDecision
	ModeFix
	ModeResume
	ModeStatus
	ModeWatch
	ModeTimeline
	ModeConvergence
	ModeStats
	ModeReset
	ModeVerifyAutoResume
	ModeEvalAB
)

type Command struct {
	Mode    CommandMode
	Payload string
	Verify  VerifyArgs
}

type VerifyArgs struct {
	Key      string
	RFC3339  string
	ThreadID string
}

func ParseCommand(args []string) (Command, error) {
	if len(args) == 0 {
		return Command{}, fmt.Errorf("usage: glm-worker <instruction> | --decision <decision> | --fix <instruction> | --resume | --status | --watch | --timeline [task-id] | --convergence [task-id] | --stats | --reset | --eval-ab <run-dir>")
	}

	switch args[0] {
	case "--decision":
		return payloadCommand(ModeDecision, args, "usage: glm-worker --decision <decision>")
	case "--fix":
		return payloadCommand(ModeFix, args, "usage: glm-worker --fix <instruction>")
	case "--resume":
		if len(args) != 1 {
			return Command{}, fmt.Errorf("usage: glm-worker --resume")
		}
		return Command{Mode: ModeResume}, nil
	case "--status":
		if len(args) != 1 {
			return Command{}, fmt.Errorf("usage: glm-worker --status")
		}
		return Command{Mode: ModeStatus}, nil
	case "--watch":
		if len(args) != 1 {
			return Command{}, fmt.Errorf("usage: glm-worker --watch")
		}
		return Command{Mode: ModeWatch}, nil
	case "--timeline":
		if len(args) > 2 {
			return Command{}, fmt.Errorf("usage: glm-worker --timeline [task-id]")
		}
		if len(args) == 2 {
			return Command{Mode: ModeTimeline, Payload: args[1]}, nil
		}
		return Command{Mode: ModeTimeline}, nil
	case "--convergence":
		if len(args) > 2 {
			return Command{}, fmt.Errorf("usage: glm-worker --convergence [task-id]")
		}
		if len(args) == 2 {
			return Command{Mode: ModeConvergence, Payload: args[1]}, nil
		}
		return Command{Mode: ModeConvergence}, nil
	case "--stats":
		if len(args) != 1 {
			return Command{}, fmt.Errorf("usage: glm-worker --stats")
		}
		return Command{Mode: ModeStats}, nil
	case "--reset":
		if len(args) != 1 {
			return Command{}, fmt.Errorf("usage: glm-worker --reset")
		}
		return Command{Mode: ModeReset}, nil
	case "--verify-auto-resume":
		if len(args) != 4 {
			return Command{}, fmt.Errorf("usage: glm-worker --verify-auto-resume <automation-key> <auto-resume-at-rfc3339> <thread-id>")
		}
		return Command{
			Mode: ModeVerifyAutoResume,
			Verify: VerifyArgs{
				Key:      args[1],
				RFC3339:  args[2],
				ThreadID: args[3],
			},
		}, nil
	case "--eval-ab":
		if len(args) != 2 {
			return Command{}, fmt.Errorf("usage: glm-worker --eval-ab <run-dir>")
		}
		return Command{Mode: ModeEvalAB, Payload: args[1]}, nil
	default:
		return Command{Mode: ModeNewTask, Payload: strings.Join(args, " ")}, nil
	}
}

func payloadCommand(mode CommandMode, args []string, usage string) (Command, error) {
	if len(args) < 2 {
		return Command{}, fmt.Errorf("%s", usage)
	}

	payload := strings.TrimSpace(strings.Join(args[1:], " "))
	if payload == "" {
		return Command{}, fmt.Errorf("%s", usage)
	}

	return Command{Mode: mode, Payload: payload}, nil
}

// テストでModelRunnerを差し替えるためのfactory。
type RunnerFactory func(cfg config.AppConfig, st *state.StateStore) workflow.ModelRunner

func defaultRunnerFactory(cfg config.AppConfig, st *state.StateStore) workflow.ModelRunner {
	return runner.NewClaudeRunner(cfg, st)
}

func Run(args []string) error {
	return run(args, config.Load, defaultRunnerFactory, os.Stdout, os.Stderr)
}

func run(
	args []string,
	loadConfig func() (config.AppConfig, error),
	runnerFactory RunnerFactory,
	stdout io.Writer,
	stderr io.Writer,
) error {
	cmd, err := ParseCommand(args)
	if err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	return Execute(cmd, cfg, runnerFactory, stdout, stderr)
}

// Executeはcmdをcfg配下で実行する。runner/workflowはrf経由で注入可能で、
// --watch・--timeline・--convergenceはstateへ書き込まないread-only参照、--status/--statsはロック取得前に、
// それ以外はプロセス間ロック後に処理する。
func Execute(cmd Command, cfg config.AppConfig, rf RunnerFactory, stdout, stderr io.Writer) error {
	if cmd.Mode == ModeWatch {
		return printWatch(state.AttachStateStore(cfg), stdout, defaultWatchFollowInterval, nil)
	}
	if cmd.Mode == ModeTimeline {
		return printTimeline(state.AttachStateStore(cfg), cmd.Payload, stdout)
	}
	if cmd.Mode == ModeConvergence {
		return printConvergence(state.AttachStateStore(cfg), cmd.Payload, stdout)
	}

	st, err := state.NewStateStore(cfg)
	if err != nil {
		return err
	}

	switch cmd.Mode {
	case ModeStatus:
		return printStatus(st, stdout)
	case ModeStats:
		return printStats(st, stdout)
	case ModeVerifyAutoResume:
		return printVerifyAutoResume(cmd, cfg, stdout)
	case ModeEvalAB:
		return printEvalAB(st, cmd.Payload, stdout)
	}

	lock, err := AcquireRepoLock(st.LockPath())
	if err != nil {
		return err
	}
	defer lock.Close()

	if cmd.Mode == ModeReset {
		return resetState(st, stdout)
	}

	r := rf(cfg, st)
	wf := workflow.NewWorkflow(cfg, st, r, stdout)

	switch cmd.Mode {
	case ModeNewTask:
		return wf.ExecuteNewTask(cmd.Payload)
	case ModeDecision:
		return wf.ExecuteDecision(cmd.Payload)
	case ModeFix:
		return wf.ExecuteExplicitFix(cmd.Payload)
	case ModeResume:
		return wf.ExecuteResume()
	default:
		return fmt.Errorf("unsupported command mode")
	}
}
