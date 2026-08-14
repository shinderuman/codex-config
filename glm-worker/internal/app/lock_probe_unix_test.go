//go:build unix

package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProbeRepoLockFreeWhenFileMissing(t *testing.T) {
	probe := ProbeRepoLock(filepath.Join(t.TempDir(), "lock"))
	if probe.State != LockFree {
		t.Fatalf("State = %q want free", probe.State)
	}
	if probe.PID != "none" {
		t.Fatalf("PID = %q want none", probe.PID)
	}
}

func TestProbeRepoLockFreeWhenHeldElsewhereAndUnlocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")

	lock, err := AcquireRepoLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}

	probe := ProbeRepoLock(path)
	if probe.State != LockFree {
		t.Fatalf("State = %q want free", probe.State)
	}
	if probe.PID != strconv.Itoa(os.Getpid()) {
		t.Fatalf("PID = %q want 直前の取得者PID %d", probe.PID, os.Getpid())
	}
}

func TestProbeRepoLockHeldWhileOtherProcessHolds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")

	lock, err := AcquireRepoLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	probe := ProbeRepoLock(path)
	if probe.State != LockHeld {
		t.Fatalf("State = %q want held", probe.State)
	}
}

// 別repoのlock pathは独立しており、他repo lock保持中でも対象repoはfreeになる。
func TestProbeRepoLockIndependentPerPath(t *testing.T) {
	dir := t.TempDir()
	otherPath := filepath.Join(dir, "other-lock")

	other, err := AcquireRepoLock(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	probe := ProbeRepoLock(filepath.Join(dir, "lock"))
	if probe.State != LockFree {
		t.Fatalf("別repo lock保持中に対象repoがfreeになりません: %q", probe.State)
	}
}

// probeはfileを作成せず、既存のlock file内容も書き換えない。
func TestProbeRepoLockNonDestructive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock")
	if err := os.WriteFile(path, []byte("99999\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	probe := ProbeRepoLock(path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "99999\n" {
		t.Fatalf("probeがlock fileを書き換えました: %q", string(data))
	}
	if probe.PID != "99999" {
		t.Fatalf("PID = %q want 99999", probe.PID)
	}
	if probe.State != LockFree {
		t.Fatalf("State = %q want free", probe.State)
	}
}

// 存在しないPIDが書かれたstale lock fileはfreeと判定され、PIDは診断表示に残る。
func TestProbeRepoLockStalePIDIsNotRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(path, []byte("61243\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	probe := ProbeRepoLock(path)
	if probe.State != LockFree {
		t.Fatalf("stale PID内容でState = %q want free", probe.State)
	}
	if !strings.HasPrefix(probe.PID, "61243") {
		t.Fatalf("PID = %q want 61243", probe.PID)
	}
}

func TestProbeRepoLockEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	probe := ProbeRepoLock(path)
	if probe.State != LockFree {
		t.Fatalf("State = %q want free", probe.State)
	}
	if probe.PID != "none" {
		t.Fatalf("PID = %q want none", probe.PID)
	}
}
