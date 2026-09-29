package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/media/srt"
	"github.com/jooservices/go-jabledownloader/internal/media/translate"
)

// TranslatorAuto lets the transcriber translate directly when it supports
// the target language (Whisper → English); otherwise a translator must be
// named explicitly.
const TranslatorAuto = "auto"

// languageRe accepts ISO 639 codes with an optional region ("en", "pt-br").
// The code becomes part of the sidecar file name and ffmpeg metadata.
var languageRe = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8})?$`)

// Options selects languages and the translator.
type Options struct {
	SpokenLang       string // empty = let the transcriber detect it
	TargetLang       string // empty = "en"
	Translator       string // empty = TranslatorAuto
	TranslatorConfig translate.Config
}

// Pipeline produces "<video stem>.<target>.srt" and applies it to the video.
type Pipeline struct {
	Audio       AudioExtractor
	ASR         Transcriber
	Applier     Applier
	Translators *translate.Registry // nil = translate.Default
	Options     Options
}

// Run subtitles video. An existing sidecar means a previous run succeeded,
// so Run skips (hard subtitles must never be burned twice) and reports it.
func (p *Pipeline) Run(ctx context.Context, video string) (srtPath string, skipped bool, err error) {
	opt, err := p.options()
	if err != nil {
		return "", false, err
	}
	srtPath = SidecarPath(video, opt.TargetLang)
	if _, err := os.Stat(srtPath); err == nil {
		return srtPath, true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("check subtitle sidecar: %w", err)
	}

	dir := filepath.Dir(video)
	base := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	audio := filepath.Join(dir, "."+base+".audio.wav")
	defer os.Remove(audio)
	if err := p.Audio.Extract(ctx, video, audio); err != nil {
		return "", false, fmt.Errorf("extract audio: %w", err)
	}
	cues, err := p.cues(ctx, audio, opt)
	if err != nil {
		return "", false, err
	}

	tmpSRT := filepath.Join(dir, "."+base+"."+opt.TargetLang+".srt.tmp")
	tmpVideo := filepath.Join(dir, "."+base+".subtitled"+filepath.Ext(video))
	defer os.Remove(tmpSRT)
	defer os.Remove(tmpVideo)
	if err := writeSRT(tmpSRT, cues); err != nil {
		return "", false, err
	}
	if err := p.Applier.Apply(ctx, video, tmpSRT, tmpVideo); err != nil {
		return "", false, fmt.Errorf("apply subtitles: %w", err)
	}
	// The sidecar marks success, so it is committed only together with the
	// video: if replacing the video fails, the sidecar is rolled back.
	if err := os.Rename(tmpSRT, srtPath); err != nil {
		return "", false, fmt.Errorf("write subtitle sidecar: %w", err)
	}
	if err := os.Rename(tmpVideo, video); err != nil {
		_ = os.Remove(srtPath)
		return "", false, fmt.Errorf("replace video with subtitled file: %w", err)
	}
	return srtPath, false, nil
}

// Validate checks the configuration without running anything, so a bad
// language or translator fails before a long download.
func (p *Pipeline) Validate() error {
	opt, err := p.options()
	if err != nil {
		return err
	}
	if opt.Translator != TranslatorAuto && !slices.Contains(p.registry().Names(), opt.Translator) {
		return fmt.Errorf("unknown translator %q (available: %s, %s)", opt.Translator, TranslatorAuto, strings.Join(p.registry().Names(), ", "))
	}
	return nil
}

// SidecarPath returns "<video stem>.<lang>.srt" next to video.
func SidecarPath(video, lang string) string {
	return strings.TrimSuffix(video, filepath.Ext(video)) + "." + lang + ".srt"
}

func (p *Pipeline) options() (Options, error) {
	if p.Audio == nil || p.ASR == nil || p.Applier == nil {
		return Options{}, errors.New("subtitle pipeline requires audio, transcriber, and applier")
	}
	opt := p.Options
	opt.SpokenLang = strings.ToLower(strings.TrimSpace(opt.SpokenLang))
	opt.TargetLang = strings.ToLower(strings.TrimSpace(opt.TargetLang))
	opt.Translator = strings.TrimSpace(opt.Translator)
	if opt.TargetLang == "" {
		opt.TargetLang = "en"
	}
	if opt.Translator == "" {
		opt.Translator = TranslatorAuto
	}
	if !languageRe.MatchString(opt.TargetLang) {
		return Options{}, fmt.Errorf("invalid subtitle language %q (use an ISO 639 code such as en or vi)", opt.TargetLang)
	}
	if opt.SpokenLang != "" && !languageRe.MatchString(opt.SpokenLang) {
		return Options{}, fmt.Errorf("invalid spoken language %q (use an ISO 639 code such as ja)", opt.SpokenLang)
	}
	return opt, nil
}

// cues transcribes audio into the target language.
func (p *Pipeline) cues(ctx context.Context, audio string, opt Options) ([]domain.Cue, error) {
	if opt.SpokenLang == opt.TargetLang {
		return p.transcribe(ctx, audio, opt.SpokenLang)
	}
	if opt.Translator == TranslatorAuto {
		direct, ok := p.ASR.(DirectTranslator)
		if !ok || !direct.SupportsTarget(opt.TargetLang) {
			return nil, fmt.Errorf("the transcriber cannot produce %q subtitles directly; choose a translator with --translator (available: %s)",
				opt.TargetLang, strings.Join(p.registry().Names(), ", "))
		}
		cues, err := direct.TranscribeTo(ctx, audio, opt.SpokenLang, opt.TargetLang)
		if err != nil {
			return nil, fmt.Errorf("transcribe to %s: %w", opt.TargetLang, err)
		}
		return cues, nil
	}

	if opt.SpokenLang == "" {
		return nil, errors.New("translation needs the spoken language: set --spoken-language")
	}
	translator, err := p.registry().New(opt.Translator, opt.TranslatorConfig)
	if err != nil {
		return nil, err
	}
	cues, err := p.transcribe(ctx, audio, opt.SpokenLang)
	if err != nil {
		return nil, err
	}
	translated, err := translator.Translate(ctx, cues, opt.SpokenLang, opt.TargetLang)
	if err != nil {
		return nil, fmt.Errorf("translate %s→%s with %s: %w", opt.SpokenLang, opt.TargetLang, translator.Name(), err)
	}
	return translated, nil
}

func (p *Pipeline) transcribe(ctx context.Context, audio, lang string) ([]domain.Cue, error) {
	cues, err := p.ASR.Transcribe(ctx, audio, lang)
	if err != nil {
		return nil, fmt.Errorf("transcribe audio: %w", err)
	}
	return cues, nil
}

func (p *Pipeline) registry() *translate.Registry {
	if p.Translators != nil {
		return p.Translators
	}
	return translate.Default
}

func writeSRT(path string, cues []domain.Cue) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create subtitle file: %w", err)
	}
	if err := srt.Format(file, cues); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close subtitle file: %w", err)
	}
	return nil
}
