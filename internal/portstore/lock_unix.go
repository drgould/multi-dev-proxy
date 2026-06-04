//go:build unix

package portstore

import (
	"os"
	"syscall"
)

// withLock runs fn while holding an exclusive advisory lock on lockPath,
// releasing it when fn returns. flock is tied to the open file description and
// released by the kernel if the process dies, so a crash can't leave a stale
// lock behind. The lock file itself is intentionally left in place (unlinking a
// flock'd file is racy).
func withLock(lockPath string, fn func() error) error {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
