package progressive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/engine"
	"github.com/jooservices/go-jabledownloader/internal/platform/httpx"
)

type recorder struct {
	mu     sync.Mutex
	events []domain.Event
}

func (r *recorder) sink(ev domain.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recorder) all() []domain.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.Event(nil), r.events...)
}

func payload(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i % 251)
	}
	return data
}

// rangeServer serves data honouring Range; fail decides per request whether
// to answer with an error status instead.
type rangeServer struct {
	data     []byte
	requests atomic.Int64
	active   atomic.Int64
	peak     atomic.Int64
	fail     func(byteRange string, attempt int64) int
	delay    time.Duration
}

func (s *rangeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	attempt := s.requests.Add(1)
	now := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		peak := s.peak.Load()
		if now <= peak || s.peak.CompareAndSwap(peak, now) {
			break
		}
	}
	time.Sleep(s.delay)
	byteRange := r.Header.Get("Range")
	if s.fail != nil {
		if status := s.fail(byteRange, attempt); status != 0 {
			w.WriteHeader(status)
			return
		}
	}
	if byteRange == "" {
		_, _ = w.Write(s.data)
		return
	}
	startText, endText, _ := strings.Cut(strings.TrimPrefix(byteRange, "bytes="), "-")
	start, _ := strconv.Atoi(startText)
	end, _ := strconv.Atoi(endText)
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(s.data)))
	w.WriteHeader(http.StatusPartialContent)
	_, _ = w.Write(s.data[start : end+1])
}

func testDownloader(client *http.Client, chunk int64) *Downloader {
	d := New(WithHTTPClient(client), WithChunkSize(chunk))
	d.backoff = time.Millisecond
	return d
}

func request(url, dir string, workers int) engine.Request {
	return engine.Request{Code: "abc-1", Dir: dir, Workers: workers,
		Source: domain.Source{Kind: domain.SourceProgressive, URL: url, Codec: "h264"}}
}

func TestChunkPlanUsesFixedSize(t *testing.T) {
	chunks := New().chunkPlan(50 << 20)
	if len(chunks) != 7 || chunks[0].size() != defaultChunkSize || chunks[6].size() != 2<<20 {
		t.Fatalf("chunks = %+v", chunks)
	}
}

func TestOptionsIgnoreInvalidValues(t *testing.T) {
	d := New(WithChunkSize(0), WithHTTPClient(nil))
	if d.chunkSize != defaultChunkSize || d.client == nil {
		t.Fatalf("defaults = %+v", d)
	}
}

func TestDownloadRangedFileMatchesSourceAndReportsProgress(t *testing.T) {
	srv := &rangeServer{data: payload(2<<20 + 123)}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	rec := &recorder{}
	dir := t.TempDir()

	res, err := testDownloader(ts.Client(), 512<<10).Download(context.Background(), request(ts.URL, dir, 2), rec.sink)

	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, _ := os.ReadFile(res.Path)
	if !bytes.Equal(got, srv.data) || res.Path != filepath.Join(dir, "abc-1-h264.mp4") {
		t.Fatalf("output mismatch: path=%s size=%d", res.Path, len(got))
	}
	events := rec.all()
	last := events[len(events)-1]
	if last.Kind != domain.EventDone || last.Bytes != int64(len(srv.data)) {
		t.Fatalf("final event = %+v", last)
	}
	if _, err := os.Stat(filepath.Join(dir, ".segments")); !os.IsNotExist(err) {
		t.Fatal("segments not discarded")
	}
}

// Regression: --workers was ignored because the engine singleton hard-coded 16.
func TestDownloadHonoursRequestWorkers(t *testing.T) {
	srv := &rangeServer{data: payload(8 << 10), delay: 20 * time.Millisecond}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	if _, err := testDownloader(ts.Client(), 1<<10).Download(context.Background(), request(ts.URL, t.TempDir(), 2), nil); err != nil {
		t.Fatal(err)
	}
	if peak := srv.peak.Load(); peak > 2 {
		t.Fatalf("peak concurrent requests = %d, want <= 2", peak)
	}
}

