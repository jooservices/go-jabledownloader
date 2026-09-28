package app

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/platform/httpx"
	"github.com/jooservices/go-jabledownloader/internal/site"
)

// BrowserFactory starts the shared browser fetcher and returns its cleanup.
type BrowserFactory func(ctx context.Context) (site.Fetcher, func(), error)

// Sites builds registered sites with the fetcher each one declares. The
// browser is started lazily, once, on the first browser-backed site.
type Sites struct {
	newBrowser BrowserFactory
	pageClient *http.Client

	mu      sync.Mutex
	browser site.Fetcher
	cleanup func()
}

// NewSites returns a site builder; newBrowser may be nil when no
// browser-backed site is used.
func NewSites(newBrowser BrowserFactory) *Sites {
	return &Sites{
		newBrowser: newBrowser,
		pageClient: httpx.NewClient(httpx.Options{Timeout: 60 * time.Second}),
	}
}

// For detects the site for a URL or bare code and builds it.
func (s *Sites) For(input string) (site.Site, error) {
	d, err := site.Detect(input)
	if err != nil {
		return nil, err
	}
	return s.build(d)
}

// ByName builds a registered site by name.
func (s *Sites) ByName(name string) (site.Site, error) {
	d, err := site.Lookup(name)
	if err != nil {
		return nil, err
	}
	return s.build(d)
}

// Close releases the browser if one was started.
func (s *Sites) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleanup != nil {
		s.cleanup()
		s.cleanup, s.browser = nil, nil
	}
}

func (s *Sites) build(d site.Descriptor) (site.Site, error) {
	if d.Fetcher != site.FetcherBrowser {
		return d.New(site.NewHTTPFetcher(s.pageClient, d.Headers)), nil
	}
	browser, err := s.sharedBrowser()
	if err != nil {
		return nil, err
	}
	return d.New(browser), nil
}

func (s *Sites) sharedBrowser() (site.Fetcher, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.browser != nil {
		return s.browser, nil
	}
	if s.newBrowser == nil {
		return nil, errors.New("no browser configured")
	}
	browser, cleanup, err := s.newBrowser(context.Background())
	if err != nil {
		return nil, err
	}
	s.browser, s.cleanup = browser, cleanup
	return browser, nil
}
