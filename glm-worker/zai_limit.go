package main

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

type zaiFiveHourLimit struct {
	ResetAtCST     string
	ResetAtRFC3339 string
}

func detectZaiFiveHourLimit(path string) (zaiFiveHourLimit, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return zaiFiveHourLimit{}, false
	}

	output := string(data)
	if !strings.Contains(output, "Request rejected (429)") {
		return zaiFiveHourLimit{}, false
	}
	if !strings.Contains(output, "["+zaiFiveHourLimitCode+"]") {
		return zaiFiveHourLimit{}, false
	}
	if !strings.Contains(output, zaiFiveHourMessage) {
		return zaiFiveHourLimit{}, false
	}

	limit := zaiFiveHourLimit{}
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

type zaiRateLimitError struct {
	Phase string
	Limit zaiFiveHourLimit
}

func (e zaiRateLimitError) Error() string {
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
