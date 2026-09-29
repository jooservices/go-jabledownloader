// Package progressive downloads progressive media (a single MP4 over HTTP)
// with parallel fixed-size Range chunks that resume across runs. Servers
// that ignore Range requests are streamed in one request instead.
package progressive

import (
	"context"
	"errors"
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

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/engine"
	"github.com/jooservices/go-jabledownloader/internal/platform/httpx"
)

const (
	defaultChunkSize = 8 << 20
	readBufferSize   = 256 << 10 // also the progress reporting granularity
	retryAttempts    = 3
	retryBackoff     = 500 * time.Millisecond
)

func init() { engine.Register(domain.SourceProgressive, New()) }

// Option configures a Downloader.
type Option func(*Downloader)

// WithChunkSize overrides the default 8 MiB chunk size.
func WithChunkSize(n int64) Option {
	return func(d *Downloader) {
		if n > 0 {
			d.chunkSize = n
		}
	}
}

// WithHTTPClient replaces the default media client (tests use it).
func WithHTTPClient(c *http.Client) Option {
	return func(d *Downloader) {
		if c != nil {
			d.client = c
		}
	}
}

// Downloader implements engine.Engine for progressive HTTP sources.
type Downloader struct {
	chunkSize int64
	client    *http.Client
	backoff   time.Duration
}

// New builds a progressive engine with a shared media HTTP client.
func New(opts ...Option) *Downloader {
	d := &Downloader{
		chunkSize: defaultChunkSize,
		client:    httpx.NewClient(httpx.Options{Workers: 16}),
		backoff:   retryBackoff,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

var _ engine.Engine = (*Downloader)(nil)

// Download implements engine.Engine.
func (d *Downloader) Download(ctx context.Context, req engine.Request, sink domain.EventSink) (*engine.Result, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if req.Source.Kind != domain.SourceProgressive {
		return nil, fmt.Errorf("progressive engine cannot download source kind %d", req.Source.Kind)
	}
	work, err := engine.WorkDir(req.Dir)
	if err != nil {
		return nil, err
	}
	defer work.Close()

	size, ranged, err := d.probe(ctx, req.Source)
	if err != nil {
		return nil, err
	}
	partial := engine.PartialPath(req.Dir)
	if ranged && size > 0 {
		err = d.downloadChunks(ctx, work, req, size, partial, sink)
	} else {
		err = d.downloadStream(ctx, req.Source, partial, sink)
	}
	if err != nil {
		_ = os.Remove(partial)
		return nil, err
	}

	final := filepath.Join(req.Dir, req.OutputName(req.Source.Codec))
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
	return &engine.Result{Path: final, Size: info.Size(), Codec: req.Source.Codec}, nil
}

// get sends a GET with the source headers; the body is idle-guarded.
func (d *Downloader) get(ctx context.Context, src domain.Source, byteRange string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	for key, values := range src.Headers {
		req.Header[key] = append([]string(nil), values...)
	}
	if byteRange != "" {
		req.Header.Set("Range", byteRange)
	}
	resp, err := httpx.Do(d.client, req, 0)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	return resp, nil
}

// probe detects the file size and whether the server honours Range.
func (d *Downloader) probe(ctx context.Context, src domain.Source) (int64, bool, error) {
	resp, err := d.get(ctx, src, "bytes=0-0")
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()
	// Bounded drain: a server ignoring Range must not stream the whole file.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusPartialContent:
		if total, err := totalFromContentRange(resp.Header.Get("Content-Range")); err == nil {
			return total, true, nil
		}
	case resp.StatusCode >= 400:
		return 0, false, &engine.HTTPError{Status: resp.StatusCode}
	}
	return resp.ContentLength, false, nil
}

func totalFromContentRange(value string) (int64, error) {
	_, total, ok := strings.Cut(value, "/")
	if !ok {
		return 0, fmt.Errorf("malformed content-range %q", value)
	}
	return strconv.ParseInt(strings.TrimSpace(total), 10, 64)
}

type chunk struct{ start, end int64 }

func (c chunk) size() int64 { return c.end - c.start + 1 }

func (d *Downloader) chunkPlan(size int64) []chunk {
	chunks := make([]chunk, 0, (size+d.chunkSize-1)/d.chunkSize)
	for start := int64(0); start < size; start += d.chunkSize {
		chunks = append(chunks, chunk{start: start, end: min(start+d.chunkSize, size) - 1})
	}
	return chunks
}

// progress tracks counters shared by the chunk workers. Bytes move in both
// directions: a failed attempt takes back what it had reported.
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

func (d *Downloader) downloadChunks(ctx context.Context, work *engine.Work, req engine.Request, size int64, partial string, sink domain.EventSink) error {
	fingerprint := engine.Fingerprint(req.Source.URL + "\x00" + strconv.FormatInt(size, 10) + "\x00" + strconv.FormatInt(d.chunkSize, 10))
	fresh, err := work.Open(fingerprint)
	if err != nil {
		return err
	}
	chunks := d.chunkPlan(size)
	total := int64(len(chunks))
	engine.Emit(sink, domain.Event{Kind: domain.EventPlan, Total: total})
	if !fresh {
		if existing := countExisting(work.SegmentsDir(), chunks); existing > 0 {
			engine.Emit(sink, domain.Event{Kind: domain.EventResume, Done: int64(existing), Total: total,
				Message: fmt.Sprintf("%d of %d chunks already on disk — resuming", existing, total)})
		}
	}

	prog := &progress{total: total, sink: sink}
	onRetry := func(hint string) { engine.Emit(sink, domain.Event{Kind: domain.EventRetry, Message: hint}) }
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(req.WorkerCount())
	for i, c := range chunks {
		out := chunkPath(work.SegmentsDir(), i)
		g.Go(func() error {
			if info, err := os.Stat(out); err == nil && info.Size() == c.size() {
				prog.add(1, 0, c.size())
				return nil
			}
			err := engine.Do(gctx, retryAttempts, d.backoff, onRetry, func() error {
				return d.fetchChunk(gctx, req.Source, c, out, prog)
			})
			if err != nil {
				prog.add(0, 1, 0)
				return fmt.Errorf("chunk %d of %d (bytes %d-%d): %w", i+1, total, c.start, c.end, err)
			}
			prog.add(1, 0, 0)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// Finished chunks stay in .segments so the next run resumes.
		return fmt.Errorf("%w — re-run to resume", err)
	}
	return concatChunks(ctx, work.SegmentsDir(), len(chunks), partial)
}

// fetchChunk writes one byte range to out, reporting bytes as they arrive.
// On failure it retracts the bytes it reported so a retry does not count
// them twice.
func (d *Downloader) fetchChunk(ctx context.Context, src domain.Source, c chunk, out string, prog *progress) (err error) {
	var reported int64
	defer func() {
		if err != nil && reported > 0 {
			prog.add(0, 0, -reported)
		}
	}()

	resp, err := d.get(ctx, src, fmt.Sprintf("bytes=%d-%d", c.start, c.end))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return &engine.HTTPError{Status: resp.StatusCode}
	}
	if want := fmt.Sprintf("bytes %d-%d/", c.start, c.end); !strings.HasPrefix(resp.Header.Get("Content-Range"), want) {
		return fmt.Errorf("unexpected content-range %q", resp.Header.Get("Content-Range"))
	}

	tmp := out + ".part"
	file, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create chunk: %w", err)
	}
	defer os.Remove(tmp) // no-op after the rename succeeds
	body := io.LimitReader(resp.Body, c.size()+1)
	buf := make([]byte, readBufferSize)
	var written int64
	for {
		n, readErr := io.ReadFull(body, buf)
		if n > 0 {
			if _, werr := file.Write(buf[:n]); werr != nil {
				_ = file.Close()
				return fmt.Errorf("write chunk: %w", werr)
			}
			written += int64(n)
			reported += int64(n)
			prog.add(0, 0, int64(n))
		}
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			break
		}
		if readErr != nil {
			_ = file.Close()
			return readErr
		}
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close chunk: %w", err)
	}
	if written != c.size() {
		return fmt.Errorf("chunk size mismatch: got %d of %d bytes: %w", written, c.size(), io.ErrUnexpectedEOF)
	}
	return os.Rename(tmp, out)
}

