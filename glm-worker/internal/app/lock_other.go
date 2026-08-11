//go:build !unix

package app

import (
	"fmt"
	"os"
)

// RepoLockはリポジトリ別のプロセス間ロック(flock非対応環境向けの排他作成フォールバック)。
type RepoLock struct {
	path string
}

// AcquireRepoLockはO_EXCLでロックファイルを作成し排他を取る。
func AcquireRepoLock(path string) (*RepoLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("STATUS: WORKER_ERROR\nERROR: another glm-worker is already running for this repository")
	}
	file.Close()
	return &RepoLock{path: path}, nil
}

func (l *RepoLock) Close() error {
	if l == nil {
		return nil
	}
	return os.Remove(l.path)
}
