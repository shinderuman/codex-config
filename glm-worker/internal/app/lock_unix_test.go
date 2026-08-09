//go:build unix

package app

import (
	"path/filepath"
	"testing"
)

func TestAcquireRepoLockSerializes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")

	first, err := AcquireRepoLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	if _, err := AcquireRepoLock(path); err == nil {
		t.Fatal("2つ目のロック取得は失敗する必要があります")
	}
}

func TestAcquireRepoLockReusableAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")

	first, err := AcquireRepoLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := AcquireRepoLock(path)
	if err != nil {
		t.Fatalf("Close後の再取得に失敗しました: %v", err)
	}
	second.Close()
}
