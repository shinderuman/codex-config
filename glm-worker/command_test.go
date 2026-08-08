package main

import "testing"

func TestParseCommandDecision(t *testing.T) {
	command, err := parseCommand([]string{"--decision", "A案で", "進める"})
	if err != nil {
		t.Fatal(err)
	}
	if command.Mode != modeDecision || command.Payload != "A案で 進める" {
		t.Fatalf("unexpected command: %#v", command)
	}
}

func TestParseCommandNewTask(t *testing.T) {
	command, err := parseCommand([]string{"調査して", "実装する"})
	if err != nil {
		t.Fatal(err)
	}
	if command.Mode != modeNewTask || command.Payload != "調査して 実装する" {
		t.Fatalf("unexpected command: %#v", command)
	}
}
