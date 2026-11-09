// Package monitor is the 24/7 engine: it polls subscriptions for new items,
// queues downloads for them, runs the queue with retries, and tells people
// what happened. It owns no I/O of its own; discovery, downloading, storage,
// and notification are injected, so every rule here is testable offline.
package monitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/notify"
	"github.com/jooservices/go-jabledownloader/internal/store"
)

// Discoverer lists the items a subscription currently yields.
type Discoverer interface {
	Discover(ctx context.Context, sub domain.Subscription) ([]domain.Item, error)
}

// Downloader downloads one video or gallery by URL or code.
type Downloader interface {
	Download(ctx context.Context, input string) error
}

// Defaults.
const (
	DefaultInterval    = 30 * time.Minute
	DefaultTick        = 30 * time.Second
	DefaultMaxAttempts = 3
)

// Monitor wires the collaborators. Zero values of the tuning fields mean the
// defaults above.
type Monitor struct {
	Store      *store.Store
	Discoverer Discoverer
	Downloader Downloader
	Notifier   notify.Notifier
	Log        *slog.Logger

	Interval    time.Duration // default poll interval of a subscription
	Tick        time.Duration // how often Run wakes up
	MaxAttempts int           // download attempts before a job stays failed
	Concurrency int           // downloads running at once (default 1)

	Now  func() time.Time
	kick chan struct{}
	once sync.Once
}

func (m *Monitor) init() {
	m.once.Do(func() {
		m.kick = make(chan struct{}, 1)
		if m.Now == nil {
			m.Now = time.Now
		}
		if m.Log == nil {
			m.Log = slog.New(slog.DiscardHandler)
		}
		if m.Notifier == nil {
			m.Notifier = notify.Nop{}
		}
		if m.Interval <= 0 {
			m.Interval = DefaultInterval
		}
		if m.Tick <= 0 {
			m.Tick = DefaultTick
		}
		if m.MaxAttempts <= 0 {
			m.MaxAttempts = DefaultMaxAttempts
		}
		if m.Concurrency <= 0 {
			m.Concurrency = 1
		}
	})
}

// Enqueue queues a download requested by a person or an AI (origin "api" or
// "mcp"). A request that is already queued or done returns the existing job.
func (m *Monitor) Enqueue(input, origin string) (domain.Job, bool, error) {
	m.init()
	job, added, err := m.Store.AddJob(domain.Job{Input: input, Origin: origin})
	if err == nil && added {
		m.wake()
	}
	return job, added, err
}

func (m *Monitor) wake() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Run polls and downloads until ctx ends. Jobs left running by a crash are
// requeued first.
func (m *Monitor) Run(ctx context.Context) error {
	m.init()
	if n, err := m.Store.RecoverInterrupted(); err != nil {
		return err
	} else if n > 0 {
		m.Log.Info("requeued interrupted jobs", "count", n)
	}
	for {
		m.PollOnce(ctx)
		m.DrainQueue(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.kick:
		case <-time.After(m.Tick):
		}
	}
}

// PollOnce checks every enabled subscription that is due.
func (m *Monitor) PollOnce(ctx context.Context) {
	m.init()
	now := m.Now()
	for _, sub := range m.Store.Subscriptions() {
		if ctx.Err() != nil {
			return
		}
		if !sub.Enabled || !m.due(sub, now) {
			continue
		}
		m.poll(ctx, sub)
	}
}

func (m *Monitor) due(sub domain.Subscription, now time.Time) bool {
	interval := sub.Interval
	if interval <= 0 {
		interval = m.Interval
	}
	return sub.LastPolled.IsZero() || !now.Before(sub.LastPolled.Add(interval))
}

// Poll checks one subscription right now, whatever its schedule.
func (m *Monitor) Poll(ctx context.Context, id string) error {
	m.init()
	for _, sub := range m.Store.Subscriptions() {
		if sub.ID == id {
			return m.poll(ctx, sub)
		}
	}
	return fmt.Errorf("subscription %q: %w", id, store.ErrNotFound)
}

