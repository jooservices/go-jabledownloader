// Package subtitle applies an SRT file to a video with ffmpeg: as a
// selectable track (soft) or burned into the pixels (hard).
package subtitle

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/jooservices/go-jabledownloader/internal/media"
)

// Subtitle modes.
const (
	ModeSoft = "soft"
	ModeHard = "hard"
)

// ParseMode validates a --subtitle-mode value; empty means soft.
func ParseMode(raw string) (string, error) {
	switch mode := strings.ToLower(strings.TrimSpace(raw)); mode {
	case "", ModeSoft:
		return ModeSoft, nil
	case ModeHard:
		return ModeHard, nil
	default:
		return "", fmt.Errorf("invalid --subtitle-mode %q (use soft or hard)", raw)
	}
}

// NewApplier returns the applier for mode. lang is the subtitle language
// (ISO 639-1, e.g. "en") recorded in the soft track metadata.
func NewApplier(mode, lang string, runner media.Runner) (media.Applier, error) {
	mode, err := ParseMode(mode)
	if err != nil {
		return nil, err
	}
	if runner == nil {
		return nil, fmt.Errorf("subtitle runner is required")
	}
	if mode == ModeHard {
		return &Hard{Runner: runner, GOOS: runtime.GOOS}, nil
	}
	return &Soft{Lang: lang, Runner: runner}, nil
}
