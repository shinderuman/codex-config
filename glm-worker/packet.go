package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

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
		return packet{}, fmt.Errorf("PACKET_BEGIN/PACKET_ENDで囲まれた出力がありません")
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

	return packet{Lines: last, Fields: fields}, nil
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
