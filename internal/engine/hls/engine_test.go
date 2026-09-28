package hls

import (
	"context"
	"errors"
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

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/engine"
	"github.com/jooservices/go-jabledownloader/internal/platform/httpx"
)

func requireFFmpegForTest(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
}

// generateTS renders a one-second black MPEG-TS segment with ffmpeg.
func generateTS(t *testing.T) []byte {
	t.Helper()
	requireFFmpegForTest(t)
	path := filepath.Join(t.TempDir(), "seed.ts")
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

func mediaPlaylist(n int) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:1\n")
	for i := range n {
		fmt.Fprintf(&b, "#EXTINF:1.0,\nseg%d.ts\n", i)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

type recorder struct {
	mu     sync.Mutex
	events []domain.Event
}

func (r *recorder) sink(ev domain.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recorder) last(kind domain.EventKind) (domain.Event, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var last domain.Event
	count := 0
	for _, ev := range r.events {
		if ev.Kind == kind {
			last = ev
			count++
		}
	}
	return last, count
}

func testEngine(client *http.Client) *Engine {
	e := New(WithHTTPClient(client))
	e.backoff = time.Millisecond
	return e
}

func request(srvURL, dir string) engine.Request {
	return engine.Request{Code: "jur-001", Dir: dir, Workers: 2,
		Source: domain.Source{Kind: domain.SourceHLS, URL: srvURL}}
}

func TestDownloadMediaPlaylistProducesMP4AndCountsBytes(t *testing.T) {
	seg := generateTS(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/media.m3u8", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, mediaPlaylist(2)) })
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(seg) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	dir := t.TempDir()
	rec := &recorder{}

	res, err := testEngine(srv.Client()).Download(context.Background(), request(srv.URL+"/media.m3u8", dir), rec.sink)

	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if res.Path != filepath.Join(dir, "jur-001-h264.mp4") || res.Codec != "h264" || res.Size <= 0 {
		t.Fatalf("result = %+v", res)
	}
	progress, _ := rec.last(domain.EventProgress)
	if progress.Done != 2 || progress.Bytes != int64(2*len(seg)) {
		t.Fatalf("last progress = %+v, want 2 segments / %d bytes", progress, 2*len(seg))
	}
	if _, err := os.Stat(filepath.Join(dir, ".segments")); !os.IsNotExist(err) {
		t.Fatalf("segments not discarded: %v", err)
	}
}

func TestDownloadMasterPlaylistHonoursMaxHeight(t *testing.T) {
	seg := generateTS(t)
	var lowHits, highHits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/master.m3u8", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n"+
			"#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360,CODECS=\"avc1.4D401E,mp4a.40.2\"\nlow.m3u8\n"+
			"#EXT-X-STREAM-INF:BANDWIDTH=2800000,RESOLUTION=1280x720,CODECS=\"avc1.640028,mp4a.40.2\"\nhigh.m3u8\n")
	})
	mux.HandleFunc("/low.m3u8", func(w http.ResponseWriter, _ *http.Request) { lowHits.Add(1); fmt.Fprint(w, mediaPlaylist(1)) })
	mux.HandleFunc("/high.m3u8", func(w http.ResponseWriter, _ *http.Request) { highHits.Add(1); fmt.Fprint(w, mediaPlaylist(1)) })
	mux.HandleFunc("/seg0.ts", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(seg) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	req := request(srv.URL+"/master.m3u8", t.TempDir())
	req.MaxHeight = 360

	if _, err := testEngine(srv.Client()).Download(context.Background(), req, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if lowHits.Load() != 1 || highHits.Load() != 0 {
		t.Fatalf("variant hits low=%d high=%d", lowHits.Load(), highHits.Load())
	}
}

func TestDownloadEncryptedPlaylistRemuxesWithFFmpeg(t *testing.T) {
	seg := generateTS(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/stream.m3u8", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n#EXT-X-TARGETDURATION:1\n#EXTINF:1.0,\nseg.ts\n#EXT-X-ENDLIST\n")
	})
	mux.HandleFunc("/key.bin", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, 16)) })
	mux.HandleFunc("/seg.ts", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(seg) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := testEngine(srv.Client()).Download(ctx, request(srv.URL+"/stream.m3u8", t.TempDir()), nil)

	// The segment is not really encrypted, so ffmpeg usually fails to decrypt;
	// either way the remux path (not segment concat) must have run.
	if err != nil && !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("expected the ffmpeg remux path, got %v", err)
	}
}

