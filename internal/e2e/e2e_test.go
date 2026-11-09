// Package e2e drives the real use-cases, engines, and ffmpeg against a local
// HTTP server, so every transport (HLS, progressive, photo galleries) is
// exercised end to end without touching a third-party site.
//
// The tests need ffmpeg and skip without it. Set E2E_OUT to keep the output
// in a chosen directory (for example a network share); otherwise a temp
// directory is used.
package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/app"
	"github.com/jooservices/go-jabledownloader/internal/config"
	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/platform/httpx"
	"github.com/jooservices/go-jabledownloader/internal/site"

	_ "github.com/jooservices/go-jabledownloader/internal/engine/hls"
	_ "github.com/jooservices/go-jabledownloader/internal/engine/progressive"
)

// origin is the running fixture server; the fake site reads it lazily.
var origin struct {
	sync.RWMutex
	url string
}

func baseURL() string {
	origin.RLock()
	defer origin.RUnlock()
	return origin.url
}

// e2eSite resolves URLs of the local server into sources by path.
type e2eSite struct{}

func (e2eSite) Name() string { return "e2e" }
func (e2eSite) List(context.Context, site.ListOptions) ([]domain.Item, error) {
	if items, ok := listingOverride(); ok {
		return items, nil
	}
	return []domain.Item{{Site: "e2e", Code: "hls-1", Title: "HLS", URL: baseURL() + "/watch/hls-1"}}, nil
}
func (e2eSite) Search(context.Context, string, int) ([]domain.Item, error) { return nil, nil }

func (e2eSite) Detail(_ context.Context, ref string) (*domain.Detail, error) {
	code := ref[strings.LastIndex(ref, "/")+1:]
	switch {
	case strings.HasPrefix(code, "hls"):
		return &domain.Detail{Site: "e2e", Code: code, Title: code, Sources: []domain.Source{
			{Kind: domain.SourceHLS, URL: baseURL() + "/hls/master.m3u8", Headers: http.Header{"Referer": {baseURL() + "/"}}},
		}}, nil
	case strings.HasPrefix(code, "mp4"):
		return &domain.Detail{Site: "e2e", Code: code, Title: code, Sources: []domain.Source{
			{Kind: domain.SourceProgressive, URL: baseURL() + "/video.mp4", Codec: "h264", Height: 720},
		}}, nil
	}
	return nil, fmt.Errorf("unknown code %q", code)
}

func (gallerySite) Gallery(_ context.Context, _ string) (*domain.Gallery, error) {
	g := &domain.Gallery{Site: "e2e", Code: "gal-1", Title: "Gallery"}
	for i := 1; i <= 3; i++ {
		g.Photos = append(g.Photos, domain.Photo{ID: fmt.Sprintf("p-%d", i), URL: fmt.Sprintf("%s/photos/%d.jpg", baseURL(), i)})
	}
	return g, nil
}

func init() {
	site.Register(site.Descriptor{
		Name: "e2e", Hosts: []string{"127.0.0.1"}, Fetcher: site.FetcherHTTP, Views: []string{"latest"},
		New: func(site.Fetcher) site.Site { return e2eSite{} },
	})
}

// gallerySite additionally implements site.GallerySite; keeping it a separate
// site keeps video inputs on the plain e2e site.
type gallerySite struct{ e2eSite }

func init() {
	site.Register(site.Descriptor{
		Name: "e2egallery", Hosts: []string{"localhost"}, Fetcher: site.FetcherHTTP, Views: []string{"latest"},
		New: func(site.Fetcher) site.Site { return gallerySite{} },
	})
}

type recorder struct {
	mu     sync.Mutex
	events []app.Event
}

