package httpx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewClientHasNoTotalTimeoutByDefault(t *testing.T) {
	if got := NewClient(Options{}).Timeout; got != 0 {
		t.Fatalf("Timeout = %s, want 0", got)
	}
	if got := NewClient(Options{Timeout: time.Second}).Timeout; got != time.Second {
		t.Fatalf("Timeout = %s, want 1s", got)
	}
}

func TestNewClientSetsDefaultUserAgentOnlyWhenMissing(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.UserAgent())
	}))
	defer srv.Close()
	client := NewClient(Options{})

	get(t, client, srv.URL, "")
	get(t, client, srv.URL, "custom-agent/1.0")

	if seen[0] != DefaultUserAgent || seen[1] != "custom-agent/1.0" {
		t.Fatalf("user agents = %q", seen)
	}
}

func TestNewClientEncodesSpacesInRedirectQuery(t *testing.T) {
	var rawQuery string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dload" {
			w.Header().Set("Location", srv.URL+"/cdn?f=hello world.mp4")
			w.WriteHeader(http.StatusFound)
			return
		}
		rawQuery = r.URL.RawQuery
	}))
	defer srv.Close()

	resp := get(t, NewClient(Options{}), srv.URL+"/dload", "")

	if resp.StatusCode != http.StatusOK || rawQuery != "f=hello%20world.mp4" {
		t.Fatalf("status=%d rawQuery=%q", resp.StatusCode, rawQuery)
	}
}

func TestNewClientStopsRedirectLoops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer srv.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if _, err := NewClient(Options{}).Do(req); err == nil {
		t.Fatal("expected redirect loop error")
	}
}

func TestDoReturnsBodyData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("payload"))
	}))
	defer srv.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)

	resp, err := Do(NewClient(Options{}), req, 0)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil || string(data) != "payload" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestDoAbortsStalledBody(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("first"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)

	resp, err := Do(NewClient(Options{}), req, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)

	if !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("err = %v, want ErrIdleTimeout", err)
	}
}

func TestDoReturnsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if _, err := Do(NewClient(Options{}), req, time.Second); err == nil {
		t.Fatal("expected error for closed server")
	}
}

func get(t *testing.T, client *http.Client, url, userAgent string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp
}
