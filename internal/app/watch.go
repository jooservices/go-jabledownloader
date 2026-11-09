package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/site"
)

// Discover lists what a subscription currently yields, so the monitor can
// diff it against what it has seen. A subscription with no site queries every
// registered site; a partial failure still returns the items found.
func (s *Service) Discover(ctx context.Context, sub domain.Subscription) ([]domain.Item, error) {
	var sites []string
	if sub.Site != "" {
		sites = []string{sub.Site}
	}
	var (
		res DiscoveryResult
		err error
	)
	switch sub.Kind {
	case domain.SubView:
		view := strings.TrimSpace(sub.Query)
		res, err = s.discover(ctx, sites, 0, "view "+view, func(ctx context.Context, st site.Site) ([]domain.Item, error) {
			return st.List(ctx, site.ListOptions{View: view})
		})
	case domain.SubSearch:
		if strings.TrimSpace(sub.Query) == "" {
			return nil, errors.New("a search subscription needs a query")
		}
		res, err = s.Search(ctx, sites, sub.Query, 1, 0)
	default:
		return nil, fmt.Errorf("unknown subscription kind %q (use %q or %q)", sub.Kind, domain.SubView, domain.SubSearch)
	}
	var derr *DiscoveryError
	if err != nil && !errors.As(err, &derr) {
		return nil, err
	}
	return res.Items, err
}

// Download downloads one video or gallery by URL or code (the monitor's
// Downloader port).
func (s *Service) Download(ctx context.Context, input string) error {
	return s.RunGet(ctx, input)
}
