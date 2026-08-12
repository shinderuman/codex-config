package autoresume

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testKey     = "glm-worker-resume-testrepo12-12345678"
	testThread  = "019f88f8-0e70-7d53-a2a3-f0c61666827c"
	testRrule   = "DTSTART:20260812T110120\nRRULE:FREQ=DAILY;COUNT=1"
	testDTStart = "20260812T110120"
)

func TestExpectedFromRFC3339OffsetConversion(t *testing.T) {
	baseUTC := time.Date(2026, 8, 12, 11, 1, 20, 0, time.UTC)
	wantMS := baseUTC.UnixMilli()

	tests := []struct {
		name    string
		rfc3339 string
	}{
		{"JST +09:00", "2026-08-12T20:01:20+09:00"},
		{"CST +08:00 Z.ai reset timezone", "2026-08-12T19:01:20+08:00"},
		{"UTC Z suffix", "2026-08-12T11:01:20Z"},
		{"UTC +00:00", "2026-08-12T11:01:20+00:00"},
		{"negative offset -05:00", "2026-08-12T06:01:20-05:00"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			utc, dt, ms, err := expectedFromRFC3339(test.rfc3339)
			if err != nil {
				t.Fatal(err)
			}
			if !utc.Equal(baseUTC) {
				t.Fatalf("UTC = %s want %s", utc, baseUTC)
			}
			if dt != testDTStart {
				t.Fatalf("DTSTART = %q want %q", dt, testDTStart)
			}
			if ms != wantMS {
				t.Fatalf("epoch ms = %d want %d", ms, wantMS)
			}
		})
	}
}

func TestExpectedFromRFC3339RejectsInvalid(t *testing.T) {
	if _, _, _, err := expectedFromRFC3339("not-a-date"); err == nil {
		t.Fatal("invalid RFC3339 should error")
	}
	if _, _, _, err := expectedFromRFC3339(""); err == nil {
		t.Fatal("empty RFC3339 should error")
	}
}

func TestParseAutomationTOMLValid(t *testing.T) {
	toml := []byte(`version = 1
id = "glm-worker-resume-testrepo12-12345678"
kind = "heartbeat"
name = "glm-worker-resume-testrepo12-12345678"
prompt = "resume task"
status = "ACTIVE"
rrule = "DTSTART:20260812T110120\nRRULE:FREQ=DAILY;COUNT=1"
target_thread_id = "019f88f8-0e70-7d53-a2a3-f0c61666827c"
created_at = 1786517102835
`)

	got, err := parseAutomationTOML(toml)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != testKey {
		t.Fatalf("ID = %q", got.ID)
	}
	if got.Status != "ACTIVE" {
		t.Fatalf("Status = %q", got.Status)
	}
	if got.Rrule != testRrule {
		t.Fatalf("Rrule = %q want %q", got.Rrule, testRrule)
	}
	if got.TargetThreadID != testThread {
		t.Fatalf("TargetThreadID = %q", got.TargetThreadID)
	}
}

func TestParseAutomationTOMLRejectsDuplicateKey(t *testing.T) {
	toml := []byte(`id = "a"
id = "b"
name = "x"
status = "ACTIVE"
rrule = "x"
target_thread_id = "y"
`)
	if _, err := parseAutomationTOML(toml); err == nil {
		t.Fatal("duplicate key should error")
	}
}

func TestParseAutomationTOMLRejectsUnterminatedString(t *testing.T) {
	toml := []byte(`id = "unterminated
name = "x"
status = "ACTIVE"
rrule = "x"
target_thread_id = "y"
`)
	if _, err := parseAutomationTOML(toml); err == nil {
		t.Fatal("unterminated string should error")
	}
}

func TestParseAutomationTOMLRejectsTrailingContent(t *testing.T) {
	toml := []byte(`id = "abc" extra
name = "x"
status = "ACTIVE"
rrule = "x"
target_thread_id = "y"
`)
	if _, err := parseAutomationTOML(toml); err == nil {
		t.Fatal("trailing content after string close should error")
	}
}

func TestParseAutomationTOMLRejectsMissingField(t *testing.T) {
	toml := []byte(`id = "a"
name = "b"
status = "ACTIVE"
rrule = "c"
`)
	if _, err := parseAutomationTOML(toml); err == nil {
		t.Fatal("missing target_thread_id should error")
	}
}

