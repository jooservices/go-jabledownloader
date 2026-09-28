package subtitle

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls   [][]string
	filters string
	err     error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if len(args) == 2 && args[1] == "-filters" {
		return []byte(f.filters), nil
	}
	return []byte("ffmpeg said no"), f.err
}

func (f *fakeRunner) LookPath(file string) (string, error) { return "/usr/bin/" + file, nil }

func (f *fakeRunner) last() string { return strings.Join(f.calls[len(f.calls)-1], " ") }

func TestParseMode(t *testing.T) {
	for in, want := range map[string]string{"": ModeSoft, " SOFT ": ModeSoft, "hard": ModeHard} {
		if got, err := ParseMode(in); err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseMode("burn"); err == nil {
		t.Fatal("expected invalid mode error")
	}
}

func TestNewApplier(t *testing.T) {
	runner := &fakeRunner{}
	if a, err := NewApplier("", "vi", runner); err != nil || a.(*Soft).Lang != "vi" {
		t.Fatalf("soft = %v, %v", a, err)
	}
	if a, err := NewApplier("hard", "en", runner); err != nil || a.(*Hard).GOOS == "" {
		t.Fatalf("hard = %v, %v", a, err)
	}
	if _, err := NewApplier("burn", "en", runner); err == nil {
		t.Fatal("expected mode error")
	}
	if _, err := NewApplier("soft", "en", nil); err == nil {
		t.Fatal("expected runner error")
	}
}

func TestSoftApplyWritesLanguageMetadata(t *testing.T) {
	for lang, want := range map[string]string{"vi": "language=vie title=vi", "en": "language=eng title=English", "pt-br": "language=por", "fil": "language=fil"} {
		runner := &fakeRunner{}
		if err := (&Soft{Lang: lang, Runner: runner}).Apply(context.Background(), "in.mp4", "s.srt", "out.mp4"); err != nil {
			t.Fatal(err)
		}
		got := runner.last()
		for _, part := range strings.Split(want, " ") {
			if !strings.Contains(got, part) || !strings.Contains(got, "mov_text") {
				t.Errorf("lang %s: args %q missing %q", lang, got, part)
			}
		}
	}
}

func TestSoftApplyErrors(t *testing.T) {
	if err := (&Soft{Lang: "xx", Runner: &fakeRunner{}}).Apply(context.Background(), "in", "s", "out"); err == nil {
		t.Fatal("expected unsupported language error")
	}
	err := (&Soft{Lang: "en", Runner: &fakeRunner{err: errors.New("exit 1")}}).Apply(context.Background(), "in", "s", "out")
	if err == nil || !strings.Contains(err.Error(), "ffmpeg said no") {
		t.Fatalf("err = %v", err)
	}
}

func TestHardApplyUsesPlatformEncoder(t *testing.T) {
	for goos, want := range map[string]string{"darwin": "h264_videotoolbox", "linux": "libx264"} {
		runner := &fakeRunner{filters: " ... subtitles         V->V       Render text subtitles"}
		if err := (&Hard{Runner: runner, GOOS: goos}).Apply(context.Background(), "in.mp4", "sub:s[1].srt", "out.mp4"); err != nil {
			t.Fatal(err)
		}
		if got := runner.last(); !strings.Contains(got, want) || !strings.Contains(got, `sub\:s\[1\].srt`) {
			t.Errorf("%s args = %q", goos, got)
		}
	}
}

func TestHardApplyErrors(t *testing.T) {
	if err := (&Hard{Runner: &fakeRunner{}}).Apply(context.Background(), "in", "s", "out"); !errors.Is(err, errNoLibass) {
		t.Fatalf("err = %v", err)
	}
	runner := &fakeRunner{filters: " subtitles\tV->V", err: errors.New("exit 1")}
	if err := (&Hard{Runner: runner}).Apply(context.Background(), "in", "s", "out"); err == nil || !strings.Contains(err.Error(), "burn hard subtitles") {
		t.Fatalf("err = %v", err)
	}
}

func TestEscapeFilterPath(t *testing.T) {
	if got := escapeFilterPath(`/a:b/it's[1]`); got != `/a\:b/it\'s\[1\]` {
		t.Fatal(got)
	}
}
