// Package packetはClaude Code出力ログからPACKETを抽出・検証する。
package packet

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// PACKET出力契約。テストや呼び出し側もこの上限に基づいて検証する。
const (
	MaxPacketLines     = 15
	MaxPacketBytes     = 6 * 1024
	MaxPacketLineBytes = 1536
)

// constraintErrorはPACKETの行数・サイズ・必須fieldなどの契約違反を表す。
type constraintError struct {
	reason string
}

func (e *constraintError) Error() string {
	return e.reason
}

// IsConstraintErrorはerrがPACKET契約違反によるものかを返す。
func IsConstraintError(err error) bool {
	var target *constraintError
	return errors.As(err, &target)
}

// PacketはPACKET_BEGIN/PACKET_ENDで囲まれた出力を表す。
type Packet struct {
	Lines  []string
	Fields map[string]string
}

// FromLinesは行集合からfield mapを構築したPacketを返す。
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

	status := value.Status()
	required, ok := requiredFields[status]
	if !ok {
		return &constraintError{reason: fmt.Sprintf("未対応のpacket STATUSです: %q", status)}
	}
	if risk := value.Risk(); risk != "LOW" && risk != "HIGH" {
		return &constraintError{reason: fmt.Sprintf("packet RISKはLOWまたはHIGHで指定してください: %q", risk)}
	}

	for _, field := range required {
		if strings.TrimSpace(value.Fields[field]) == "" {
			return &constraintError{reason: fmt.Sprintf("packetに必須field %sがありません", field)}
		}
	}
	return nil
}

var requiredFields = map[string][]string{
	"NEEDS_SOL_DECISION": {"STATUS", "RISK", "DECISION", "EVIDENCE", "OPTIONS", "RECOMMENDATION", "TARGETS"},
	"IMPLEMENTED":        {"STATUS", "RISK", "SUMMARY", "REQUIREMENT_COVERAGE", "TESTS", "UNVERIFIED"},
	"PASS":               {"STATUS", "RISK", "SUMMARY", "REQUIREMENT_COVERAGE", "TEST_EVIDENCE", "ISSUES", "RESIDUAL_RISK", "TARGETS"},
	"FIX_REQUIRED":       {"STATUS", "RISK", "SUMMARY", "REQUIREMENT_COVERAGE", "TEST_EVIDENCE", "ISSUES", "RESIDUAL_RISK", "TARGETS"},
	"NEEDS_SOL_REVIEW":   {"STATUS", "RISK", "SUMMARY", "REQUIREMENT_COVERAGE", "TEST_EVIDENCE", "ISSUES", "RESIDUAL_RISK", "TARGETS", "SOL_QUESTION"},
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

	return strings.Join(lines, "\n")
}
