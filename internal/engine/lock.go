package engine

import (
	"errors"
	"fmt"
	"os"
)

// ErrBusy means another process is downloading into the same directory.
var ErrBusy = errors.New("download directory is busy (another download is running)")

// lock is an advisory OS file lock. The kernel releases it when the process
// exits, so a crashed or killed run (including a container's PID 1) never
// leaves a stale lock behind.
type lock struct {
	file *os.File
}

func acquireLock(path string) (*lock, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &lock{file: file}, nil
}

// release deletes the lock file and drops the OS lock. The order is
// platform-specific (see removeLockFile).
func (l *lock) release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	return removeLockFile(file)
}