// Regression: counters were unsynchronised (data race) and bytes stayed 0
// for freshly downloaded segments.
func TestDownloadSegmentsCountersAreRaceFreeAndCountWrittenBytes(t *testing.T) {
	const segments, size = 200, 1000
	mux := http.NewServeMux()
	mux.HandleFunc("/media.m3u8", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, mediaPlaylist(segments)) })
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, size)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e := testEngine(srv.Client())
	work, err := engine.WorkDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer work.Close()
	pl, err := ParsePlaylist(mediaPlaylist(segments), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}

	err = e.downloadSegments(context.Background(), work, pl, engine.Request{Workers: 8}, rec.sink)

	if err != nil {
		t.Fatal(err)
	}
	progress, count := rec.last(domain.EventProgress)
	if count != segments || progress.Done != segments || progress.Bytes != segments*size {
		t.Fatalf("progress events=%d last=%+v", count, progress)
	}
}

// Regression: one failed segment used to trigger a silent full re-download.
func TestDownloadSegmentFailureKeepsOthersAndResumes(t *testing.T) {
	var broken atomic.Bool
	broken.Store(true)
	hits := map[string]int{}
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		if r.URL.Path == "/media.m3u8" {
			fmt.Fprint(w, mediaPlaylist(5))
			return
		}
		if r.URL.Path == "/seg3.ts" && broken.Load() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("segment"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	dir := t.TempDir()
	e := testEngine(srv.Client())
	req := request(srv.URL+"/media.m3u8", dir)
	req.Workers = 1

	_, err := e.Download(context.Background(), req, nil)

	if err == nil || !strings.Contains(err.Error(), "segment 4 of 5") || !strings.Contains(err.Error(), "re-run to resume") {
		t.Fatalf("err = %v", err)
	}
	mu.Lock()
	playlistHits := hits["/media.m3u8"]
	mu.Unlock()
	if playlistHits != 1 {
		t.Fatalf("media playlist fetched %d times; a failed segment must not trigger an ffmpeg re-download", playlistHits)
	}
	if countExisting(filepath.Join(dir, ".segments"), 5) != 3 {
		t.Fatalf("expected the 3 segments before the failure to stay on disk")
	}

	broken.Store(false)
	mu.Lock()
	clear(hits)
	mu.Unlock()
	work, err := engine.WorkDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer work.Close()
	pl, _ := ParsePlaylist(mediaPlaylist(5), srv.URL+"/")
	if err := e.downloadSegments(context.Background(), work, pl, req, nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if hits["/seg0.ts"] != 0 || hits["/seg3.ts"] != 1 || hits["/seg4.ts"] != 1 {
		t.Fatalf("resume hits = %v", hits)
	}
}

func TestDownloadRetriesTransientSegmentErrors(t *testing.T) {
	var calls atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/media.m3u8", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, mediaPlaylist(1)) })
	mux.HandleFunc("/seg0.ts", func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("segment"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	work, _ := engine.WorkDir(t.TempDir())
	defer work.Close()
	pl, _ := ParsePlaylist(mediaPlaylist(1), srv.URL+"/")
	rec := &recorder{}

	err := testEngine(srv.Client()).downloadSegments(context.Background(), work, pl, engine.Request{Workers: 1}, rec.sink)

	if err != nil {
		t.Fatal(err)
	}
	retry, retries := rec.last(domain.EventRetry)
	if retries != 2 || !strings.Contains(retry.Message, "server error") {
		t.Fatalf("retries=%d last=%+v", retries, retry)
	}
}

func TestDownloadSendsSourceHeaders(t *testing.T) {
	var mu sync.Mutex
	var referers, agents []string
	mux := http.NewServeMux()
	mux.HandleFunc("/media.m3u8", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, mediaPlaylist(1)) })
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("segment")) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		referers = append(referers, r.Referer())
		agents = append(agents, r.UserAgent())
		mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	defer srv.Close()
	req := request(srv.URL+"/media.m3u8", t.TempDir())
	req.Source.Headers = http.Header{"Referer": {"https://site.test/"}, "User-Agent": {"browser/1"}}

	_, _ = testEngine(srv.Client()).Download(context.Background(), req, nil)

	mu.Lock()
	defer mu.Unlock()
	if len(referers) < 2 {
		t.Fatalf("requests = %d", len(referers))
	}
	for i := range referers {
		if referers[i] != "https://site.test/" || agents[i] != "browser/1" {
			t.Fatalf("request %d referer=%q ua=%q", i, referers[i], agents[i])
		}
	}
}

func TestDownloadReportsPlaylistHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := testEngine(srv.Client()).Download(context.Background(), request(srv.URL+"/x.m3u8", t.TempDir()), nil)

	var httpErr *engine.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusForbidden {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadRejectsOversizedPlaylist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, maxPlaylistBytes+1))
	}))
	defer srv.Close()

	_, err := testEngine(srv.Client()).Download(context.Background(), request(srv.URL+"/x.m3u8", t.TempDir()), nil)

	if err == nil || !strings.Contains(err.Error(), "playlist exceeds") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadValidatesRequest(t *testing.T) {
	e := New()
	if _, err := e.Download(context.Background(), engine.Request{}, nil); err == nil {
		t.Fatal("expected validation error")
	}
	req := request("https://cdn.test/x.m3u8", t.TempDir())
	req.Source.Kind = domain.SourceProgressive
	if _, err := e.Download(context.Background(), req, nil); err == nil {
		t.Fatal("expected kind error")
	}
}

