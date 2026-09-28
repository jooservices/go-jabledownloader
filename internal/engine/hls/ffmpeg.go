package hls

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/engine"
	"github.com/jooservices/go-jabledownloader/internal/platform/httpx"
)

// Remote inputs may only use network protocols: a hostile playlist must not
// make ffmpeg read local files. Concat inputs are local files only.
const (
	remoteProtocols = "https,http,tls,tcp,crypto"
	localProtocols  = "file,concat"
)

func requireFFmpeg() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return errors.New("ffmpeg is required to produce an mp4 — install it and ensure it is on PATH")
	}
	return nil
}

// concat muxes n downloaded segments from segDir into out.
func concat(ctx context.Context, segDir string, n int, out string) error {
	list, err := concatList(segDir, n)
	if err != nil {
		return err
	}
	listPath := filepath.Join(filepath.Dir(out), ".concat.txt")
	if err := os.WriteFile(listPath, []byte(list), 0o644); err != nil {
		return fmt.Errorf("write concat list: %w", err)
	}
	defer os.Remove(listPath)
	if err := requireFFmpeg(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", localProtocols,
		"-f", "concat", "-safe", "0", "-i", listPath,
		"-c", "copy", "-movflags", "faststart", "-y", out,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg concat: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// concatList renders an ffmpeg concat list; single quotes are escaped per
// the concat demuxer syntax.
func concatList(segDir string, n int) (string, error) {
	var b strings.Builder
	for i := range n {
		seg := segmentPath(segDir, i)
		if _, err := os.Stat(seg); err != nil {
			return "", fmt.Errorf("segment %d missing: %w", i, err)
		}
		fmt.Fprintf(&b, "file '%s'\n", strings.ReplaceAll(seg, "'", `'\''`))
	}
	return b.String(), nil
}

// remux lets ffmpeg download and mux the media playlist directly, reporting
// time-based progress parsed from `-progress pipe:1`.
func remux(ctx context.Context, mediaURL string, headers http.Header, out string, sink domain.EventSink) error {
	if err := requireFFmpeg(); err != nil {
		return err
	}
	args, err := remuxArgs(mediaURL, headers, out)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}
	reportProgress(stdout, sink)
	if err := cmd.Wait(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func remuxArgs(mediaURL string, headers http.Header, out string) ([]string, error) {
	headerBlock, err := ffmpegHeaders(headers)
	if err != nil {
		return nil, err
	}
	return []string{
		"-hide_banner", "-loglevel", "error", "-nostats",
		"-protocol_whitelist", remoteProtocols,
		"-headers", headerBlock,
		"-i", mediaURL,
		"-c", "copy", "-movflags", "faststart", "-y",
		"-progress", "pipe:1",
		out,
	}, nil
}

// ffmpegHeaders renders headers as ffmpeg's CRLF-separated -headers value,
// adding the default User-Agent and rejecting values that could inject
// extra header lines.
func ffmpegHeaders(headers http.Header) (string, error) {
	merged := headers.Clone()
	if merged == nil {
		merged = http.Header{}
	}
	if merged.Get("User-Agent") == "" {
		merged.Set("User-Agent", httpx.DefaultUserAgent)
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		for _, value := range merged[key] {
			if strings.ContainsAny(key+value, "\r\n") {
				return "", fmt.Errorf("invalid header %q: contains a line break", key)
			}
			fmt.Fprintf(&b, "%s: %s\r\n", key, value)
		}
	}
	return b.String(), nil
}

// reportProgress reads ffmpeg `-progress` key=value blocks until EOF and
// emits one event per block.
func reportProgress(r io.Reader, sink domain.EventSink) {
	var ev domain.Event
	ev.Kind = domain.EventTimeProgress
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		if !ok {
			continue
		}
		switch key {
		case "out_time_us":
			if us, err := strconv.ParseInt(value, 10, 64); err == nil {
				ev.Seconds = float64(us) / 1e6
			}
		case "speed":
			if speed, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(value), "x"), 64); err == nil {
				ev.Speed = speed
			}
		case "progress":
			engine.Emit(sink, ev)
		}
	}
}
