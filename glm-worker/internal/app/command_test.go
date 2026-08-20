package app

import (
	"strings"
	"testing"
)

func TestParseCommandModes(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		mode    CommandMode
		payload string
	}{
		{name: "new task", args: []string{"調査して", "実装する"}, mode: ModeNewTask, payload: "調査して 実装する"},
		{name: "decision", args: []string{"--decision", "A案で", "進める"}, mode: ModeDecision, payload: "A案で 進める"},
		{name: "fix", args: []string{"--fix", "指摘を修正"}, mode: ModeFix, payload: "指摘を修正"},
		{name: "resume", args: []string{"--resume"}, mode: ModeResume},
		{name: "status", args: []string{"--status"}, mode: ModeStatus},
		{name: "stats", args: []string{"--stats"}, mode: ModeStats},
		{name: "reset", args: []string{"--reset"}, mode: ModeReset},
		{
			name: "verify-auto-resume",
			args: []string{"--verify-auto-resume", "key-1234", "2026-08-12T20:01:20+09:00", "thread-uuid"},
			mode: ModeVerifyAutoResume,
		},
		{name: "eval-ab", args: []string{"--eval-ab", "/tmp/ab-run"}, mode: ModeEvalAB, payload: "/tmp/ab-run"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, err := ParseCommand(test.args)
			if err != nil {
				t.Fatal(err)
			}
			if command.Mode != test.mode || command.Payload != test.payload {
				t.Fatalf("command = %#v", command)
			}
		})
	}
}

func TestParseCommandStdinPayloadModes(t *testing.T) {
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	tests := []struct {
		name        string
		args        []string
		mode        CommandMode
		stdinBytes  int64
		expectedSHA string
	}{
		{name: "decision stdin", args: []string{"--decision-stdin", "3850"}, mode: ModeDecision, stdinBytes: 3850},
		{name: "fix stdin", args: []string{"--fix-stdin", "2507"}, mode: ModeFix, stdinBytes: 2507},
		{
			name:        "decision stdin with sha256",
			args:        []string{"--decision-stdin", "100", "--sha256", strings.ToUpper(digest)},
			mode:        ModeDecision,
			stdinBytes:  100,
			expectedSHA: digest,
		},
		{
			name:        "fix stdin with sha256",
			args:        []string{"--fix-stdin", "1", "--sha256", digest},
			mode:        ModeFix,
			stdinBytes:  1,
			expectedSHA: digest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, err := ParseCommand(test.args)
			if err != nil {
				t.Fatal(err)
			}
			if command.Mode != test.mode || command.StdinBytes != test.stdinBytes || command.Payload != "" || command.SHA256 != test.expectedSHA {
				t.Fatalf("command = %#v", command)
			}
		})
	}
}

func TestParseCommandRejectsInvalidStdinArguments(t *testing.T) {
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tests := [][]string{
		{"--decision-stdin"},
		{"--decision-stdin", "abc"},
		{"--decision-stdin", "0"},
		{"--decision-stdin", "-1"},
		{"--decision-stdin", "10", "extra"},
		{"--decision-stdin", "10", "--sha256"},
		{"--decision-stdin", "10", "--sha256", "short"},
		{"--decision-stdin", "10", "--sha256", digest[:63] + "g"},
		{"--decision-stdin", "10", "--sha256", digest, "extra"},
		{"--decision-stdin", "10", "--decision", "payload"},
		{"--fix-stdin"},
		{"--fix-stdin", "10", "--checksum", digest},
	}

	for _, args := range tests {
		if _, err := ParseCommand(args); err == nil {
			t.Fatalf("invalid argsを受理しました: %#v", args)
		}
	}
}

func TestParseCommandRejectsInvalidArguments(t *testing.T) {
	tests := [][]string{
		nil,
		{"--decision"},
		{"--decision", "   "},
		{"--fix"},
		{"--resume", "extra"},
		{"--status", "extra"},
		{"--stats", "extra"},
		{"--reset", "extra"},
		{"--verify-auto-resume"},
		{"--verify-auto-resume", "key"},
		{"--verify-auto-resume", "key", "date"},
		{"--verify-auto-resume", "key", "date", "thread", "extra"},
		{"--eval-ab"},
		{"--eval-ab", "dir", "extra"},
	}

	for _, args := range tests {
		if _, err := ParseCommand(args); err == nil {
			t.Fatalf("invalid argsを受理しました: %#v", args)
		}
	}
}

func TestParseCommandVerifyAutoResumeArgs(t *testing.T) {
	command, err := ParseCommand([]string{
		"--verify-auto-resume",
		"glm-worker-resume-abcd1234-ef012345",
		"2026-08-12T20:01:20+09:00",
		"019f88f8-0e70-7d53-a2a3-f0c61666827c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if command.Mode != ModeVerifyAutoResume {
		t.Fatalf("Mode = %d", command.Mode)
	}
	if command.Verify.Key != "glm-worker-resume-abcd1234-ef012345" {
		t.Fatalf("Key = %q", command.Verify.Key)
	}
	if command.Verify.RFC3339 != "2026-08-12T20:01:20+09:00" {
		t.Fatalf("RFC3339 = %q", command.Verify.RFC3339)
	}
	if command.Verify.ThreadID != "019f88f8-0e70-7d53-a2a3-f0c61666827c" {
		t.Fatalf("ThreadID = %q", command.Verify.ThreadID)
	}
}
