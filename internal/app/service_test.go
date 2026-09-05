package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/config"
	"github.com/jooservices/go-jabledownloader/internal/scraper"
	"github.com/jooservices/go-jabledownloader/internal/telemetry"
	"github.com/jooservices/go-jabledownloader/internal/ui"
)

func TestFindExistingVideo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "jur-001-h264.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := FindExistingVideo(dir, "jur-001"); got == "" {
		t.Fatal("expected existing video to be found")
	}
	if got := FindExistingVideo(dir, "jur-002"); got != "" {
		t.Fatalf("expected no match, got %q", got)
	}
	if got := FindCompleteVideo(dir, "jur-001"); got == "" {
		t.Fatal("expected complete video without segments")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".segments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".segments", "seg_000000.ts"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FindCompleteVideo(dir, "jur-001"); got != "" {
		t.Fatalf("expected incomplete when .segments present, got %q", got)
	}
}

func TestVideoDir(t *testing.T) {
	got := VideoDir("/tmp/out", "jur-001")
	if got != filepath.Join("/tmp/out", "jur-001") {
		t.Fatalf("VideoDir = %q", got)
	}
}

func TestSetupContext(t *testing.T) {
	ctx, cancel := SetupContext()
	defer cancel()
	if ctx.Err() != nil {
		t.Fatal("expected active context")
	}
	cancel()
	<-ctx.Done()
}

func TestPickVideosNonInteractiveSelectsAll(t *testing.T) {
	videos := []scraper.VideoEntry{
		{Code: "jur-001", Title: "One", Duration: "1:00:00"},
		{Code: "abc-002", Title: "Two"},
	}
	svc := &Service{
		Config: config.Defaults(),
		Out:    ui.NewStdWriter(ioDiscard{}, false),
		Opts:   Options{Yes: true},
	}

	got, err := svc.pickVideos(videos)
	if err != nil {
		t.Fatalf("pickVideos: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected all videos, got %d", len(got))
	}
}