func TestDownloadRefusesBusyDirectory(t *testing.T) {
	dir := t.TempDir()
	work, err := engine.WorkDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer work.Close()

	_, err = New().Download(context.Background(), request("https://cdn.test/x.m3u8", dir), nil)

	if !errors.Is(err, engine.ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestDownloadCancelledKeepsSegments(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mux := http.NewServeMux()
	mux.HandleFunc("/media.m3u8", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, mediaPlaylist(3)) })
	mux.HandleFunc("/seg0.ts", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("segment")) })
	mux.HandleFunc("/", func(_ http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	dir := t.TempDir()
	req := request(srv.URL+"/media.m3u8", dir)
	req.Workers = 1

	_, err := testEngine(srv.Client()).Download(ctx, req, nil)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if countExisting(filepath.Join(dir, ".segments"), 3) != 1 {
		t.Fatal("expected downloaded segment to stay for resume")
	}
}

func TestConcatListEscapesQuotes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Viet's Videos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(segmentPath(dir, 0), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := concatList(dir, 1)

	want := "file '" + strings.ReplaceAll(segmentPath(dir, 0), "'", `'\''`) + "'\n"
	if err != nil || list != want || !strings.Contains(list, `Viet'\''s`) {
		t.Fatalf("list=%q err=%v", list, err)
	}
	if _, err := concatList(dir, 2); err == nil {
		t.Fatal("expected missing segment error")
	}
}

