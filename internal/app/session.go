// Package app orchestrates the download use-cases: it wires scraper, hls,
// config, ui and telemetry together. Commands stay thin cobra wrappers
// around the services in this package.
package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

// SetupContext returns a context cancelled on SIGINT/SIGTERM.
func SetupContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx, cancel
}

// VideoDir returns the output directory for one video code.
func VideoDir(outDir, code string) string {
	return filepath.Join(outDir, code)
}

// FindExistingVideo reports whether a "<code>-*.mp4" file already exists in
// dir, returning its path. Prefers the primary download
// ("<code>-<codec>.mp4") and skips derived names like "*.hard.mp4".
func FindExistingVideo(dir, code string) string {
	matches, err := filepath.Glob(filepath.Join(dir, code+"-*.mp4"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	var primary, fallback string
	prefix := code + "-"
	for _, m := range matches {
		base := filepath.Base(m)
		lower := strings.ToLower(base)
		if strings.Contains(lower, ".hard.") ||
			strings.Contains(lower, ".withsubs.") ||
			strings.HasPrefix(lower, ".") {
			continue
		}
		// Primary layout: <code>-<codec>.mp4 (single hyphen after code, no extra dots in stem).
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		rest := strings.TrimPrefix(stem, prefix)
		if rest != "" && !strings.Contains(rest, ".") && !strings.Contains(rest, "-") {
			primary = m
			break
		}
		if fallback == "" {
			fallback = m
		}
	}
	if primary != "" {
		return primary
	}
	return fallback
}

// FindCompleteVideo is like FindExistingVideo but returns empty when a
// resumable `.segments` directory is present (download may be incomplete).
func FindCompleteVideo(dir, code string) string {
	existing := FindExistingVideo(dir, code)
	if existing == "" {
		return ""
	}
	if hasResumeState(dir) {
		return ""
	}
	return existing
}

func hasResumeState(dir string) bool {
	entries, err := os.ReadDir(filepath.Join(dir, ".segments"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "seg_") || name == ".source" {
			return true
		}
	}
	return false
}

// Plan is a resolved video ready for download.
type Plan struct {
	Code      string
	Title     string
	URL       string
	Duration  string
	Estimated int64
}

// PlanError marks a failure that produced a partial batch result.
type PlanError struct {
	Failed int
}

func (e *PlanError) Error() string {
	return fmt.Sprintf("%d video(s) failed", e.Failed)
}
