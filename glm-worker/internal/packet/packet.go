// Package packetはClaude Code出力ログからPACKETを抽出・検証する。
package packet

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// PACKET出力契約。テストや呼び出し側もこの上限に基づいて検証する。
const (
	MaxPacketLines     = 15
	MaxPacketBytes     = 6 * 1024
	MaxPacketLineBytes = 1536
	MaxDiagnosticBytes = 6 * 1024
)

type constraintError struct {
	reason string
}

func (e *constraintError) Error() string {
	return e.reason
}

func IsConstraintError(err error) bool {
	var target *constraintError
	return errors.As(err, &target)
}

// PacketはPACKET_BEGIN/PACKET_ENDで囲まれた出力を表す。
type Packet struct {
	Lines  []string
	Fields map[string]string
}

func FromLines(lines []string) Packet {
	fields := make(map[string]string)
	for _, line := range lines {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if _, exists := fields[key]; exists {
			continue
		}
		fields[key] = value
	}

	return Packet{
		Lines:  append([]string(nil), lines...),
		Fields: fields,
	}
}

// ParseLastはpathから最後の完成PACKETを抽出し検証する。
func ParseLast(path string) (Packet, error) {
	file, err := os.Open(path)
	if err != nil {
		return Packet{}, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)

	inPacket := false
	current := make([]string, 0)
	var last []string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch line {
		case "PACKET_BEGIN":
			inPacket = true
			current = current[:0]
		case "PACKET_END":
			if inPacket {
				last = append([]string(nil), current...)
				inPacket = false
			}
		default:
			if inPacket {
				current = append(current, scanner.Text())
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Packet{}, err
	}
	if len(last) == 0 {
		return Packet{}, &constraintError{reason: "PACKET_BEGIN/PACKET_ENDで囲まれた出力がありません"}
	}

	result := FromLines(last)
	if err := Validate(result); err != nil {
		return Packet{}, err
	}
	return result, nil
}

// ValidateはPACKETの行数・サイズ・必須fieldの契約を検証する。
func Validate(value Packet) error {
	lineCount := len(value.Lines) + 2
	if lineCount > MaxPacketLines {
		return &constraintError{reason: fmt.Sprintf("packetはPACKET_BEGIN/PACKET_ENDを含め%d行以内にしてください: %d行", MaxPacketLines, lineCount)}
	}

	if size := value.ByteSize(); size > MaxPacketBytes {
		return &constraintError{reason: fmt.Sprintf("packetは%d bytes以内にしてください: %d bytes", MaxPacketBytes, size)}
	}

	for _, line := range value.Lines {
		if len(line) > MaxPacketLineBytes {
			return &constraintError{reason: fmt.Sprintf("packetの1行は%d bytes以内にしてください", MaxPacketLineBytes)}
		}
	}

	seen := make(map[string]struct{}, len(value.Lines))
	for _, line := range value.Lines {
		key, _, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return &constraintError{reason: "packetの各行はKEY: value形式にしてください"}
		}
		if _, exists := seen[key]; exists {
			return &constraintError{reason: fmt.Sprintf("packet field %sが重複しています", key)}
		}
		seen[key] = struct{}{}
	}

	status := value.Status()
	required, ok := requiredFields[status]
	if !ok {
		return &constraintError{reason: fmt.Sprintf("未対応のpacket STATUSです: %q", status)}
	}
	if risk := value.Risk(); risk != "LOW" && risk != "HIGH" {
		return &constraintError{reason: fmt.Sprintf("packet RISKはLOWまたはHIGHで指定してください: %q", risk)}
	}
	if (status == "NEEDS_SOL_DECISION" || status == "NEEDS_SOL_REVIEW") && value.Risk() != "HIGH" {
		return &constraintError{reason: fmt.Sprintf("%sのRISKはHIGHにしてください", status)}
	}
	if status == "PASS" && value.Risk() != "LOW" {
		return &constraintError{reason: "PASSのRISKはLOWにしてください。高リスクならNEEDS_SOL_REVIEWを返してください"}
	}

	for _, field := range required {
		if strings.TrimSpace(value.Fields[field]) == "" {
			return &constraintError{reason: fmt.Sprintf("packetに必須field %sがありません", field)}
		}
	}
	return nil
}

var requiredFields = map[string][]string{
	"NEEDS_SOL_DECISION": {"STATUS", "RISK", "DECISION", "EVIDENCE", "OPTIONS", "RECOMMENDATION", "TEST_OBLIGATIONS", "TARGETS", "ARTIFACTS"},
	"IMPLEMENTED":        {"STATUS", "RISK", "SUMMARY", "REQUIREMENT_COVERAGE", "TESTS", "UNVERIFIED", "ARTIFACTS"},
	"PASS":               {"STATUS", "RISK", "SUMMARY", "REQUIREMENT_COVERAGE", "INVARIANTS", "TEST_EVIDENCE", "ISSUES", "RESIDUAL_RISK", "TARGETS", "ARTIFACTS"},
	"FIX_REQUIRED":       {"STATUS", "RISK", "SUMMARY", "REQUIREMENT_COVERAGE", "INVARIANTS", "TEST_EVIDENCE", "ISSUES", "RESIDUAL_RISK", "TARGETS", "ARTIFACTS"},
	"NEEDS_SOL_REVIEW":   {"STATUS", "RISK", "SUMMARY", "REQUIREMENT_COVERAGE", "INVARIANTS", "TEST_EVIDENCE", "ISSUES", "RESIDUAL_RISK", "TARGETS", "ARTIFACTS", "SOL_QUESTION"},
}

func (p Packet) Status() string {
	return p.Fields["STATUS"]
}

func (p Packet) Risk() string {
	return p.Fields["RISK"]
}

func (p Packet) String() string {
	return strings.Join(p.Lines, "\n")
}

// ByteSizeはPACKET_BEGIN/ENDを含む全体のbyte数を返す。
func (p Packet) ByteSize() int {
	return len("PACKET_BEGIN\n") + len(p.String()) + len("\nPACKET_END")
}

// Tailはpathの末尾count行を返す。読めない場合は空文字列。
func Tail(path string, count int) string {
	if count <= 0 {
		return ""
	}

	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	lines := make([]string, 0, count)

	for scanner.Scan() {
		if len(lines) == count {
			copy(lines, lines[1:])
			lines[count-1] = scanner.Text()
			continue
		}
		lines = append(lines, scanner.Text())
	}

	result := strings.Join(lines, "\n")
	if len(result) <= MaxDiagnosticBytes {
		return result
	}
	prefix := "[前方を省略]\n"
	start := len(result) - (MaxDiagnosticBytes - len(prefix))
	for start < len(result) && !utf8.RuneStart(result[start]) {
		start++
	}
	return prefix + result[start:]
}