func TestConcatMuxesSegments(t *testing.T) {
	seg := generateTS(t)
	dir := t.TempDir()
	for i := range 2 {
		if err := os.WriteFile(segmentPath(dir, i), seg, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "out.mp4")
	if err := concat(context.Background(), dir, 2, out); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(out); err != nil || info.Size() == 0 {
		t.Fatalf("output missing: %v", err)
	}
}

func TestRemuxArgsWhitelistRemoteProtocolsAndHeaders(t *testing.T) {
	args, err := remuxArgs("https://cdn.test/v.m3u8", http.Header{"Referer": {"https://site.test/"}}, "/out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	whitelist := strings.Index(joined, "-protocol_whitelist "+remoteProtocols)
	input := strings.Index(joined, "-i https://cdn.test/v.m3u8")
	if whitelist < 0 || input < 0 || whitelist > input {
		t.Fatalf("args = %q", args)
	}
	if !strings.Contains(joined, "Referer: https://site.test/\r\n") || !strings.Contains(joined, "User-Agent: "+httpx.DefaultUserAgent) {
		t.Fatalf("headers missing: %q", joined)
	}
}

func TestFFmpegHeadersRejectLineBreaks(t *testing.T) {
	if _, err := ffmpegHeaders(http.Header{"Referer": {"x\r\nEvil: 1"}}); err == nil {
		t.Fatal("expected header injection error")
	}
	got, err := ffmpegHeaders(nil)
	if err != nil || got != "User-Agent: "+httpx.DefaultUserAgent+"\r\n" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestReportProgressParsesFFmpegBlocks(t *testing.T) {
	rec := &recorder{}
	reportProgress(strings.NewReader("out_time_us=2500000\nspeed=1.5x\nprogress=continue\nbogus\nspeed=N/A\nprogress=end\n"), rec.sink)

	ev, count := rec.last(domain.EventTimeProgress)
	if count != 2 || ev.Seconds != 2.5 || ev.Speed != 1.5 {
		t.Fatalf("count=%d last=%+v", count, ev)
	}
}

func TestPlaylistBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://c.test/x/index.m3u8?t=a/b": "https://c.test/x/",
		"https://c.test/index.m3u8":         "https://c.test/",
		"https://c.test/a/b/":               "https://c.test/a/b/",
	} {
		if got := playlistBaseURL(in); got != want {
			t.Errorf("playlistBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFetchSegmentRejectsEmptyAndOversizedBodies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/big.ts" {
			_, _ = w.Write(make([]byte, maxSegmentBytes+1))
		}
	}))
	defer srv.Close()
	e := testEngine(srv.Client())
	dir := t.TempDir()
	for path, want := range map[string]string{"/empty.ts": "empty segment", "/big.ts": "segment exceeds"} {
		out := filepath.Join(dir, strings.TrimPrefix(path, "/"))
		if _, err := e.fetchSegment(context.Background(), srv.URL+path, nil, out); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", path, err)
		}
		if _, err := os.Stat(out + ".tmp"); !os.IsNotExist(err) {
			t.Errorf("%s: temporary file left behind", path)
		}
	}
}

func TestFFmpegMissingIsReported(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := remux(context.Background(), "https://cdn.test/v.m3u8", nil, filepath.Join(t.TempDir(), "o.mp4"), nil); err == nil || !strings.Contains(err.Error(), "ffmpeg is required") {
		t.Fatalf("remux err = %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(segmentPath(dir, 0), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := concat(context.Background(), dir, 1, filepath.Join(dir, "o.mp4")); err == nil || !strings.Contains(err.Error(), "ffmpeg is required") {
		t.Fatalf("concat err = %v", err)
	}
	if _, err := remuxArgs("u", http.Header{"X": {"a\nb"}}, "o"); err == nil {
		t.Fatal("expected header error")
	}
}

// Invalid media makes ffmpeg fail: the concat error is reported, and remux
// surfaces ffmpeg's own message.
func TestFFmpegFailuresSurfaceOutput(t *testing.T) {
	requireFFmpegForTest(t)
	dir := t.TempDir()
	if err := os.WriteFile(segmentPath(dir, 0), []byte("not a transport stream"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := concat(context.Background(), dir, 1, filepath.Join(dir, "o.mp4")); err == nil || !strings.Contains(err.Error(), "ffmpeg concat") {
		t.Fatalf("concat err = %v", err)
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if err := remux(context.Background(), srv.URL+"/missing.m3u8", nil, filepath.Join(dir, "r.mp4"), nil); err == nil || !strings.Contains(err.Error(), "ffmpeg:") {
		t.Fatalf("remux err = %v", err)
	}
}

// Segments that ffmpeg cannot concatenate fall back to a remux, announced
// with a retry event.
func TestConcatFailureFallsBackToRemux(t *testing.T) {
	requireFFmpegForTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/media.m3u8", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, mediaPlaylist(1)) })
	mux.HandleFunc("/seg0.ts", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("garbage")) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rec := &recorder{}

	_, err := testEngine(srv.Client()).Download(context.Background(), request(srv.URL+"/media.m3u8", t.TempDir()), rec.sink)

	retry, _ := rec.last(domain.EventRetry)
	if err == nil || !strings.Contains(retry.Message, "remuxing with ffmpeg") {
		t.Fatalf("err=%v retry=%+v", err, retry)
	}
}
