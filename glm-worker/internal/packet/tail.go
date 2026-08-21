package packet

import (
	"bufio"
	"os"
	"strings"
	"unicode/utf8"
)

// Tailはpathの末尾count行を返す。読めない場合は空文字列。診断表示だけへ使い、
// 結果受理の入力にはしない。
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
