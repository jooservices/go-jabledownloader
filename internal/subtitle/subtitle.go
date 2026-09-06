// Package subtitle adds English subtitles to a downloaded MP4 using host
// ffmpeg plus mlx_whisper (Apple Silicon Whisper via MLX).
package subtitle

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultModel is the mlx-community checkpoint used for English translation.
// Note: whisper-large-v3-turbo often ignores --task translate and emits Japanese;
// medium reliably translates to English on Apple MLX.
const DefaultModel = "mlx-community/whisper-medium"

// Subtitle embedding modes.
const (
	ModeSoft = "soft" // separate mov_text track + .en.srt sidecar
	ModeHard = "hard" // burn text into video pixels + keep .en.srt sidecar
)

// Options control English subtitle embedding.
type Options struct {
	// Model is a Hugging Face MLX Whisper repo (or local path). Empty uses DefaultModel.
	Model string
	// Language is the spoken language hint for Whisper (e.g. "ja"). Empty = auto-detect.
	Language string
	// Mode is ModeSoft or ModeHard. Empty defaults to ModeSoft.
	Mode string
	// Verbose forwards mlx_whisper progress to stderr.
	Verbose bool
}

// lookPath is overridden in tests.
var lookPath = exec.LookPath

// commandContext is overridden in tests.
var commandContext = exec.CommandContext

// ParseMode validates a subtitle mode string.
func ParseMode(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ModeSoft, nil
	}
	switch s {
	case ModeSoft, ModeHard:
		return s, nil
	default:
		return "", fmt.Errorf("invalid --subtitle-mode %q (use soft or hard)", raw)
	}
}

// EmbedEnglish generates an English SRT via mlx_whisper (--task translate) and
// either muxes a soft track or burns hard subtitles into the MP4.
// The original file is replaced in place on success. A sidecar .en.srt is kept.
func EmbedEnglish(ctx context.Context, videoPath string, opt Options) error {
	if strings.TrimSpace(videoPath) == "" {
		return fmt.Errorf("video path is required")
	}
	if _, err := os.Stat(videoPath); err != nil {
		return fmt.Errorf("video: %w", err)
	}
	if _, err := lookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg is required to extract audio and embed subtitles")
	}
	mode, err := ParseMode(opt.Mode)
	if err != nil {
		return err
	}
	whisper, err := resolveWhisper()
	if err != nil {
		return err
	}

	model := strings.TrimSpace(opt.Model)
	if model == "" {
		model = DefaultModel
	}

	dir := filepath.Dir(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	audioPath := filepath.Join(dir, "."+base+".subtitle.wav")
	srtPath := filepath.Join(dir, base+".en.srt")
	tmpOut := filepath.Join(dir, "."+base+".withsubs.mp4")
	whisperDir, err := os.MkdirTemp(dir, "."+base+".whisper-*")
	if err != nil {
		return fmt.Errorf("create whisper output dir: %w", err)
	}
	defer os.Remove(audioPath)
	defer os.Remove(tmpOut)
	defer os.RemoveAll(whisperDir)

	srcMode := filePerm(videoPath)

	if err := extractAudio(ctx, videoPath, audioPath); err != nil {
		return err
	}
	// mlx_whisper ResultWriter uses Path.with_suffix(".srt"), which replaces any
	// existing suffix. Write into an isolated temp dir so a pre-existing
	// *.en.srt cannot be mistaken for this run's output.
	whisperName := "subtitle-en"
	whisperSRT := filepath.Join(whisperDir, whisperName+".srt")
	if err := runWhisperTranslate(ctx, whisper, audioPath, whisperDir, whisperName, model, opt); err != nil {
		return err
	}
	if err := resolveEnglishSRT(whisperSRT, filepath.Join(whisperDir, "subtitle.srt"), srtPath); err != nil {
		return err
	}
	if err := validateEnglishSRT(srtPath); err != nil {
		return err
	}
	_ = os.Chmod(srtPath, srcMode)

	switch mode {
	case ModeHard:
		if err := burnHardSubs(ctx, videoPath, srtPath, tmpOut); err != nil {
			return err
		}
	default:
		if err := muxSoftSubs(ctx, videoPath, srtPath, tmpOut); err != nil {
			return err
		}
	}
	_ = os.Chmod(tmpOut, srcMode)
	if err := os.Rename(tmpOut, videoPath); err != nil {
		return fmt.Errorf("replace video with subtitled file: %w", err)
	}
	return nil
}

// resolveEnglishSRT finds the SRT mlx_whisper wrote in this run and normalizes
// it to dest (*.en.srt). Only preferred/legacy names from the current Whisper
// output directory are accepted — never a pre-existing dest sidecar.
func resolveEnglishSRT(preferred, legacy, dest string) error {
	candidates := []string{preferred, legacy}
	var found string
	for _, c := range candidates {
		if c == "" || c == dest {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			found = c
			break
		}
	}
	if found == "" {
		return fmt.Errorf("whisper did not produce %s (also looked for %s)", preferred, legacy)
	}
	_ = os.Remove(dest)
	if err := os.Rename(found, dest); err != nil {
		return fmt.Errorf("normalize subtitle path to %s: %w", dest, err)
	}
	return nil
}

func filePerm(path string) os.FileMode {
	fi, err := os.Stat(path)
	if err != nil {
		return 0o644
	}
	return fi.Mode().Perm()
}