func chunkPath(dir string, i int) string {
	return filepath.Join(dir, fmt.Sprintf("seg_%06d", i))
}

func countExisting(dir string, chunks []chunk) int {
	existing := 0
	for i, c := range chunks {
		if info, err := os.Stat(chunkPath(dir, i)); err == nil && info.Size() == c.size() {
			existing++
		}
	}
	return existing
}

func concatChunks(ctx context.Context, dir string, n int, partial string) error {
	out, err := os.Create(partial)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	for i := range n {
		if err := ctx.Err(); err != nil {
			_ = out.Close()
			return err
		}
		if err := appendFile(out, chunkPath(dir, i)); err != nil {
			_ = out.Close()
			return fmt.Errorf("append chunk %d: %w", i, err)
		}
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}
	return nil
}

func appendFile(dst io.Writer, path string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	_, err = io.Copy(dst, src)
	return err
}

// downloadStream is the fallback for servers that ignore Range requests.
func (d *Downloader) downloadStream(ctx context.Context, src domain.Source, partial string, sink domain.EventSink) error {
	resp, err := d.get(ctx, src, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return &engine.HTTPError{Status: resp.StatusCode}
	}
	file, err := os.Create(partial)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	buf := make([]byte, readBufferSize)
	var written int64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := file.Write(buf[:n]); werr != nil {
				_ = file.Close()
				return fmt.Errorf("write output: %w", werr)
			}
			written += int64(n)
			engine.Emit(sink, domain.Event{Kind: domain.EventProgress, Done: 0, Total: 1, Bytes: written})
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = file.Close()
			return fmt.Errorf("read body: %w", readErr)
		}
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}
	engine.Emit(sink, domain.Event{Kind: domain.EventProgress, Done: 1, Total: 1, Bytes: written})
	return nil
}
