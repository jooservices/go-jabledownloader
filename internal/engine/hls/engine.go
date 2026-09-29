// Package hls downloads HLS streams: it resolves the master/media playlist,
// fetches segments with a bounded worker pool (resumable across runs), and
// muxes them into an mp4 with ffmpeg. Encrypted or fMP4 streams, and
// segment sets ffmpeg cannot concatenate, are remuxed by ffmpeg directly.
package hls

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/engine"
	"github.com/jooservices/go-jabledownloader/internal/platform/httpx"
)

const (
	maxPlaylistBytes = 8 << 20
	maxSegmentBytes  = 256 << 20
	retryAttempts    = 3
	retryBackoff     = time.Second
)

func init() { engine.Register(domain.SourceHLS, New()) }

// Option configures an Engine.
type Option func(*Engine)

// WithHTTPClient replaces the default media client (tests use it).
func WithHTTPClient(c *http.Client) Option {
	return func(e *Engine) {
		if c != nil {
			e.client = c
		}
	}
}

// Engine implements engine.Engine for HLS sources.
type Engine struct {
	client  *http.Client
	backoff time.Duration
}

// New builds an HLS engine with a shared media HTTP client.
func New(opts ...Option) *Engine {
	e := &Engine{client: httpx.NewClient(httpx.Options{Workers: 16}), backoff: retryBackoff}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

var _ engine.Engine = (*Engine)(nil)

// Download implements engine.Engine.
func (e *Engine) Download(ctx context.Context, req engine.Request, sink domain.EventSink) (*engine.Result, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if req.Source.Kind != domain.SourceHLS {
		return nil, fmt.Errorf("hls engine cannot download source kind %d", req.Source.Kind)
	}
	work, err := engine.WorkDir(req.Dir)
	if err != nil {
		return nil, err
	}
	defer work.Close()

	stream, err := e.resolve(ctx, req.Source, req.MaxHeight)
	if err != nil {
		return nil, fmt.Errorf("resolve playlist: %w", err)
	}
	partial := engine.PartialPath(req.Dir)
	if err := e.fetchInto(ctx, work, stream, req, partial, sink); err != nil {
		_ = os.Remove(partial)
		return nil, err
	}

	final := filepath.Join(req.Dir, req.OutputName(stream.codec))
	if err := engine.Finalize(partial, final); err != nil {
		return nil, err
	}
	if err := work.Discard(); err != nil {
		return nil, err
	}
	info, err := os.Stat(final)
	if err != nil {
		return nil, fmt.Errorf("stat output: %w", err)
	}
	engine.Emit(sink, domain.Event{Kind: domain.EventDone, Bytes: info.Size()})
	return &engine.Result{Path: final, Size: info.Size(), Codec: stream.codec}, nil
}

// fetchInto produces the muxed mp4 at partial, preferring resumable segment
// downloads and falling back to an ffmpeg remux when segments cannot be used.
func (e *Engine) fetchInto(ctx context.Context, work *engine.Work, stream *stream, req engine.Request, partial string, sink domain.EventSink) error {
	if stream.playlist.Encrypted || stream.playlist.NeedsInit {
		return remux(ctx, stream.mediaURL, req.Source.Headers, partial, sink)
	}
	if err := e.downloadSegments(ctx, work, stream.playlist, req, sink); err != nil {
		return err
	}
	err := concat(ctx, work.SegmentsDir(), len(stream.playlist.Segments), partial)
	if err == nil || ctx.Err() != nil {
		return err
	}
	engine.Emit(sink, domain.Event{Kind: domain.EventRetry, Message: fmt.Sprintf("segment concat failed (%v) — remuxing with ffmpeg", err)})
	return remux(ctx, stream.mediaURL, req.Source.Headers, partial, sink)
}

type stream struct {
	playlist *Playlist
	codec    string
	mediaURL string
}

func (e *Engine) resolve(ctx context.Context, src domain.Source, maxHeight int) (*stream, error) {
	body, err := e.fetchPlaylist(ctx, src.URL, src.Headers)
	if err != nil {
		return nil, fmt.Errorf("fetch playlist: %w", err)
	}
	if !strings.Contains(body, "#EXT-X-STREAM-INF:") {
		pl, err := ParsePlaylist(body, playlistBaseURL(src.URL))
		if err != nil {
			return nil, err
		}
		return &stream{playlist: pl, codec: "h264", mediaURL: src.URL}, nil
	}

	variant, err := ResolveMasterPlaylist(body, playlistBaseURL(src.URL), maxHeight)
	if err != nil {
		return nil, fmt.Errorf("resolve master playlist: %w", err)
	}
	media, err := e.fetchPlaylist(ctx, variant.URL, src.Headers)
	if err != nil {
		return nil, fmt.Errorf("fetch media playlist: %w", err)
	}
	pl, err := ParsePlaylist(media, playlistBaseURL(variant.URL))
	if err != nil {
		return nil, err
	}
	return &stream{playlist: pl, codec: variant.Codec, mediaURL: variant.URL}, nil
}

func (e *Engine) fetchPlaylist(ctx context.Context, rawURL string, headers http.Header) (string, error) {
	resp, err := e.get(ctx, rawURL, headers)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPlaylistBytes+1))
	if err != nil {
		return "", fmt.Errorf("read playlist: %w", err)
	}
	if len(body) > maxPlaylistBytes {
		return "", fmt.Errorf("playlist exceeds %d bytes", maxPlaylistBytes)
	}
	return string(body), nil
}

