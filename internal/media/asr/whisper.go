// Package asr transcribes speech with a host Whisper CLI.
// Apple Silicon uses MLX when mlx_whisper is installed. Every other machine
// uses the portable openai-whisper CLI.
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

// DefaultModel is the MLX model used when that backend is selected and the
// caller did not pass one. The portable CLI uses its own default ("medium").
const DefaultModel = "mlx-community/whisper-medium"

// Whisper implements media.Transcriber and media.DirectTranslator.
type Whisper struct {
	Runner media.Runner
	Model  string
	// Backend is auto, mlx, or whisper. Empty means auto.
	Backend string
	// GOOS and GOARCH select the auto backend. Empty uses the current process.
	GOOS, GOARCH string
	// Progress, when set and Runner supports streaming, receives Whisper's
	// live output (the CLI passes stderr under --verbose).
	Progress io.Writer
}

var (
	_ media.Transcriber      = (*Whisper)(nil)
	_ media.DirectTranslator = (*Whisper)(nil)
)

// New builds a Whisper transcriber. An empty model uses the selected
// backend's default. backend is validated even when transcription is not run.
func New(runner media.Runner, model, backend string) (*Whisper, error) {
	if runner == nil {
		return nil, errors.New("asr runner is required")
	}
	choice, err := ParseBackend(backend)
	if err != nil {
		return nil, err
	}
	return &Whisper{
		Runner:  runner,
		Model:   strings.TrimSpace(model),
		Backend: choice,
	}, nil
}

// SupportsTarget reports whether Whisper can translate directly into lang;
// its translate task only produces English.
func (w *Whisper) SupportsTarget(lang string) bool {
	return strings.EqualFold(strings.TrimSpace(lang), "en")
}

// Transcribe produces cues in the spoken language.
func (w *Whisper) Transcribe(ctx context.Context, audio, spokenLang string) ([]domain.Cue, error) {
	cues, _, err := w.run(ctx, audio, spokenLang, "transcribe")
	return cues, err
}

// TranscribeTo translates speech straight into English.
func (w *Whisper) TranscribeTo(ctx context.Context, audio, spokenLang, targetLang string) ([]domain.Cue, error) {
	if !w.SupportsTarget(targetLang) {
		return nil, fmt.Errorf("whisper cannot translate directly into %q", targetLang)
	}
	cues, engine, err := w.run(ctx, audio, spokenLang, "translate")
	if err != nil {
		return nil, err
	}
	return cues, checkEnglish(cues, engine.defaultModel())
}

func (w *Whisper) run(ctx context.Context, audio, spokenLang, task string) ([]domain.Cue, speechEngine, error) {
	if strings.TrimSpace(audio) == "" {
		return nil, nil, errors.New("audio path is required")
	}
	engine, argv, err := w.prepared()
	if err != nil {
		return nil, nil, err
	}
	outDir, err := os.MkdirTemp("", "jabledownloader-whisper-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create whisper output dir: %w", err)
	}
	defer os.RemoveAll(outDir)

	model := w.Model
	if model == "" {
		model = engine.defaultModel()
	}
	args := append(append([]string{}, argv[1:]...), engine.args(audio, model, task, outDir, spokenLang)...)
	if err := w.exec(ctx, argv[0], args); err != nil {
		return nil, nil, fmt.Errorf("%s %s: %w", engine.name(), task, err)
	}
	cues, err := readSRT(engine.srtPath(outDir, audio))
	return cues, engine, err
}

// prepared resolves the backend and its argv. Explicit mlx or whisper never
// substitutes the other CLI.
func (w *Whisper) prepared() (speechEngine, []string, error) {
	choice := w.Backend
	if choice == "" {
		choice = BackendAuto
	}
	engine, err := selectEngine(choice, w.GOOS, w.GOARCH, w.Runner)
	if err != nil {
		return nil, nil, err
	}
	argv, err := engine.locate(w.Runner)
	if err != nil {
		return nil, nil, err
	}
	return engine, argv, nil
}

// exec runs Whisper, streaming its output when Progress is set.
func (w *Whisper) exec(ctx context.Context, name string, args []string) error {
	verbose := "False"
	if streamer, ok := w.Runner.(media.StreamingRunner); ok && w.Progress != nil {
		verbose = "True"
		args = append(args, "--verbose", verbose)
		return streamer.RunStreaming(ctx, w.Progress, name, args...)
	}
	output, err := w.Runner.Run(ctx, name, append(args, "--verbose", verbose)...)
	if err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(output)))
	}
	return nil
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
// failure when a model ignores --task translate. model is the default for
// the backend that produced the cues.
func checkEnglish(cues []domain.Cue, model string) error {
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
		return fmt.Errorf("subtitle output looks Japanese (jp_chars=%d en_chars=%d) — Whisper translate failed for this model; try --whisper-model %s", japanese, latin, model)
	}
	return nil
}