func TestParseAutomationTOMLRejectsBareRequiredField(t *testing.T) {
	toml := []byte(`id = 123
name = "x"
status = "ACTIVE"
rrule = "y"
target_thread_id = "z"
`)
	if _, err := parseAutomationTOML(toml); err == nil {
		t.Fatal("bare value for required string field should error")
	}
}

func TestValidateRruleContract(t *testing.T) {
	valid := "DTSTART:20260812T110120\nRRULE:FREQ=DAILY;COUNT=1"

	if reason := validateRrule(valid, testDTStart); reason != "" {
		t.Fatalf("valid rrule rejected: %s", reason)
	}

	rejects := []struct {
		name  string
		rrule string
	}{
		{"TZID", "DTSTART;TZID=Asia/Tokyo:20260812T200120\nRRULE:FREQ=DAILY;COUNT=1"},
		{"wrong frequency", "DTSTART:20260812T110120\nRRULE:FREQ=HOURLY;COUNT=1"},
		{"wrong count", "DTSTART:20260812T110120\nRRULE:FREQ=DAILY;COUNT=2"},
		{"missing COUNT", "DTSTART:20260812T110120\nRRULE:FREQ=DAILY"},
		{"extra INTERVAL", "DTSTART:20260812T110120\nRRULE:FREQ=DAILY;COUNT=1;INTERVAL=2"},
		{"extra line", "DTSTART:20260812T110120\nRRULE:FREQ=DAILY;COUNT=1\nEXTRA"},
		{"single line", "DTSTART:20260812T110120"},
		{"wrong order", "RRULE:FREQ=DAILY;COUNT=1\nDTSTART:20260812T110120"},
		{"wrong DTSTART time", "DTSTART:19990101T000000\nRRULE:FREQ=DAILY;COUNT=1"},
		{"trailing newline", "DTSTART:20260812T110120\nRRULE:FREQ=DAILY;COUNT=1\n"},
		{"multiple DTSTART", "DTSTART:20260812T110120\nDTSTART:20260812T110120"},
		{"empty", ""},
	}

	for _, c := range rejects {
		t.Run(c.name, func(t *testing.T) {
			if reason := validateRrule(c.rrule, testDTStart); reason == "" {
				t.Fatalf("rrule %q should be rejected", c.rrule)
			}
		})
	}
}

func TestParseBasicStringEscapeSequences(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`"a\nb"`, "a\nb"},
		{`"a\tb"`, "a\tb"},
		{`"a\rb"`, "a\rb"},
		{`"a\"b"`, `a"b`},
		{`"a\\b"`, `a\b`},
		{`"a\xzb"`, `a\xzb`},
	}
	for _, c := range cases {
		got, err := parseBasicString(c.input)
		if err != nil {
			t.Fatalf("parseBasicString(%q): %v", c.input, err)
		}
		if got != c.want {
			t.Fatalf("parseBasicString(%q) = %q want %q", c.input, got, c.want)
		}
	}
}

func TestParseBasicStringRejectsMalformed(t *testing.T) {
	if _, err := parseBasicString(`"unterminated`); err == nil {
		t.Fatal("unterminated string should error")
	}
	dangling := string([]byte{'"', 'a', '\\'})
	if _, err := parseBasicString(dangling); err == nil {
		t.Fatal("dangling escape without close should error")
	}
}

func TestWriteResult(t *testing.T) {
	var sb strings.Builder
	WriteResult(&sb, Result{Outcome: Pass, AutomationKey: "k", TargetThread: "t",
		ExpectedUTC: "2026-08-12T11:01:20Z", TOMLDTStart: "20260812T110120", DBNextRunUTC: "2026-08-12T11:01:20Z"})
	out := sb.String()
	if !strings.Contains(out, "VERIFICATION: PASS") || !strings.Contains(out, "AUTOMATION_KEY: k") {
		t.Fatalf("pass output = %q", out)
	}

	sb.Reset()
	WriteResult(&sb, Result{Outcome: Fail, Reason: "missing"})
	if !strings.Contains(sb.String(), "VERIFICATION: FAIL") || !strings.Contains(sb.String(), "REASON: missing") {
		t.Fatalf("fail output = %q", sb.String())
	}

	sb.Reset()
	WriteResult(&sb, Result{Outcome: Unavailable, Reason: "no sqlite3"})
	if !strings.Contains(sb.String(), "VERIFICATION: UNAVAILABLE") {
		t.Fatalf("unavailable output = %q", sb.String())
	}
}

