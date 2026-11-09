// Package server exposes the downloader to programs: a JSON HTTP API and a
// Model Context Protocol (MCP) server so an AI assistant can search, request
// downloads, and manage monitored sources. Both transports call the same
// Core, so they can never disagree about behaviour.
package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/store"
)

// Backend answers read-only questions about the sites.
type Backend interface {
	Latest(ctx context.Context, sites []string, count int) ([]domain.Item, error)
	Search(ctx context.Context, sites []string, keyword string, count int) ([]domain.Item, error)
	Inspect(ctx context.Context, input string) (*domain.Detail, error)
}

// Queue accepts download requests (the monitor implements it).
type Queue interface {
	Enqueue(input, origin string) (domain.Job, bool, error)
}

// Poller checks one subscription immediately.
type Poller interface {
	Poll(ctx context.Context, id string) error
}

// ErrInvalid marks a request the caller got wrong (HTTP 400, MCP isError).
var ErrInvalid = errors.New("invalid request")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Defaults for list sizes.
const (
	defaultCount = 10
	maxCount     = 100
)

// Core is the transport-independent API.
type Core struct {
	Backend Backend
	Queue   Queue
	Poller  Poller
	Store   *store.Store
	Version string
}

func clampCount(n int) int {
	switch {
	case n <= 0:
		return defaultCount
	case n > maxCount:
		return maxCount
	default:
		return n
	}
}

// Latest lists the newest videos.
func (c *Core) Latest(ctx context.Context, sites []string, count int) ([]domain.Item, error) {
	return c.Backend.Latest(ctx, sites, clampCount(count))
}

// Search finds keyword on the given sites (all when empty).
func (c *Core) Search(ctx context.Context, sites []string, keyword string, count int) ([]domain.Item, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, invalid("keyword is required")
	}
	return c.Backend.Search(ctx, sites, keyword, clampCount(count))
}

// Inspect resolves a video without downloading it.
func (c *Core) Inspect(ctx context.Context, input string) (*domain.Detail, error) {
	if strings.TrimSpace(input) == "" {
		return nil, invalid("input is required")
	}
	return c.Backend.Inspect(ctx, strings.TrimSpace(input))
}

// Download queues a download of a URL or code.
func (c *Core) Download(input, origin string) (job domain.Job, created bool, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return domain.Job{}, false, invalid("input is required")
	}
	return c.Queue.Enqueue(input, origin)
}

// Jobs lists download jobs, newest first.
func (c *Core) Jobs() []domain.Job {
	jobs := c.Store.Jobs()
	for i, j := 0, len(jobs)-1; i < j; i, j = i+1, j-1 {
		jobs[i], jobs[j] = jobs[j], jobs[i]
	}
	return jobs
}

// Job returns one job.
func (c *Core) Job(id string) (domain.Job, error) { return c.Store.Job(id) }

// SubscribeRequest describes a source to monitor.
type SubscribeRequest struct {
	Site         string `json:"site"`
	Kind         string `json:"kind"`
	Query        string `json:"query"`
	AutoDownload *bool  `json:"auto_download"` // default true
	Backfill     bool   `json:"backfill"`
	IntervalMins int    `json:"interval_minutes"`
	MaxPerPoll   int    `json:"max_per_poll"`
}

// Subscribe registers a source to monitor.
func (c *Core) Subscribe(req SubscribeRequest) (domain.Subscription, error) {
	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		kind = domain.SubSearch
	}
	if kind != domain.SubSearch && kind != domain.SubView {
		return domain.Subscription{}, invalid("kind must be %q or %q", domain.SubSearch, domain.SubView)
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return domain.Subscription{}, invalid("query is required")
	}
	if req.IntervalMins < 0 || req.MaxPerPoll < 0 {
		return domain.Subscription{}, invalid("interval_minutes and max_per_poll cannot be negative")
	}
	auto := req.AutoDownload == nil || *req.AutoDownload
	return c.Store.AddSubscription(domain.Subscription{
		Site: strings.TrimSpace(req.Site), Kind: kind, Query: query,
		Enabled: true, AutoDownload: auto, Backfill: req.Backfill,
		Interval:   time.Duration(req.IntervalMins) * time.Minute,
		MaxPerPoll: req.MaxPerPoll,
	})
}

// Subscriptions lists the monitored sources.
func (c *Core) Subscriptions() []domain.Subscription { return c.Store.Subscriptions() }

// Unsubscribe removes a monitored source.
func (c *Core) Unsubscribe(id string) error { return c.Store.RemoveSubscription(id) }

// PollNow checks a subscription immediately.
func (c *Core) PollNow(ctx context.Context, id string) error {
	if c.Poller == nil {
		return errors.New("monitoring is not running")
	}
	return c.Poller.Poll(ctx, id)
}
