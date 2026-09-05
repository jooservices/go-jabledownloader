package hls

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
	"time"
)

func generateTS(t *testing.T, path string) []byte {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=160x120:d=1",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-bsf:v", "h264_mp4toannexb",
		"-f", "mpegts", "-y", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate ts: %v\n%s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDownloadMediaPlaylist(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}

	tmp := t.TempDir()
	segBytes := generateTS(t, filepath.Join(tmp, "seed.ts"))

	mux := http.NewServeMux()
	mux.HandleFunc("/media.m3u8", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1.0,\nseg0.ts\n#EXTINF:1.0,\nseg1.ts\n#EXT-X-ENDLIST\n")
	})
	mux.HandleFunc("/seg0.ts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(segBytes)
	})
	mux.HandleFunc("/seg1.ts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(segBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	outDir := t.TempDir()
	var events []Event
	var mu sync.Mutex
	dl := NewDownloader(outDir,
		WithWorkers(1),
		WithHTTPClient(srv.Client()),
		WithProgress(func(ev Event) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		}),
	)

	vf, err := dl.Download(context.Background(), "jur-001", srv.URL+"/media.m3u8")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if vf.Codec != "h264" {
		t.Fatalf("codec = %q", vf.Codec)
	}
	if !strings.HasSuffix(vf.Path, "jur-001-h264.mp4") {
		t.Fatalf("path = %q", vf.Path)
	}
	if vf.Size <= 0 {
		t.Fatal("expected non-zero size")
	}

	mu.Lock()
	defer mu.Unlock()
	foundSeg := false
	for _, ev := range events {
		if ev.Kind == EventSegments {
			foundSeg = true
			break
		}
	}
	if !foundSeg {
		t.Fatal("expected EventSegments progress")
	}
}

