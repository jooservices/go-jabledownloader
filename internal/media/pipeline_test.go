package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/media/translate"
)

type audioFake struct {
	calls int
	err   error
}

func (f *audioFake) Extract(context.Context, string, string) error {
	f.calls++
	return f.err
}

type asrFake struct {
	transcribed, direct int
	spoken              string
	err                 error
}

func (f *asrFake) Transcribe(_ context.Context, _, lang string) ([]domain.Cue, error) {
	f.transcribed++
	f.spoken = lang
	return []domain.Cue{{Start: time.Second, End: 2 * time.Second, Text: "spoken"}}, f.err
}

// directASR adds Whisper's direct-to-English capability.
type directASR struct{ asrFake }

func (f *directASR) SupportsTarget(lang string) bool { return lang == "en" }

func (f *directASR) TranscribeTo(context.Context, string, string, string) ([]domain.Cue, error) {
	f.direct++
	return []domain.Cue{{Start: time.Second, End: 2 * time.Second, Text: "English"}}, f.err
}

type applierFake struct {
	calls int
	err   error
}

func (f *applierFake) Apply(_ context.Context, _, srtPath, out string) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	data, err := os.ReadFile(srtPath)
	if err != nil {
		return err
	}
	return os.WriteFile(out, append([]byte("video+"), data...), 0o600)
}

type prefixTranslator struct{}

func (prefixTranslator) Name() string { return "prefix" }

func (prefixTranslator) Translate(_ context.Context, cues []domain.Cue, from, to string) ([]domain.Cue, error) {
	out := append([]domain.Cue(nil), cues...)
	for i := range out {
		out[i].Text = from + "->" + to + ":" + out[i].Text
	}
	return out, nil
}

type setup struct {
	pipeline *Pipeline
	audio    *audioFake
	asr      *directASR
	applier  *applierFake
	video    string
}

