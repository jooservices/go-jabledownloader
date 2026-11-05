package asr

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jooservices/go-jabledownloader/internal/media"
)

// Host speech-to-text engines. MLX uses the Apple Silicon Metal stack.
// OpenAI Whisper is the portable CLI for Intel and AMD on macOS, Linux, and Windows.
const (
	BackendAuto    = "auto"
	BackendMLX     = "mlx"
	BackendWhisper = "whisper"
)

// ParseBackend accepts auto, mlx, or whisper. Empty means auto.
func ParseBackend(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", BackendAuto:
		return BackendAuto, nil
	case BackendMLX:
		return BackendMLX, nil
	case BackendWhisper:
		return BackendWhisper, nil
	default:
		return "", fmt.Errorf("invalid --whisper-backend %q (use auto, mlx, or whisper)", raw)
	}
}

// speechEngine is one host CLI. argv[0] is the executable; the rest is a prefix
// (uvx --from mlx-whisper mlx_whisper).
type speechEngine interface {
	name() string
	defaultModel() string
	locate(media.Runner) ([]string, error)
	args(audio, model, task, outDir, spokenLang string) []string
	srtPath(outDir, audio string) string
}

type mlxEngine struct{}

func (mlxEngine) name() string         { return "mlx_whisper" }
func (mlxEngine) defaultModel() string { return "mlx-community/whisper-medium" }

func (mlxEngine) locate(runner media.Runner) ([]string, error) {
	if path, err := runner.LookPath("mlx_whisper"); err == nil {
		return []string{path}, nil
	}
	if path, err := runner.LookPath("uvx"); err == nil {
		return []string{path, "--from", "mlx-whisper", "mlx_whisper"}, nil
	}
	return nil, fmt.Errorf("mlx_whisper not found on PATH (and uvx missing)\n\n%s", installHint(BackendMLX))
}

func (mlxEngine) args(audio, model, task, outDir, spokenLang string) []string {
	args := []string{audio,
		"--model", model,
		"--task", task,
		"--output-format", "srt",
		"--output-dir", outDir,
		"--output-name", "transcription",
		"--condition-on-previous-text", "False",
	}
	if lang := strings.TrimSpace(spokenLang); lang != "" {
		args = append(args, "--language", lang)
	}
	return args
}

func (mlxEngine) srtPath(outDir, _ string) string {
	return filepath.Join(outDir, "transcription.srt")
}

type whisperEngine struct{}

func (whisperEngine) name() string         { return "whisper" }
func (whisperEngine) defaultModel() string { return "medium" }

func (whisperEngine) locate(runner media.Runner) ([]string, error) {
	path, err := runner.LookPath("whisper")
	if err != nil {
		return nil, fmt.Errorf("whisper not found on PATH\n\n%s", installHint(BackendWhisper))
	}
	return []string{path}, nil
}

func (whisperEngine) args(audio, model, task, outDir, spokenLang string) []string {
	args := []string{audio,
		"--model", model,
		"--task", task,
		"--output_format", "srt",
		"--output_dir", outDir,
		"--condition_on_previous_text", "False",
	}
	if lang := strings.TrimSpace(spokenLang); lang != "" {
		args = append(args, "--language", lang)
	}
	return args
}

func (whisperEngine) srtPath(outDir, audio string) string {
	stem := strings.TrimSuffix(filepath.Base(audio), filepath.Ext(audio))
	return filepath.Join(outDir, stem+".srt")
}

// selectEngine picks the host CLI. Apple M prefers MLX. Intel and AMD on
// every OS prefer the portable Whisper CLI. An explicit backend never falls
// through to the other one. uvx can launch MLX only on Apple Silicon; on
// other machines it is not treated as an installed backend.
func selectEngine(choice, goos, goarch string, runner media.Runner) (speechEngine, error) {
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	switch choice {
	case BackendMLX:
		return mlxEngine{}, nil
	case BackendWhisper:
		return whisperEngine{}, nil
	}
	apple := goos == "darwin" && goarch == "arm64"
	mlxReady := look(runner, "mlx_whisper") || (apple && look(runner, "uvx"))
	whisperReady := look(runner, "whisper")
	switch {
	case apple && mlxReady:
		return mlxEngine{}, nil
	case whisperReady:
		return whisperEngine{}, nil
	case mlxReady:
		return mlxEngine{}, nil
	default:
		return nil, fmt.Errorf("no speech-to-text command found\n\n%s", installHint(BackendAuto))
	}
}

func look(runner media.Runner, file string) bool {
	_, err := runner.LookPath(file)
	return err == nil
}

func installHint(backend string) string {
	mlx := "Apple Silicon (M1 or later):\n  uv tool install mlx-whisper\n  or: pipx install mlx-whisper"
	portable := "Intel or AMD on macOS, Ubuntu, and Windows:\n  pipx install openai-whisper\n  or: pip install openai-whisper"
	switch backend {
	case BackendMLX:
		return mlx
	case BackendWhisper:
		return portable
	default:
		return mlx + "\n" + portable
	}
}
