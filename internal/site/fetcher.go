package site

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// FetchMode controls how long a fetcher waits for page-specific signals.
type FetchMode int

const (
	// FetchReady waits only for a ready document body (listing pages).
	FetchReady FetchMode = iota
	// FetchHLS also waits until the player injects its stream URL.
	FetchHLS
)

// Fetcher returns the HTML of a page. Sites receive one so tests can inject
// fixture content instead of a browser or network.
type Fetcher interface {
	FetchHTML(ctx context.Context, url string, mode FetchMode) (string, error)
}

// maxHTMLSize caps how much of a third-party page is read into memory.
const maxHTMLSize = 16 << 20

// HTTPFetcher fetches server-rendered HTML with a plain HTTP client.
type HTTPFetcher struct {
	client  *http.Client
	headers http.Header
}

// NewHTTPFetcher builds a Fetcher that sends headers with every request.
func NewHTTPFetcher(client *http.Client, headers http.Header) *HTTPFetcher {
	return &HTTPFetcher{client: client, headers: headers}
}

// FetchHTML performs a GET and returns the response body.
func (h *HTTPFetcher) FetchHTML(ctx context.Context, url string, _ FetchMode) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	for key, values := range h.headers {
		req.Header[key] = append([]string(nil), values...)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("get %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("get %s: http %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTMLSize+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", url, err)
	}
	if len(body) > maxHTMLSize {
		return "", fmt.Errorf("read %s: response exceeds %d bytes", url, maxHTMLSize)
	}
	return string(body), nil
}
