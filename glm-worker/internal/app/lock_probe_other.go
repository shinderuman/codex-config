//go:build !unix

package app

import "os"

// ProbeRepoLockはflock非対応環境ではfile参照だけに縮退する。
// 既存lock fileは現取得者の実保持と停止後に残ったfileを区別できないため
// held/ freeを断定せずunknownとし、観測自体がrepo stateを変更しない。
func ProbeRepoLock(path string) LockProbe {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LockProbe{State: LockFree, PID: "none"}
		}
		return LockProbe{State: LockUnknown, PID: "unknown"}
	}
	return LockProbe{State: LockUnknown, PID: parseLockPID(data)}
}