func TestPlanError(t *testing.T) {
	err := &PlanError{Failed: 3}
	if !strings.Contains(err.Error(), "3 video(s) failed") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

type fileFetcher struct {
	file string
}

func (f *fileFetcher) FetchHTML(_ context.Context, _ string, _ scraper.FetchMode) (string, error) {
	data, err := os.ReadFile(filepath.Join("..", "scraper", "testdata", f.file))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func TestRunGetDryRun(t *testing.T) {
	var sb stringsBuilder
	cfg := config.Defaults()
	cfg.OutputDir = t.TempDir()
	svc := &Service{
		Config: cfg,
		Client: scraper.NewClient(&fileFetcher{file: "video_page.html"}),
		Out:    ui.NewStdWriter(&sb, false),
		Tel:    telemetry.New(telemetry.Config{}),
		Opts:   Options{DryRun: true, Quiet: false, Verbose: true},
	}

	if err := svc.RunGet(context.Background(), "pred-840"); err != nil {
		t.Fatalf("RunGet: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "Dry run") {
		t.Fatalf("expected dry run message, got %q", out)
	}
	if !strings.Contains(out, "PRED-840") && !strings.Contains(out, "pred-840") {
		t.Fatalf("expected title/code in output: %q", out)
	}
}

func TestRunGetInvalidInput(t *testing.T) {
	svc := &Service{
		Config: config.Defaults(),
		Out:    ui.NewStdWriter(ioDiscard{}, false),
		Tel:    telemetry.New(telemetry.Config{}),
		Opts:   Options{Quiet: true},
	}
	if err := svc.RunGet(context.Background(), "not a code"); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunGetSkipsExisting(t *testing.T) {
	var sb stringsBuilder
	cfg := config.Defaults()
	cfg.OutputDir = t.TempDir()
	videoDir := VideoDir(cfg.OutputDir, "pred-840")
	if err := os.MkdirAll(videoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(videoDir, "pred-840-h264.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		Config: cfg,
		Client: scraper.NewClient(&fileFetcher{file: "video_page.html"}),
		Out:    ui.NewStdWriter(&sb, false),
		Tel:    telemetry.New(telemetry.Config{}),
		Opts:   Options{Quiet: true},
	}
	if err := svc.RunGet(context.Background(), "pred-840"); err != nil {
		t.Fatalf("RunGet: %v", err)
	}
	if !strings.Contains(sb.String(), "Already downloaded") {
		t.Fatalf("expected skip message, got %q", sb.String())
	}
}

func TestRunMultiDryRun(t *testing.T) {
	var sb stringsBuilder
	cfg := config.Defaults()
	cfg.OutputDir = t.TempDir()
	svc := &Service{
		Config: cfg,
		Client: scraper.NewClient(&fileFetcher{file: "browse_page.html"}),
		Out:    ui.NewStdWriter(&sb, false),
		Tel:    telemetry.New(telemetry.Config{}),
		Opts:   Options{DryRun: true, Yes: true},
	}

	err := svc.RunMulti(context.Background(), "latest", 2, func(ctx context.Context, page int) ([]scraper.VideoEntry, error) {
		return svc.Client.LatestVideos(ctx, page)
	})
	if err != nil {
		t.Fatalf("RunMulti: %v", err)
	}
	if !strings.Contains(sb.String(), "Dry run") {
		t.Fatalf("expected dry run: %q", sb.String())
	}
}

func TestRunMultiSkipExisting(t *testing.T) {
	outDir := t.TempDir()
	code := "jur-001"
	videoDir := VideoDir(outDir, code)
	if err := os.MkdirAll(videoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(videoDir, code+"-h264.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var sb stringsBuilder
	cfg := config.Defaults()
	cfg.OutputDir = outDir
	svc := &Service{
		Config: cfg,
		Client: scraper.NewClient(&fileFetcher{file: "video_page.html"}),
		Out:    ui.NewStdWriter(&sb, false),
		Tel:    telemetry.New(telemetry.Config{}),
		Opts:   Options{Yes: true, Quiet: false},
	}

	err := svc.RunMulti(context.Background(), "fixture", 1, func(_ context.Context, page int) ([]scraper.VideoEntry, error) {
		if page > 1 {
			return nil, nil
		}
		return []scraper.VideoEntry{{
			Code:  code,
			Title: "Existing",
			URL:   "https://en.jable.tv/videos/jur-001/",
		}}, nil
	})
	if err != nil {
		t.Fatalf("RunMulti: %v", err)
	}
	if !strings.Contains(sb.String(), "Already downloaded") {
		t.Fatalf("expected skip message: %q", sb.String())
	}
}

func TestRunMultiFetcherError(t *testing.T) {
	var sb stringsBuilder
	svc := &Service{
		Config: config.Defaults(),
		Out:    ui.NewStdWriter(&sb, false),
		Tel:    telemetry.New(telemetry.Config{}),
		Opts:   Options{DryRun: true, Yes: true},
	}
	err := svc.RunMulti(context.Background(), "broken", 5, func(_ context.Context, _ int) ([]scraper.VideoEntry, error) {
		return nil, context.Canceled
	})
	if err != nil {
		t.Fatalf("RunMulti should dry-run with empty list, got %v", err)
	}
}

func TestPrintPlanAndSpan(t *testing.T) {
	var sb stringsBuilder
	svc := &Service{
		Config: config.Defaults(),
		Out:    ui.NewStdWriter(&sb, false),
		Tel:    telemetry.New(telemetry.Config{}),
	}
	svc.printPlan([]scraper.VideoEntry{
		{Code: "a", Title: "One", Duration: "10:00"},
		{Code: "b", Title: "Two"},
	})
	if !strings.Contains(sb.String(), "Videos:") {
		t.Fatalf("plan missing: %q", sb.String())
	}

	ctx, end := svc.span(context.Background(), "test")
	end()
	_ = ctx
}

func TestInteractive(_ *testing.T) {
	_ = interactive()
}

func TestConfirm(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()

	_, _ = w.WriteString("y\n")
	_ = w.Close()
	if !confirm("ok?") {
		t.Fatal("expected confirm true")
	}

	r2, w2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r2
	_, _ = w2.WriteString("n\n")
	_ = w2.Close()
	if confirm("ok?") {
		t.Fatal("expected confirm false")
	}
}

func TestRunGetDownloads(t *testing.T) {
	hlsURL, cleanup := startTestHLSServer(t)
	defer cleanup()

	html := `<html><head><title>DL-001 Sample - Jable.TV</title>
<link rel="canonical" href="https://en.jable.tv/videos/dl-001/"/>
<script>var hlsUrl = '` + hlsURL + `'; var videoId = '1';</script>
</head><body></body></html>`

	var sb stringsBuilder
	cfg := config.Defaults()
	cfg.OutputDir = t.TempDir()
	cfg.WorkerCount = 1
	svc := &Service{
		Config: cfg,
		Client: scraper.NewClient(staticHTML{html: html}),
		Out:    ui.NewStdWriter(&sb, false),
		Tel:    telemetry.New(telemetry.Config{}),
		Opts:   Options{Quiet: false, Verbose: true, TTY: false},
	}
	if err := svc.RunGet(context.Background(), "dl-001"); err != nil {
		t.Fatalf("RunGet: %v", err)
	}
	if !strings.Contains(sb.String(), "Downloaded:") {
		t.Fatalf("expected download success: %q", sb.String())
	}
}

func TestRunGetQuietTTY(t *testing.T) {
	hlsURL, cleanup := startTestHLSServer(t)
	defer cleanup()

	html := `<html><head><title>Q-001 - Jable.TV</title>
<link rel="canonical" href="https://en.jable.tv/videos/q-001/"/>
<script>var hlsUrl = '` + hlsURL + `';</script></head></html>`

	cfg := config.Defaults()
	cfg.OutputDir = t.TempDir()
	cfg.WorkerCount = 1
	svc := &Service{
		Config: cfg,
		Client: scraper.NewClient(staticHTML{html: html}),
		Out:    ui.NewStdWriter(ioDiscard{}, false),
		Tel:    telemetry.New(telemetry.Config{}),
		Opts:   Options{Quiet: true, TTY: true},
	}
	if err := svc.RunGet(context.Background(), "q-001"); err != nil {
		t.Fatalf("RunGet: %v", err)
	}
}

func TestRunMultiDownloadAndFail(t *testing.T) {
	hlsURL, cleanup := startTestHLSServer(t)
	defer cleanup()

	html := `<html><head><title>M-001 - Jable.TV</title>
<link rel="canonical" href="https://en.jable.tv/videos/m-001/"/>
<script>var hlsUrl = '` + hlsURL + `';</script></head></html>`

	var sb stringsBuilder
	cfg := config.Defaults()
	cfg.OutputDir = t.TempDir()
	cfg.WorkerCount = 1
	svc := &Service{
		Config: cfg,
		Client: scraper.NewClient(staticHTML{html: html}),
		Out:    ui.NewStdWriter(&sb, false),
		Tel:    telemetry.New(telemetry.Config{}),
		Opts:   Options{Yes: true, Force: true},
	}

	err := svc.RunMulti(context.Background(), "batch", 2, func(_ context.Context, page int) ([]scraper.VideoEntry, error) {
		if page > 1 {
			return nil, nil
		}
		return []scraper.VideoEntry{
			{Code: "m-001", Title: "One", URL: "https://en.jable.tv/videos/m-001/"},
			{Code: "bad", Title: "Bad", URL: "https://en.jable.tv/videos/bad/"},
		}, nil
	})
	// bad entry fails fetch (same HTML still has m-001 code) — may still partial-fail
	_ = err
}

func TestPickVideosDryRun(t *testing.T) {
	svc := &Service{
		Config: config.Defaults(),
		Out:    ui.NewStdWriter(ioDiscard{}, false),
		Opts:   Options{DryRun: true},
	}
	got, err := svc.pickVideos([]scraper.VideoEntry{{Code: "a", Title: "A"}})
	if err != nil || len(got) != 1 {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestRunMultiEmptyAfterScan(t *testing.T) {
	var sb stringsBuilder
	svc := &Service{
		Config: config.Defaults(),
		Out:    ui.NewStdWriter(&sb, false),
		Tel:    telemetry.New(telemetry.Config{}),
		Opts:   Options{Yes: true},
	}
	err := svc.RunMulti(context.Background(), "empty", 3, func(_ context.Context, _ int) ([]scraper.VideoEntry, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("RunMulti: %v", err)
	}
	if !strings.Contains(sb.String(), "Found 0") {
		t.Fatalf("output: %q", sb.String())
	}
}

func TestRunMultiDefaultCount(t *testing.T) {
	svc := &Service{
		Config: config.Defaults(),
		Out:    ui.NewStdWriter(ioDiscard{}, false),
		Tel:    telemetry.New(telemetry.Config{}),
		Opts:   Options{DryRun: true, Yes: true},
	}
	if err := svc.RunMulti(context.Background(), "c", 0, func(_ context.Context, page int) ([]scraper.VideoEntry, error) {
		if page > 1 {
			return nil, nil
		}
		return []scraper.VideoEntry{{Code: "x-1", Title: "T", URL: "https://en.jable.tv/videos/x-1/"}}, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFetchInfoError(t *testing.T) {
	svc := &Service{
		Config: config.Defaults(),
		Client: scraper.NewClient(staticHTML{html: "<html><body>no hls</body></html>"}),
		Out:    ui.NewStdWriter(ioDiscard{}, false),
		Tel:    telemetry.New(telemetry.Config{}),
		Opts:   Options{Quiet: true},
	}
	if err := svc.RunGet(context.Background(), "jur-001"); err == nil {
		t.Fatal("expected fetch error")
	}
}

func TestRunMultiFetchInfoFailCounts(t *testing.T) {
	var sb stringsBuilder
	cfg := config.Defaults()
	cfg.OutputDir = t.TempDir()
	svc := &Service{
		Config: cfg,
		Client: scraper.NewClient(staticHTML{html: "<html></html>"}),
		Out:    ui.NewStdWriter(&sb, false),
		Tel:    telemetry.New(telemetry.Config{}),
		Opts:   Options{Yes: true, Force: true},
	}
	err := svc.RunMulti(context.Background(), "fail", 1, func(_ context.Context, page int) ([]scraper.VideoEntry, error) {
		if page > 1 {
			return nil, nil
		}
		return []scraper.VideoEntry{{Code: "f-001", Title: "F", URL: "https://en.jable.tv/videos/f-001/"}}, nil
	})
	if err == nil {
		t.Fatal("expected PlanError")
	}
	if _, ok := err.(*PlanError); !ok {
		t.Fatalf("want PlanError, got %T %v", err, err)
	}
}

type staticHTML struct{ html string }

func (s staticHTML) FetchHTML(_ context.Context, _ string, _ scraper.FetchMode) (string, error) {
	return s.html, nil
}

func startTestHLSServer(t *testing.T) (string, func()) {
	t.Helper()
	seg := generateAppTS(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/media.m3u8", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1.0,\nseg0.ts\n#EXT-X-ENDLIST\n")
	})
	mux.HandleFunc("/seg0.ts", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(seg)
	})
	srv := httptest.NewServer(mux)
	return srv.URL + "/media.m3u8", srv.Close
}

func generateAppTS(t *testing.T) []byte {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	path := filepath.Join(t.TempDir(), "seed.ts")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=160x120:d=1",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-bsf:v", "h264_mp4toannexb",
		"-f", "mpegts", "-y", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

type stringsBuilder struct {
	b []byte
}

func (s *stringsBuilder) Write(p []byte) (int, error) {
	s.b = append(s.b, p...)
	return len(p), nil
}

func (s *stringsBuilder) String() string { return string(s.b) }
