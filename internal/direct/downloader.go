// Package direct downloads progressive media files over plain HTTP using
// parallel Range requests. It mirrors the hls engine's conventions: pure
// engine (no ui/config/telemetry imports), progress leaves via ProgressFunc,
// resume state lives in a ".segments" directory keyed by a source fingerprint.
package direct

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// VideoFile describes a downloaded video on disk.
type VideoFile struct {
	Path  string
	Size  int64
	Codec string
}

// EventKind classifies progress events emitted by the downloader.
type EventKind int

const (
	// EventSegments reports chunk download progress.
	EventSegments EventKind = iota
	// EventRetry reports a transient failure that will be retried.
	EventRetry
	// EventResume reports that existing chunks were found on disk.
	EventResume
)

// Event is a progress event. The app layer decides how to render it.
type Event struct {
	Kind    EventKind
	Done    int64
	Total   int64
	Bytes   int64
	Failed  int64
	Seconds float64
	Speed   float64
	Message string
}

// ProgressFunc receives download progress events.
type ProgressFunc func(Event)

// Option configures a Downloader.
type Option func(*Downloader)

// WithWorkers sets the number of concurrent chunk workers.
func WithWorkers(n int) Option {
	return func(d *Downloader) {
		if n > 0 {
			d.workers = n
		}
	}
}

// WithHTTPClient overrides the default client (tests inject fixtures here).
func WithHTTPClient(c *http.Client) Option {
	return func(d *Downloader) {
		if c != nil {
			d.client = c
		}
	}
}

// WithProgress attaches a progress event callback.
func WithProgress(fn ProgressFunc) Option {
	return func(d *Downloader) {
		d.progress = fn
	}
}

// Downloader downloads a progressive file into an output directory.
type Downloader struct {
	outDir   string
	workers  int
	client   *http.Client
	progress ProgressFunc
}

// NewDownloader builds a Downloader for outDir with sensible defaults.
func NewDownloader(outDir string, opts ...Option) *Downloader {
	d := &Downloader{
		outDir:  outDir,
		workers: 16,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

func (d *Downloader) emit(ev Event) {
	if d.progress != nil {
		d.progress(ev)
	}
}

// Download fetches url into "<code>-<codec>.mp4". It prefers parallel Range
// chunks with resume; when the server ignores ranges it falls back to a
// single streaming request.
func (d *Downloader) Download(ctx context.Context, code, codec, url string) (*VideoFile, error) {
	size, rangeOK, err := d.probe(ctx, url)
	if err != nil {
		return nil, err
	}

	mp4Path := filepath.Join(d.outDir, fmt.Sprintf("%s-%s.mp4", code, codec))
	if !rangeOK || size <= 0 {
		return d.downloadStream(ctx, codec, url, mp4Path)
	}

	if err := d.downloadChunks(ctx, url, size, mp4Path); err != nil {
		return nil, err
	}
	fi, err := os.Stat(mp4Path)
	if err != nil {
		return nil, fmt.Errorf("stat mp4: %w", err)
	}
	return &VideoFile{Path: mp4Path, Size: fi.Size(), Codec: codec}, nil
}

// probe detects the file size and whether the server honors Range requests.
func (d *Downloader) probe(ctx context.Context, url string) (int64, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, false, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Range", "bytes=0-0")

	resp, err := d.client.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("http get: %w — hint: check your connection", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode == http.StatusPartialContent {
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if n, err := totalFromContentRange(cr); err == nil {
				return n, true, nil
			}
		}
	}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		if cr := resp.Header.Get("Content-Range"); strings.HasPrefix(cr, "bytes */") {
			if n, err := strconv.ParseInt(strings.TrimPrefix(cr, "bytes */"), 10, 64); err == nil {
				return n, false, nil
			}
		}
	}
	if resp.StatusCode >= 400 {
		return 0, false, fmt.Errorf("http status %d", resp.StatusCode)
	}

	size := resp.ContentLength
	return size, false, nil
}

func totalFromContentRange(cr string) (int64, error) {
	i := strings.LastIndex(cr, "/")
	if i < 0 {
		return 0, fmt.Errorf("malformed content-range %q", cr)
	}
	return strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64)
}

