package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/app"
	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/engine"
	"github.com/jooservices/go-jabledownloader/internal/site"
	"github.com/jooservices/go-jabledownloader/internal/update"
)

// Real pages captured by internal/site/jable/fixtures_refresh_test.go.
const jableFixtures = "../../internal/site/jable/testdata"

type jableManifest struct {
	VideoCode string `json:"video_code"`
	VideoURL  string `json:"video_url"`
}

func loadJableManifest(t *testing.T) jableManifest {
	t.Helper()
	var m jableManifest
	data, err := os.ReadFile(filepath.Join(jableFixtures, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// fixtureBrowser serves the real Jable video page for /videos/ URLs and the
// real listing otherwise.
type fixtureBrowser struct{ t *testing.T }

func (f fixtureBrowser) FetchHTML(_ context.Context, url string, _ site.FetchMode) (string, error) {
	name := "browse_page.html"
	if strings.Contains(url, "/videos/") {
		name = "video_page.html"
	}
	data, err := os.ReadFile(filepath.Join(jableFixtures, name))
	return string(data), err
}

type fakeEngine struct{ reqs []engine.Request }

func (e *fakeEngine) Download(_ context.Context, req engine.Request, sink domain.EventSink) (*engine.Result, error) {
	e.reqs = append(e.reqs, req)
	sink(domain.Event{Kind: domain.EventProgress, Done: 1, Total: 1, Bytes: 3})
	path := filepath.Join(req.Dir, req.OutputName("h264"))
	if err := os.MkdirAll(req.Dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte("mp4"), 0o644); err != nil {
		return nil, err
	}
	return &engine.Result{Path: path, Size: 3, Codec: "h264"}, nil
}

type harness struct {
	deps   deps
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	home   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OBS_ENDPOINT", "")
	h := &harness{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, home: home}
	h.deps = deps{
		stdin:  strings.NewReader(""),
		stdout: h.stdout,
		stderr: h.stderr,
		browser: func(context.Context) (site.Fetcher, func(), error) {
			return fixtureBrowser{t: t}, func() {}, nil
		},
		latestRelease: func(context.Context) (*update.Release, error) { return nil, errors.New("offline") },
		install:       func(context.Context, *update.Asset) ([]string, error) { return nil, nil },
	}
	return h
}

func (h *harness) run(args ...string) int {
	return run(append(args, "--out", filepath.Join(h.home, "videos")), h.deps)
}

func withFakeEngine(t *testing.T) *fakeEngine {
	t.Helper()
	previous, err := engine.For(domain.SourceHLS)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeEngine{}
	engine.Register(domain.SourceHLS, fake)
	t.Cleanup(func() { engine.Register(domain.SourceHLS, previous) })
	return fake
}

func TestRootCommandHasCommands(t *testing.T) {
	root := newRootCmd(newHarness(t).deps)
	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"download", "get", "latest", "search", "site", "update", "config", "completion"} {
		if !names[want] {
			t.Errorf("missing command %q", want)
		}
	}
}

func TestRunExitCodes(t *testing.T) {
	h := newHarness(t)
	if code := run([]string{"--help"}, h.deps); code != exitOK {
		t.Fatalf("--help exit = %d", code)
	}
	if code := run([]string{"--version"}, h.deps); code != exitOK || !strings.Contains(h.stdout.String(), version) {
		t.Fatalf("--version exit = %d out=%q", code, h.stdout.String())
	}
	if code := run([]string{"bogus"}, h.deps); code != exitError || !strings.Contains(h.stderr.String(), "Error:") {
		t.Fatalf("unknown command exit = %d", code)
	}
	if exitCodeFor(&app.PlanError{Failed: 1}) != exitPartial || exitCodeFor(&app.DiscoveryError{}) != exitPartial ||
		exitCodeFor(errors.New("x")) != exitError || exitCodeFor(fmt.Errorf("download: %w", context.Canceled)) != exitInterrupted {
		t.Fatal("exitCodeFor mapping")
	}
}

func TestInterruptedRunExplainsResume(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	code := execute(ctx, []string{"download", loadJableManifest(t).VideoCode, "--out", t.TempDir()}, h.deps)

	if code != exitInterrupted || !strings.Contains(h.stderr.String(), "re-run the same command to resume") {
		t.Fatalf("exit=%d stderr=%q", code, h.stderr.String())
	}
}

// `get --json` keys are a CLI contract (v4.3).
func TestGetJSONUsesRealPageAndStableKeys(t *testing.T) {
	h := newHarness(t)
	m := loadJableManifest(t)

	if code := h.run("get", m.VideoCode, "--json"); code != exitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &detail); err != nil {
		t.Fatalf("invalid JSON %q: %v", h.stdout.String(), err)
	}
	sources := detail["sources"].([]any)
	src := sources[0].(map[string]any)
	for _, key := range []string{"Kind", "URL", "Codec", "Height"} {
		if _, ok := src[key]; !ok {
			t.Errorf("source JSON missing %q: %v", key, src)
		}
	}
	if detail["code"] != m.VideoCode || detail["site"] != "jable" {
		t.Fatalf("detail = %v", detail)
	}
}

func TestGetText(t *testing.T) {
	h := newHarness(t)
	m := loadJableManifest(t)
	if code := h.run("get", m.VideoURL); code != exitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if out := h.stdout.String(); !strings.Contains(out, "Code:    "+m.VideoCode) || !strings.Contains(out, ".m3u8") || strings.Contains(out, "\033[") {
		t.Fatalf("output = %q", out)
	}
}

func TestDownloadWritesIntoSiteLayout(t *testing.T) {
	h := newHarness(t)
	fake := withFakeEngine(t)
	m := loadJableManifest(t)

	if code := h.run("download", m.VideoCode, "--workers", "3"); code != exitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	want := filepath.Join(h.home, "videos", "jable", m.VideoCode)
	if len(fake.reqs) != 1 || fake.reqs[0].Dir != want || fake.reqs[0].Workers != 3 {
		t.Fatalf("requests = %+v", fake.reqs)
	}
	if ua := fake.reqs[0].Source.Headers.Get("Referer"); ua != "https://en.jable.tv/" {
		t.Fatalf("source headers = %v", fake.reqs[0].Source.Headers)
	}
	if out := h.stdout.String(); !strings.Contains(out, "Downloaded: "+filepath.Join(want, m.VideoCode+"-h264.mp4")) {
		t.Fatalf("output = %q", out)
	}

	h.stdout.Reset()
	if code := h.run("download", m.VideoCode); code != exitOK || !strings.Contains(h.stdout.String(), "Already downloaded") {
		t.Fatalf("second run exit=%d out=%q", code, h.stdout.String())
	}
}

func TestDownloadRejectsBadInputsBeforeWork(t *testing.T) {
	m := loadJableManifest(t)
	for name, args := range map[string][]string{
		"name path":     {"download", m.VideoCode, "--name", "../x.mp4"},
		"quality":       {"download", m.VideoCode, "--quality", "4k"},
		"path template": {"download", m.VideoCode, "--path-template", "../{code}"},
		"subtitle mode": {"download", m.VideoCode, "--subtitle-mode", "burn"},
		"subtitle lang": {"download", m.VideoCode, "--subtitle", "--subtitle-lang", "../x"},
		"translator":    {"download", m.VideoCode, "--subtitle", "--translator", "missing"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			fake := withFakeEngine(t)
			if code := h.run(args...); code != exitError || len(fake.reqs) != 0 {
				t.Fatalf("exit=%d downloads=%d stderr=%q", code, len(fake.reqs), h.stderr.String())
			}
		})
	}
}

