// Package ui renders CLI output. It holds no business logic: services and
// commands feed it values, it formats them for the terminal.
package ui

import (
	"os"
	"strconv"
)

// ANSI escape sequences and glyph icons used by the terminal UI. Output is
// sanitized by StdWriter when colors are disabled.
const (
	ColorReset  = "\033[0m"
	ColorBold   = "\033[1m"
	ColorDim    = "\033[2m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorCyan   = "\033[36m"
	ColorWhite  = "\033[37m"

	IconOk    = "✓"
	IconSkip  = "○"
	IconErr   = "✗"
	IconArrow = "→"
	IconVideo = "🎬"
	IconClock = "⏱"
	IconDisk  = "💾"
	IconSpark = "✨"
)

// ColorEnabled reports whether ANSI colors should be emitted. Honors
// NO_COLOR (https://no-color.org), TERM=dumb, and an explicit override.
func ColorEnabled(out *os.File) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	if force, ok := os.LookupEnv("FORCE_COLOR"); ok {
		if b, err := strconv.ParseBool(force); err == nil {
			return b
		}
	}
	fi, err := out.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
