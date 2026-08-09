package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	maxPacketLines     = 15
	maxPacketBytes     = 6 * 1024
	maxPacketLineBytes = 1536
)

type packetConstraintError struct {
	reason string
}

func (e *packetConstraintError) Error() string {
	return e.reason
}

func isPacketConstraintError(err error) bool {
	var target *packetConstraintError
	return errors.As(err, &target)
}

type packet struct {
	Lines  []string
	Fields map[string]string
}

func parseLastPacket(path string) (packet, error) {
	file, err := os.Open(path)
	if err != nil {
		return packet{}, err
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
		return packet{}, err
	}
	if len(last) == 0 {
		return packet{}, &packetConstraintError{reason: "PACKET_BEGIN/PACKET_ENDで囲まれた出力がありません"}
	}

	fields := make(map[string]string)
	for _, line := range last {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if _, exists := fields[key]; exists {
			continue
		}
		fields[key] = strings.TrimSpace(value)
	}

	result := packet{Lines: last, Fields: fields}
	if err := validatePacket(result); err != nil {
		return packet{}, err
	}
	return result, nil
}

func validatePacket(value packet) error {
	lineCount := len(value.Lines) + 2
	if lineCount > maxPacketLines {
		return &packetConstraintError{reason: fmt.Sprintf("packetはPACKET_BEGIN/PACKET_ENDを含め%d行以内にしてください: %d行", maxPacketLines, lineCount)}
	}

	if size := value.ByteSize(); size > maxPacketBytes {
		return &packetConstraintError{reason: fmt.Sprintf("packetは%d bytes以内にしてください: %d bytes", maxPacketBytes, size)}
	}

	for _, line := range value.Lines {
		if len(line) > maxPacketLineBytes {
			return &packetConstraintError{reason: fmt.Sprintf("packetの1行は%d bytes以内にしてください", maxPacketLineBytes)}
		}
	}

	status := value.Status()
	required, ok := requiredPacketFields[status]
	if !ok {
		return &packetConstraintError{reason: fmt.Sprintf("未対応のpacket STATUSです: %q", status)}
	}
	if risk := value.Risk(); risk != "LOW" && risk != "HIGH" {
		return &packetConstraintError{reason: fmt.Sprintf("packet RISKはLOWまたはHIGHで指定してください: %q", risk)}
	}

	for _, field := range required {
		if strings.TrimSpace(value.Fields[field]) == "" {
			return &packetConstraintError{reason: fmt.Sprintf("packetに必須field %sがありません", field)}
		}
	}
	return nil
}

var requiredPacketFields = map[string][]string{
	"NEEDS_SOL_DECISION": {"STATUS", "RISK", "DECISION", "EVIDENCE", "OPTIONS", "RECOMMENDATION", "TARGETS"},
	"IMPLEMENTED":        {"STATUS", "RISK", "SUMMARY", "REQUIREMENT_COVERAGE", "TESTS", "UNVERIFIED"},
	"PASS":               {"STATUS", "RISK", "SUMMARY", "REQUIREMENT_COVERAGE", "TEST_EVIDENCE", "ISSUES", "RESIDUAL_RISK", "TARGETS"},
	"FIX_REQUIRED":       {"STATUS", "RISK", "SUMMARY", "REQUIREMENT_COVERAGE", "TEST_EVIDENCE", "ISSUES", "RESIDUAL_RISK", "TARGETS"},
	"NEEDS_SOL_REVIEW":   {"STATUS", "RISK", "SUMMARY", "REQUIREMENT_COVERAGE", "TEST_EVIDENCE", "ISSUES", "RESIDUAL_RISK", "TARGETS", "SOL_QUESTION"},
}

func (p packet) Status() string {
	return p.Fields["STATUS"]
}

func (p packet) Risk() string {
	return p.Fields["RISK"]
}

func (p packet) String() string {
	return strings.Join(p.Lines, "\n")
}

func (p packet) ByteSize() int {
	return len("PACKET_BEGIN\n") + len(p.String()) + len("\nPACKET_END")
}

func printPacket(value packet) {
	fmt.Println(value.String())
}

func tailLines(path string, count int) string {
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
