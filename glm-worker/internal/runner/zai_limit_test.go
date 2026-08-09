package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectZaiFiveHourLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.log")
	content := "API Error: Request rejected (429) · [1308][Usage limit reached for 5 hour. Your limit will reset at 2026-07-22 14:06:34][202607221342470f952f313a624fd3]\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	limit, ok := DetectZaiFiveHourLimit(path)
	if !ok {
		t.Fatal("expected Z.ai 5h limit")
	}
	if limit.ResetAtCST != "2026-07-22 14:06:34" {
		t.Fatalf("ResetAtCST = %q", limit.ResetAtCST)
	}
	if limit.ResetAtRFC3339 != "2026-07-22T14:06:34+08:00" {
		t.Fatalf("ResetAtRFC3339 = %q", limit.ResetAtRFC3339)
	}
}

func TestDetectZaiFiveHourLimitRejectsGeneric429(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.log")
	if err := os.WriteFile(path, []byte("API Error: Request rejected (429)\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, ok := DetectZaiFiveHourLimit(path); ok {
		t.Fatal("generic 429 must not be treated as Z.ai 5h limit")
	}
}

func TestDetectZaiFiveHourLimitRejectsDifferentCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.log")
	content := "API Error: Request rejected (429) · [9999][Usage limit reached for 5 hour. Your limit will reset at 2026-07-22 14:06:34]\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, ok := DetectZaiFiveHourLimit(path); ok {
		t.Fatal("different Z.ai error code must not be treated as 5h limit")
	}
}
