// Package appはCLI引数解析・実行調停・プロセス間ロック・コマンド出力を担う。
package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
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
	// StdinBytesはdecision/fix payloadをstdinから読み取るbyte数。
	// 0は従来のargv payload modeを意味する。
	StdinBytes int64
	// SHA256はstdin payloadの送信元が計算した期待値。空なら照合しない。
	SHA256 string
	Verify VerifyArgs
}

type VerifyArgs struct {
	Key      string
	RFC3339  string
	ThreadID string
}

func ParseCommand(args []string) (Command, error) {
	if len(args) == 0 {
		return Command{}, fmt.Errorf("usage: glm-worker <instruction> | --decision <decision> | --decision-stdin <payload-bytes> [--sha256 <hex>] | --fix <instruction> | --fix-stdin <payload-bytes> [--sha256 <hex>] | --resume | --status | --watch | --timeline [task-id] | --convergence [task-id] | --stats | --reset | --eval-ab <run-dir>")
	}

	switch args[0] {
	case "--decision":
		return payloadCommand(ModeDecision, args, "usage: glm-worker --decision <decision>")
	case "--decision-stdin":
		return stdinPayloadCommand(ModeDecision, args, "usage: glm-worker --decision-stdin <payload-bytes> [--sha256 <hex>]")
	case "--fix":
		return payloadCommand(ModeFix, args, "usage: glm-worker --fix <instruction>")
	case "--fix-stdin":
		return stdinPayloadCommand(ModeFix, args, "usage: glm-worker --fix-stdin <payload-bytes> [--sha256 <hex>]")
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

func stdinPayloadCommand(mode CommandMode, args []string, usage string) (Command, error) {
	if len(args) != 2 && len(args) != 4 {
		return Command{}, fmt.Errorf("%s", usage)
	}

	payloadBytes, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || payloadBytes <= 0 {
		return Command{}, fmt.Errorf("%s", usage)
	}

	command := Command{Mode: mode, StdinBytes: payloadBytes}
	if len(args) == 4 {
		if args[2] != "--sha256" {
			return Command{}, fmt.Errorf("%s", usage)
		}
		digest, err := parsePayloadSHA256(args[3])
		if err != nil {
			return Command{}, fmt.Errorf("%s", usage)
		}
		command.SHA256 = digest
	}
	return command, nil
}

func parsePayloadSHA256(value string) (string, error) {
	if len(value) != 64 {
		return "", fmt.Errorf("sha256 must be 64 hex characters")
	}
	for _, c := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return "", fmt.Errorf("sha256 must be 64 hex characters")
		}
	}
	return strings.ToLower(value), nil
}

// readStdinPayloadは宣言byte数だけstdinから読み取り、期待sha256と照合する。
// 呼び出し元のexec sessionがstdin pipeを開いたまま保つため、EOFまで読む過剰byte検知は行えない。
func readStdinPayload(in io.Reader, want int64, expectedSHA string) (string, error) {
	var buf bytes.Buffer
	written, err := io.CopyN(&buf, in, want)
	if err != nil {
		return "", fmt.Errorf("stdin payload read failed after %d of %d bytes: %w", written, want, err)
	}

	payload := buf.Bytes()
	if expectedSHA != "" {
		sum := sha256.Sum256(payload)
		actual := hex.EncodeToString(sum[:])
		if !strings.EqualFold(actual, expectedSHA) {
			return "", fmt.Errorf("stdin payload sha256 mismatch: expected %s, got %s", expectedSHA, actual)
		}
	}
	return string(payload), nil
}

// テストでModelRunnerを差し替えるためのfactory。
type RunnerFactory func(cfg config.AppConfig, st *state.StateStore) workflow.ModelRunner

func defaultRunnerFactory(cfg config.AppConfig, st *state.StateStore) workflow.ModelRunner {
	return runner.NewClaudeRunner(cfg, st)
}

func Run(args []string) error {
	return run(args, config.Load, defaultRunnerFactory, os.Stdin, os.Stdout, os.Stderr)
}

func run(
	args []string,
	loadConfig func() (config.AppConfig, error),
	runnerFactory RunnerFactory,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	cmd, err := ParseCommand(args)
	if err != nil {
		return err
	}
	if cmd.StdinBytes > 0 {
		payload, err := readStdinPayload(stdin, cmd.StdinBytes, cmd.SHA256)
		if err != nil {
			return err
		}
		cmd.Payload = payload
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	return Execute(cmd, cfg, runnerFactory, stdout, stderr)
}

// Executeはcmdをcfg配下で実行する。runner/workflowはrf経由で注入可能で、
// --watch・--timeline・--convergence・--eval-abはstateへ書き込まないread-only参照、
// --status/--statsはロック取得前に、それ以外はプロセス間ロック後に処理する。
// stdin payload modeの読み取り・照合はrun()がstate初期化前に完了しており、
// 不足・不一致時はここへ到達しない。
func Execute(cmd Command, cfg config.AppConfig, rf RunnerFactory, stdout, stderr io.Writer) error {
	if cmd.StdinBytes > 0 && cmd.Payload == "" {
		return fmt.Errorf("stdin payload mode requires the payload to be read before execute")
	}
	if cmd.Mode == ModeWatch {
		return printWatch(state.AttachStateStore(cfg), stdout, defaultWatchFollowInterval, nil)
	}
	if cmd.Mode == ModeTimeline {
		return printTimeline(state.AttachStateStore(cfg), cmd.Payload, stdout)
	}
	if cmd.Mode == ModeConvergence {
		return printConvergence(state.AttachStateStore(cfg), cmd.Payload, stdout)
	}
	if cmd.Mode == ModeEvalAB {
		return printEvalAB(state.AttachStateStore(cfg), cmd.Payload, stdout)
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