// downloadChunks splits the file into ranges and downloads them in parallel.
func (d *Downloader) downloadChunks(ctx context.Context, url string, size int64, mp4Path string) error {
	segDir, err := d.prepareChunkDir(url, size)
	if err != nil {
		return err
	}

	chunks := d.chunkPlan(size)
	total := int64(len(chunks))

	existing := 0
	for i := range chunks {
		if fi, err := os.Stat(filepath.Join(segDir, fmt.Sprintf("seg_%06d", i))); err == nil && fi.Size() == chunks[i].size {
			existing++
		}
	}
	if existing > 0 {
		d.emit(Event{
			Kind:    EventResume,
			Done:    int64(existing),
			Total:   total,
			Message: fmt.Sprintf("%d of %d chunks already on disk — resuming", existing, len(chunks)),
		})
	}

	var done, failed, totalBytes int64
	var mu sync.Mutex

	update := func() {
		d.emit(Event{
			Kind:   EventSegments,
			Done:   done + failed,
			Total:  total,
			Bytes:  totalBytes,
			Failed: failed,
		})
	}

	g, gctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, d.workers)

	for i, c := range chunks {
		i, c := i, c
		g.Go(func() error {
			select {
			case <-gctx.Done():
				return gctx.Err()
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()

			outPath := filepath.Join(segDir, fmt.Sprintf("seg_%06d", i))
			if fi, err := os.Stat(outPath); err == nil && fi.Size() == c.size {
				mu.Lock()
				done++
				totalBytes += c.size
				update()
				mu.Unlock()
				return nil
			}

			if err := d.downloadRange(gctx, url, c.start, c.end, outPath); err != nil {
				mu.Lock()
				failed++
				update()
				mu.Unlock()
				return fmt.Errorf("chunk %d (%d-%d): %w", i, c.start, c.end, err)
			}

			mu.Lock()
			done++
			totalBytes += c.size
			update()
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		// Keep .segments so a later run can resume after cancel/error.
		return err
	}

	if err := d.concatChunks(ctx, segDir, chunks, mp4Path); err != nil {
		return err
	}
	_ = os.RemoveAll(segDir)
	return nil
}

type chunk struct {
	start, end, size int64
}

func (d *Downloader) chunkPlan(size int64) []chunk {
	n := int64(d.workers)
	if size < int64(d.workers)*1024*1024 {
		n = (size + 1024*1024 - 1) / (1024 * 1024)
	}
	if n < 1 {
		n = 1
	}
	chunkSize := size / n
	if chunkSize < 1 {
		chunkSize = 1
	}

	chunks := make([]chunk, 0, n)
	for i := int64(0); i < n; i++ {
		start := i * chunkSize
		end := start + chunkSize - 1
		if i == n-1 {
			// Cover any remainder that integer division dropped.
			end = size - 1
		}
		if start > end {
			break
		}
		chunks = append(chunks, chunk{start: start, end: end, size: end - start + 1})
	}
	return chunks
}

// downloadRange fetches one byte range into outPath with retries.
func (d *Downloader) downloadRange(ctx context.Context, url string, start, end int64, outPath string) error {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := d.fetchRange(ctx, url, start, end, outPath); err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return ctx.Err()
			}
			d.emit(Event{Kind: EventRetry, Message: retryHint(err)})
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			continue
		}
		return nil
	}
	return fmt.Errorf("download range %d-%d: %w", start, end, lastErr)
}

// retryHint returns a short human message for a failed chunk attempt.
func retryHint(err error) string {
	if err == nil {
		return "transient failure — retrying"
	}
	if hint := httpHint(statusCode(err)); hint != "" {
		return hint
	}
	return "transient failure — retrying"
}