// get issues a GET with the source headers and returns a 200 response whose
// body is guarded by the idle timeout.
func (e *Engine) get(ctx context.Context, rawURL string, headers http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	for key, values := range headers {
		req.Header[key] = append([]string(nil), values...)
	}
	resp, err := httpx.Do(e.client, req, 0)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &engine.HTTPError{Status: resp.StatusCode}
	}
	return resp, nil
}

// progress tracks segment counters shared by the worker pool.
type progress struct {
	mu                  sync.Mutex
	done, failed, bytes int64
	total               int64
	sink                domain.EventSink
}

func (p *progress) add(done, failed, bytes int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done += done
	p.failed += failed
	p.bytes += bytes
	engine.Emit(p.sink, domain.Event{Kind: domain.EventProgress, Done: p.done, Total: p.total, Bytes: p.bytes, Failed: p.failed})
}

func (e *Engine) downloadSegments(ctx context.Context, work *engine.Work, pl *Playlist, req engine.Request, sink domain.EventSink) error {
	fresh, err := work.Open(playlistFingerprint(pl))
	if err != nil {
		return err
	}
	total := int64(len(pl.Segments))
	engine.Emit(sink, domain.Event{Kind: domain.EventPlan, Total: total})
	if !fresh {
		if existing := countExisting(work.SegmentsDir(), len(pl.Segments)); existing > 0 {
			engine.Emit(sink, domain.Event{Kind: domain.EventResume, Done: int64(existing), Total: total,
				Message: fmt.Sprintf("%d of %d segments already on disk — resuming", existing, total)})
		}
	}

	prog := &progress{total: total, sink: sink}
	onRetry := func(hint string) { engine.Emit(sink, domain.Event{Kind: domain.EventRetry, Message: hint}) }
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(req.WorkerCount())
	for i, seg := range pl.Segments {
		out := segmentPath(work.SegmentsDir(), i)
		g.Go(func() error {
			if info, err := os.Stat(out); err == nil && info.Size() > 0 {
				prog.add(1, 0, info.Size())
				return nil
			}
			var written int64
			err := engine.Do(gctx, retryAttempts, e.backoff, onRetry, func() error {
				n, err := e.fetchSegment(gctx, seg.URL, req.Source.Headers, out)
				written = n
				return err
			})
			if err != nil {
				prog.add(0, 1, 0)
				return fmt.Errorf("segment %d of %d: %w", i+1, total, err)
			}
			prog.add(1, 0, written)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// Downloaded segments stay in .segments so the next run resumes.
		return fmt.Errorf("%w — re-run to resume", err)
	}
	return nil
}

func (e *Engine) fetchSegment(ctx context.Context, rawURL string, headers http.Header, out string) (int64, error) {
	resp, err := e.get(ctx, rawURL, headers)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	tmp := out + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return 0, fmt.Errorf("create segment: %w", err)
	}
	n, copyErr := io.Copy(file, io.LimitReader(resp.Body, maxSegmentBytes+1))
	closeErr := file.Close()
	switch {
	case copyErr != nil:
		err = fmt.Errorf("write segment: %w", copyErr)
	case n > maxSegmentBytes:
		err = fmt.Errorf("segment exceeds %d bytes", maxSegmentBytes)
	case n == 0:
		err = errors.New("empty segment")
	case closeErr != nil:
		err = fmt.Errorf("close segment: %w", closeErr)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("finalize segment: %w", err)
	}
	return n, nil
}

func segmentPath(dir string, i int) string {
	return filepath.Join(dir, fmt.Sprintf("seg_%06d.ts", i))
}

func countExisting(dir string, n int) int {
	existing := 0
	for i := range n {
		if info, err := os.Stat(segmentPath(dir, i)); err == nil && info.Size() > 0 {
			existing++
		}
	}
	return existing
}

func playlistFingerprint(pl *Playlist) string {
	var b strings.Builder
	for _, seg := range pl.Segments {
		b.WriteString(seg.URL)
		b.WriteByte(0)
	}
	return engine.Fingerprint(b.String())
}

// playlistBaseURL returns the directory URL that relative playlist entries
// resolve against (query and fragment dropped).
func playlistBaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	dir := path.Dir(u.Path)
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	u.Path, u.RawQuery, u.Fragment = dir, "", ""
	return u.String()
}
