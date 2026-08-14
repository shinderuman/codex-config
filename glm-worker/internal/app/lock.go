package app

import "strings"

// LockStateは対象repository lock fileの実保持状態。
type LockState string

const (
	LockHeld    LockState = "held"
	LockFree    LockState = "free"
	LockUnknown LockState = "unknown"
)

// LockProbeはlock保持判定と、lock fileに書かれていた前回取得者のPID(診断情報)。
// PIDはlock保持の権威ではなく、stale PID・PID reuseでrunning判定しない。
type LockProbe struct {
	State LockState
	PID   string
}

// parseLockPIDはlock file先頭行のPIDを診断情報として返す。
// flockではunlock後も内容が残るため、存在していてもlock保持の証拠にならない。
func parseLockPID(data []byte) string {
	text := string(data)
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = text[:i]
	}
	if strings.TrimSpace(text) == "" {
		return "none"
	}
	return text
}
