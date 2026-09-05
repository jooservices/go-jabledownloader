package hls

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
	// EventSegments reports segment download progress.
	EventSegments EventKind = iota
	// EventFFmpeg reports direct ffmpeg download progress.
	EventFFmpeg
	// EventRetry reports a transient failure that will be retried.
	EventRetry
	// EventResume reports that existing segments were found on disk.
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

// WithWorkers sets the number of concurrent segment workers.
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

// WithMaxHeight selects the best master-playlist variant at or below height
// (e.g. 720). Zero keeps the previous "highest bandwidth" behavior.
func WithMaxHeight(height int) Option {
	return func(d *Downloader) {
		if height > 0 {
			d.maxHeight = height
		}
	}
}

// Downloader downloads an HLS stream into an output directory.
type Downloader struct {
	outDir    string
	workers   int
	client    *http.Client
	progress  ProgressFunc
	maxHeight int
}

// NewDownloader builds a Downloader for outDir with sensible defaults.
func NewDownloader(outDir string, opts ...Option) *Downloader {
	d := &Downloader{
		outDir:  outDir,
		workers: 16,
		client:  NewHTTPClient(),
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

// Download resolves the playlist and downloads the video into a file named
// "<code>-<codec>.mp4" (e.g. "jur-827-h264.mp4"). It prefers the segment
// strategy and falls back to a direct ffmpeg download when the stream is
// encrypted or concatenation fails.
func (d *Downloader) Download(ctx context.Context, code, hlsURL string) (*VideoFile, error) {
	stream, err := d.resolvePlaylist(ctx, hlsURL)
	if err != nil {
		return nil, fmt.Errorf("resolve playlist: %w", err)
	}

	mp4Path := filepath.Join(d.outDir, fmt.Sprintf("%s-%s.mp4", code, stream.codec))

	if stream.pl.Encrypted {
		return d.downloadDirect(ctx, stream.mediaURL, stream.codec, mp4Path)
	}

	result, err := d.downloadSegments(ctx, stream.pl, stream.codec, mp4Path)
	if err != nil {
		return nil, err
	}
	if result != nil {
		return result, nil
	}

	vf, err := d.downloadDirect(ctx, stream.mediaURL, stream.codec, mp4Path)
	if err == nil {
		_ = os.RemoveAll(filepath.Join(d.outDir, ".segments"))
	}
	return vf, err
}

type resolvedStream struct {
	pl       *Playlist
	codec    string
	mediaURL string
}

func (d *Downloader) resolvePlaylist(ctx context.Context, hlsURL string) (*resolvedStream, error) {
	body, err := d.fetch(ctx, hlsURL)
	if err != nil {
		return nil, fmt.Errorf("fetch playlist: %w", err)
	}
	content := string(body)

	baseURL := hlsURL[:len(hlsURL)-len(filepath.Base(hlsURL))]

	if !strings.Contains(content, "#EXT-X-STREAM-INF:") {
		pl, err := ParsePlaylist(content, baseURL)
		if err != nil {
			return nil, err
		}
		return &resolvedStream{pl: pl, codec: "h264", mediaURL: hlsURL}, nil
	}

	variant, err := ResolveMasterPlaylist(content, baseURL, d.maxHeight)
	if err != nil {
		return nil, fmt.Errorf("resolve master playlist: %w", err)
	}

	body2, err := d.fetch(ctx, variant.URL)
	if err != nil {
		return nil, fmt.Errorf("fetch media playlist: %w", err)
	}

	baseURL2 := variant.URL[:len(variant.URL)-len(filepath.Base(variant.URL))]
	pl, err := ParsePlaylist(string(body2), baseURL2)
	if err != nil {
		return nil, err
	}
	return &resolvedStream{pl: pl, codec: variant.Codec, mediaURL: variant.URL}, nil
}

// fetch performs a GET with status hinting, sharing one code path (DRY).
func (d *Downloader) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w — hint: check your connection", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if h := httpHint(resp.StatusCode); h != "" {
			return nil, fmt.Errorf("http status %d — hint: %s", resp.StatusCode, h)
		}
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func (d *Downloader) downloadDirect(ctx context.Context, hlsURL, codec, mp4Path string) (*VideoFile, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg is required")
	}
	partial := filepath.Join(d.outDir, ".download.part.mp4")
	_ = os.Remove(partial)
	if err := d.downloadWithFFmpeg(ctx, hlsURL, partial); err != nil {
		_ = os.Remove(partial)
		return nil, err
	}
	if err := os.Rename(partial, mp4Path); err != nil {
		_ = os.Remove(partial)
		return nil, fmt.Errorf("finalize mp4: %w", err)
	}
	fi, err := os.Stat(mp4Path)
	if err != nil {
		return nil, fmt.Errorf("stat mp4: %w", err)
	}
	return &VideoFile{Path: mp4Path, Size: fi.Size(), Codec: codec}, nil
}