// validateEnglishSRT rejects outputs that are still mostly Japanese — a known
// failure mode when a model ignores Whisper --task translate.
func validateEnglishSRT(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	jp, en := 0, 0
	for _, r := range text {
		switch {
		case r >= 0x3040 && r <= 0x30ff, r >= 0x3400 && r <= 0x9fff, r >= 0xff66 && r <= 0xff9f:
			jp++
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
			en++
		}
	}
	if jp == 0 {
		return nil
	}
	// Allow a little JP (names/onoms) but require English to dominate.
	if en == 0 || jp*2 > en {
		return fmt.Errorf("subtitle output looks Japanese (jp_chars=%d en_chars=%d) — Whisper translate failed for this model; try --whisper-model mlx-community/whisper-medium", jp, en)
	}
	return nil
}

// resolveWhisper returns the argv prefix for mlx_whisper.
// Prefers a PATH binary; falls back to `uvx --from mlx-whisper mlx_whisper`.
func resolveWhisper() ([]string, error) {
	if p, err := lookPath("mlx_whisper"); err == nil {
		return []string{p}, nil
	}
	if uvx, err := lookPath("uvx"); err == nil {
		return []string{uvx, "--from", "mlx-whisper", "mlx_whisper"}, nil
	}
	return nil, fmt.Errorf("mlx_whisper not found on PATH (and uvx missing).\n\n" +
		"Install on this Mac host (run yourself):\n" +
		"  uv tool install mlx-whisper\n" +
		"or:\n" +
		"  pipx install mlx-whisper\n" +
		"Then ensure mlx_whisper is on PATH and re-run with --subtitle")
}

func extractAudio(ctx context.Context, videoPath, audioPath string) error {
	cmd := commandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-y", "-i", videoPath,
		"-vn", "-ac", "1", "-ar", "16000",
		"-c:a", "pcm_s16le",
		audioPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg extract audio: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runWhisperTranslate(ctx context.Context, whisper []string, audioPath, outDir, outName, model string, opt Options) error {
	args := append(append([]string{}, whisper[1:]...),
		audioPath,
		"--model", model,
		"--task", "translate",
		"--output-format", "srt",
		"--output-dir", outDir,
		"--output-name", outName,
		"--verbose", "False",
		"--condition-on-previous-text", "False",
	)
	if lang := strings.TrimSpace(opt.Language); lang != "" {
		args = append(args, "--language", lang)
	}
	if opt.Verbose {
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "--verbose" {
				args[i+1] = "True"
				break
			}
		}
	}

	cmd := commandContext(ctx, whisper[0], args...)
	cmd.Stderr = os.Stderr
	if opt.Verbose {
		cmd.Stdout = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mlx_whisper translate: %w", err)
	}
	return nil
}

func muxSoftSubs(ctx context.Context, videoPath, srtPath, outPath string) error {
	cmd := commandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-y",
		"-i", videoPath,
		"-i", srtPath,
		"-map", "0",
		"-map", "1",
		"-c", "copy",
		"-c:s", "mov_text",
		"-metadata:s:s:0", "language=eng",
		"-metadata:s:s:0", "title=English",
		outPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg mux soft subtitles: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func burnHardSubs(ctx context.Context, videoPath, srtPath, outPath string) error {
	if !ffmpegHasFilter("subtitles") {
		return fmt.Errorf("hard subtitle burn-in needs ffmpeg built with libass (subtitles filter).\n\n" +
			"Your current ffmpeg has no 'subtitles' filter.\n" +
			"Install/rebuild yourself (agents must not install), then confirm:\n" +
			"  ffmpeg -hide_banner -filters | grep subtitles\n" +
			"On macOS Homebrew, use a formula build that enables libass, or install a full static build.\n" +
			"Until then use: --subtitle-mode soft")
	}

	abs, err := filepath.Abs(srtPath)
	if err != nil {
		return fmt.Errorf("resolve srt path: %w", err)
	}
	// Prefer filename= so absolute paths parse cleanly with libass builds.
	filter := "subtitles=filename=" + escapeFilterPath(abs) +
		`:force_style=FontSize=20\,Outline=1\,Shadow=0`

	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-y", "-i", videoPath,
		"-vf", filter,
		"-c:a", "copy",
	}
	if runtime.GOOS == "darwin" {
		args = append(args, "-c:v", "h264_videotoolbox", "-b:v", "8M")
	} else {
		args = append(args, "-c:v", "libx264", "-crf", "18", "-preset", "medium")
	}
	args = append(args, outPath)

	cmd := commandContext(ctx, "ffmpeg", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg burn hard subtitles: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func ffmpegHasFilter(name string) bool {
	cmd := commandContext(context.Background(), "ffmpeg", "-hide_banner", "-filters")
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return false
	}
	// Filter table lines look like: " ... subtitles          V->V       ..."
	needle := " " + name + " "
	return strings.Contains(string(out), needle) || strings.Contains(string(out), " "+name+"\t")
}

// escapeFilterPath escapes a filesystem path for an ffmpeg filtergraph option.
func escapeFilterPath(p string) string {
	p = filepath.ToSlash(p)
	p = strings.ReplaceAll(p, `\`, `\\`)
	p = strings.ReplaceAll(p, `:`, `\:`)
	p = strings.ReplaceAll(p, `'`, `\'`)
	p = strings.ReplaceAll(p, `[`, `\[`)
	p = strings.ReplaceAll(p, `]`, `\]`)
	return p
}
