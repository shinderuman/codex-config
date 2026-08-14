//go:build unix

package app

import (
	"os"
	"syscall"
)

// ProbeRepoLockは対象repo lock fileのflock実保持を非破壊で判定する。
// lock fileが存在しない場合はfree。fileは開くが作成せず、内容も書き換えない。
// 独自のflockを取得して即解放するため、並行するworkerの保持とは排他されない。
func ProbeRepoLock(path string) LockProbe {
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return LockProbe{State: LockFree, PID: "none"}
		}
		return LockProbe{State: LockUnknown, PID: "unknown"}
	}
	defer file.Close()

	pidBytes := make([]byte, 32)
	n, _ := file.ReadAt(pidBytes, 0)
	pid := parseLockPID(pidBytes[:n])

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return LockProbe{State: LockHeld, PID: pid}
	}

	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return LockProbe{State: LockFree, PID: pid}
}