func newSetup(t *testing.T, opt Options) setup {
	t.Helper()
	video := filepath.Join(t.TempDir(), "abc-1-h264.mp4")
	if err := os.WriteFile(video, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := translate.NewRegistry()
	registry.Register("prefix", func(translate.Config) (translate.Translator, error) { return prefixTranslator{}, nil })
	s := setup{audio: &audioFake{}, asr: &directASR{}, applier: &applierFake{}, video: video}
	s.pipeline = &Pipeline{Audio: s.audio, ASR: s.asr, Applier: s.applier, Translators: registry, Options: opt}
	return s
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRunDefaultsToDirectEnglish(t *testing.T) {
	s := newSetup(t, Options{SpokenLang: "ja"})

	srtPath, skipped, err := s.pipeline.Run(context.Background(), s.video)

	if err != nil || skipped {
		t.Fatalf("skipped=%v err=%v", skipped, err)
	}
	if s.asr.direct != 1 || s.asr.transcribed != 0 || s.applier.calls != 1 {
		t.Fatalf("calls direct=%d transcribe=%d apply=%d", s.asr.direct, s.asr.transcribed, s.applier.calls)
	}
	if srtPath != strings.TrimSuffix(s.video, ".mp4")+".en.srt" || !strings.Contains(readFile(t, srtPath), "English") {
		t.Fatalf("sidecar %q", srtPath)
	}
	if !strings.HasPrefix(readFile(t, s.video), "video+1\n") {
		t.Fatal("video was not replaced with the subtitled copy")
	}
	entries, _ := os.ReadDir(filepath.Dir(s.video))
	if len(entries) != 2 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestRunWithNamedTranslator(t *testing.T) {
	s := newSetup(t, Options{SpokenLang: "ja", TargetLang: "vi", Translator: "prefix"})

	srtPath, _, err := s.pipeline.Run(context.Background(), s.video)

	if err != nil {
		t.Fatal(err)
	}
	if s.asr.transcribed != 1 || s.asr.direct != 0 || s.asr.spoken != "ja" {
		t.Fatalf("transcribe=%d direct=%d spoken=%q", s.asr.transcribed, s.asr.direct, s.asr.spoken)
	}
	if !strings.HasSuffix(srtPath, ".vi.srt") || !strings.Contains(readFile(t, srtPath), "ja->vi:spoken") {
		t.Fatalf("sidecar %q", srtPath)
	}
}

func TestRunSameLanguageSkipsTranslation(t *testing.T) {
	s := newSetup(t, Options{SpokenLang: "en", TargetLang: "en"})

	if _, _, err := s.pipeline.Run(context.Background(), s.video); err != nil {
		t.Fatal(err)
	}
	if s.asr.transcribed != 1 || s.asr.direct != 0 {
		t.Fatalf("transcribe=%d direct=%d", s.asr.transcribed, s.asr.direct)
	}
}

func TestRunSkipsWhenSidecarExists(t *testing.T) {
	s := newSetup(t, Options{})
	existing := SidecarPath(s.video, "en")
	if err := os.WriteFile(existing, []byte("done"), 0o600); err != nil {
		t.Fatal(err)
	}

	srtPath, skipped, err := s.pipeline.Run(context.Background(), s.video)

	if err != nil || !skipped || srtPath != existing || s.audio.calls != 0 {
		t.Fatalf("srt=%q skipped=%v err=%v audio=%d", srtPath, skipped, err, s.audio.calls)
	}
}

func TestRunErrors(t *testing.T) {
	boom := errors.New("boom")
	for name, tc := range map[string]struct {
		opt    Options
		mutate func(*setup)
		want   string
	}{
		"auto without direct support": {Options{SpokenLang: "ja", TargetLang: "vi"}, nil, "choose a translator with --translator (available: prefix)"},
		"unknown translator":          {Options{SpokenLang: "ja", TargetLang: "vi", Translator: "missing"}, nil, "unknown translator"},
		"translation without spoken":  {Options{TargetLang: "vi", Translator: "prefix"}, nil, "--spoken-language"},
		"invalid target":              {Options{TargetLang: "../x"}, nil, "invalid subtitle language"},
		"invalid spoken":              {Options{SpokenLang: "JA JA"}, nil, "invalid spoken language"},
		"audio failure":               {Options{}, func(s *setup) { s.audio.err = boom }, "extract audio"},
		"transcribe failure":          {Options{SpokenLang: "en", TargetLang: "en"}, func(s *setup) { s.asr.err = boom }, "transcribe audio"},
		"direct failure":              {Options{}, func(s *setup) { s.asr.err = boom }, "transcribe to en"},
		"apply failure":               {Options{}, func(s *setup) { s.applier.err = boom }, "apply subtitles"},
		"missing steps":               {Options{}, func(s *setup) { s.pipeline.Applier = nil }, "requires audio"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSetup(t, tc.opt)
			if tc.mutate != nil {
				tc.mutate(&s)
			}

			_, _, err := s.pipeline.Run(context.Background(), s.video)

			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if readFile(t, s.video) != "video" {
				t.Fatal("original video modified on failure")
			}
			if _, statErr := os.Stat(SidecarPath(s.video, "en")); statErr == nil {
				t.Fatal("sidecar written on failure")
			}
		})
	}
}

func TestRunUsesDefaultRegistry(t *testing.T) {
	s := newSetup(t, Options{SpokenLang: "ja", TargetLang: "ja", Translator: "none"})
	s.pipeline.Translators = nil

	if _, _, err := s.pipeline.Run(context.Background(), s.video); err != nil {
		t.Fatal(err)
	}
}

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		opt  Options
		want string
	}{
		{Options{}, ""},
		{Options{TargetLang: "vi", Translator: "prefix"}, ""},
		{Options{Translator: "missing"}, `unknown translator "missing" (available: auto, prefix)`},
		{Options{TargetLang: "EN US"}, "invalid subtitle language"},
	} {
		err := newSetup(t, tc.opt).pipeline.Validate()
		if (tc.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("Validate(%+v) = %v, want %q", tc.opt, err, tc.want)
		}
	}
}
