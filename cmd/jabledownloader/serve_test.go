package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/app"
)

// syncBuffer is a bytes.Buffer safe for the daemon goroutine and the test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type fakeRunner struct{ have map[string]bool }

func (f fakeRunner) Run(context.Context, string, ...string) ([]byte, error) { return nil, nil }

func (f fakeRunner) LookPath(name string) (string, error) {
	if f.have[name] {
		return "/bin/" + name, nil
	}
	return "", errors.New("not found")
}

func daemonHarness(t *testing.T, stdin string) (*harness, *syncBuffer) {
	t.Helper()
	h := newHarness(t)
	errs := &syncBuffer{}
	h.deps.stderr = errs
	h.deps.stdin = strings.NewReader(stdin)
	h.deps.runner = fakeRunner{}
	return h, errs
}

func TestMCPCommandOverStdio(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"latest_videos","arguments":{"sites":["jable"],"count":2}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"inspect_video","arguments":{"input":"nope"}}}`,
	}, "\n") + "\n"
	h, errs := daemonHarness(t, in)
	state := filepath.Join(h.home, "state.json")

	code := execute(context.Background(), []string{"mcp", "--no-monitor", "--state", state, "--out", filepath.Join(h.home, "v")}, h.deps)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	out := h.stdout.String()
	if strings.Count(out, "\n") != 3 {
		t.Fatalf("expected three replies, got: %q", out)
	}
	if !strings.Contains(out, `"jabledownloader"`) || !strings.Contains(out, "latest") && !strings.Contains(out, `\"site\": \"jable\"`) {
		t.Fatalf("unexpected MCP output: %q", out)
	}
	if !strings.Contains(errs.String(), "subtitles disabled on this host") {
		t.Fatalf("a host without Whisper must run without subtitles: %s", errs.String())
	}
}

func TestServeCommandServesAndStops(t *testing.T) {
	h, errs := daemonHarness(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		done <- execute(ctx, []string{"serve", "--addr", "127.0.0.1:0", "--state", filepath.Join(h.home, "state.json"),
			"--no-subtitle", "--interval", "1h", "--out", filepath.Join(h.home, "v")}, h.deps)
	}()

	re := regexp.MustCompile(`api=(http://127\.0\.0\.1:\d+)`)
	var base string
	for deadline := time.Now().Add(5 * time.Second); base == "" && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if m := re.FindStringSubmatch(errs.String()); m != nil {
			base = m[1]
		}
	}
	if base == "" {
		t.Fatalf("daemon did not start: %s", errs.String())
	}
	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("healthz = %d", resp.StatusCode)
	}
	resp, err = http.Post(base+"/v1/downloads", "application/json", strings.NewReader(`{"input":"https://nowhere.invalid/x"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("download request = %d", resp.StatusCode)
	}
	cancel()
	select {
	case code := <-done:
		if code != exitOK && code != exitInterrupted {
			t.Fatalf("exit %d: %s", code, errs.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not stop")
	}
}

func TestServeRefusesOpenBindWithoutToken(t *testing.T) {
	h, errs := daemonHarness(t, "")
	code := execute(context.Background(), []string{"serve", "--addr", "0.0.0.0:0", "--no-monitor", "--no-subtitle",
		"--state", filepath.Join(h.home, "state.json")}, h.deps)
	if code != exitError || !strings.Contains(errs.String(), "without a token") {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
}

func TestServeFailsOnUnreadableState(t *testing.T) {
	h, errs := daemonHarness(t, "")
	code := execute(context.Background(), []string{"serve", "--no-monitor", "--no-subtitle", "--state", h.home}, h.deps)
	if code != exitError {
		t.Fatalf("a state path that is a directory must fail, exit %d: %s", code, errs.String())
	}
}

func TestSubtitleHostCheck(t *testing.T) {
	cases := []struct {
		have map[string]bool
		ok   bool
	}{
		{map[string]bool{"ffmpeg": true, "mlx_whisper": true}, true},
		{map[string]bool{"ffmpeg": true, "uvx": true}, true},
		{map[string]bool{"ffmpeg": true}, false},
		{map[string]bool{"mlx_whisper": true}, false},
	}
	for _, tc := range cases {
		if err := subtitleHostCheck(fakeRunner{have: tc.have}); (err == nil) != tc.ok {
			t.Errorf("%v: err=%v", tc.have, err)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if firstNonEmpty("", "  ", " x ", "y") != "x" || firstNonEmpty("", " ") != "" {
		t.Fatal("firstNonEmpty")
	}
}

func TestLogReporterLogsMilestonesOnly(t *testing.T) {
	var buf bytes.Buffer
	r := logReporter{log: slog.New(slog.NewTextHandler(&buf, nil))}
	for _, ev := range []app.Event{
		app.VideoResolved{Site: "jable", Code: "A-1", Dir: "/d"},
		app.VideoSkipped{Code: "A-1", Path: "/p"},
		app.VideoDownloaded{Code: "A-1", Path: "/p", Size: 5},
		app.GalleryDownloaded{Code: "G", Dir: "/g", Saved: 3},
		app.SubtitleDone{Video: "/p", SRT: "/p.srt"},
		app.ItemFailed{Code: "A-2", Err: errors.New("boom")},
		app.DiscoveryDone{Site: "jable", Err: errors.New("down")},
		app.DiscoveryDone{Site: "eporner"},
		app.DownloadProgress{Code: "A-1"},
	} {
		r.Report(ev)
	}
	out := buf.String()
	for _, want := range []string{"resolved", "already downloaded", "downloaded", "gallery downloaded", "subtitles", "item failed", "discovery failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "\n") != 7 {
		t.Fatalf("progress and successful discovery must not be logged:\n%s", out)
	}
}

func TestBackendAdapter(t *testing.T) {
	h, _ := daemonHarness(t, "")
	svc, cleanup, err := newService(&options{outDir: h.home}, h.deps, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	b := backend{svc: svc}
	items, err := b.Latest(context.Background(), []string{"jable"}, 2)
	if err != nil || len(items) == 0 {
		t.Fatalf("latest: %v %v", items, err)
	}
	if items, err = b.Search(context.Background(), []string{"jable"}, "x", 2); err != nil || len(items) == 0 {
		t.Fatalf("search: %v %v", items, err)
	}
	m := loadJableManifest(t)
	if d, err := b.Inspect(context.Background(), m.VideoURL); err != nil || d.Code == "" {
		t.Fatalf("inspect: %v %v", d, err)
	}
}
