package audio

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestFFmpegExtractUsesSpeechAudioArgs(t *testing.T) {
	runner := &fakeRunner{}
	if err := (&FFmpeg{Runner: runner}).Extract(context.Background(), "video.mp4", "audio.wav"); err != nil {
		t.Fatal(err)
	}
	if runner.name != "ffmpeg" {
		t.Fatalf("command %q, want ffmpeg", runner.name)
	}
	got := strings.Join(runner.args, " ")
	for _, want := range []string{"-vn", "-ac 1", "-ar 16000", "-c:a pcm_s16le", "audio.wav"} {
		if !strings.Contains(got, want) {
			t.Errorf("args %q do not contain %q", got, want)
		}
	}
}

func TestFFmpegExtractReportsRunnerOutput(t *testing.T) {
	runner := &fakeRunner{output: []byte("bad input"), err: errors.New("exit status 1")}
	err := (&FFmpeg{Runner: runner}).Extract(context.Background(), "video.mp4", "audio.wav")
	if err == nil || !strings.Contains(err.Error(), "bad input") {
		t.Fatalf("expected runner output, got %v", err)
	}
}

func TestFFmpegValidatesInputsAndRunner(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("expected runner error")
	}
	if f, err := New(&fakeRunner{}); err != nil || f.Runner == nil {
		t.Fatalf("New = %v, %v", f, err)
	}
	f := &FFmpeg{Runner: &fakeRunner{}}
	if err := f.Extract(context.Background(), "", "audio.wav"); err == nil {
		t.Fatal("expected video path error")
	}
	if err := f.Extract(context.Background(), "video.mp4", ""); err == nil {
		t.Fatal("expected output path error")
	}
}

type fakeRunner struct {
	name   string
	args   []string
	output []byte
	err    error
}

func (f *fakeRunner) LookPath(file string) (string, error) {
	if file == "ffmpeg" {
		return "/usr/bin/ffmpeg", nil
	}
	return "", os.ErrNotExist
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.name = name
	f.args = append([]string(nil), args...)
	return f.output, f.err
}
