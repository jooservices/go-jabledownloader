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
// dir, returning its path.
func FindExistingVideo(dir, code string) string {
	matches, err := filepath.Glob(filepath.Join(dir, code+"-*.mp4"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
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
