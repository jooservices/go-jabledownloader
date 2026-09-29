// Package audio provides audio extraction for the media pipeline.
package audio

import (
	"context"
	"fmt"
	"strings"

	"github.com/jooservices/go-jabledownloader/internal/media"
)

// FFmpeg extracts speech-ready mono PCM audio at 16 kHz.
type FFmpeg struct {
	Runner media.Runner
}

var _ media.AudioExtractor = (*FFmpeg)(nil)

// New constructs an FFmpeg extractor using runner.
func New(runner media.Runner) (*FFmpeg, error) {
	if runner == nil {
		return nil, fmt.Errorf("audio runner is required")
	}
	return &FFmpeg{Runner: runner}, nil
}

// Extract writes a 16 kHz mono PCM WAV to outWAV.
func (f *FFmpeg) Extract(ctx context.Context, video, outWAV string) error {
	if strings.TrimSpace(video) == "" || strings.TrimSpace(outWAV) == "" {
		return fmt.Errorf("video and output WAV paths are required")
	}
	out, err := f.Runner.Run(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-y", "-i", video,
		"-vn", "-ac", "1", "-ar", "16000",
		"-c:a", "pcm_s16le",
		outWAV,
	)
	if err != nil {
		return fmt.Errorf("ffmpeg extract audio: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
