package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLastPacketUsesLastCompletePacket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	content := `noise
PACKET_BEGIN
STATUS: IMPLEMENTED
RISK: LOW
SUMMARY: first
REQUIREMENT_COVERAGE: covered
TESTS: pass
UNVERIFIED: none
PACKET_END
more noise
PACKET_BEGIN
STATUS: NEEDS_SOL_REVIEW
RISK: HIGH
SUMMARY: review
REQUIREMENT_COVERAGE: covered
TEST_EVIDENCE: pass
ISSUES: none
RESIDUAL_RISK: review required
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

func TestParseLastPacketRejectsOversizedPacket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	content := `PACKET_BEGIN
STATUS: IMPLEMENTED
RISK: LOW
SUMMARY: ` + strings.Repeat("x", maxPacketLineBytes+1) + `
REQUIREMENT_COVERAGE: covered
TESTS: pass
UNVERIFIED: none
PACKET_END
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := parseLastPacket(path)
	if err == nil || !isPacketConstraintError(err) {
		t.Fatalf("packet constraint errorを期待しました: %v", err)
	}
}

func TestParseLastPacketRejectsMissingRequiredField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	content := `PACKET_BEGIN
STATUS: IMPLEMENTED
RISK: LOW
SUMMARY: implemented
TESTS: pass
UNVERIFIED: none
PACKET_END
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := parseLastPacket(path); err == nil || !isPacketConstraintError(err) {
		t.Fatalf("必須field欠落をpacket制約違反として拒否する必要があります: %v", err)
	}
}

func TestParseLastPacketRejectsMissingPacket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	if err := os.WriteFile(path, []byte("STATUS: PASS\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := parseLastPacket(path); err == nil || !isPacketConstraintError(err) {
		t.Fatalf("packet欠落をpacket制約違反として拒否する必要があります: %v", err)
	}
}
