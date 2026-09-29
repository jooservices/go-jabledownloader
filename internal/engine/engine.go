// Package engine holds the site-agnostic download contract, the engine
// registry keyed by source kind, and infrastructure every engine shares:
// resumable work directories, atomic finalize, and retry classification.
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jooservices/go-jabledownloader/internal/domain"
)

// Request describes one download handed to an engine.
type Request struct {
	Source    domain.Source
	Code      string // names the default output file "<code>-<codec>.mp4"
	Dir       string // per-video directory holding resume state and output
	FileName  string // optional output file name overriding the default
	Workers   int    // concurrent transfers; values < 1 mean 1
	MaxHeight int    // HLS variant cap; 0 keeps the best
}

// Validate checks the fields every engine needs.
func (r Request) Validate() error {
	switch {
	case strings.TrimSpace(r.Dir) == "":
		return errors.New("download directory is required")
	case strings.TrimSpace(r.Source.URL) == "":
		return errors.New("source URL is required")
	case r.Code == "" && r.FileName == "":
		return errors.New("video code or file name is required")
	}
	return nil
}

// OutputName returns FileName when set, otherwise "<code>-<codec>.mp4".
func (r Request) OutputName(codec string) string {
	if r.FileName != "" {
		return r.FileName
	}
	if codec == "" {
		codec = "video"
	}
	return fmt.Sprintf("%s-%s.mp4", r.Code, codec)
}

// WorkerCount returns Workers clamped to at least one.
func (r Request) WorkerCount() int {
	return max(r.Workers, 1)
}

// Result describes the finalized download.
type Result struct {
	Path  string
	Size  int64
	Codec string
}

// Engine downloads one source into the requested directory.
type Engine interface {
	Download(ctx context.Context, req Request, sink domain.EventSink) (*Result, error)
}

var registry = struct {
	sync.RWMutex
	engines map[domain.SourceKind]Engine
}{engines: make(map[domain.SourceKind]Engine)}

// Register associates an engine with a source kind, replacing any previous
// one. A nil engine removes the registration.
func Register(kind domain.SourceKind, e Engine) {
	registry.Lock()
	defer registry.Unlock()
	if e == nil {
		delete(registry.engines, kind)
		return
	}
	registry.engines[kind] = e
}

// For returns the engine registered for kind.
func For(kind domain.SourceKind) (Engine, error) {
	registry.RLock()
	e := registry.engines[kind]
	registry.RUnlock()
	if e == nil {
		return nil, fmt.Errorf("no engine for kind %d", kind)
	}
	return e, nil
}

// Emit sends ev to sink when a sink is configured.
func Emit(sink domain.EventSink, ev domain.Event) {
	if sink != nil {
		sink(ev)
	}
}
