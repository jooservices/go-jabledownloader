package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Work owns resumable state for one download directory. Call Close when the
// engine no longer uses the directory so another process can acquire it.
type Work struct {
	dir      string
	segments string
	lock     *lock
}

// WorkDir prepares dir and acquires its lock; Open creates .segments.
func WorkDir(dir string) (*Work, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("work directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create work directory: %w", err)
	}
	l, err := acquireLock(filepath.Join(dir, ".lock"))
	if err != nil {
		return nil, err
	}
	return &Work{dir: dir, segments: filepath.Join(dir, ".segments"), lock: l}, nil
}

// Close releases the work directory lock.
func (w *Work) Close() error {
	if w == nil {
		return nil
	}
	err := w.lock.release()
	w.lock = nil
	return err
}

// Dir returns the work directory path.
func (w *Work) Dir() string { return w.dir }

// SegmentsDir returns the resumable segment directory path.
func (w *Work) SegmentsDir() string { return w.segments }

// Open selects a fingerprint for the work directory. It returns true when
// existing segments were discarded and a fresh set was started.
func (w *Work) Open(fingerprint string) (bool, error) {
	if w == nil || w.lock == nil {
		return false, errors.New("work directory is not open")
	}
	if strings.TrimSpace(fingerprint) == "" {
		return false, errors.New("fingerprint is required")
	}
	meta := filepath.Join(w.segments, ".source")
	data, err := os.ReadFile(meta)
	if err == nil && strings.TrimSpace(string(data)) == fingerprint {
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read source fingerprint: %w", err)
	}
	if err := os.RemoveAll(w.segments); err != nil {
		return false, fmt.Errorf("reset segments: %w", err)
	}
	if err := os.MkdirAll(w.segments, 0o755); err != nil {
		return false, fmt.Errorf("recreate segments: %w", err)
	}
	if err := os.WriteFile(meta, []byte(fingerprint+"\n"), 0o644); err != nil {
		return false, fmt.Errorf("write source fingerprint: %w", err)
	}
	return true, nil
}

// Discard removes the resumable segments once the output is finalized.
func (w *Work) Discard() error {
	if err := os.RemoveAll(w.segments); err != nil {
		return fmt.Errorf("remove segments: %w", err)
	}
	return nil
}

// Finalize atomically moves a completed partial file to its final path.
func Finalize(partial, final string) error {
	if partial == "" || final == "" {
		return errors.New("partial and final paths are required")
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return fmt.Errorf("create final directory: %w", err)
	}
	if err := os.Rename(partial, final); err != nil {
		return fmt.Errorf("finalize download: %w", err)
	}
	return nil
}

// PartialPath returns the shared temporary output path.
func PartialPath(dir string) string { return filepath.Join(dir, ".download.part.mp4") }

// Fingerprint returns a stable SHA-256 fingerprint for source material.
func Fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