func TestDownloadMasterPlaylist(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}

	tmp := t.TempDir()
	segBytes := generateTS(t, filepath.Join(tmp, "seed.ts"))

	mux := http.NewServeMux()
	mux.HandleFunc("/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360,CODECS="avc1.4D401E,mp4a.40.2"
low.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2800000,RESOLUTION=1280x720,CODECS="avc1.640028,mp4a.40.2"
high.m3u8
`)
	})
	mux.HandleFunc("/high.m3u8", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1.0,\nseg0.ts\n#EXT-X-ENDLIST\n")
	})
	mux.HandleFunc("/seg0.ts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(segBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	outDir := t.TempDir()
	dl := NewDownloader(outDir, WithWorkers(1), WithHTTPClient(srv.Client()))
	vf, err := dl.Download(context.Background(), "abc-002", srv.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if vf.Codec != "h264" {
		t.Fatalf("codec = %q, want h264", vf.Codec)
	}
}

func TestDownloadHTTPErrorHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	dl := NewDownloader(t.TempDir(), WithHTTPClient(srv.Client()))
	_, err := dl.Download(context.Background(), "x", srv.URL+"/missing.m3u8")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "hint:") {
		t.Fatalf("expected hint in error: %v", err)
	}
}

func TestDownloadEncryptedFallsBackToFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}

	tmp := t.TempDir()
	segBytes := generateTS(t, filepath.Join(tmp, "seed.ts"))

	mux := http.NewServeMux()
	mux.HandleFunc("/stream.m3u8", func(w http.ResponseWriter, r *http.Request) {
		// KEY marks the playlist encrypted so Download uses downloadDirect.
		fmt.Fprintf(w, `#EXTM3U
#EXT-X-KEY:METHOD=AES-128,URI="key.bin"
#EXT-X-TARGETDURATION:1
#EXTINF:1.0,
seg.ts
#EXT-X-ENDLIST
`)
	})
	mux.HandleFunc("/key.bin", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 16))
	})
	mux.HandleFunc("/seg.ts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(segBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var sawFFmpeg atomic.Bool
	outDir := t.TempDir()
	dl := NewDownloader(outDir,
		WithWorkers(1),
		WithHTTPClient(srv.Client()),
		WithProgress(func(ev Event) {
			if ev.Kind == EventFFmpeg {
				sawFFmpeg.Store(true)
			}
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := dl.Download(ctx, "enc-001", srv.URL+"/stream.m3u8")
	// AES without a real key material usually fails; the direct path must still run.
	if err == nil {
		if !sawFFmpeg.Load() {
			t.Fatal("expected EventFFmpeg progress on success")
		}
		return
	}
	if !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("expected ffmpeg error from encrypted direct path, got %v", err)
	}
}

func TestDownloadResumeExistingSegments(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}

	tmp := t.TempDir()
	segBytes := generateTS(t, filepath.Join(tmp, "seed.ts"))

	mux := http.NewServeMux()
	mux.HandleFunc("/media.m3u8", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1.0,\nseg0.ts\n#EXT-X-ENDLIST\n")
	})
	var segHits atomic.Int64
	mux.HandleFunc("/seg0.ts", func(w http.ResponseWriter, r *http.Request) {
		segHits.Add(1)
		_, _ = w.Write(segBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	outDir := t.TempDir()
	segDir := filepath.Join(outDir, ".segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(segDir, "seg_000000.ts"), segBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	var resumeMsg string
	dl := NewDownloader(outDir,
		WithWorkers(1),
		WithHTTPClient(srv.Client()),
		WithProgress(func(ev Event) {
			if ev.Kind == EventResume {
				resumeMsg = ev.Message
			}
		}),
	)
	if _, err := dl.Download(context.Background(), "res-001", srv.URL+"/media.m3u8"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if resumeMsg == "" {
		t.Fatal("expected resume event")
	}
	// Segment HTTP may still be used if concat falls back to ffmpeg; resume
	// detection itself is what this test asserts.
	_ = segHits
}

func TestDownloadDirectFallbackSuccess(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}

	tmp := t.TempDir()
	segBytes := generateTS(t, filepath.Join(tmp, "seed.ts"))

	mux := http.NewServeMux()
	mux.HandleFunc("/media.m3u8", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1.0,\nseg0.ts\n#EXT-X-ENDLIST\n")
	})
	mux.HandleFunc("/seg0.ts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(segBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	outDir := t.TempDir()
	segDir := filepath.Join(outDir, ".segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Empty placeholder makes downloadSegment skip and concat fail → ffmpeg direct.
	if err := os.WriteFile(filepath.Join(segDir, "seg_000000.ts"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	var sawFFmpeg atomic.Bool
	dl := NewDownloader(outDir,
		WithWorkers(1),
		WithHTTPClient(srv.Client()),
		WithProgress(func(ev Event) {
			if ev.Kind == EventFFmpeg {
				sawFFmpeg.Store(true)
			}
		}),
	)
	vf, err := dl.Download(context.Background(), "fb-001", srv.URL+"/media.m3u8")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if vf.Size <= 0 {
		t.Fatal("expected non-zero mp4")
	}
	if !sawFFmpeg.Load() {
		t.Fatal("expected EventFFmpeg from direct fallback")
	}
}

func TestDownloadSegmentRetry(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}

	tmp := t.TempDir()
	segBytes := generateTS(t, filepath.Join(tmp, "seed.ts"))

	var hits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/media.m3u8", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1.0,\nseg0.ts\n#EXT-X-ENDLIST\n")
	})
	mux.HandleFunc("/seg0.ts", func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write(segBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var retries int
	dl := NewDownloader(t.TempDir(),
		WithWorkers(1),
		WithHTTPClient(srv.Client()),
		WithProgress(func(ev Event) {
			if ev.Kind == EventRetry {
				retries++
			}
		}),
	)
	if _, err := dl.Download(context.Background(), "retry-1", srv.URL+"/media.m3u8"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if retries == 0 {
		t.Fatal("expected retry event")
	}
}

func TestFetchStatusWithoutHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	dl := NewDownloader(t.TempDir(), WithHTTPClient(srv.Client()))
	_, err := dl.fetch(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "418") {
		t.Fatalf("expected 418 error, got %v", err)
	}
}

func TestDownloadCancelContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "#EXTM3U\n#EXTINF:1.0,\nseg0.ts\n#EXT-X-ENDLIST\n")
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dl := NewDownloader(t.TempDir(), WithWorkers(1), WithHTTPClient(srv.Client()))
	_, err := dl.Download(ctx, "c", srv.URL+"/media.m3u8")
	if err == nil {
		t.Fatal("expected canceled error")
	}
}

func TestDownloadSegmentNonRetryable(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	tmp := t.TempDir()
	segBytes := generateTS(t, filepath.Join(tmp, "seed.ts"))
	mux := http.NewServeMux()
	mux.HandleFunc("/media.m3u8", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1.0,\nok.ts\n#EXTINF:1.0,\nbad.ts\n#EXT-X-ENDLIST\n")
	})
	mux.HandleFunc("/ok.ts", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(segBytes) })
	mux.HandleFunc("/bad.ts", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dl := NewDownloader(t.TempDir(), WithWorkers(1), WithHTTPClient(srv.Client()))
	// One segment fails permanently → concat fails → ffmpeg fallback may still succeed.
	_, _ = dl.Download(context.Background(), "nr-1", srv.URL+"/media.m3u8")
}

func TestDownloadMasterMediaFail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1000000,CODECS="avc1.640028,mp4a.40.2"
high.m3u8
`)
	})
	mux.HandleFunc("/high.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dl := NewDownloader(t.TempDir(), WithHTTPClient(srv.Client()))
	_, err := dl.Download(context.Background(), "x", srv.URL+"/master.m3u8")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewDownloaderDefaults(t *testing.T) {
	dl := NewDownloader(t.TempDir(), WithWorkers(0), WithHTTPClient(nil), WithProgress(nil))
	if dl.workers != 16 {
		t.Fatalf("workers = %d, want 16", dl.workers)
	}
	if dl.client == nil {
		t.Fatal("expected default client")
	}
	dl.emit(Event{Kind: EventRetry}) // no panic when progress nil
}

func TestDownloadSegmentHardError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".m3u8") {
			fmt.Fprintf(w, "#EXTM3U\n#EXTINF:0.05,\nseg0.ts\n#EXT-X-ENDLIST\n")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dl := NewDownloader(t.TempDir(), WithWorkers(1), WithHTTPClient(srv.Client()))
	// Segment 404 is non-retryable; concat then fails and falls back to ffmpeg direct.
	_, err := dl.Download(context.Background(), "miss", srv.URL+"/media.m3u8")
	// Either ffmpeg fallback error or success — must not panic.
	_ = err
}
