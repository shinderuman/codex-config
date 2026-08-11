//go:build unix

package app

import (
	"fmt"
	"os"
	"syscall"
)

type RepoLock struct {
	file *os.File
}

// AcquireRepoLockはflock(LOCK_EX|LOCK_NB)で排他ロックを取得する。
func AcquireRepoLock(path string) (*RepoLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("GLM worker lockを開けません: %w", err)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("STATUS: WORKER_ERROR\nERROR: another glm-worker is already running for this repository")
	}

	if err := file.Truncate(0); err == nil {
		_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	}

	return &RepoLock{file: file}, nil
}

func (l *RepoLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	return l.file.Close()
}
