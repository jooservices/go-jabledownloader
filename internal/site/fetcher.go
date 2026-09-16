package site

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// FetchMode controls how long a fetcher waits for page-specific signals.
type FetchMode int

const (
	// FetchReady waits only for a ready document body (listing pages).
	FetchReady FetchMode = iota
	// FetchHLS waits until the player injects its stream URL (video pages).
	FetchHLS
)

// Fetcher returns the rendered HTML of a page. Sites receive a Fetcher so
// tests can inject fixture content instead of launching a browser or network.
type Fetcher interface {
	FetchHTML(ctx context.Context, url string, mode FetchMode) (string, error)
}

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// headerTransport decorates every request with headers a site/CDN expects.
type headerTransport struct {
	base      http.RoundTripper
	referer   string
	userAgent string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.userAgent != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", t.userAgent)
	}
	if t.referer != "" && req.Header.Get("Referer") == "" {
		req.Header.Set("Referer", t.referer)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "*/*")
	}
	return t.base.RoundTrip(req)
}

// NewHeaderClient returns an http.Client that sends the given referer on
// every request (plus a desktop User-Agent). Site packages use it so their
// CDN/HTML requests are not blocked.
func NewHeaderClient(referer string) *http.Client {
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &headerTransport{
			base:      http.DefaultTransport,
			referer:   referer,
			userAgent: userAgent,
		},
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			// Some CDNs (EPORNER) reject HTTP/2 redirect targets whose query
			// still contains raw spaces. Encode them like curl does.
			if strings.Contains(req.URL.RawQuery, " ") {
				req.URL.RawQuery = strings.ReplaceAll(req.URL.RawQuery, " ", "%20")
			}
			return nil
		},
	}
}

// HTTPFetcher fetches raw HTML with a plain HTTP client. It never launches a
// browser, so it suits sites whose pages are server-rendered.
type HTTPFetcher struct {
	client *http.Client
}

// NewHTTPFetcher builds a Fetcher on top of an http.Client.
func NewHTTPFetcher(client *http.Client) *HTTPFetcher {
	return &HTTPFetcher{client: client}
}

// FetchHTML performs a GET and returns the response body.
func (h *HTTPFetcher) FetchHTML(ctx context.Context, url string, _ FetchMode) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("get %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("get %s: http %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", url, err)
	}
	return string(body), nil
}

// HostMatches reports whether host equals or is a subdomain of pattern.
func HostMatches(host, pattern string) bool {
	host = strings.ToLower(host)
	pattern = strings.ToLower(pattern)
	return host == pattern || strings.HasSuffix(host, "."+pattern)
}