func statusCode(err error) int {
	// extract "http status N" from wrapped error text
	m := strings.LastIndex(err.Error(), "http status ")
	if m < 0 {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(err.Error()[m+len("http status "):]))
	return n
}

func (d *Downloader) fetchRange(ctx context.Context, url string, start, end int64, outPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}

	tmp := outPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create chunk: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write chunk: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close chunk: %w", err)
	}
	return os.Rename(tmp, outPath)
}

// concatChunks writes the ordered chunks into mp4Path.
func (d *Downloader) concatChunks(ctx context.Context, segDir string, chunks []chunk, mp4Path string) error {
	partial := filepath.Join(d.outDir, ".download.part.mp4")
	_ = os.Remove(partial)
	out, err := os.Create(partial)
	if err != nil {
		return fmt.Errorf("create partial: %w", err)
	}

	for i := range chunks {
		select {
		case <-ctx.Done():
			out.Close()
			os.Remove(partial)
			return ctx.Err()
		default:
		}
		f, err := os.Open(filepath.Join(segDir, fmt.Sprintf("seg_%06d", i)))
		if err != nil {
			out.Close()
			os.Remove(partial)
			return fmt.Errorf("open chunk %d: %w", i, err)
		}
		if _, err := io.Copy(out, f); err != nil {
			f.Close()
			out.Close()
			os.Remove(partial)
			return fmt.Errorf("write chunk %d: %w", i, err)
		}
		f.Close()
	}
	if err := out.Close(); err != nil {
		os.Remove(partial)
		return fmt.Errorf("close partial: %w", err)
	}
	if err := os.Rename(partial, mp4Path); err != nil {
		os.Remove(partial)
		return fmt.Errorf("finalize mp4: %w", err)
	}
	return nil
}

// prepareChunkDir reuses existing chunks only when they match the current
// URL+size fingerprint; otherwise it clears and rewrites ".source".
func (d *Downloader) prepareChunkDir(url string, size int64) (string, error) {
	segDir := filepath.Join(d.outDir, ".segments")
	metaPath := filepath.Join(segDir, ".source")
	want := sourceFingerprint(url, size)

	if data, err := os.ReadFile(metaPath); err == nil && strings.TrimSpace(string(data)) == want {
		return segDir, nil
	}
	_ = os.RemoveAll(segDir)
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		return "", fmt.Errorf("create segments dir: %w", err)
	}
	if err := os.WriteFile(metaPath, []byte(want+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write chunk source: %w", err)
	}
	return segDir, nil
}

func sourceFingerprint(url string, size int64) string {
	h := sha256.New()
	_, _ = h.Write([]byte(url))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.FormatInt(size, 10)))
	return hex.EncodeToString(h.Sum(nil))
}

// downloadStream is the fallback for servers that ignore Range requests.
func (d *Downloader) downloadStream(ctx context.Context, codec, url, mp4Path string) (*VideoFile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	partial := filepath.Join(d.outDir, ".download.part.mp4")
	_ = os.Remove(partial)
	f, err := os.Create(partial)
	if err != nil {
		return nil, fmt.Errorf("create partial: %w", err)
	}

	var bytes int64
	buf := make([]byte, 256*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(partial)
				return nil, fmt.Errorf("write: %w", werr)
			}
			bytes += int64(n)
			d.emit(Event{Kind: EventSegments, Done: 1, Total: 1, Bytes: bytes})
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			f.Close()
			os.Remove(partial)
			return nil, fmt.Errorf("read: %w", rerr)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(partial)
		return nil, fmt.Errorf("close partial: %w", err)
	}
	if err := os.Rename(partial, mp4Path); err != nil {
		os.Remove(partial)
		return nil, fmt.Errorf("finalize mp4: %w", err)
	}
	return &VideoFile{Path: mp4Path, Size: bytes, Codec: codec}, nil
}

func httpHint(status int) string {
	switch status {
	case http.StatusTooManyRequests:
		return "rate limited — consider a slower download rate"
	case http.StatusForbidden:
		return "blocked by the CDN — a referer or account may be required"
	default:
		return ""
	}
}
