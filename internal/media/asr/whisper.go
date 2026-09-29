// Package asr transcribes speech with MLX Whisper (Apple Silicon host).
package asr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/media"
	"github.com/jooservices/go-jabledownloader/internal/media/srt"
)

// DefaultModel reliably follows --task translate; whisper-large-v3-turbo
// often ignores it and emits Japanese.
const DefaultModel = "mlx-community/whisper-medium"

// Whisper implements media.Transcriber and media.DirectTranslator.
type Whisper struct {
	Runner media.Runner
	Model  string
	// Progress, when set and Runner supports streaming, receives Whisper's
	// live output (the CLI passes stderr under --verbose).
	Progress io.Writer
}

var (
	_ media.Transcriber      = (*Whisper)(nil)
	_ media.DirectTranslator = (*Whisper)(nil)
)

// New builds a Whisper transcriber; an empty model uses DefaultModel.
func New(runner media.Runner, model string) (*Whisper, error) {
	if runner == nil {
		return nil, errors.New("asr runner is required")
	}
	if model = strings.TrimSpace(model); model == "" {
		model = DefaultModel
	}
	return &Whisper{Runner: runner, Model: model}, nil
}

// SupportsTarget reports whether Whisper can translate directly into lang;
// its translate task only produces English.
func (w *Whisper) SupportsTarget(lang string) bool {
	return strings.EqualFold(strings.TrimSpace(lang), "en")
}

// Transcribe produces cues in the spoken language.
func (w *Whisper) Transcribe(ctx context.Context, audio, spokenLang string) ([]domain.Cue, error) {
	return w.run(ctx, audio, spokenLang, "transcribe")
}

// TranscribeTo translates speech straight into English.
func (w *Whisper) TranscribeTo(ctx context.Context, audio, spokenLang, targetLang string) ([]domain.Cue, error) {
	if !w.SupportsTarget(targetLang) {
		return nil, fmt.Errorf("whisper cannot translate directly into %q", targetLang)
	}
	cues, err := w.run(ctx, audio, spokenLang, "translate")
	if err != nil {
		return nil, err
	}
	return cues, checkEnglish(cues)
}

func (w *Whisper) run(ctx context.Context, audio, spokenLang, task string) ([]domain.Cue, error) {
	if strings.TrimSpace(audio) == "" {
		return nil, errors.New("audio path is required")
	}
	argv, err := w.command()
	if err != nil {
		return nil, err
	}
	// An isolated output dir guarantees the SRT read back is this run's.
	outDir, err := os.MkdirTemp("", "jabledownloader-whisper-*")
	if err != nil {
		return nil, fmt.Errorf("create whisper output dir: %w", err)
	}
	defer os.RemoveAll(outDir)

	const outputName = "transcription"
	args := append(argv[1:], audio,
		"--model", w.Model,
		"--task", task,
		"--output-format", "srt",
		"--output-dir", outDir,
		"--output-name", outputName,
		"--condition-on-previous-text", "False",
	)
	if lang := strings.TrimSpace(spokenLang); lang != "" {
		args = append(args, "--language", lang)
	}
	if err := w.exec(ctx, argv[0], args); err != nil {
		return nil, fmt.Errorf("mlx_whisper %s: %w", task, err)
	}
	return readSRT(filepath.Join(outDir, outputName+".srt"))
}

// exec runs Whisper, streaming its output when Progress is set.
func (w *Whisper) exec(ctx context.Context, name string, args []string) error {
	if streamer, ok := w.Runner.(media.StreamingRunner); ok && w.Progress != nil {
		return streamer.RunStreaming(ctx, w.Progress, name, append(args, "--verbose", "True")...)
	}
	output, err := w.Runner.Run(ctx, name, append(args, "--verbose", "False")...)
	if err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// command resolves mlx_whisper, falling back to `uvx --from mlx-whisper`.
func (w *Whisper) command() ([]string, error) {
	if path, err := w.Runner.LookPath("mlx_whisper"); err == nil {
		return []string{path}, nil
	}
	if path, err := w.Runner.LookPath("uvx"); err == nil {
		return []string{path, "--from", "mlx-whisper", "mlx_whisper"}, nil
	}
	return nil, errors.New("mlx_whisper not found on PATH (and uvx missing)\n\n" +
		"Install on this Mac host (run yourself):\n  uv tool install mlx-whisper\nor:\n  pipx install mlx-whisper")
}

func readSRT(path string) ([]domain.Cue, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("whisper did not produce %s: %w", filepath.Base(path), err)
	}
	defer file.Close()
	cues, err := srt.Parse(file)
	if err != nil {
		return nil, fmt.Errorf("parse whisper SRT: %w", err)
	}
	return cues, nil
}

// checkEnglish rejects output that is still mostly Japanese — the known
// failure when a model ignores --task translate.
func checkEnglish(cues []domain.Cue) error {
	var japanese, latin int
	for _, cue := range cues {
		for _, r := range cue.Text {
			switch {
			case r >= 0x3040 && r <= 0x30ff, r >= 0x3400 && r <= 0x9fff, r >= 0xff66 && r <= 0xff9f:
				japanese++
			case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
				latin++
			}
		}
	}
	if japanese > 0 && (latin == 0 || japanese*2 > latin) {
		return fmt.Errorf("subtitle output looks Japanese (jp_chars=%d en_chars=%d) — Whisper translate failed for this model; try --whisper-model %s", japanese, latin, DefaultModel)
	}
	return nil
}
