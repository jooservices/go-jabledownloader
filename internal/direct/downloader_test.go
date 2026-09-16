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
