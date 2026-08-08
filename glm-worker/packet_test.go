package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLastPacketUsesLastCompletePacket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	content := `noise
PACKET_BEGIN
STATUS: PASS
RISK: LOW
PACKET_END
more noise
PACKET_BEGIN
STATUS: NEEDS_SOL_REVIEW
RISK: HIGH
TARGETS: foo.go:Run
PACKET_END
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	packet, err := parseLastPacket(path)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Status() != "NEEDS_SOL_REVIEW" {
		t.Fatalf("status = %q", packet.Status())
	}
	if packet.Risk() != "HIGH" {
		t.Fatalf("risk = %q", packet.Risk())
	}
}

func TestParseLastPacketRejectsMissingPacket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	if err := os.WriteFile(path, []byte("STATUS: PASS\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := parseLastPacket(path); err == nil {
		t.Fatal("expected error")
	}
}
