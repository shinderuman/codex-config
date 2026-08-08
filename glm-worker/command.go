package main

import (
	"fmt"
	"strings"
)

type commandMode int

const (
	modeNewTask commandMode = iota
	modeDecision
	modeFix
	modeStatus
	modeReset
)

type command struct {
	Mode    commandMode
	Payload string
}

func parseCommand(args []string) (command, error) {
	if len(args) == 0 {
		return command{}, fmt.Errorf("usage: glm-worker <instruction> | --decision <decision> | --fix <instruction> | --status | --reset")
	}

	switch args[0] {
	case "--decision":
		return payloadCommand(modeDecision, args, "usage: glm-worker --decision <decision>")
	case "--fix":
		return payloadCommand(modeFix, args, "usage: glm-worker --fix <instruction>")
	case "--status":
		if len(args) != 1 {
			return command{}, fmt.Errorf("usage: glm-worker --status")
		}
		return command{Mode: modeStatus}, nil
	case "--reset":
		if len(args) != 1 {
			return command{}, fmt.Errorf("usage: glm-worker --reset")
		}
		return command{Mode: modeReset}, nil
	default:
		return command{Mode: modeNewTask, Payload: strings.Join(args, " ")}, nil
	}
}

func payloadCommand(mode commandMode, args []string, usage string) (command, error) {
	if len(args) < 2 {
		return command{}, fmt.Errorf("%s", usage)
	}

	payload := strings.TrimSpace(strings.Join(args[1:], " "))
	if payload == "" {
		return command{}, fmt.Errorf("%s", usage)
	}

	return command{Mode: mode, Payload: payload}, nil
}
