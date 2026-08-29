package hls

import (
	"net/http"
	"time"
)

// userAgent mirrors a desktop Chrome build.
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// referer is required by the CDN.
const referer = "https://en.jable.tv/"

// headerTransport decorates every request with the headers the CDN expects.
// It centralizes what was previously duplicated per request (DRY).
type headerTransport struct {
	base http.RoundTripper
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", userAgent)
	}
	if req.Header.Get("Referer") == "" {
		req.Header.Set("Referer", referer)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "*/*")
	}
	return t.base.RoundTrip(req)
}

// NewHTTPClient returns an http.Client preconfigured with the site headers.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   60 * time.Second,
		Transport: &headerTransport{base: http.DefaultTransport},
	}
}
