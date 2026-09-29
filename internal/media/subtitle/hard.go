package subtitle

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jooservices/go-jabledownloader/internal/media"
)

var errNoLibass = errors.New("hard subtitle burn-in needs ffmpeg built with libass (subtitles filter).\n\n" +
	"Your current ffmpeg has no 'subtitles' filter.\n" +
	"Install/rebuild yourself (agents must not install), then confirm:\n" +
	"  ffmpeg -hide_banner -filters | grep subtitles\n" +
	"On macOS Homebrew, use a formula build that enables libass, or install a full static build.\n" +
	"Until then use: --subtitle-mode soft")

// Hard burns subtitles into video pixels using ffmpeg's libass filter.
type Hard struct {
	Runner media.Runner
	GOOS   string // selects the encoder: videotoolbox on darwin, libx264 elsewhere
}

// Apply burns srt into video and writes out.
func (h *Hard) Apply(ctx context.Context, video, srt, out string) error {
	filters, _ := h.Runner.Run(ctx, "ffmpeg", "-hide_banner", "-filters")
	if !hasFilter(filters, "subtitles") {
		return errNoLibass
	}
	abs, err := filepath.Abs(srt)
	if err != nil {
		return fmt.Errorf("resolve srt path: %w", err)
	}
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", video,
		"-vf", "subtitles=filename=" + escapeFilterPath(abs) + `:force_style=FontSize=20\,Outline=1\,Shadow=0`,
		"-c:a", "copy",
	}
	if h.GOOS == "darwin" {
		args = append(args, "-c:v", "h264_videotoolbox", "-b:v", "8M")
	} else {
		args = append(args, "-c:v", "libx264", "-crf", "18", "-preset", "medium")
	}
	output, err := h.Runner.Run(ctx, "ffmpeg", append(args, out)...)
	if err != nil {
		return fmt.Errorf("ffmpeg burn hard subtitles: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// hasFilter scans `ffmpeg -filters` output (" ... subtitles   V->V ...").
func hasFilter(output []byte, name string) bool {
	text := string(output)
	return strings.Contains(text, " "+name+" ") || strings.Contains(text, " "+name+"\t")
}

// escapeFilterPath escapes a path for an ffmpeg filtergraph option.
func escapeFilterPath(path string) string {
	return strings.NewReplacer(`\`, `\\`, `:`, `\:`, `'`, `\'`, `[`, `\[`, `]`, `\]`).Replace(filepath.ToSlash(path))
}
