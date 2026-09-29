// Package media turns a downloaded video into a subtitled one through
// replaceable steps: extract audio → transcribe → translate → apply
// subtitles. Each step is an interface so implementations can change
// without touching the pipeline; translators are added via the translate
// registry.
package media

import (
	"context"
	"io"
	"os/exec"

	"github.com/jooservices/go-jabledownloader/internal/domain"
)

// Runner executes host binaries. It is the seam that keeps tests free of
// ffmpeg and Whisper.
type Runner interface {
	// Run executes name and returns its combined output.
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	// LookPath resolves a binary on PATH.
	LookPath(file string) (string, error)
}

// OSRunner is the production Runner backed by os/exec.
type OSRunner struct{}

// Run executes a command and returns its combined output.
func (OSRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// LookPath resolves a command using the host PATH.
func (OSRunner) LookPath(file string) (string, error) { return exec.LookPath(file) }

// StreamingRunner is an optional Runner capability: forward a command's
// output to out while it runs (long Whisper jobs show progress this way).
type StreamingRunner interface {
	RunStreaming(ctx context.Context, out io.Writer, name string, args ...string) error
}

// RunStreaming executes a command with stdout and stderr sent to out.
func (OSRunner) RunStreaming(ctx context.Context, out io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

// AudioExtractor extracts a speech-ready audio file from a video.
type AudioExtractor interface {
	Extract(ctx context.Context, video, outWAV string) error
}

// Transcriber turns speech into timed cues in the spoken language.
type Transcriber interface {
	Transcribe(ctx context.Context, audio, spokenLang string) ([]domain.Cue, error)
}

// DirectTranslator is an optional Transcriber capability: produce cues in
// a target language in one step (Whisper can do this for English).
type DirectTranslator interface {
	SupportsTarget(lang string) bool
	TranscribeTo(ctx context.Context, audio, spokenLang, targetLang string) ([]domain.Cue, error)
}

// Applier writes a copy of video with the subtitle file applied to out.
type Applier interface {
	Apply(ctx context.Context, video, srt, out string) error
}