func TestCheckTOMLMismatches(t *testing.T) {
	params := Params{AutomationKey: testKey, ExpectedThreadID: testThread}
	base := AutomationTOML{
		ID: testKey, Name: testKey, Status: "ACTIVE",
		Rrule: testRrule, TargetThreadID: testThread,
	}

	cases := []struct {
		name   string
		mutate func(AutomationTOML) AutomationTOML
	}{
		{"id mismatch", func(a AutomationTOML) AutomationTOML { a.ID = "other"; return a }},
		{"name mismatch", func(a AutomationTOML) AutomationTOML { a.Name = "other"; return a }},
		{"status not ACTIVE", func(a AutomationTOML) AutomationTOML { a.Status = "PAUSED"; return a }},
		{"thread mismatch", func(a AutomationTOML) AutomationTOML { a.TargetThreadID = "deadbeef"; return a }},
		{"rrule DTSTART mismatch", func(a AutomationTOML) AutomationTOML {
			a.Rrule = "DTSTART:19990101T000000\nRRULE:FREQ=DAILY;COUNT=1"
			return a
		}},
		{"rrule single line", func(a AutomationTOML) AutomationTOML { a.Rrule = "RRULE:FREQ=DAILY;COUNT=1"; return a }},
		{"rrule wrong frequency", func(a AutomationTOML) AutomationTOML {
			a.Rrule = "DTSTART:20260812T110120\nRRULE:FREQ=HOURLY;COUNT=1"
			return a
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if reason := checkTOML(c.mutate(base), params, testDTStart); reason == "" {
				t.Fatalf("%s: expected mismatch reason", c.name)
			}
		})
	}

	if reason := checkTOML(base, params, testDTStart); reason != "" {
		t.Fatalf("valid TOML should pass: %s", reason)
	}
}

func TestCheckDBMismatches(t *testing.T) {
	params := Params{AutomationKey: testKey}
	expectedMS := time.Date(2026, 8, 12, 11, 1, 20, 0, time.UTC).UnixMilli()
	base := DBRow{ID: testKey, Status: "ACTIVE", Rrule: testRrule, NextRunAt: expectedMS, HasNextRun: true}

	cases := []struct {
		name   string
		mutate func(DBRow) DBRow
	}{
		{"id mismatch", func(d DBRow) DBRow { d.ID = "other"; return d }},
		{"status not ACTIVE", func(d DBRow) DBRow { d.Status = "PAUSED"; return d }},
		{"next_run_at NULL", func(d DBRow) DBRow { d.HasNextRun = false; return d }},
		{"next_run_at wrong", func(d DBRow) DBRow { d.NextRunAt = expectedMS + 60000; return d }},
		{"rrule mismatch", func(d DBRow) DBRow { d.Rrule = "different"; return d }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if reason := checkDB(c.mutate(base), params, expectedMS, testRrule); reason == "" {
				t.Fatalf("%s: expected mismatch reason", c.name)
			}
		})
	}

	if reason := checkDB(base, params, expectedMS, testRrule); reason != "" {
		t.Fatalf("valid DB row should pass: %s", reason)
	}
}

func TestVerifyPass(t *testing.T) {
	dir := t.TempDir()
	writeValidTOML(t, dir)

	fakeDB := func(_, _ string) (DBRow, error) {
		return DBRow{
			ID: testKey, Status: "ACTIVE", Rrule: testRrule,
			NextRunAt:  time.Date(2026, 8, 12, 11, 1, 20, 0, time.UTC).UnixMilli(),
			HasNextRun: true,
		}, nil
	}

	result := Verify(Params{
		AutomationKey:    testKey,
		ExpectedRFC3339:  "2026-08-12T20:01:20+09:00",
		ExpectedThreadID: testThread,
		AutomationsDir:   dir,
		DBPath:           "unused",
	}, fakeDB)

	if result.Outcome != Pass {
		t.Fatalf("Outcome = %d reason = %s", result.Outcome, result.Reason)
	}
	if result.TOMLDTStart != testDTStart {
		t.Fatalf("DTStart = %q", result.TOMLDTStart)
	}
}