func (r *recorder) Report(ev app.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func has[T app.Event](r *recorder) (T, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range r.events {
		if v, ok := ev.(T); ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// fixtures builds media with ffmpeg and returns a ready server.
type fixtures struct {
	dir        string
	failSeg    atomic.Bool // answer 500 for segment 2 while set
	rangeCalls atomic.Int64
}

func ffmpeg(t *testing.T, args ...string) {
	t.Helper()
	out, err := exec.Command("ffmpeg", append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
}

func newFixtures(t *testing.T) *fixtures {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	f := &fixtures{dir: t.TempDir()}
	src := filepath.Join(f.dir, "src.mp4")
	// ~15 MB so the progressive engine needs two 8 MiB chunks.
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=25", "-t", "4",
		"-c:v", "libx264", "-crf", "0", "-pix_fmt", "yuv444p", src)
	if info, err := os.Stat(src); err != nil || info.Size() < 9<<20 {
		t.Fatalf("fixture too small to need two 8 MiB chunks: %v %v", info, err)
	}
	if err := os.Rename(src, filepath.Join(f.dir, "video.mp4")); err != nil {
		t.Fatal(err)
	}
	hlsDir := filepath.Join(f.dir, "hls")
	if err := os.MkdirAll(hlsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25", "-t", "5", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-g", "25", "-keyint_min", "25", "-sc_threshold", "0",
		"-f", "hls", "-hls_time", "1", "-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(hlsDir, "seg%d.ts"), filepath.Join(hlsDir, "index.m3u8"))
	master := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=320x180\nindex.m3u8\n"
	if err := os.WriteFile(filepath.Join(hlsDir, "master.m3u8"), []byte(master), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixtures) serve(t *testing.T) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/video.mp4", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			f.rangeCalls.Add(1)
		}
		http.ServeFile(w, r, filepath.Join(f.dir, "video.mp4"))
	})
	files := http.FileServer(http.Dir(f.dir))
	mux.HandleFunc("/hls/", func(w http.ResponseWriter, r *http.Request) {
		if f.failSeg.Load() && strings.HasSuffix(r.URL.Path, "seg2.ts") {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		files.ServeHTTP(w, r)
	})
	mux.HandleFunc("/photos/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = fmt.Fprintf(w, "\xff\xd8fake-jpeg-%s\xff\xd9", filepath.Base(r.URL.Path))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	origin.Lock()
	origin.url = srv.URL
	origin.Unlock()
}

func outDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("E2E_OUT"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := os.MkdirTemp(dir, t.Name()+"-")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	return t.TempDir()
}

func service(t *testing.T, out string, rec *recorder) *app.Service {
	t.Helper()
	cfg := config.Defaults()
	cfg.OutputDir = out
	cfg.WorkerCount = 4
	return &app.Service{
		Config:   cfg,
		Sites:    app.NewSites(nil),
		Reporter: rec,
		Opts:     app.Options{Yes: true},
	}
}

// probe asserts ffmpeg can fully decode the file.
func probe(t *testing.T, path string) {
	t.Helper()
	if out, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-f", "null", "-").CombinedOutput(); err != nil || len(strings.TrimSpace(string(out))) > 0 {
		t.Fatalf("%s is not a clean media file: %v %s", path, err, out)
	}
}

func TestHLSDownloadEndToEnd(t *testing.T) {
	f := newFixtures(t)
	f.serve(t)
	out, rec := outDir(t), &recorder{}
	svc := service(t, out, rec)

	if err := svc.RunGet(context.Background(), baseURL()+"/watch/hls-1"); err != nil {
		t.Fatalf("RunGet: %v", err)
	}
	done, ok := has[app.VideoDownloaded](rec)
	if !ok || done.Size == 0 {
		t.Fatalf("no VideoDownloaded event: %+v", rec.events)
	}
	want := filepath.Join(out, "e2e", "hls-1", "hls-1-h264.mp4")
	if done.Path != want {
		t.Fatalf("path = %q, want %q", done.Path, want)
	}
	probe(t, done.Path)
	if _, err := os.Stat(filepath.Join(out, "e2e", "hls-1", ".segments")); !os.IsNotExist(err) {
		t.Fatalf("resume state not cleaned: %v", err)
	}

	// Second run is a no-op.
	rec2 := &recorder{}
	if err := service(t, out, rec2).RunGet(context.Background(), baseURL()+"/watch/hls-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := has[app.VideoSkipped](rec2); !ok {
		t.Fatal("second run did not skip the finished video")
	}
}

func TestHLSResumesAfterSegmentFailure(t *testing.T) {
	f := newFixtures(t)
	f.serve(t)
	out := outDir(t)
	f.failSeg.Store(true)

	err := service(t, out, &recorder{}).RunGet(context.Background(), baseURL()+"/watch/hls-1")
	if err == nil || !strings.Contains(err.Error(), "re-run to resume") {
		t.Fatalf("first run err = %v, want resumable failure", err)
	}
	if _, statErr := os.Stat(filepath.Join(out, "e2e", "hls-1", ".segments", ".source")); statErr != nil {
		t.Fatalf("resume state missing after failure: %v", statErr)
	}

	f.failSeg.Store(false)
	rec := &recorder{}
	if err := service(t, out, rec).RunGet(context.Background(), baseURL()+"/watch/hls-1"); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	done, ok := has[app.VideoDownloaded](rec)
	if !ok {
		t.Fatal("resumed run did not finish")
	}
	probe(t, done.Path)
}

func TestProgressiveDownloadUsesRangeChunks(t *testing.T) {
	f := newFixtures(t)
	f.serve(t)
	out, rec := outDir(t), &recorder{}

	if err := service(t, out, rec).RunGet(context.Background(), baseURL()+"/watch/mp4-1"); err != nil {
		t.Fatalf("RunGet: %v", err)
	}
	done, ok := has[app.VideoDownloaded](rec)
	if !ok {
		t.Fatal("no VideoDownloaded event")
	}
	src, err := os.Stat(filepath.Join(f.dir, "video.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if done.Size != src.Size() {
		t.Fatalf("downloaded %d bytes, source has %d", done.Size, src.Size())
	}
	if f.rangeCalls.Load() < 3 { // one probe + at least two chunks
		t.Fatalf("expected a probe plus multiple range requests, got %d", f.rangeCalls.Load())
	}
	probe(t, done.Path)
}

func TestGalleryDownloadEndToEnd(t *testing.T) {
	f := newFixtures(t)
	f.serve(t)
	out, rec := outDir(t), &recorder{}
	// "localhost" selects the gallery site; the photo URLs use the server IP.
	input := strings.Replace(baseURL(), "127.0.0.1", "localhost", 1) + "/gallery"

	if err := service(t, out, rec).RunGet(context.Background(), input); err != nil {
		t.Fatalf("RunGet: %v", err)
	}
	summary, ok := has[app.GalleryDownloaded](rec)
	if !ok || summary.Saved != 3 || summary.Failed != 0 {
		t.Fatalf("summary = %+v ok=%v", summary, ok)
	}
	matches, _ := filepath.Glob(filepath.Join(out, "e2e", "gal-1", "*.jpg"))
	if len(matches) != 3 {
		t.Fatalf("photos on disk = %v", matches)
	}
}

func TestDiscoveryAcrossRegisteredSite(t *testing.T) {
	f := newFixtures(t)
	f.serve(t)
	res, err := service(t, outDir(t), &recorder{}).Latest(context.Background(), []string{"e2e"}, 1, 5)
	if err != nil || len(res.Items) != 1 || res.Items[0].Code != "hls-1" {
		t.Fatalf("Latest = %+v, %v", res, err)
	}
}

func TestDefaultUserAgentIsSent(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.UserAgent())
	}))
	defer srv.Close()
	resp, err := httpx.NewClient(httpx.Options{}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if ua, _ := got.Load().(string); ua != httpx.DefaultUserAgent {
		t.Fatalf("User-Agent = %q", ua)
	}
}
