//go:build windows

package portstore

import (
	"os"

	"golang.org/x/sys/windows"
)

// withLock runs fn while holding an exclusive lock on lockPath via LockFileEx,
// releasing it when fn returns. Windows releases the lock when the handle is
// closed (including on process exit), so a crash can't leave a stale lock.
func withLock(lockPath string, fn func() error) error {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, ol); err != nil {
		return err
	}
	defer windows.UnlockFileEx(h, 0, 1, 0, ol)
	return fn()
}
