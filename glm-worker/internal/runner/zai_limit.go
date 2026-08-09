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
	autoResumeGrace      = 2 * time.Minute
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
	Phase     string
	Limit     ZaiFiveHourLimit
	TaskID    string
	RepoRoot  string
	RepoShort string
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
	autoResumeAvailable, autoResumeAt := autoResumeSchedule(e.Limit.ResetAtRFC3339)
	autoResumeKey := autoResumeKey(e.RepoShort, e.TaskID)

	return fmt.Sprintf(
		"STATUS: RATE_LIMITED\nLIMIT: ZAI_GLM_CODING_PLAN_5H\nPHASE: %s\nTASK_ID: %s\nREPO_ROOT: %s\nRESET_AT_CST: %s\nRESET_TIMEZONE: CST (China Standard Time, UTC+8)\nRESET_AT_RFC3339: %s\nAUTO_RESUME_AVAILABLE: %t\nAUTO_RESUME_AT_RFC3339: %s\nAUTO_RESUME_KEY: %s\nRESUME_AVAILABLE: true\nRESUME_COMMAND: glm-worker --resume",
		e.Phase,
		valueOrUnknown(e.TaskID),
		valueOrUnknown(e.RepoRoot),
		resetAtCST,
		resetAtRFC3339,
		autoResumeAvailable,
		autoResumeAt,
		autoResumeKey,
	)
}

func autoResumeSchedule(resetAtRFC3339 string) (bool, string) {
	resetAt, err := time.Parse(time.RFC3339, resetAtRFC3339)
	if err != nil {
		return false, "unknown"
	}
	return true, resetAt.Add(autoResumeGrace).Format(time.RFC3339)
}

func autoResumeKey(repoShort string, taskID string) string {
	if repoShort == "" {
		repoShort = "unknown-repo"
	}
	if taskID == "" {
		taskID = "unknown-task"
	} else if len(taskID) > 8 {
		taskID = taskID[:8]
	}
	return fmt.Sprintf("glm-worker-resume-%s-%s", repoShort, taskID)
}

func valueOrUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