// Regression: bytes from a failed attempt were counted again on retry.
func TestDownloadRetryDoesNotOvercountBytes(t *testing.T) {
	srv := &rangeServer{data: payload(4 << 10)}
	var failedOnce atomic.Bool
	srv.fail = func(byteRange string, _ int64) int {
		if byteRange == "bytes=0-4095" && failedOnce.CompareAndSwap(false, true) {
			return http.StatusServiceUnavailable
		}
		return 0
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	rec := &recorder{}

	if _, err := testDownloader(ts.Client(), 4<<10).Download(context.Background(), request(ts.URL, t.TempDir(), 1), rec.sink); err != nil {
		t.Fatal(err)
	}
	for _, ev := range rec.all() {
		if ev.Kind == domain.EventProgress && ev.Bytes > int64(len(srv.data)) {
			t.Fatalf("progress overcounted: %+v", ev)
		}
	}
}

func TestDownloadFailedChunkResumesOnlyMissingChunks(t *testing.T) {
	srv := &rangeServer{data: payload(4 << 10)}
	var broken atomic.Bool
	broken.Store(true)
	srv.fail = func(byteRange string, _ int64) int {
		if byteRange == "bytes=3072-4095" && broken.Load() {
			return http.StatusNotFound
		}
		return 0
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	d := testDownloader(ts.Client(), 1<<10)
	dir := t.TempDir()
	req := request(ts.URL, dir, 1)

	_, err := d.Download(context.Background(), req, nil)
	if err == nil || !strings.Contains(err.Error(), "chunk 4 of 4") || !strings.Contains(err.Error(), "re-run to resume") {
		t.Fatalf("err = %v", err)
	}

	broken.Store(false)
	srv.requests.Store(0)
	if _, err := d.Download(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	if got := srv.requests.Load(); got != 2 {
		t.Fatalf("resume requests = %d, want probe + 1 missing chunk", got)
	}
}

// Regression (C1): EPORNER redirects to CDN URLs with raw spaces and needs
// a desktop User-Agent; the media client must handle both.
func TestDownloadFollowsRedirectWithSpacesAndSendsUserAgent(t *testing.T) {
	srv := &rangeServer{data: payload(1 << 10)}
	var agents sync.Map
	var cdn *httptest.Server
	cdn = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents.Store(r.UserAgent(), true)
		if r.URL.Path == "/dload" {
			w.Header().Set("Location", cdn.URL+"/file?name=hello world.mp4")
			w.WriteHeader(http.StatusFound)
			return
		}
		srv.ServeHTTP(w, r)
	}))
	defer cdn.Close()
	d := testDownloader(httpx.NewClient(httpx.Options{}), 1<<10)

	res, err := d.Download(context.Background(), request(cdn.URL+"/dload", t.TempDir(), 1), nil)

	if err != nil || res.Size != int64(len(srv.data)) {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, ok := agents.Load(httpx.DefaultUserAgent); !ok {
		t.Fatal("default User-Agent not sent")
	}
}

func TestDownloadStreamsWhenRangeIsIgnored(t *testing.T) {
	data := []byte("stream fallback")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) }))
	defer ts.Close()

	res, err := testDownloader(ts.Client(), 4).Download(context.Background(), request(ts.URL, t.TempDir(), 1), nil)

	if err != nil || res.Size != int64(len(data)) {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestDownloadRejectsOversizedRangeBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startText, endText, _ := strings.Cut(strings.TrimPrefix(r.Header.Get("Range"), "bytes="), "-")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %s-%s/8", startText, endText))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(make([]byte, 64)) // far more than the requested range
	}))
	defer ts.Close()

	_, err := testDownloader(ts.Client(), 4).Download(context.Background(), request(ts.URL, t.TempDir(), 1), nil)

	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadRejectsMalformedContentRange(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=0-0" {
			w.Header().Set("Content-Range", "bytes 0-0/4")
		} else {
			w.Header().Set("Content-Range", "broken")
		}
		w.WriteHeader(http.StatusPartialContent)
	}))
	defer ts.Close()

	_, err := testDownloader(ts.Client(), 4).Download(context.Background(), request(ts.URL, t.TempDir(), 1), nil)

	if err == nil || !strings.Contains(err.Error(), "content-range") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadReportsHTTPErrorAndValidation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer ts.Close()
	d := testDownloader(ts.Client(), 4)

	_, err := d.Download(context.Background(), request(ts.URL, t.TempDir(), 1), nil)
	var httpErr *engine.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusForbidden {
		t.Fatalf("err = %v", err)
	}
	if _, err := d.Download(context.Background(), engine.Request{}, nil); err == nil {
		t.Fatal("expected validation error")
	}
	wrongKind := request(ts.URL, t.TempDir(), 1)
	wrongKind.Source.Kind = domain.SourceHLS
	if _, err := d.Download(context.Background(), wrongKind, nil); err == nil {
		t.Fatal("expected kind error")
	}
}

func TestDownloadCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New().Download(ctx, request("http://127.0.0.1:1/video", t.TempDir(), 1), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadRefusesBusyDirectory(t *testing.T) {
	dir := t.TempDir()
	work, err := engine.WorkDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer work.Close()

	if _, err := New().Download(context.Background(), request("https://cdn.test/v.mp4", dir, 1), nil); !errors.Is(err, engine.ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestDownloadStreamReportsHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			_, _ = w.Write([]byte("x")) // ignores Range → stream fallback
			return
		}
		w.WriteHeader(http.StatusGone)
	}))
	defer ts.Close()

	_, err := testDownloader(ts.Client(), 4).Download(context.Background(), request(ts.URL, t.TempDir(), 1), nil)

	var httpErr *engine.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusGone {
		t.Fatalf("err = %v", err)
	}
}

func TestTotalFromContentRange(t *testing.T) {
	if n, err := totalFromContentRange("bytes 0-0/1234"); err != nil || n != 1234 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for _, bad := range []string{"bytes 0-0", "bytes 0-0/*"} {
		if _, err := totalFromContentRange(bad); err == nil {
			t.Errorf("totalFromContentRange(%q): expected error", bad)
		}
	}
}

func TestConcatChunksErrors(t *testing.T) {
	dir := t.TempDir()
	if err := concatChunks(context.Background(), dir, 1, filepath.Join(dir, "out")); err == nil {
		t.Fatal("expected missing chunk error")
	}
	if err := os.WriteFile(chunkPath(dir, 0), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := concatChunks(ctx, dir, 1, filepath.Join(dir, "out")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled err = %v", err)
	}
	if err := concatChunks(context.Background(), dir, 1, filepath.Join(dir, "missing-dir", "out")); err == nil {
		t.Fatal("expected create error")
	}
}

// A stream that breaks mid-body fails instead of producing a short file.
func TestDownloadStreamReportsBrokenBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			_, _ = w.Write([]byte("x"))
			return
		}
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("partial"))
	}))
	defer ts.Close()
	dir := t.TempDir()

	_, err := testDownloader(ts.Client(), 4).Download(context.Background(), request(ts.URL, dir, 1), nil)

	if err == nil || !strings.Contains(err.Error(), "read body") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(engine.PartialPath(dir)); !os.IsNotExist(statErr) {
		t.Fatal("partial file left behind")
	}
}