// 実障害回帰: automation_update invalid status → TOML実体が存在しない。
// suggested_create → 候補カード表示だけで実体未作成。VerifyはこれをFAILとして検出する。
func TestVerifyFailsWhenTOMLMissing(t *testing.T) {
	dir := t.TempDir()

	result := Verify(Params{
		AutomationKey:    testKey,
		ExpectedRFC3339:  "2026-08-12T20:01:20+09:00",
		ExpectedThreadID: testThread,
		AutomationsDir:   dir,
		DBPath:           "unused",
	}, func(_, _ string) (DBRow, error) { return DBRow{}, ErrRowNotFound })

	if result.Outcome != Fail {
		t.Fatalf("missing TOML must FAIL, got %d: %s", result.Outcome, result.Reason)
	}
}

func TestVerifyFailsWhenRowMissing(t *testing.T) {
	dir := t.TempDir()
	writeValidTOML(t, dir)

	result := Verify(Params{
		AutomationKey:    testKey,
		ExpectedRFC3339:  "2026-08-12T20:01:20+09:00",
		ExpectedThreadID: testThread,
		AutomationsDir:   dir,
		DBPath:           "unused",
	}, func(_, _ string) (DBRow, error) { return DBRow{}, ErrRowNotFound })

	if result.Outcome != Fail {
		t.Fatalf("missing DB row must FAIL, got %d: %s", result.Outcome, result.Reason)
	}
}

func TestVerifyUnavailableWhenSqlite3Missing(t *testing.T) {
	dir := t.TempDir()
	writeValidTOML(t, dir)

	result := Verify(Params{
		AutomationKey:    testKey,
		ExpectedRFC3339:  "2026-08-12T20:01:20+09:00",
		ExpectedThreadID: testThread,
		AutomationsDir:   dir,
		DBPath:           "unused",
	}, func(_, _ string) (DBRow, error) { return DBRow{}, ErrSqlite3NotFound })

	if result.Outcome != Unavailable {
		t.Fatalf("sqlite3 missing must be UNAVAILABLE, got %d: %s", result.Outcome, result.Reason)
	}
}

func TestVerifyUnavailableWhenDBUnreadable(t *testing.T) {
	dir := t.TempDir()
	writeValidTOML(t, dir)

	result := Verify(Params{
		AutomationKey:    testKey,
		ExpectedRFC3339:  "2026-08-12T20:01:20+09:00",
		ExpectedThreadID: testThread,
		AutomationsDir:   dir,
		DBPath:           "unused",
	}, func(_, _ string) (DBRow, error) { return DBRow{}, ErrDBUnreadable })

	if result.Outcome != Unavailable {
		t.Fatalf("DB unreadable must be UNAVAILABLE, got %d: %s", result.Outcome, result.Reason)
	}
}

func TestVerifyRejectsInvalidKeyFormat(t *testing.T) {
	result := Verify(Params{
		AutomationKey:   "bad'; DROP TABLE--",
		ExpectedRFC3339: "2026-08-12T20:01:20+09:00",
		AutomationsDir:  t.TempDir(),
		DBPath:          "unused",
	}, func(_, _ string) (DBRow, error) { return DBRow{}, nil })

	if result.Outcome != Fail {
		t.Fatalf("invalid key must FAIL, got %d", result.Outcome)
	}
}

func TestVerifyRejectsInvalidRFC3339(t *testing.T) {
	result := Verify(Params{
		AutomationKey:   testKey,
		ExpectedRFC3339: "not-a-date",
		AutomationsDir:  t.TempDir(),
		DBPath:          "unused",
	}, func(_, _ string) (DBRow, error) { return DBRow{}, nil })

	if result.Outcome != Fail {
		t.Fatalf("invalid RFC3339 must FAIL, got %d", result.Outcome)
	}
}

func TestVerifyRejectsEmptyThreadID(t *testing.T) {
	result := Verify(Params{
		AutomationKey:    testKey,
		ExpectedRFC3339:  "2026-08-12T20:01:20+09:00",
		ExpectedThreadID: "",
		AutomationsDir:   t.TempDir(),
		DBPath:           "unused",
	}, func(_, _ string) (DBRow, error) { return DBRow{}, nil })

	if result.Outcome != Fail {
		t.Fatalf("empty thread ID must FAIL, got %d", result.Outcome)
	}
}

