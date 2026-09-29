package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkDirRejectsConcurrentLock(t *testing.T) {
	dir := t.TempDir()
	first, err := WorkDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	if _, err := WorkDir(dir); !errors.Is(err, ErrBusy) {
		t.Fatalf("second WorkDir error = %v, want ErrBusy", err)
	}
}

// A lock file left by a killed process (for example a container's PID 1)
// holds no OS lock, so the next run must acquire it.
func TestWorkDirAcquiresLeftoverLockFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".lock"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := WorkDir(dir)
	if err != nil {
		t.Fatalf("WorkDir with leftover lock file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkCloseReleasesLockForNextRun(t *testing.T) {
	dir := t.TempDir()
	first, err := WorkDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".lock")); !os.IsNotExist(err) {
		t.Fatalf("lock file still present: %v", err)
	}

	second, err := WorkDir(dir)
	if err != nil {
		t.Fatalf("WorkDir after Close: %v", err)
	}
	_ = second.Close()
}
