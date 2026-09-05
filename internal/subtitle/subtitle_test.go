package subtitle

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMode(t *testing.T) {
	soft, err := ParseMode("")
	if err != nil || soft != ModeSoft {
		t.Fatalf("empty => soft, got %q %v", soft, err)
	}
	hard, err := ParseMode("HARD")
	if err != nil || hard != ModeHard {
		t.Fatalf("HARD => hard, got %q %v", hard, err)
	}
	if _, err := ParseMode("burn"); err == nil {
		t.Fatal("expected error for burn")
	}
}

func TestResolveWhisperPrefersBinary(t *testing.T) {
	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	lookPath = func(file string) (string, error) {
		if file == "mlx_whisper" {
			return "/tmp/mlx_whisper", nil
		}
		return "", exec.ErrNotFound
	}
	got, err := resolveWhisper()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "/tmp/mlx_whisper" {
		t.Fatalf("got %#v", got)
	}
}

func TestResolveWhisperFallsBackToUvx(t *testing.T) {
	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	lookPath = func(file string) (string, error) {
		if file == "uvx" {
			return "/usr/bin/uvx", nil
		}
		return "", exec.ErrNotFound
	}
	got, err := resolveWhisper()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/uvx", "--from", "mlx-whisper", "mlx_whisper"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestResolveWhisperMissing(t *testing.T) {
	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	_, err := resolveWhisper()
	if err == nil || !strings.Contains(err.Error(), "uv tool install mlx-whisper") {
		t.Fatalf("expected install hint, got %v", err)
	}
}

func TestResolveEnglishSRTLegacy(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "clip.srt")
	dest := filepath.Join(dir, "clip.en.srt")
	preferred := filepath.Join(dir, "clip-en.srt")
	if err := os.WriteFile(legacy, []byte("1\n00:00:00,000 --> 00:00:01,000\nHi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := resolveEnglishSRT(preferred, legacy, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy should be renamed away: %v", err)
	}
}

func TestResolveEnglishSRTMissing(t *testing.T) {
	dir := t.TempDir()
	err := resolveEnglishSRT(filepath.Join(dir, "a.srt"), filepath.Join(dir, "b.srt"), filepath.Join(dir, "c.en.srt"))
	if err == nil || !strings.Contains(err.Error(), "did not produce") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateEnglishSRT(t *testing.T) {
	dir := t.TempDir()
	en := filepath.Join(dir, "ok.srt")
	jp := filepath.Join(dir, "bad.srt")
	if err := os.WriteFile(en, []byte("1\n00:00:00,000 --> 00:00:01,000\nHello there\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jp, []byte("1\n00:00:00,000 --> 00:00:01,000\nこんにちは\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateEnglishSRT(en); err != nil {
		t.Fatal(err)
	}
	if err := validateEnglishSRT(jp); err == nil {
		t.Fatal("expected Japanese rejection")
	}
}

func TestEscapeFilterPath(t *testing.T) {
	got := escapeFilterPath(`/tmp/foo:bar's.srt`)
	if !strings.Contains(got, `\:`) || !strings.Contains(got, `\'`) {
		t.Fatalf("expected escapes: %q", got)
	}
	if strings.Contains(got, "'") && !strings.Contains(got, `\'`) {
		t.Fatalf("unexpected raw quote: %q", got)
	}
}

func TestMuxSoftSubs(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "clip.mp4")
	srt := filepath.Join(dir, "clip.en.srt")
	out := filepath.Join(dir, "out.mp4")

	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=320x240:d=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", "-y", video)
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate video: %v\n%s", err, outBytes)
	}
	if err := os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,000\nHello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := muxSoftSubs(context.Background(), video, srt, out); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("expected muxed output: %v", err)
	}
	probe := exec.Command("ffprobe", "-v", "error", "-select_streams", "s",
		"-show_entries", "stream=codec_name:stream_tags=language",
		"-of", "csv=p=0", out)
	probeOut, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v\n%s", err, probeOut)
	}
	if !strings.Contains(string(probeOut), "mov_text") {
		t.Fatalf("expected mov_text subtitle stream, got %q", probeOut)
	}
}

func TestBurnHardSubs(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	if !ffmpegHasFilter("subtitles") {
		t.Skip("ffmpeg has no subtitles filter (libass); hard burn-in unsupported on this build")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "clip.mp4")
	srt := filepath.Join(dir, "clip.en.srt")
	out := filepath.Join(dir, "out.mp4")

	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=320x240:d=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", "-y", video)
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate video: %v\n%s", err, outBytes)
	}
	if err := os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,000\nHello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := burnHardSubs(context.Background(), video, srt, out); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("expected burned output: %v", err)
	}
}

func TestBurnHardSubsRequiresFilter(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	if ffmpegHasFilter("subtitles") {
		t.Skip("this ffmpeg has subtitles; cannot assert missing-filter error")
	}
	err := burnHardSubs(context.Background(), "x.mp4", "x.srt", "o.mp4")
	if err == nil || !strings.Contains(err.Error(), "--subtitle-mode soft") {
		t.Fatalf("expected libass hint, got %v", err)
	}
}

func TestExtractAudio(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "clip.mp4")
	audio := filepath.Join(dir, "clip.wav")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:a", "aac", "-y", video)
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate audio mp4: %v\n%s", err, outBytes)
	}
	if err := extractAudio(context.Background(), video, audio); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(audio)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("expected wav: %v", err)
	}
}
