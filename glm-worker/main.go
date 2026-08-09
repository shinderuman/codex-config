package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	command, err := parseCommand(args)
	if err != nil {
		return err
	}

	config, err := loadConfig()
	if err != nil {
		return err
	}

	state, err := newStateStore(config)
	if err != nil {
		return err
	}

	if command.Mode == modeStatus {
		return printStatus(state)
	}
	if command.Mode == modeStats {
		return printStats(state)
	}

	lock, err := acquireRepoLock(state.LockPath())
	if err != nil {
		return err
	}
	defer lock.Close()

	if command.Mode == modeReset {
		return resetState(state)
	}

	runner := newClaudeRunner(config, state)
	workflow := newWorkflow(config, state, runner)

	return workflow.Execute(command)
}
