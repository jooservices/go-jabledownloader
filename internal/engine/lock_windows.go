//go:build windows

package engine

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lockFile(file *os.File) error {
	const flags = windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY
	err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrBusy
	}
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	return nil
}

// removeLockFile unlocks first: Windows cannot delete a file that is open.
// If another process grabbed the lock in between, it keeps using the file
// and removal fails harmlessly; the next release deletes it.
func removeLockFile(file *os.File) error {
	name := file.Name()
	if err := file.Close(); err != nil {
		return fmt.Errorf("release lock: %w", err)
	}
	if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return fmt.Errorf("remove lock: %w", err)
	}
	return nil
}