func (m *Monitor) poll(ctx context.Context, sub domain.Subscription) error {
	items, err := m.Discoverer.Discover(ctx, sub)
	stamp := func(s *domain.Subscription) {
		s.LastPolled = m.Now()
		s.LastError = ""
		if err != nil {
			s.LastError = err.Error()
		}
	}
	if _, uerr := m.Store.UpdateSubscription(sub.ID, stamp); uerr != nil {
		m.Log.Warn("update subscription", "id", sub.ID, "err", uerr)
	}
	// A partial failure still yields items from the sites that answered.
	if err != nil && len(items) == 0 {
		m.Log.Warn("poll failed", "id", sub.ID, "err", err)
		return err
	}
	if sub.MaxPerPoll > 0 && len(items) > sub.MaxPerPoll {
		items = items[:sub.MaxPerPoll]
	}

	var fresh []domain.Item
	for _, it := range items {
		if !m.Store.IsSeen(it.Site, it.Code) {
			fresh = append(fresh, it)
		}
	}
	if _, merr := m.Store.MarkSeen(fresh...); merr != nil {
		return merr
	}
	// The first poll only records what already exists, unless the user asked
	// to backfill it; otherwise subscribing would download a whole backlog.
	if sub.LastPolled.IsZero() && !sub.Backfill {
		m.Log.Info("baseline recorded", "id", sub.ID, "items", len(fresh))
		return err
	}
	for _, it := range fresh {
		m.announce(ctx, sub, it)
	}
	return err
}

func (m *Monitor) announce(ctx context.Context, sub domain.Subscription, it domain.Item) {
	title := it.Title
	if title == "" {
		title = it.Code
	}
	if sub.AutoDownload {
		_, added, err := m.Store.AddJob(domain.Job{
			Input: it.URL, Site: it.Site, Code: it.Code, Title: it.Title, Origin: "sub:" + sub.ID,
		})
		if err != nil {
			m.Log.Warn("queue job", "code", it.Code, "err", err)
		} else if added {
			m.wake()
		}
	}
	m.notify(ctx, notify.Message{Title: "New on " + it.Site + ": " + title, URL: it.URL})
}

func (m *Monitor) notify(ctx context.Context, msg notify.Message) {
	if err := m.Notifier.Notify(ctx, msg); err != nil {
		m.Log.Warn("notify failed", "err", err)
	}
}

// DrainQueue runs every queued job (Concurrency at a time) and returns when
// none is left or ctx ends. Failed jobs below MaxAttempts go back to the
// queue for the next Run cycle.
func (m *Monitor) DrainQueue(ctx context.Context) {
	m.init()
	for ctx.Err() == nil {
		batch := m.claim(m.Concurrency)
		if len(batch) == 0 {
			return
		}
		var wg sync.WaitGroup
		for _, job := range batch {
			wg.Add(1)
			go func() {
				defer wg.Done()
				m.run(ctx, job)
			}()
		}
		wg.Wait()
		if m.onlyRetries(batch) {
			return // wait for the next cycle instead of hammering a failing site
		}
	}
}

// claim marks up to n queued jobs running and returns them.
func (m *Monitor) claim(n int) []domain.Job {
	var out []domain.Job
	for _, j := range m.Store.Jobs() {
		if j.State != domain.JobQueued {
			continue
		}
		claimed, err := m.Store.UpdateJob(j.ID, func(j *domain.Job) { j.State = domain.JobRunning; j.Attempts++ })
		if err != nil {
			continue
		}
		out = append(out, claimed)
		if len(out) == n {
			break
		}
	}
	return out
}

// onlyRetries reports whether every job in batch ended up queued again.
func (m *Monitor) onlyRetries(batch []domain.Job) bool {
	for _, j := range batch {
		if cur, err := m.Store.Job(j.ID); err != nil || cur.State != domain.JobQueued {
			return false
		}
	}
	return true
}

func (m *Monitor) run(ctx context.Context, job domain.Job) {
	m.Log.Info("download started", "job", job.ID, "input", job.Input)
	err := m.Downloader.Download(ctx, job.Input)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		// Shutdown, not a failure: leave it queued so a restart resumes it.
		_, _ = m.Store.UpdateJob(job.ID, func(j *domain.Job) { j.State = domain.JobQueued; j.Attempts-- })
		return
	}
	if err == nil {
		_, _ = m.Store.UpdateJob(job.ID, func(j *domain.Job) { j.State = domain.JobDone; j.Error = ""; j.FinishedAt = m.Now() })
		m.notify(ctx, notify.Message{Title: "Downloaded: " + labelOf(job), URL: job.Input})
		return
	}
	retry := job.Attempts < m.MaxAttempts
	_, _ = m.Store.UpdateJob(job.ID, func(j *domain.Job) {
		j.Error = err.Error()
		if retry {
			j.State = domain.JobQueued
			return
		}
		j.State = domain.JobFailed
		j.FinishedAt = m.Now()
	})
	m.Log.Warn("download failed", "job", job.ID, "attempt", job.Attempts, "retry", retry, "err", err)
	if !retry {
		m.notify(ctx, notify.Message{Title: "Download failed: " + labelOf(job), Body: err.Error(), URL: job.Input})
	}
}

func labelOf(j domain.Job) string {
	switch {
	case j.Title != "":
		return j.Title
	case j.Code != "":
		return j.Code
	default:
		return j.Input
	}
}