func TestDownloadDryRun(t *testing.T) {
	h := newHarness(t)
	fake := withFakeEngine(t)
	if code := h.run("download", loadJableManifest(t).VideoCode, "--dry-run"); code != exitOK || len(fake.reqs) != 0 {
		t.Fatalf("exit=%d downloads=%d", code, len(fake.reqs))
	}
	if !strings.Contains(h.stdout.String(), "Dry run") {
		t.Fatalf("output = %q", h.stdout.String())
	}
}

func TestDiscoveryCommands(t *testing.T) {
	m := loadJableManifest(t)
	for name, args := range map[string][]string{
		"latest":      {"latest", "--site", "jable", "--count", "2"},
		"search":      {"search", "cute", "girl", "--site", "jable"},
		"site list":   {"site", "jable", "list", "--view", "hot"},
		"site search": {"site", "jable", "search", "cute"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			if code := h.run(args...); code != exitOK {
				t.Fatalf("exit %d: %s", code, h.stderr.String())
			}
			if out := h.stdout.String(); !strings.Contains(out, m.VideoCode) || !strings.Contains(out, "jable") {
				t.Fatalf("output = %q", out)
			}
		})
	}
}

func TestDiscoveryJSONKeepsStdoutClean(t *testing.T) {
	h := newHarness(t)
	if code := h.run("latest", "--site", "jable", "--json", "--count", "3"); code != exitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	var items []domain.Item
	if err := json.Unmarshal(h.stdout.Bytes(), &items); err != nil || len(items) != 3 {
		t.Fatalf("stdout is not the JSON list: %q (%v)", h.stdout.String(), err)
	}
}

func TestDiscoveryDownloadWithYes(t *testing.T) {
	h := newHarness(t)
	fake := withFakeEngine(t)
	if code := h.run("latest", "--site", "jable", "--count", "1", "--download", "--yes"); code != exitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if len(fake.reqs) != 1 {
		t.Fatalf("downloads = %d", len(fake.reqs))
	}
}

func TestDiscoveryErrors(t *testing.T) {
	h := newHarness(t)
	if code := h.run("site", "jable", "list", "--view", "bogus"); code != exitPartial || !strings.Contains(h.stderr.String(), "valid: latest, hot") {
		t.Fatalf("exit=%d stderr=%q", code, h.stderr.String())
	}
	if code := h.run("site", "bogus"); code != exitError || !strings.Contains(h.stderr.String(), "requires list or search") {
		t.Fatalf("unknown site exit = %d", code)
	}
}

