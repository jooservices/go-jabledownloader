package direct

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func rangeServer(payload []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHdr := r.Header.Get("Range")
		if rangeHdr == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
			return
		}
		start, end, ok := parseRange(rangeHdr, len(payload))
		if !ok {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(payload)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
}

func parseRange(h string, size int) (int, int, bool) {
	h = strings.TrimPrefix(h, "bytes=")
	parts := strings.SplitN(h, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	start, err1 := strconv.Atoi(parts[0])
	end, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || start < 0 || end >= size || start > end {
		return 0, 0, false
	}
	return start, end, true
}

func TestDownloadParallelRanges(t *testing.T) {
	payload := make([]byte, 3*1024*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	srv := rangeServer(payload)
	defer srv.Close()

	outDir := t.TempDir()
	dl := NewDownloader(outDir, WithWorkers(3))

	vf, err := dl.Download(context.Background(), "vid-001", "h264", srv.URL)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if vf.Codec != "h264" {
		t.Fatalf("codec = %q", vf.Codec)
	}
	if vf.Size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", vf.Size, len(payload))
	}
	if filepath.Base(vf.Path) != "vid-001-h264.mp4" {
		t.Fatalf("path = %q", vf.Path)
	}
	got, err := os.ReadFile(vf.Path)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatal("output content mismatch")
	}
	if _, err := os.Stat(filepath.Join(outDir, ".segments")); !os.IsNotExist(err) {
		t.Fatal("expected .segments to be removed after success")
	}
}

func TestDownloadSingleStreamFallback(t *testing.T) {
	payload := []byte("single-stream payload without range support")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	outDir := t.TempDir()
	dl := NewDownloader(outDir)

	vf, err := dl.Download(context.Background(), "vid-002", "h264", srv.URL)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if vf.Size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", vf.Size, len(payload))
	}
	got, err := os.ReadFile(vf.Path)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatal("output content mismatch")
	}
}

func TestDownloadResumesExistingChunks(t *testing.T) {
	payload := make([]byte, 3*1024*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	srv := rangeServer(payload)
	defer srv.Close()

	outDir := t.TempDir()

	// Seed one already-downloaded chunk with a matching fingerprint.
	seed := func() error {
		if err := os.MkdirAll(filepath.Join(outDir, ".segments"), 0o755); err != nil {
			return err
		}
		fp := sourceFingerprint(srv.URL, int64(len(payload)))
		if err := os.WriteFile(filepath.Join(outDir, ".segments", ".source"), []byte(fp+"\n"), 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(outDir, ".segments", "seg_000000"), payload[:1024*1024], 0o644)
	}
	if err := seed(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var resumed bool
	dl := NewDownloader(outDir, WithWorkers(3), WithProgress(func(ev Event) {
		if ev.Kind == EventResume {
			resumed = true
		}
	}))

	vf, err := dl.Download(context.Background(), "vid-003", "h264", srv.URL)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !resumed {
		t.Fatal("expected resume event")
	}
	got, err := os.ReadFile(vf.Path)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatal("output content mismatch after resume")
	}
}

func TestDownloadHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	dl := NewDownloader(t.TempDir())
	if _, err := dl.Download(context.Background(), "vid", "h264", srv.URL); err == nil {
		t.Fatal("expected error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWithHTTPClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "yes" {
			http.Error(w, "missing header", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Length", "5")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	c := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.Header.Set("X-Test", "yes")
		return http.DefaultTransport.RoundTrip(req)
	})}
	dl := NewDownloader(t.TempDir(), WithHTTPClient(c))
	vf, err := dl.Download(context.Background(), "v", "h264", srv.URL)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if vf.Size != 5 {
		t.Fatalf("size = %d, want 5", vf.Size)
	}
}

func TestDownloadRangeNotSatisfiableFallback(t *testing.T) {
	payload := []byte("fallback-content-when-range-unsupported")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(payload)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dl := NewDownloader(t.TempDir())
	vf, err := dl.Download(context.Background(), "v", "h264", srv.URL)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if vf.Size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", vf.Size, len(payload))
	}
}

func TestDownloadRetriesTransientChunkError(t *testing.T) {
	payload := make([]byte, 2*1024*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	var chunk0Attempts int
	var retried bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=0-1048575" && chunk0Attempts == 0 {
			chunk0Attempts++
			http.Error(w, "flaky", http.StatusInternalServerError)
			return
		}
		start, end, ok := parseRange(r.Header.Get("Range"), len(payload))
		if !ok {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer srv.Close()

	outDir := t.TempDir()
	dl := NewDownloader(outDir, WithWorkers(2), WithProgress(func(ev Event) {
		if ev.Kind == EventRetry {
			retried = true
		}
	}))

	vf, err := dl.Download(context.Background(), "v", "h264", srv.URL)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !retried {
		t.Fatal("expected retry event")
	}
	got, err := os.ReadFile(vf.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatal("content mismatch after retry")
	}
}

func TestDownloadFailsAfterRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	dl := NewDownloader(t.TempDir(), WithWorkers(1))
	if _, err := dl.Download(context.Background(), "v", "h264", srv.URL); err == nil {
		t.Fatal("expected error after retries")
	}
}

