// Package httpx builds the HTTP clients shared by page fetchers and download
// engines: one desktop User-Agent, redirect targets safe for strict CDNs,
// bounded connection setup, and (for media) no total request timeout.
package httpx

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

// DefaultUserAgent is the desktop Chrome User-Agent sent when a request does
// not carry its own. It is the only User-Agent literal in the module.
const DefaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// Options configures a client.
type Options struct {
	// Timeout bounds a whole request including the body. Zero disables it,
	// which media downloads need; they use Do's idle timeout instead.
	Timeout time.Duration
	// Workers sizes the per-host idle connection pool.
	Workers int
	// ResponseHeaderTimeout bounds the wait for response headers.
	ResponseHeaderTimeout time.Duration
}

// NewClient returns a client with the module defaults applied.
func NewClient(opts Options) *http.Client {
	if opts.ResponseHeaderTimeout <= 0 {
		opts.ResponseHeaderTimeout = 30 * time.Second
	}
	workers := max(opts.Workers, 1)
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          workers,
		MaxIdleConnsPerHost:   workers,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
	}
	return &http.Client{
		Timeout:       opts.Timeout,
		Transport:     &userAgentTransport{base: transport},
		CheckRedirect: encodeRedirectSpaces,
	}
}

// userAgentTransport fills in DefaultUserAgent when the caller set none.
type userAgentTransport struct {
	base http.RoundTripper
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") != "" {
		return t.base.RoundTrip(req)
	}
	clone := req.Clone(req.Context())
	clone.Header.Set("User-Agent", DefaultUserAgent)
	return t.base.RoundTrip(clone)
}

// maxRedirects mirrors net/http's default redirect limit.
const maxRedirects = 10

// encodeRedirectSpaces percent-encodes raw spaces in redirect queries. Some
// CDNs (EPORNER) redirect to such URLs and then reject the raw request line.
func encodeRedirectSpaces(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("stopped after 10 redirects")
	}
	if strings.Contains(req.URL.RawQuery, " ") {
		req.URL.RawQuery = strings.ReplaceAll(req.URL.RawQuery, " ", "%20")
	}
	return nil
}
