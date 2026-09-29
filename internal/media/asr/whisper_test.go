package asr

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWhisperTranscribeUsesLanguageAndParsesSRT(t *testing.T) {
	runner := &fakeRunner{srt: "1\n00:00:00,000 --> 00:00:01,000\nHello\n"}
	w, _ := New(runner, "model")
	cues, err := w.Transcribe(context.Background(), "audio.wav", "ja")
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 1 || cues[0].Text != "Hello" {
		t.Fatalf("got %#v", cues)
	}
	args := strings.Join(runner.args, " ")
	for _, want := range []string{"--task transcribe", "--language ja", "--model model"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q do not contain %q", args, want)
		}
	}
}

func TestWhisperTranscribeToEnglishUsesTranslate(t *testing.T) {
	runner := &fakeRunner{srt: "1\n00:00:00,000 --> 00:00:01,000\nHello world\n"}
	cues, err := (&Whisper{Runner: runner}).TranscribeTo(context.Background(), "audio.wav", "ja", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 1 || !strings.Contains(strings.Join(runner.args, " "), "--task translate") {
		t.Fatalf("cues=%#v args=%q", cues, runner.args)
	}
}

func TestWhisperRejectsJapaneseDirectEnglishOutput(t *testing.T) {
	runner := &fakeRunner{srt: "1\n00:00:00,000 --> 00:00:01,000\nこんにちは世界\n"}
	_, err := (&Whisper{Runner: runner}).TranscribeTo(context.Background(), "audio.wav", "ja", "en")
	if err == nil || !strings.Contains(err.Error(), "looks Japanese") {
		t.Fatalf("expected Japanese validation error, got %v", err)
	}
}

func TestWhisperSupportsEnglishOnly(t *testing.T) {
	w := &Whisper{}
	if !w.SupportsTarget(" EN ") || w.SupportsTarget("vi") {
		t.Fatal("unexpected target support")
	}
}

func TestWhisperFallsBackToUvx(t *testing.T) {
	runner := &fakeRunner{uvx: "/usr/bin/uvx", srt: "1\n00:00:00,000 --> 00:00:01,000\nHello\n"}
	if _, err := (&Whisper{Runner: runner}).Transcribe(context.Background(), "audio.wav", "ja"); err != nil {
		t.Fatal(err)
	}
	if runner.name != "/usr/bin/uvx" || len(runner.args) < 4 || strings.Join(runner.args[:3], " ") != "--from mlx-whisper mlx_whisper" {
		t.Fatalf("unexpected command: %q %q", runner.name, runner.args)
	}
}

func TestWhisperReportsRunnerFailure(t *testing.T) {
	runner := &fakeRunner{err: errors.New("failed")}
	_, err := (&Whisper{Runner: runner}).Transcribe(context.Background(), "audio.wav", "ja")
	if err == nil || !strings.Contains(err.Error(), "mlx_whisper transcribe") {
		t.Fatalf("got %v", err)
	}
}

func TestWhisperReportsMissingOrInvalidOutput(t *testing.T) {
	if _, err := (&Whisper{Runner: &fakeRunner{skipWrite: true}}).Transcribe(context.Background(), "a.wav", ""); err == nil || !strings.Contains(err.Error(), "did not produce") {
		t.Fatalf("missing output err = %v", err)
	}
	if _, err := (&Whisper{Runner: &fakeRunner{srt: "x\nbroken\n"}}).Transcribe(context.Background(), "a.wav", ""); err == nil || !strings.Contains(err.Error(), "parse whisper SRT") {
		t.Fatalf("invalid output err = %v", err)
	}
}

func TestWhisperValidationAndResolutionErrors(t *testing.T) {
	if _, err := New(nil, ""); err == nil {
		t.Fatal("expected runner error")
	}
	runner := &fakeRunner{missing: true}
	w, err := New(runner, " ")
	if err != nil || w.Model != DefaultModel {
		t.Fatalf("w=%+v err=%v", w, err)
	}
	if _, err := w.Transcribe(context.Background(), "", "ja"); err == nil {
		t.Fatal("expected audio path error")
	}
	if _, err := w.Transcribe(context.Background(), "audio.wav", "ja"); err == nil {
		t.Fatal("expected missing binary error")
	}
	if _, err := w.TranscribeTo(context.Background(), "audio.wav", "ja", "vi"); err == nil {
		t.Fatal("expected unsupported target error")
	}
}

type fakeRunner struct {
	name      string
	args      []string
	uvx       string
	srt       string
	err       error
	missing   bool
	skipWrite bool
}

func (f *fakeRunner) LookPath(file string) (string, error) {
	if f.missing {
		return "", os.ErrNotExist
	}
	if file == "mlx_whisper" && f.uvx == "" {
		return "/usr/bin/mlx_whisper", nil
	}
	if file == "uvx" && f.uvx != "" {
		return f.uvx, nil
	}
	return "", os.ErrNotExist
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.name = name
	f.args = append([]string(nil), args...)
	if f.err != nil {
		return nil, f.err
	}
	for i := 0; i < len(args)-1 && !f.skipWrite; i++ {
		if args[i] == "--output-dir" {
			if err := os.WriteFile(filepath.Join(args[i+1], "transcription.srt"), []byte(f.srt), 0o600); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

type streamingRunner struct {
	fakeRunner
	streamed bool
}

func (s *streamingRunner) RunStreaming(ctx context.Context, out io.Writer, name string, args ...string) error {
	s.streamed = true
	_, _ = io.WriteString(out, "[00:01.000 --> 00:02.000] Hello\n")
	_, err := s.Run(ctx, name, args...)
	return err
}

func TestWhisperStreamsProgressWhenRequested(t *testing.T) {
	runner := &streamingRunner{fakeRunner: fakeRunner{srt: "1\n00:00:00,000 --> 00:00:01,000\nHello\n"}}
	var progress strings.Builder
	w := &Whisper{Runner: runner, Progress: &progress}

	if _, err := w.Transcribe(context.Background(), "audio.wav", "ja"); err != nil {
		t.Fatal(err)
	}
	if !runner.streamed || !strings.Contains(progress.String(), "Hello") || !strings.Contains(strings.Join(runner.args, " "), "--verbose True") {
		t.Fatalf("streamed=%v progress=%q args=%q", runner.streamed, progress.String(), runner.args)
	}

	quiet := &streamingRunner{fakeRunner: fakeRunner{srt: "1\n00:00:00,000 --> 00:00:01,000\nHello\n"}}
	if _, err := (&Whisper{Runner: quiet}).Transcribe(context.Background(), "audio.wav", "ja"); err != nil || quiet.streamed {
		t.Fatalf("without Progress must not stream: streamed=%v err=%v", quiet.streamed, err)
	}
}
