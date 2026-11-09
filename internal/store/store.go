// Package store persists subscriptions, the ledger of items already seen,
// and download jobs in one JSON file, written atomically. It is deliberately
// small: a single process owns the file, and the whole state is a few
// hundred kilobytes even after years of monitoring.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/domain"
)

// ErrNotFound is returned for an unknown subscription or job id.
var ErrNotFound = errors.New("not found")

const schemaVersion = 1

type state struct {
	Version       int                   `json:"version"`
	Subscriptions []domain.Subscription `json:"subscriptions"`
	Seen          map[string]time.Time  `json:"seen"` // "<site>/<code>" -> first seen
	Jobs          []domain.Job          `json:"jobs"`
	NextID        int                   `json:"next_id"`
}

// Store is safe for concurrent use.
type Store struct {
	path string
	now  func() time.Time

	mu sync.Mutex
	s  state
}

// Open loads the store at path, creating an empty one if the file is absent.
func Open(path string) (*Store, error) {
	st := &Store{path: path, now: time.Now, s: state{Version: schemaVersion, Seen: map[string]time.Time{}}}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return st, nil
	case err != nil:
		return nil, fmt.Errorf("read store: %w", err)
	}
	if err := json.Unmarshal(data, &st.s); err != nil {
		return nil, fmt.Errorf("parse store %s: %w", path, err)
	}
	if st.s.Seen == nil {
		st.s.Seen = map[string]time.Time{}
	}
	return st, nil
}

// save writes the state through a temp file and rename, so a crash leaves
// either the old or the new file, never a torn one. Callers hold mu.
func (st *Store) save() error {
	data, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(st.path), ".store-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), st.path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

func (st *Store) newID(prefix string) string {
	st.s.NextID++
	return fmt.Sprintf("%s%d", prefix, st.s.NextID)
}

// AddSubscription stores sub, assigning its id and creation time.
func (st *Store) AddSubscription(sub domain.Subscription) (domain.Subscription, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	sub.ID = st.newID("s")
	sub.CreatedAt = st.now()
	st.s.Subscriptions = append(st.s.Subscriptions, sub)
	return sub, st.save()
}

// Subscriptions returns a copy of every subscription.
func (st *Store) Subscriptions() []domain.Subscription {
	st.mu.Lock()
	defer st.mu.Unlock()
	return slices.Clone(st.s.Subscriptions)
}

// UpdateSubscription applies fn to the subscription with id.
func (st *Store) UpdateSubscription(id string, fn func(*domain.Subscription)) (domain.Subscription, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i := range st.s.Subscriptions {
		if st.s.Subscriptions[i].ID == id {
			fn(&st.s.Subscriptions[i])
			return st.s.Subscriptions[i], st.save()
		}
	}
	return domain.Subscription{}, fmt.Errorf("subscription %q: %w", id, ErrNotFound)
}

// RemoveSubscription deletes the subscription with id.
func (st *Store) RemoveSubscription(id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i := range st.s.Subscriptions {
		if st.s.Subscriptions[i].ID == id {
			st.s.Subscriptions = slices.Delete(st.s.Subscriptions, i, i+1)
			return st.save()
		}
	}
	return fmt.Errorf("subscription %q: %w", id, ErrNotFound)
}

func seenKey(site, code string) string { return site + "/" + code }

// IsSeen reports whether the item was already recorded.
func (st *Store) IsSeen(site, code string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	_, ok := st.s.Seen[seenKey(site, code)]
	return ok
}

// MarkSeen records items as seen and returns how many were new.
func (st *Store) MarkSeen(items ...domain.Item) (int, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	added := 0
	for _, it := range items {
		k := seenKey(it.Site, it.Code)
		if _, ok := st.s.Seen[k]; !ok {
			st.s.Seen[k] = st.now()
			added++
		}
	}
	if added == 0 {
		return 0, nil
	}
	return added, st.save()
}

// AddJob stores a queued job unless an unfinished or successful job already
// exists for the same input; it then returns that job and false.
func (st *Store) AddJob(job domain.Job) (domain.Job, bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, j := range st.s.Jobs {
		if j.Input == job.Input && j.State != domain.JobFailed {
			return j, false, nil
		}
	}
	job.ID = st.newID("j")
	job.State = domain.JobQueued
	job.CreatedAt = st.now()
	st.s.Jobs = append(st.s.Jobs, job)
	return job, true, st.save()
}

// Jobs returns a copy of all jobs, oldest first.
func (st *Store) Jobs() []domain.Job {
	st.mu.Lock()
	defer st.mu.Unlock()
	return slices.Clone(st.s.Jobs)
}

// Job returns one job.
func (st *Store) Job(id string) (domain.Job, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, j := range st.s.Jobs {
		if j.ID == id {
			return j, nil
		}
	}
	return domain.Job{}, fmt.Errorf("job %q: %w", id, ErrNotFound)
}

// UpdateJob applies fn to the job with id.
func (st *Store) UpdateJob(id string, fn func(*domain.Job)) (domain.Job, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i := range st.s.Jobs {
		if st.s.Jobs[i].ID == id {
			fn(&st.s.Jobs[i])
			return st.s.Jobs[i], st.save()
		}
	}
	return domain.Job{}, fmt.Errorf("job %q: %w", id, ErrNotFound)
}

// RecoverInterrupted requeues jobs left running by a crashed process, so a
// restart resumes them (the engines resume partial files themselves).
func (st *Store) RecoverInterrupted() (int, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	n := 0
	for i := range st.s.Jobs {
		if st.s.Jobs[i].State == domain.JobRunning {
			st.s.Jobs[i].State = domain.JobQueued
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, st.save()
}
