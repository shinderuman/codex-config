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

// RejectCategoryはpacket検証不合格のerrorを集計用の安定categoryへ分類する。
// 理由文字列のphrasingに依存するが、これらはParse/Validate/ValidateArtifacts内で固定済み。
func RejectCategory(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "ARTIFACTS") || strings.Contains(msg, "artifact"):
		return "artifacts"
	case strings.Contains(msg, "複数検出"):
		return "multiple-packets"
	case strings.Contains(msg, "非空の本文"):
		return "stray-body"
	case strings.Contains(msg, "入れ子"):
		return "nested-marker"
	case strings.Contains(msg, "対応するPACKET_BEGINがない"):
		return "stray-marker"
	case strings.Contains(msg, "閉じられていません"):
		return "unclosed-marker"
	case strings.Contains(msg, "行以内") || strings.Contains(msg, "bytes以内"):
		return "size"
	case strings.Contains(msg, "必須field"):
		return "missing-field"
	// 固有の理由句へmatchさせる。"TARGETS"だけだと重複field錯誤(packet field TARGETSが重複しています)を
	// targets-noneへ誤集計し、新契約の拒否metricを汚染する。
	case strings.Contains(msg, "TARGETSはnoneにできません"):
		return "targets-none"
	case strings.Contains(msg, "RISK"):
		return "risk"
	case strings.Contains(msg, "STATUS"):
		return "status"
	case strings.Contains(msg, "KEY: value") || strings.Contains(msg, "重複") || strings.Contains(msg, "PACKET_BEGIN"):
		return "malformed"
	default:
		return "other"
	}
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

// ParseはpathからPACKETを抽出し検証する。完全なPACKET_BEGIN/PACKET_ENDの組は1応答にちょうど1組だけを許容し、他のmarker構造とmarker前後の非空本文をfail closedで拒否する。
func Parse(path string) (Packet, error) {
	file, err := os.Open(path)
	if err != nil {
		return Packet{}, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)

	seenBegin := false
	seenEnd := false
	var body []string

	for scanner.Scan() {
		raw := scanner.Text()
		switch strings.TrimSpace(raw) {
		case "PACKET_BEGIN":
			switch {
			case seenEnd:
				return Packet{}, &constraintError{reason: "PACKETが複数検出されました: 完全なPACKET_BEGIN/PACKET_ENDの組は1回の応答にちょうど1組だけにしてください"}
			case seenBegin:
				return Packet{}, &constraintError{reason: "PACKET_BEGINが入れ子になっています: markerは1組だけにしてください"}
			default:
				seenBegin = true
			}
		case "PACKET_END":
			if !seenBegin || seenEnd {
				return Packet{}, &constraintError{reason: "対応するPACKET_BEGINがないPACKET_ENDが検出されました"}
			}
			seenEnd = true
		default:
			if !seenBegin || seenEnd {
				if strings.TrimSpace(raw) != "" {
					if seenBegin {
						return Packet{}, &constraintError{reason: "PACKET_ENDの後に非空の本文があります: PACKET_ENDを最後の物理行にしてください"}
					}
					return Packet{}, &constraintError{reason: "PACKET_BEGINの前に非空の本文があります: PACKET_BEGINを最初の物理行にしてください"}
				}
				continue
			}
			body = append(body, raw)
		}
	}
	if err := scanner.Err(); err != nil {
		return Packet{}, err
	}
	switch {
	case !seenBegin:
		return Packet{}, &constraintError{reason: "PACKET_BEGIN/PACKET_ENDで囲まれた出力がありません"}
	case !seenEnd:
		return Packet{}, &constraintError{reason: "PACKET_BEGINがPACKET_ENDで閉じられていません"}
	}

	result := FromLines(body)
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
	// SolはNEEDS_SOL_REVIEWをTARGETSとSOL_QUESTIONに限定した確認で消費するため、
	// noneはSolへ確認対象を与えず圧縮意味情報gateに反する。機械検証できる最小契約のみここへ置き、
	// 自然言語の意味充足そのものはreviewer判断へ委ねる。
	if status == "NEEDS_SOL_REVIEW" && strings.EqualFold(value.Fields["TARGETS"], "none") {
		return &constraintError{reason: "NEEDS_SOL_REVIEWのTARGETSはnoneにできません: Solが読むべき最小対象をfile:symbol/行範囲で指定してください"}
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
