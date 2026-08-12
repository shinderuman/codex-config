// Package autoresumeはauto-resume automationの作成結果をfail-closedで検証する。
// Codex appのautomation_update応答だけを信頼せず、保存済みTOML実体とSQLite rowを
// 期待ID・対象thread・status ACTIVE・絶対時刻で照合する。
// DBへの書込みやautomation自作は行わず、sqlite3 CLI経由の読取のみ行う。
package autoresume

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Outcome int

const (
	Pass Outcome = iota
	Fail
	Unavailable
)

type Params struct {
	AutomationKey    string
	ExpectedRFC3339  string
	ExpectedThreadID string
	AutomationsDir   string
	DBPath           string
}

type AutomationTOML struct {
	ID             string
	Name           string
	Status         string
	Rrule          string
	TargetThreadID string
}

type DBRow struct {
	ID         string
	Status     string
	Rrule      string
	NextRunAt  int64
	HasNextRun bool
}

type Result struct {
	Outcome       Outcome
	Reason        string
	AutomationKey string
	TargetThread  string
	ExpectedUTC   string
	TOMLDTStart   string
	DBNextRunUTC  string
}

type DBReader func(dbPath, key string) (DBRow, error)

var (
	ErrSqlite3NotFound = errors.New("sqlite3 binary not found")
	ErrDBUnreadable    = errors.New("codex sqlite db unreadable")
	ErrRowNotFound     = errors.New("automation row not found")
)

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func Verify(params Params, readDB DBReader) Result {
	result := Result{
		AutomationKey: params.AutomationKey,
		TargetThread:  params.ExpectedThreadID,
	}

	if !keyPattern.MatchString(params.AutomationKey) {
		result.Outcome = Fail
		result.Reason = fmt.Sprintf("invalid automation key format: %q", params.AutomationKey)
		return result
	}

	if params.ExpectedThreadID == "" {
		result.Outcome = Fail
		result.Reason = "expected thread ID is empty"
		return result
	}

	expectedUTC, expectedDTStart, expectedEpochMS, err := expectedFromRFC3339(params.ExpectedRFC3339)
	if err != nil {
		result.Outcome = Fail
		result.Reason = err.Error()
		return result
	}
	result.ExpectedUTC = expectedUTC.Format(time.RFC3339)
	result.TOMLDTStart = expectedDTStart

	tomlPath := filepath.Join(params.AutomationsDir, params.AutomationKey, "automation.toml")
	data, err := os.ReadFile(tomlPath)
	if err != nil {
		result.Outcome = Fail
		result.Reason = fmt.Sprintf("automation.toml not found: %s", tomlPath)
		return result
	}

	toml, err := parseAutomationTOML(data)
	if err != nil {
		result.Outcome = Fail
		result.Reason = fmt.Sprintf("TOML parse: %v", err)
		return result
	}

	if reason := checkTOML(toml, params, expectedDTStart); reason != "" {
		result.Outcome = Fail
		result.Reason = reason
		return result
	}

	db, err := readDB(params.DBPath, params.AutomationKey)
	if err != nil {
		if errors.Is(err, ErrRowNotFound) {
			result.Outcome = Fail
			result.Reason = "SQLite automation row not found (entity uncreated)"
		} else {
			result.Outcome = Unavailable
			result.Reason = fmt.Sprintf("DB verification unavailable: %v", err)
		}
		return result
	}

	if reason := checkDB(db, params, expectedEpochMS, toml.Rrule); reason != "" {
		result.Outcome = Fail
		result.Reason = reason
		return result
	}

	result.Outcome = Pass
	result.DBNextRunUTC = time.UnixMilli(expectedEpochMS).UTC().Format(time.RFC3339)
	return result
}

func expectedFromRFC3339(rfc3339 string) (time.Time, string, int64, error) {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return time.Time{}, "", 0, fmt.Errorf("invalid RFC3339 %q: %w", rfc3339, err)
	}
	utc := t.UTC()
	return utc, utc.Format(dtStartLayout), utc.UnixMilli(), nil
}

