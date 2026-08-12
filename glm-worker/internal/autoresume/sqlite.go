package autoresume

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ReadDBRowSqlite3はsqlite3 CLI経由でautomations表を読む。DB書込みは行わず、
// Codex app内部schemaの非公開columnへはaccessしない。
func ReadDBRowSqlite3(dbPath, key string) (DBRow, error) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return DBRow{}, ErrSqlite3NotFound
	}
	if _, err := os.Stat(dbPath); err != nil {
		return DBRow{}, fmt.Errorf("%w: %s", ErrDBUnreadable, dbPath)
	}

	// keyはVerify側で ^[A-Za-z0-9_-]+$ を満たすためsingle-quote injectionは不可。
	query := fmt.Sprintf(
		"SELECT id, status, rrule, next_run_at FROM automations WHERE id = '%s';",
		key,
	)

	var stderr bytes.Buffer
	cmd := exec.Command("sqlite3", "-json", dbPath, query)
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return DBRow{}, fmt.Errorf("%w: %s", ErrDBUnreadable, strings.TrimSpace(stderr.String()))
	}

	// sqlite3 -json は結果0件時に空文字列を返す ([] ではなく)。
	if len(bytes.TrimSpace(output)) == 0 {
		return DBRow{}, ErrRowNotFound
	}

	var rows []struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		Rrule     string `json:"rrule"`
		NextRunAt *int64 `json:"next_run_at"`
	}
	if err := json.Unmarshal(output, &rows); err != nil {
		return DBRow{}, fmt.Errorf("%w: json parse: %v", ErrDBUnreadable, err)
	}

	if len(rows) == 0 {
		return DBRow{}, ErrRowNotFound
	}

	row := rows[0]
	dbRow := DBRow{
		ID:     row.ID,
		Status: row.Status,
		Rrule:  row.Rrule,
	}
	if row.NextRunAt != nil {
		dbRow.NextRunAt = *row.NextRunAt
		dbRow.HasNextRun = true
	}
	return dbRow, nil
}
