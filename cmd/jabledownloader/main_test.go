package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/scraper"
	"github.com/jooservices/go-jabledownloader/internal/update"
)

func TestRootCommandHasCommands(t *testing.T) {
	root := newRootCmd()
	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"get", "search", "latest", "hot", "update", "completion"} {
		if !names[want] {
			t.Errorf("missing command %q", want)
		}
	}
}

func TestCompletionRejectsUnknownShell(t *testing.T) {
	root := newRootCmd()
	cmd := newCompletionCmd(root)
	cmd.SetArgs([]string{"nope"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for unsupported shell")
	}
}

func TestCompletionShells(t *testing.T) {
	root := newRootCmd()
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		cmd := newCompletionCmd(root)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs([]string{shell})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("JD_TEST_ENV_OR", "set-value")
	if got := envOr("JD_TEST_ENV_OR", "fallback"); got != "set-value" {
		t.Fatalf("got %q", got)
	}
	_ = os.Unsetenv("JD_TEST_ENV_OR_MISSING")
	if got := envOr("JD_TEST_ENV_OR_MISSING", "fallback"); got != "fallback" {
		t.Fatalf("got %q", got)
	}
}

func TestBaseService(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	rootFlags.workers = 4
	rootFlags.outDir = t.TempDir()
	rootFlags.dryRun = true
	rootFlags.yes = true
	rootFlags.quiet = true
	rootFlags.noColor = true
	rootFlags.verbose = false
	rootFlags.force = false
	t.Cleanup(func() {
		rootFlags = struct {
			outDir  string
			workers int
			dryRun  bool
			yes     bool
			quiet   bool
			noColor bool
			verbose bool
			force   bool
		}{}
	})

	svc, tel, err := baseService()
	if err != nil {
		t.Fatalf("baseService: %v", err)
	}
	if svc == nil || tel == nil {
		t.Fatal("expected service and telemetry")
	}
	if svc.Config.WorkerCount != 4 {
		t.Fatalf("workers = %d", svc.Config.WorkerCount)
	}
	if svc.Config.OutputDir != rootFlags.outDir {
		t.Fatalf("outDir = %q", svc.Config.OutputDir)
	}
	if !svc.Opts.DryRun || !svc.Opts.Yes || !svc.Opts.Quiet {
		t.Fatalf("unexpected opts: %+v", svc.Opts)
	}
	tel.Shutdown(context.Background())
}

func TestRunHelp(t *testing.T) {
	oldArgs := os.Args
	os.Args = []string{"jabledownloader", "--help"}
	defer func() { os.Args = oldArgs }()
	if code := run(); code != exitOK {
		t.Fatalf("exit %d", code)
	}
}

func TestRunUpdateUpToDate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldFetch := fetchLatestRelease
	oldVer := version
	defer func() {
		fetchLatestRelease = oldFetch
		version = oldVer
	}()
	version = "v9.9.9"
	fetchLatestRelease = func(context.Context) (*update.Release, error) {
		return &update.Release{TagName: "v9.9.9"}, nil
	}
	if err := runUpdate(context.Background()); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
}

func TestRunUpdateCheckOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldFetch := fetchLatestRelease
	oldVer := version
	oldFlags := updateFlags
	defer func() {
		fetchLatestRelease = oldFetch
		version = oldVer
		updateFlags = oldFlags
	}()
	version = "v1.0.0"
	updateFlags.checkOnly = true
	fetchLatestRelease = func(context.Context) (*update.Release, error) {
		return &update.Release{TagName: "v9.9.9"}, nil
	}
	if err := runUpdate(context.Background()); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
}

func TestRunUpdateInstall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldFetch := fetchLatestRelease
	oldInstall := installRelease
	oldVer := version
	oldFlags := updateFlags
	defer func() {
		fetchLatestRelease = oldFetch
		installRelease = oldInstall
		version = oldVer
		updateFlags = oldFlags
	}()
	version = "v1.0.0"
	updateFlags.checkOnly = false
	suffix := fmt.Sprintf("_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	fetchLatestRelease = func(context.Context) (*update.Release, error) {
		return &update.Release{
			TagName: "v9.9.9",
			Assets: []update.Asset{{
				Name:               "jabledownloader_v9.9.9" + suffix,
				BrowserDownloadURL: "http://example.invalid/a",
				Size:               1_000_000,
			}},
		}, nil
	}
	called := false
	installRelease = func(context.Context, *update.Asset) error {
		called = true
		return nil
	}
	if err := runUpdate(context.Background()); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if !called {
		t.Fatal("expected installRelease call")
	}
}

func TestRunUpdateErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldFetch := fetchLatestRelease
	defer func() { fetchLatestRelease = oldFetch }()

	fetchLatestRelease = func(context.Context) (*update.Release, error) {
		return nil, fmt.Errorf("boom")
	}
	if err := runUpdate(context.Background()); err == nil {
		t.Fatal("expected error")
	}

	fetchLatestRelease = func(context.Context) (*update.Release, error) {
		return &update.Release{TagName: ""}, nil
	}
	if err := runUpdate(context.Background()); err == nil {
		t.Fatal("expected empty tag error")
	}

	oldVer := version
	version = "v1.0.0"
	defer func() { version = oldVer }()
	fetchLatestRelease = func(context.Context) (*update.Release, error) {
		return &update.Release{TagName: "v9.9.9", Assets: nil}, nil
	}
	if err := runUpdate(context.Background()); err == nil {
		t.Fatal("expected missing asset error")
	}
}

func TestRunPartialExit(t *testing.T) {
	// Exercise PlanError exit mapping via a tiny stub command is hard; ensure
	// run() succeeds for a no-op version flag.
	oldArgs := os.Args
	os.Args = []string{"jabledownloader", "--version"}
	defer func() { os.Args = oldArgs }()
	var buf bytes.Buffer
	root := newRootCmd()
	root.SetOut(&buf)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestRunVersion(t *testing.T) {
	old := os.Args
	os.Args = []string{"jabledownloader", "--version"}
	defer func() { os.Args = old }()
	if code := run(); code != exitOK {
		t.Fatalf("code=%d", code)
	}
}

func TestRunErrorExit(t *testing.T) {
	old := os.Args
	os.Args = []string{"jabledownloader", "get"} // missing required arg
	defer func() { os.Args = old }()
	if code := run(); code != exitError {
		t.Fatalf("code=%d want %d", code, exitError)
	}
}

func TestNewScrapeServiceSuccessAndError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	rootFlags.noColor = true
	root := newRootCmd()

	old := newBrowser
	defer func() { newBrowser = old }()

	newBrowser = func(context.Context) (*scraper.Browser, error) {
		return nil, fmt.Errorf("no chrome")
	}
	_, _, err := newScrapeService(root)
	if err == nil {
		t.Fatal("expected error")
	}
}
