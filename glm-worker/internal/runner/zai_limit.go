package runner

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	zaiFiveHourLimitCode = "1308"
	zaiFiveHourMessage   = "Usage limit reached for 5 hour."
)

var zaiResetPattern = regexp.MustCompile(`Your limit will reset at ([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2})`)

// ZaiFiveHourLimitはZ.ai GLM Coding Planの5h上限到達情報を表す。
type ZaiFiveHourLimit struct {
	ResetAtCST     string
	ResetAtRFC3339 string
}

// DetectZaiFiveHourLimitは出力ログからZ.ai 5h上限到達を検出する。
func DetectZaiFiveHourLimit(path string) (ZaiFiveHourLimit, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ZaiFiveHourLimit{}, false
	}

	output := string(data)
	if !strings.Contains(output, "Request rejected (429)") {
		return ZaiFiveHourLimit{}, false
	}
	if !strings.Contains(output, "["+zaiFiveHourLimitCode+"]") {
		return ZaiFiveHourLimit{}, false
	}
	if !strings.Contains(output, zaiFiveHourMessage) {
		return ZaiFiveHourLimit{}, false
	}

	limit := ZaiFiveHourLimit{}
	match := zaiResetPattern.FindStringSubmatch(output)
	if len(match) != 2 {
		return limit, true
	}

	limit.ResetAtCST = match[1]

	// Z.ai Coding Planのreset時刻は中国標準時 (CST, UTC+8) として扱う。
	chinaStandardTime := time.FixedZone("CST", 8*60*60)
	resetAt, err := time.ParseInLocation(
		"2006-01-02 15:04:05",
		limit.ResetAtCST,
		chinaStandardTime,
	)
	if err == nil {
		limit.ResetAtRFC3339 = resetAt.Format(time.RFC3339)
	}

	return limit, true
}

// ZaiRateLimitErrorは5h上限到達を呼び出し元へ伝達する業務エラー。
type ZaiRateLimitError struct {
	Phase string
	Limit ZaiFiveHourLimit
}

func (e ZaiRateLimitError) Error() string {
	resetAtCST := e.Limit.ResetAtCST
	if resetAtCST == "" {
		resetAtCST = "unknown"
	}

	resetAtRFC3339 := e.Limit.ResetAtRFC3339
	if resetAtRFC3339 == "" {
		resetAtRFC3339 = "unknown"
	}

	return fmt.Sprintf(
		"STATUS: RATE_LIMITED\nLIMIT: ZAI_GLM_CODING_PLAN_5H\nPHASE: %s\nRESET_AT_CST: %s\nRESET_TIMEZONE: CST (China Standard Time, UTC+8)\nRESET_AT_RFC3339: %s\nRESUME_AVAILABLE: true\nRESUME_COMMAND: glm-worker --resume",
		e.Phase,
		resetAtCST,
		resetAtRFC3339,
	)
}