func TestVerifyFailsOnTOMLStatusNotActive(t *testing.T) {
	dir := t.TempDir()
	toml := validTOMLBytes()
	toml = []byte(replaceFirst(string(toml), `status = "ACTIVE"`, `status = "PAUSED"`))
	writeTOML(t, dir, toml)

	result := Verify(Params{
		AutomationKey:    testKey,
		ExpectedRFC3339:  "2026-08-12T20:01:20+09:00",
		ExpectedThreadID: testThread,
		AutomationsDir:   dir,
		DBPath:           "unused",
	}, func(_, _ string) (DBRow, error) { return DBRow{}, ErrRowNotFound })

	if result.Outcome != Fail {
		t.Fatalf("PAUSED status must FAIL, got %d", result.Outcome)
	}
}

// 実DBへ書込みはテスト専用の一時DBに対してのみ行い、Codex app DBへは触れない。
func TestReadDBRowSqlite3Integration(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	schema := `CREATE TABLE automations (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		prompt TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'ACTIVE',
		next_run_at INTEGER,
		last_run_at INTEGER,
		cwds TEXT NOT NULL DEFAULT '[]',
		rrule TEXT NOT NULL DEFAULT 'FREQ=HOURLY;INTERVAL=24;BYMINUTE=0',
		model TEXT,
		reasoning_effort TEXT,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		target_type TEXT,
		project_id TEXT
	);`
	if err := exec.Command("sqlite3", dbPath, schema).Run(); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	expectedMS := time.Date(2026, 8, 12, 11, 1, 20, 0, time.UTC).UnixMilli()
	insert := `INSERT INTO automations (id, name, prompt, status, next_run_at, cwds, rrule, created_at, updated_at) VALUES ('` +
		testKey + `', '` + testKey + `', 'prompt', 'ACTIVE', ` + itoa(expectedMS) +
		`, '[]', 'DTSTART:20260812T110120' || char(10) || 'RRULE:FREQ=DAILY;COUNT=1', 1, 1);`
	if err := exec.Command("sqlite3", dbPath, insert).Run(); err != nil {
		t.Fatalf("insert: %v", err)
	}

	row, err := ReadDBRowSqlite3(dbPath, testKey)
	if err != nil {
		t.Fatal(err)
	}
	if row.ID != testKey {
		t.Fatalf("ID = %q", row.ID)
	}
	if row.Status != "ACTIVE" {
		t.Fatalf("Status = %q", row.Status)
	}
	if !row.HasNextRun || row.NextRunAt != expectedMS {
		t.Fatalf("NextRunAt = %d has=%v want %d", row.NextRunAt, row.HasNextRun, expectedMS)
	}
	if row.Rrule != testRrule {
		t.Fatalf("Rrule = %q want %q", row.Rrule, testRrule)
	}

	if _, err := ReadDBRowSqlite3(dbPath, "nonexistent-key1234"); err != ErrRowNotFound {
		t.Fatalf("missing row = %v want ErrRowNotFound", err)
	}

	missingDB := filepath.Join(t.TempDir(), "nope.db")
	if _, err := ReadDBRowSqlite3(missingDB, testKey); err == nil {
		t.Fatal("missing DB file should error")
	}
}

func TestReadDBRowSqlite3RejectsNonexistentDB(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	missing := filepath.Join(t.TempDir(), "absent.db")
	_, err := ReadDBRowSqlite3(missing, testKey)
	if err == nil {
		t.Fatal("nonexistent DB must error")
	}
}

func writeValidTOML(t *testing.T, dir string) {
	t.Helper()
	writeTOML(t, dir, validTOMLBytes())
}

func writeTOML(t *testing.T, dir string, data []byte) {
	t.Helper()
	tomlDir := filepath.Join(dir, testKey)
	if err := os.MkdirAll(tomlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tomlDir, "automation.toml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func validTOMLBytes() []byte {
	return []byte(`version = 1
id = "glm-worker-resume-testrepo12-12345678"
kind = "heartbeat"
name = "glm-worker-resume-testrepo12-12345678"
prompt = "resume task"
status = "ACTIVE"
rrule = "DTSTART:20260812T110120\nRRULE:FREQ=DAILY;COUNT=1"
target_thread_id = "019f88f8-0e70-7d53-a2a3-f0c61666827c"
created_at = 1786517102835
`)
}

func replaceFirst(s, old, new string) string {
	idx := indexOf(s, old)
	if idx < 0 {
		return s
	}
	return s[:idx] + new + s[idx+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