func TestConfigCommands(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"config", "set", "output_dir", "/tmp/videos"},
		{"config", "set", "worker_count", "8"},
		{"config", "set", "path_template", "{site}-{code}"},
	} {
		if code := run(args, h.deps); code != exitOK {
			t.Fatalf("%v exit %d: %s", args, code, h.stderr.String())
		}
	}
	h.stdout.Reset()
	if code := run([]string{"config", "get", "path_template"}, h.deps); code != exitOK || strings.TrimSpace(h.stdout.String()) != "{site}-{code}" {
		t.Fatalf("get path_template = %q", h.stdout.String())
	}
	if code := run([]string{"config"}, h.deps); code != exitOK || !strings.Contains(h.stdout.String(), "worker_count:  8") {
		t.Fatalf("config show = %q", h.stdout.String())
	}
	for _, key := range []string{"output_dir", "worker_count", "path"} {
		if code := run([]string{"config", "get", key}, h.deps); code != exitOK {
			t.Fatalf("get %s failed", key)
		}
	}
	for _, args := range [][]string{
		{"config", "set", "path_template", "../{code}"},
		{"config", "set", "path_template", "{site}"},
		{"config", "set", "worker_count", "0"},
		{"config", "set", "output_dir", " "},
		{"config", "set", "bogus", "x"},
		{"config", "get", "bogus"},
	} {
		if code := run(args, h.deps); code != exitError {
			t.Errorf("%v exit = %d, want error", args, code)
		}
	}
}

func TestUpdateCommand(t *testing.T) {
	asset := update.Asset{Name: "jabledownloader_v9.9.9_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz", Size: 1}
	latest := func(tag string, assets ...update.Asset) func(context.Context) (*update.Release, error) {
		return func(context.Context) (*update.Release, error) {
			return &update.Release{TagName: tag, Assets: assets}, nil
		}
	}
	for name, tc := range map[string]struct {
		release func(context.Context) (*update.Release, error)
		install func(context.Context, *update.Asset) ([]string, error)
		args    []string
		code    int
		want    string
	}{
		"up to date":  {latest("dev"), nil, nil, exitOK, "Current:"},
		"check only":  {latest("v9.9.9"), nil, []string{"--check"}, exitOK, "newer version is available"},
		"install":     {latest("v9.9.9", asset), func(context.Context, *update.Asset) ([]string, error) { return []string{"old binary kept"}, nil }, nil, exitOK, "warning: old binary kept"},
		"no asset":    {latest("v9.9.9"), nil, nil, exitError, ""},
		"no tag":      {latest(""), nil, nil, exitError, ""},
		"offline":     {nil, nil, nil, exitError, ""},
		"install err": {latest("v9.9.9", asset), func(context.Context, *update.Asset) ([]string, error) { return nil, errors.New("checksum mismatch") }, nil, exitError, ""},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			if tc.release != nil {
				h.deps.latestRelease = tc.release
			}
			if tc.install != nil {
				h.deps.install = tc.install
			}
			old := version
			version = "v1.0.0"
			if name == "up to date" {
				version = "v9.9.9"
				h.deps.latestRelease = latest("v9.9.9")
			}
			defer func() { version = old }()

			code := run(append([]string{"update"}, tc.args...), h.deps)

			if code != tc.code || !strings.Contains(h.stdout.String(), tc.want) {
				t.Fatalf("exit=%d out=%q err=%q", code, h.stdout.String(), h.stderr.String())
			}
		})
	}
}

func TestCompletion(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		h := newHarness(t)
		if code := run([]string{"completion", shell}, h.deps); code != exitOK || h.stdout.Len() == 0 {
			t.Fatalf("%s exit=%d", shell, code)
		}
	}
	if code := run([]string{"completion", "tcsh"}, newHarness(t).deps); code != exitError {
		t.Fatal("expected unsupported shell error")
	}
}

func TestTelemetryConfigWarning(t *testing.T) {
	h := newHarness(t)
	t.Setenv("OBS_ENDPOINT", "http://obs.example.test")
	t.Setenv("OBS_USER", "user")
	if code := h.run("download", loadJableManifest(t).VideoCode, "--dry-run"); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(h.stderr.String(), "warning: telemetry disabled") {
		t.Fatalf("stderr = %q", h.stderr.String())
	}
}

func TestParseQualityAndEnvOr(t *testing.T) {
	for in, want := range map[string]int{"": 0, "best": 0, "720": 720, "1080p": 1080, " 480P ": 480} {
		if got, err := parseQuality(in); err != nil || got != want {
			t.Errorf("parseQuality(%q) = %d, %v", in, got, err)
		}
	}
	if _, err := parseQuality("4k"); err == nil {
		t.Fatal("expected invalid quality")
	}
	t.Setenv("JD_TEST_ENV", "set")
	if envOr("JD_TEST_ENV", "x") != "set" || envOr("JD_TEST_UNSET", "x") != "x" {
		t.Fatal("envOr")
	}
}