const dtStartLayout = "20060102T150405"

func checkTOML(toml AutomationTOML, params Params, expectedDTStart string) string {
	if toml.ID != params.AutomationKey {
		return fmt.Sprintf("TOML id mismatch: got %q want %q", toml.ID, params.AutomationKey)
	}
	if toml.Name != params.AutomationKey {
		return fmt.Sprintf("TOML name mismatch: got %q want %q", toml.Name, params.AutomationKey)
	}
	if toml.Status != "ACTIVE" {
		return fmt.Sprintf("TOML status is %q want ACTIVE", toml.Status)
	}
	if toml.TargetThreadID != params.ExpectedThreadID {
		return fmt.Sprintf("target_thread_id mismatch: got %q want %q", toml.TargetThreadID, params.ExpectedThreadID)
	}
	if reason := validateRrule(toml.Rrule, expectedDTStart); reason != "" {
		return reason
	}
	return ""
}

func checkDB(db DBRow, params Params, expectedEpochMS int64, tomlRrule string) string {
	if db.ID != params.AutomationKey {
		return fmt.Sprintf("DB id mismatch: got %q want %q", db.ID, params.AutomationKey)
	}
	if db.Status != "ACTIVE" {
		return fmt.Sprintf("DB status is %q want ACTIVE", db.Status)
	}
	if !db.HasNextRun {
		return "DB next_run_at is NULL"
	}
	if db.NextRunAt != expectedEpochMS {
		got := time.UnixMilli(db.NextRunAt).UTC()
		want := time.UnixMilli(expectedEpochMS).UTC()
		return fmt.Sprintf("DB next_run_at mismatch: got %d (%s) want %d (%s)", db.NextRunAt, got.Format(time.RFC3339), expectedEpochMS, want.Format(time.RFC3339))
	}
	if db.Rrule != tomlRrule {
		return "DB rrule does not match TOML rrule"
	}
	return ""
}

// validateRruleは1回限りのUTC実行規則だけを許可する。
func validateRrule(rrule string, expectedDTStart string) string {
	lines := strings.Split(rrule, "\n")
	if len(lines) != 2 {
		return fmt.Sprintf("rrule must be exactly 2 lines, got %d", len(lines))
	}
	if strings.HasPrefix(lines[0], "DTSTART;") {
		return "DTSTART must not use TZID"
	}
	wantLine1 := "DTSTART:" + expectedDTStart
	if lines[0] != wantLine1 {
		return fmt.Sprintf("DTSTART line: got %q want %q", lines[0], wantLine1)
	}
	if lines[1] != "RRULE:FREQ=DAILY;COUNT=1" {
		return fmt.Sprintf("RRULE line: got %q want %q", lines[1], "RRULE:FREQ=DAILY;COUNT=1")
	}
	return ""
}

func WriteResult(w io.Writer, r Result) {
	switch r.Outcome {
	case Pass:
		fmt.Fprintln(w, "VERIFICATION: PASS")
		fmt.Fprintf(w, "AUTOMATION_KEY: %s\n", r.AutomationKey)
		fmt.Fprintf(w, "TARGET_THREAD: %s\n", r.TargetThread)
		fmt.Fprintf(w, "EXPECTED_AT_UTC: %s\n", r.ExpectedUTC)
		fmt.Fprintf(w, "TOML_DTSTART: %s\n", r.TOMLDTStart)
		fmt.Fprintf(w, "DB_NEXT_RUN_AT_UTC: %s\n", r.DBNextRunUTC)
		fmt.Fprintln(w, "STATUS: ACTIVE")
	case Fail:
		fmt.Fprintln(w, "VERIFICATION: FAIL")
		fmt.Fprintf(w, "REASON: %s\n", r.Reason)
	case Unavailable:
		fmt.Fprintln(w, "VERIFICATION: UNAVAILABLE")
		fmt.Fprintf(w, "REASON: %s\n", r.Reason)
	}
}
