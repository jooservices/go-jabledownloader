package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/jooservices/go-jabledownloader/internal/app"
	"github.com/jooservices/go-jabledownloader/internal/config"
	"github.com/jooservices/go-jabledownloader/internal/media"
	"github.com/jooservices/go-jabledownloader/internal/media/asr"
	"github.com/jooservices/go-jabledownloader/internal/media/audio"
	"github.com/jooservices/go-jabledownloader/internal/media/subtitle"
	"github.com/jooservices/go-jabledownloader/internal/site"
	"github.com/jooservices/go-jabledownloader/internal/site/jable"
	"github.com/jooservices/go-jabledownloader/internal/telemetry"
	"github.com/jooservices/go-jabledownloader/internal/ui/cli"
	"github.com/jooservices/go-jabledownloader/internal/update"

	// Download engines, sites, and translators register themselves.
	_ "github.com/jooservices/go-jabledownloader/internal/engine/hls"
	_ "github.com/jooservices/go-jabledownloader/internal/engine/progressive"
	_ "github.com/jooservices/go-jabledownloader/internal/site/eporner"
	_ "github.com/jooservices/go-jabledownloader/internal/site/javphotos"
)

// deps are the process-level collaborators; tests replace them.
type deps struct {
	stdin         io.Reader
	stdout        io.Writer
	stderr        io.Writer
	stdinTTY      bool // enables the picker and discovery prompts
	color         bool // stdout supports ANSI colors
	browser       app.BrowserFactory
	latestRelease func(context.Context) (*update.Release, error)
	install       func(context.Context, *update.Asset) ([]string, error)
	runner        media.Runner
}

func productionDeps() deps {
	return deps{
		stdin:         os.Stdin,
		stdout:        os.Stdout,
		stderr:        os.Stderr,
		stdinTTY:      term.IsTerminal(int(os.Stdin.Fd())),
		color:         cli.ColorEnabled(os.Stdout),
		browser:       launchBrowser(os.Stderr),
		latestRelease: update.LatestRelease,
		install:       update.Install,
		runner:        media.OSRunner{},
	}
}

// launchBrowser starts headless Chrome for Cloudflare-protected sites.
func launchBrowser(stderr io.Writer) app.BrowserFactory {
	return func(ctx context.Context) (site.Fetcher, func(), error) {
		browser, err := jable.NewBrowser(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("launch browser: %w\n\n  Chrome/Chromium is required to pass Cloudflare protection.\n  Install from: https://www.google.com/chrome/", err)
		}
		if browser.SandboxFallback {
			fmt.Fprintln(stderr, "warning: Chrome only started with --no-sandbox; set CHROME_NO_SANDBOX=1 to silence or run where the sandbox is available")
		}
		return browser, browser.Close, nil
	}
}

// newService builds the app service for one command run. The cleanup
// releases the browser and flushes telemetry.
func newService(o *options, d deps, fileName string, machineOutput bool) (*app.Service, func(), error) {
	cfg, err := loadConfig(o)
	if err != nil {
		return nil, nil, err
	}
	maxHeight, err := parseQuality(o.quality)
	if err != nil {
		return nil, nil, err
	}
	subtitler, err := newSubtitler(o, d.runner, d.stderr)
	if err != nil {
		return nil, nil, err
	}

	tel, telErr := telemetry.New(telemetry.Config{
		Endpoint: strings.TrimSpace(os.Getenv("OBS_ENDPOINT")),
		Org:      envOr("OBS_ORG", "jooservices"),
		Stream:   envOr("OBS_STREAM", "jabledownloader"),
		User:     os.Getenv("OBS_USER"),
		Password: os.Getenv("OBS_PASSWORD"),
		Version:  version,
	})
	if telErr != nil {
		fmt.Fprintf(d.stderr, "warning: %v\n", telErr)
	}

	color := d.color && !o.noColor
	reporter := cli.NewReporter(d.stdout, cli.ReporterOptions{Color: color, TTY: color, Quiet: o.quiet, Verbose: o.verbose})
	if machineOutput {
		// Keep stdout clean for JSON; progress goes to stderr, results only.
		reporter = cli.NewReporter(d.stderr, cli.ReporterOptions{Quiet: true})
	}
	sites := app.NewSites(d.browser)
	svc := &app.Service{
		Config:    cfg,
		Sites:     sites,
		Tel:       tel,
		Reporter:  reporter,
		Prompter:  cli.NewPrompter(d.stdin, cli.NewStdWriter(d.stdout, color), d.stdinTTY),
		Subtitler: subtitler,
		Opts: app.Options{
			DryRun: o.dryRun, Yes: o.yes, Quiet: o.quiet, Force: o.force,
			Subtitle: o.subtitle, FileName: fileName, MaxHeight: maxHeight,
		},
	}
	cleanup := func() {
		sites.Close()
		tel.Shutdown(context.Background())
	}
	return svc, cleanup, nil
}

func loadConfig(o *options) (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if o.workers > 0 {
		cfg.WorkerCount = o.workers
	}
	if o.outDir != "" {
		cfg.OutputDir = o.outDir
	}
	if o.pathTemplate != "" {
		cfg.PathTemplate = o.pathTemplate
	}
	if err := app.ValidatePathTemplate(cfg.PathTemplate); err != nil {
		return nil, err
	}
	return cfg, nil
}

// newSubtitler builds the subtitle pipeline; the mode is validated even
// without --subtitle so a typo fails fast.
func newSubtitler(o *options, runner media.Runner, progress io.Writer) (app.Subtitler, error) {
	if _, err := subtitle.ParseMode(o.subtitleMode); err != nil {
		return nil, err
	}
	if !o.subtitle {
		return nil, nil
	}
	whisper, err := asr.New(runner, o.whisperModel)
	if err != nil {
		return nil, err
	}
	if o.verbose {
		whisper.Progress = progress
	}
	extractor, err := audio.New(runner)
	if err != nil {
		return nil, err
	}
	target := o.subtitleLang
	if target == "" {
		target = "en"
	}
	applier, err := subtitle.NewApplier(o.subtitleMode, target, runner)
	if err != nil {
		return nil, err
	}
	pipeline := &media.Pipeline{
		Audio:   extractor,
		ASR:     whisper,
		Applier: applier,
		Options: media.Options{SpokenLang: o.spokenLanguage, TargetLang: target, Translator: o.translator},
	}
	if err := pipeline.Validate(); err != nil {
		return nil, err
	}
	return pipeline, nil
}

func parseQuality(raw string) (int, error) {
	s := strings.TrimSuffix(strings.TrimSpace(strings.ToLower(raw)), "p")
	if s == "" || s == "best" {
		return 0, nil
	}
	switch n, err := strconv.Atoi(s); {
	case err == nil && (n == 240 || n == 360 || n == 480 || n == 720 || n == 1080):
		return n, nil
	default:
		return 0, fmt.Errorf("invalid --quality %q (use best, 240, 360, 480, 720, or 1080)", raw)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