func (d *Downloader) downloadWithFFmpeg(ctx context.Context, hlsURL, mp4Path string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-headers", "User-Agent: "+userAgent+"\r\nReferer: "+referer+"\r\n",
		"-i", hlsURL,
		"-c", "copy",
		"-movflags", "faststart",
		"-y",
		"-progress", "pipe:1",
		"-nostats",
		"-loglevel", "error",
		mp4Path,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	reTime := regexp.MustCompile(`out_time_us=(\d+)`)
	reSpeed := regexp.MustCompile(`speed=\s*([\d.]+)x`)

	var lastTimeUs int64
	var lastSpeed float64
	var outBuf strings.Builder

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				outBuf.Write(buf[:n])
				data := outBuf.String()
				idx := strings.LastIndex(data, "\n")
				if idx >= 0 {
					for _, line := range strings.Split(data[:idx], "\n") {
						if m := reTime.FindStringSubmatch(line); len(m) >= 2 {
							lastTimeUs, _ = strconv.ParseInt(m[1], 10, 64)
						}
						if m := reSpeed.FindStringSubmatch(line); len(m) >= 2 {
							lastSpeed, _ = strconv.ParseFloat(m[1], 64)
						}
					}
					outBuf.Reset()
					if idx+1 < len(data) {
						outBuf.WriteString(data[idx+1:])
					}
				}
			}
			if err != nil {
				return
			}
			d.emit(Event{
				Kind:    EventFFmpeg,
				Seconds: float64(lastTimeUs) / 1e6,
				Speed:   lastSpeed,
			})
		}
	}()

	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("ffmpeg: %w", err)
		}
		return nil
	}
}

