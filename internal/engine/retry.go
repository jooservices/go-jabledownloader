package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/platform/httpx"
)

// Do invokes fn up to attempts times, retrying only errors Classify marks as
// transient. The backoff grows linearly with the attempt and honours ctx.
// onRetry, when set, receives the hint for each retry.
func Do(ctx context.Context, attempts int, backoff time.Duration, onRetry func(hint string), fn func() error) error {
	if attempts < 1 {
		return errors.New("retry attempts must be positive")
	}
	var last error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		last = fn()
		if last == nil {
			return nil
		}
		retryable, hint := Classify(last)
		if !retryable || attempt == attempts || ctx.Err() != nil {
			break
		}
		if onRetry != nil {
			onRetry(hint)
		}
		timer := time.NewTimer(time.Duration(attempt) * backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return last
}

// Classify reports whether err is transient and gives a short user hint.
func Classify(err error) (retryable bool, hint string) {
	var httpErr *HTTPError
	var netErr net.Error
	switch {
	case err == nil:
		return false, ""
	case errors.As(err, &httpErr):
		return httpErr.Status == http.StatusTooManyRequests || httpErr.Status >= 500, statusHint(httpErr.Status)
	case errors.Is(err, httpx.ErrIdleTimeout):
		return true, "connection stalled — retrying"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return true, "connection dropped — retrying"
	case errors.As(err, &netErr):
		return true, "network error — check your connection"
	}
	return false, ""
}

func statusHint(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "access denied — the site token may have expired, retry the download"
	case status == http.StatusForbidden:
		return "blocked by the CDN or Cloudflare — retry later or try a different network"
	case status == http.StatusNotFound:
		return "not found — the video may have been removed"
	case status == http.StatusTooManyRequests:
		return "rate limited — backing off"
	case status >= 500:
		return "server error — retrying usually helps"
	}
	return ""
}

// HTTPError is a non-success HTTP response status.
type HTTPError struct {
	Status int
}

func (e *HTTPError) Error() string {
	if hint := statusHint(e.Status); hint != "" {
		return fmt.Sprintf("http status %d — hint: %s", e.Status, hint)
	}
	return fmt.Sprintf("http status %d", e.Status)
}
