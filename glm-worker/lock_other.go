//go:build !unix

package main

import (
	"fmt"
	"os"
)

type repoLock struct {
	path string
}

func acquireRepoLock(path string) (*repoLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("STATUS: WORKER_ERROR\nERROR: another glm-worker is already running for this repository")
	}
	file.Close()
	return &repoLock{path: path}, nil
}

func (l *repoLock) Close() error {
	if l == nil {
		return nil
	}
	return os.Remove(l.path)
}