func TestConcatChunksMissingChunk(t *testing.T) {
	outDir := t.TempDir()
	segDir := filepath.Join(outDir, ".segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dl := NewDownloader(outDir)
	plan := []chunk{{start: 0, end: 0, size: 1}}
	if err := dl.concatChunks(context.Background(), segDir, plan, filepath.Join(outDir, "o.mp4")); err == nil {
		t.Fatal("expected error for missing chunk")
	}
}

func TestProbeMalformedContentRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Range", "garbage")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	dl := NewDownloader(t.TempDir())
	size, rangeOK, err := dl.probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if rangeOK || size != 1 {
		t.Fatalf("size=%d rangeOK=%v, want size=1 rangeOK=false", size, rangeOK)
	}
}

func TestDownloadStreamWriteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic(http.ErrAbortHandler)
	}))
	defer srv.Close()

	dl := NewDownloader(t.TempDir())
	if _, err := dl.Download(context.Background(), "v", "h264", srv.URL); err == nil {
		t.Fatal("expected error on truncated body")
	}
}

func TestProbeNoContentRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "7")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("partial"))
	}))
	defer srv.Close()

	dl := NewDownloader(t.TempDir())
	size, rangeOK, err := dl.probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if rangeOK || size != 7 {
		t.Fatalf("size=%d rangeOK=%v, want size=7 rangeOK=false", size, rangeOK)
	}
}

func TestPrepareChunkDirFingerprintMismatch(t *testing.T) {
	outDir := t.TempDir()
	segDir := filepath.Join(outDir, ".segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(segDir, ".source"), []byte("stale-fingerprint\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(segDir, "seg_000000"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	dl := NewDownloader(outDir)
	got, err := dl.prepareChunkDir("https://example.test/v.mp4", 1024)
	if err != nil {
		t.Fatalf("prepareChunkDir: %v", err)
	}
	if got != segDir {
		t.Fatalf("dir = %q", got)
	}
	data, err := os.ReadFile(filepath.Join(segDir, ".source"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "stale-fingerprint\n" {
		t.Fatal("expected fingerprint to be rewritten on mismatch")
	}
}

func TestRetryHintAndStatusCode(t *testing.T) {
	if got := retryHint(nil); got != "transient failure — retrying" {
		t.Fatalf("retryHint(nil) = %q", got)
	}
	if got := retryHint(fmt.Errorf("boom")); got != "transient failure — retrying" {
		t.Fatalf("retryHint(boom) = %q", got)
	}
	if got := retryHint(fmt.Errorf("http status 429")); got == "" || got == "transient failure — retrying" {
		t.Fatalf("retryHint(429) = %q", got)
	}
	if got := statusCode(fmt.Errorf("http status 403")); got != 403 {
		t.Fatalf("statusCode = %d", got)
	}
	if got := statusCode(fmt.Errorf("no status here")); got != 0 {
		t.Fatalf("statusCode(none) = %d", got)
	}
}

func TestProbeNetworkError(t *testing.T) {
	dl := NewDownloader(t.TempDir())
	if _, _, err := dl.probe(context.Background(), "http://127.0.0.1:1/x"); err == nil {
		t.Fatal("expected network error")
	}
}

func TestDownloadRangeContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dl := NewDownloader(t.TempDir())
	if err := dl.downloadRange(ctx, srv.URL, 0, 10, filepath.Join(t.TempDir(), "seg")); err == nil {
		t.Fatal("expected context error")
	}
}

func TestHTTPHint(t *testing.T) {
	if httpHint(http.StatusTooManyRequests) == "" {
		t.Fatal("expected hint for 429")
	}
	if httpHint(http.StatusForbidden) == "" {
		t.Fatal("expected hint for 403")
	}
	if httpHint(http.StatusOK) != "" {
		t.Fatal("expected empty hint for 200")
	}
}

func TestChunkPlan(t *testing.T) {
	dl := NewDownloader(t.TempDir(), WithWorkers(3))
	if got := len(dl.chunkPlan(500 * 1024)); got != 1 {
		t.Fatalf("small file chunks = %d, want 1", got)
	}
	plan := dl.chunkPlan(5 * 1024 * 1024)
	if len(plan) != 3 {
		t.Fatalf("5MB chunks = %d, want 3 (workers)", len(plan))
	}
	var total int64
	for _, c := range plan {
		total += c.size
	}
	if total != 5*1024*1024 {
		t.Fatalf("plan covers %d bytes, want %d", total, 5*1024*1024)
	}
}

func TestConcatChunksContextCancel(t *testing.T) {
	outDir := t.TempDir()
	segDir := filepath.Join(outDir, ".segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(segDir, "seg_000000"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dl := NewDownloader(outDir)
	plan := []chunk{{start: 0, end: 0, size: 1}}
	if err := dl.concatChunks(ctx, segDir, plan, filepath.Join(outDir, "out.mp4")); err == nil {
		t.Fatal("expected error on cancelled context")
	}
}
