package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/app"
	"github.com/jooservices/go-jabledownloader/internal/scraper"
	"github.com/jooservices/go-jabledownloader/internal/update"
)

func TestRootCommandHasCommands(t *testing.T) {
	root := newRootCmd()
	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"get", "search", "latest", "hot", "update", "config", "completion"} {
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
			quality string
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
	if got := exitCodeFor(&app.PlanError{Failed: 2}); got != exitPartial {
		t.Fatalf("PlanError code=%d want %d", got, exitPartial)
	}
	if got := exitCodeFor(errors.New("boom")); got != exitError {
		t.Fatalf("generic error code=%d want %d", got, exitError)
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

func TestSearchHasCountFlag(t *testing.T) {
	cmd := newSearchCmd()
	if cmd.Flags().Lookup("count") == nil {
		t.Fatal("search missing --count")
	}
}

func TestParseQuality(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"best", 0, false},
		{"", 0, false},
		{"720", 720, false},
		{"720p", 720, false},
		{"481", 0, true},
		{"nope", 0, true},
		{"0", 0, true},
	}
	for _, tc := range cases {
		got, err := parseQuality(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseQuality(%q): expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseQuality(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseQuality(%q)=%d want %d", tc.in, got, tc.want)
		}
	}
}

func TestConfigCommandSetGet(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"config", "set", "worker_count", "8"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"config", "set", "output_dir", "/tmp/jable-out"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	root.SetArgs([]string{"config"})
	var show strings.Builder
	root.SetOut(&show)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show.String(), "worker_count") || !strings.Contains(show.String(), "8") {
		t.Fatalf("show output: %q", show.String())
	}

	for _, key := range []string{"worker_count", "output_dir", "path"} {
		var buf strings.Builder
		root.SetOut(&buf)
		root.SetArgs([]string{"config", "get", key})
		if err := root.Execute(); err != nil {
			t.Fatalf("get %s: %v", key, err)
		}
		if strings.TrimSpace(buf.String()) == "" {
			t.Fatalf("empty get %s", key)
		}
	}

	root.SetOut(io.Discard)
	root.SetArgs([]string{"config", "get", "nope"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected unknown get key error")
	}
	root.SetArgs([]string{"config", "set", "worker_count", "0"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected invalid worker_count error")
	}
	root.SetArgs([]string{"config", "set", "output_dir", "  "})
	if err := root.Execute(); err == nil {
		t.Fatal("expected empty output_dir error")
	}
	root.SetArgs([]string{"config", "set", "nope", "x"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected unknown set key error")
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
