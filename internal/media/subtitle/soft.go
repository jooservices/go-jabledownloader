package subtitle

import (
	"context"
	"fmt"
	"strings"

	"github.com/jooservices/go-jabledownloader/internal/media"
)

// Soft muxes an SRT file into a video as a mov_text subtitle track.
type Soft struct {
	Lang   string
	Runner media.Runner
}

// Apply muxes srt into video and writes out.
func (s *Soft) Apply(ctx context.Context, video, srt, out string) error {
	code, err := iso6392Code(s.Lang)
	if err != nil {
		return err
	}
	output, err := s.Runner.Run(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", video, "-i", srt,
		"-map", "0", "-map", "1",
		"-c", "copy", "-c:s", "mov_text",
		"-metadata:s:s:0", "language="+code,
		"-metadata:s:s:0", "title="+trackTitle(s.Lang),
		out,
	)
	if err != nil {
		return fmt.Errorf("ffmpeg mux soft subtitles: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func trackTitle(lang string) string {
	if strings.EqualFold(strings.TrimSpace(lang), "en") {
		return "English"
	}
	return strings.ToLower(strings.TrimSpace(lang))
}

// iso6392Code maps an ISO 639-1 code (optionally with a region) to the
// three-letter code MP4 metadata uses; three-letter codes pass through.
func iso6392Code(lang string) (string, error) {
	primary, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(lang)), "-")
	if code, ok := iso6392[primary]; ok {
		return code, nil
	}
	if len(primary) == 3 {
		return primary, nil
	}
	return "", fmt.Errorf("unsupported subtitle language %q", lang)
}

var iso6392 = map[string]string{
	"ar": "ara", "cs": "ces", "da": "dan", "de": "deu", "el": "ell",
	"en": "eng", "es": "spa", "fi": "fin", "fr": "fra", "he": "heb",
	"hi": "hin", "id": "ind", "it": "ita", "ja": "jpn", "ko": "kor",
	"nl": "nld", "no": "nor", "pl": "pol", "pt": "por", "ru": "rus",
	"sv": "swe", "th": "tha", "tr": "tur", "uk": "ukr", "vi": "vie",
	"zh": "zho",
}