func (d *Downloader) downloadSegments(ctx context.Context, pl *Playlist, codec, mp4Path string) (*VideoFile, error) {
	segDir, err := d.prepareSegmentDir(pl)
	if err != nil {
		return nil, err
	}

	totalSegments := len(pl.Segments)
	jobs := make(chan segmentJob, d.workers*2)
	g, ctx := errgroup.WithContext(ctx)

	var doneSegments, failedSegments, totalBytes int64

	existing := 0
	for i := range pl.Segments {
		outPath := filepath.Join(segDir, fmt.Sprintf("seg_%06d.ts", i))
		if fi, err := os.Stat(outPath); err == nil && fi.Size() > 0 {
			existing++
		}
	}
	if existing > 0 {
		d.emit(Event{
			Kind:    EventResume,
			Done:    int64(existing),
			Total:   int64(totalSegments),
			Message: fmt.Sprintf("%d of %d segments already on disk — resuming", existing, totalSegments),
		})
	}

	for i := 0; i < d.workers; i++ {
		g.Go(func() error {
			for job := range jobs {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				if err := d.downloadSegment(ctx, job.url, job.outPath); err != nil {
					failedSegments++
				} else {
					doneSegments++
					if job.size > 0 {
						totalBytes += job.size
					}
				}
				d.emit(Event{
					Kind:   EventSegments,
					Done:   doneSegments + failedSegments,
					Total:  int64(totalSegments),
					Bytes:  totalBytes,
					Failed: failedSegments,
				})
			}
			return nil
		})
	}

	g.Go(func() error {
		defer close(jobs)
		for i, seg := range pl.Segments {
			outPath := filepath.Join(segDir, fmt.Sprintf("seg_%06d.ts", i))
			var size int64
			if fi, err := os.Stat(outPath); err == nil {
				size = fi.Size()
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case jobs <- segmentJob{index: i, url: seg.URL, outPath: outPath, size: size}:
			}
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		// Keep .segments so a later run can resume after cancel/error.
		return nil, err
	}

	partial := filepath.Join(d.outDir, ".download.part.mp4")
	_ = os.Remove(partial)
	result, err := ConcatSegments(ctx, segDir, totalSegments, codec, partial)
	if err != nil {
		_ = os.Remove(partial)
		// Fall back to direct ffmpeg; leave segments for a possible retry.
		return nil, nil
	}
	if err := os.Rename(partial, mp4Path); err != nil {
		_ = os.Remove(partial)
		return nil, fmt.Errorf("finalize mp4: %w", err)
	}
	result.Path = mp4Path
	_ = os.RemoveAll(segDir)
	return result, nil
}

func playlistFingerprint(pl *Playlist) string {
	h := sha256.New()
	for _, seg := range pl.Segments {
		_, _ = h.Write([]byte(seg.URL))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// prepareSegmentDir keeps existing segments only when they match the current
// playlist fingerprint; otherwise it clears and rewrites `.source`.
func (d *Downloader) prepareSegmentDir(pl *Playlist) (string, error) {
	segDir := filepath.Join(d.outDir, ".segments")
	metaPath := filepath.Join(segDir, ".source")
	want := playlistFingerprint(pl)
	if data, err := os.ReadFile(metaPath); err == nil && strings.TrimSpace(string(data)) == want {
		return segDir, nil
	}
	_ = os.RemoveAll(segDir)
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		return "", fmt.Errorf("create segments dir: %w", err)
	}
	if err := os.WriteFile(metaPath, []byte(want+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write segment source: %w", err)
	}
	return segDir, nil
}

type segmentJob struct {
	index   int
	url     string
	outPath string
	size    int64
}

func (d *Downloader) downloadSegment(ctx context.Context, url, outPath string) error {
	if fi, err := os.Stat(outPath); err == nil && fi.Size() > 0 {
		return nil
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			d.emit(Event{Kind: EventRetry, Message: httpHint(http.StatusTooManyRequests)})
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

		resp, err := d.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("http get: %w — hint: check your connection", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			h := httpHint(resp.StatusCode)
			resp.Body.Close()
			if h != "" {
				lastErr = fmt.Errorf("http status %d — hint: %s", resp.StatusCode, h)
			} else {
				lastErr = fmt.Errorf("http status %d", resp.StatusCode)
			}
			if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
				d.emit(Event{Kind: EventRetry, Message: h})
				continue
			}
			return lastErr
		}

		tmpPath := outPath + ".tmp"
		out, err := os.Create(tmpPath)
		if err != nil {
			resp.Body.Close()
			return fmt.Errorf("create file: %w", err)
		}

		_, copyErr := io.Copy(out, resp.Body)
		resp.Body.Close()
		closeErr := out.Close()

		if copyErr != nil {
			os.Remove(tmpPath)
			lastErr = fmt.Errorf("write file: %w", copyErr)
			continue
		}
		if closeErr != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("close file: %w", closeErr)
		}

		if err := os.Rename(tmpPath, outPath); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("rename temp file: %w", err)
		}

		return nil
	}

	return fmt.Errorf("retry exhausted: %w", lastErr)
}
