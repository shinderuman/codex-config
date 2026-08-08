//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

type repoLock struct {
	file *os.File
}

func acquireRepoLock(path string) (*repoLock, error) {
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

	return &repoLock{file: file}, nil
}

func (l *repoLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	return l.file.Close()
}
