package app

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/jooservices/go-jabledownloader/internal/site"
)

// Sites builds site implementations with the fetcher each provider needs.
// Jable requires a browser (Cloudflare) and starts it lazily on first use;
// EPORNER and other server-rendered sites use a plain HTTP fetcher.
type Sites struct {
	browserFactory func(ctx context.Context) (site.Fetcher, func(), error)
	httpCli        *http.Client

	mu             sync.Mutex
	browserFetcher site.Fetcher
	cleanup        func()
}

// NewSites assembles the site registry. browserFactory is called once, lazily,
// when a browser-backed site (jable) is first requested.
func NewSites(browserFactory func(ctx context.Context) (site.Fetcher, func(), error)) *Sites {
	return &Sites{
		browserFactory: browserFactory,
		httpCli:        site.NewHeaderClient("https://www.eporner.com/"),
	}
}

// httpClient returns the client used for direct downloads (headers set).
func (s *Sites) httpClient() *http.Client {
	return s.httpCli
}

// For auto-detects the site from a CLI input and builds it.
func (s *Sites) For(input string) (site.Site, error) {
	name, err := site.DetectName(input)
	if err != nil {
		return nil, err
	}
	return s.build(name)
}

// Jable returns the Jable site, used by the listing commands.
func (s *Sites) Jable() (site.Site, error) {
	return s.build("jable")
}

// Close releases lazily-created browser resources.
func (s *Sites) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleanup != nil {
		s.cleanup()
		s.cleanup = nil
	}
}

func (s *Sites) build(name string) (site.Site, error) {
	switch name {
	case "jable":
		fetcher, err := s.browser()
		if err != nil {
			return nil, err
		}
		return site.New("jable", fetcher)
	default:
		return site.New(name, site.NewHTTPFetcher(s.httpCli))
	}
}

func (s *Sites) browser() (site.Fetcher, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.browserFetcher == nil {
		if s.browserFactory == nil {
			return nil, fmt.Errorf("no browser factory configured")
		}
		fetcher, cleanup, err := s.browserFactory(context.Background())
		if err != nil {
			return nil, err
		}
		s.browserFetcher, s.cleanup = fetcher, cleanup
	}
	return s.browserFetcher, nil
}
