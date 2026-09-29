package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/platform/httpx"
)

func TestDoStopsOnNonRetryableError(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 3, time.Millisecond, nil, func() error {
		calls++
		return &HTTPError{Status: 404}
	})
	if calls != 1 || err == nil {
		t.Fatalf("calls=%d err=%v, want one call and error", calls, err)
	}
}

func TestDoRetriesTransientErrorsAndReportsHints(t *testing.T) {
	calls := 0
	var hints []string
	err := Do(context.Background(), 3, time.Millisecond, func(h string) { hints = append(hints, h) }, func() error {
		calls++
		if calls < 3 {
			return &HTTPError{Status: 503}
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("calls=%d err=%v, want three calls and success", calls, err)
	}
	if len(hints) != 2 || !strings.Contains(hints[0], "server error") {
		t.Fatalf("hints = %q", hints)
	}
}

func TestDoReturnsLastErrorWhenAttemptsExhausted(t *testing.T) {
	err := Do(context.Background(), 2, time.Millisecond, nil, func() error { return &HTTPError{Status: 502} })
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != 502 {
		t.Fatalf("err = %v", err)
	}
}

func TestDoHonoursCancellationDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := Do(ctx, 3, time.Hour, func(string) { cancel() }, func() error {
		calls++
		return &HTTPError{Status: 503}
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d err=%v, want cancellation after first call", calls, err)
	}
}

func TestDoRejectsInvalidAttempts(t *testing.T) {
	if err := Do(context.Background(), 0, 0, nil, func() error { return nil }); err == nil {
		t.Fatal("expected attempts error")
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
		hint      string
	}{
		{"nil", nil, false, ""},
		{"rate limit", &HTTPError{Status: 429}, true, "rate limited"},
		{"server", fmt.Errorf("wrapped: %w", &HTTPError{Status: 500}), true, "server error"},
		{"unauthorized", &HTTPError{Status: 401}, false, "access denied"},
		{"cdn block", &HTTPError{Status: 403}, false, "blocked by the CDN"},
		{"not found", &HTTPError{Status: 404}, false, "not found"},
		{"teapot", &HTTPError{Status: 418}, false, ""},
		{"idle", fmt.Errorf("read: %w", httpx.ErrIdleTimeout), true, "stalled"},
		{"dropped", io.ErrUnexpectedEOF, true, "dropped"},
		{"network", &net.OpError{Op: "dial", Err: errors.New("offline")}, true, "network"},
		{"other", errors.New("disk full"), false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			retryable, hint := Classify(tc.err)
			if retryable != tc.retryable || !strings.Contains(hint, tc.hint) || (tc.hint == "" && hint != "") {
				t.Fatalf("Classify() = %v, %q", retryable, hint)
			}
		})
	}
}

func TestHTTPErrorMessageIncludesHint(t *testing.T) {
	if got := (&HTTPError{Status: 404}).Error(); got != "http status 404 — hint: not found — the video may have been removed" {
		t.Fatal(got)
	}
	if got := (&HTTPError{Status: 418}).Error(); got != "http status 418" {
		t.Fatal(got)
	}
}
