//go:build !windows

package engine

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func lockFile(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrBusy
	}
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	return nil
}

// removeLockFile unlinks the file while still holding the lock, then unlocks
// by closing it, so no other process can lock the unlinked file first.
func removeLockFile(file *os.File) error {
	removeErr := os.Remove(file.Name())
	closeErr := file.Close()
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return fmt.Errorf("remove lock: %w", removeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("release lock: %w", closeErr)
	}
	return nil
}
