// Package translate holds the subtitle Translator contract and the registry
// new translators plug into.
//
// Adding a translator: create a package under internal/media/translate/,
// call translate.Register("<name>", factory) in its init, blank-import it
// in cmd/jabledownloader, and make contracttest.Run pass for it. The
// pipeline and the --translator flag pick it up without further changes.
package translate

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/jooservices/go-jabledownloader/internal/domain"
)

// ErrUnsupportedPair means a translator cannot translate between the
// requested languages.
var ErrUnsupportedPair = errors.New("unsupported language pair")

// Translator translates timed subtitle cues. Implementations must keep cue
// count, indexes, and timings, honour ctx, and be safe for concurrent use.
type Translator interface {
	Name() string
	Translate(ctx context.Context, cues []domain.Cue, from, to string) ([]domain.Cue, error)
}

// Config carries translator-specific settings (API keys, endpoints) so each
// translator reads its own values without changing this package.
type Config struct {
	Values map[string]string
}

// Factory constructs a translator from its configuration.
type Factory func(Config) (Translator, error)

// Registry maps translator names to factories.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

// Default is the registry translators register into from init.
var Default = NewRegistry()

// Register adds a factory to Default.
func Register(name string, factory Factory) { Default.Register(name, factory) }

// Register adds a factory. Empty names, nil factories, and duplicate names
// are programmer errors and panic.
func (r *Registry) Register(name string, factory Factory) {
	name = strings.TrimSpace(name)
	if name == "" || factory == nil {
		panic("translate.Register requires a name and a factory")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[name]; exists {
		panic(fmt.Sprintf("translator %q already registered", name))
	}
	r.factories[name] = factory
}

// New constructs the named translator.
func (r *Registry) New(name string, cfg Config) (Translator, error) {
	r.mu.RLock()
	factory, ok := r.factories[strings.TrimSpace(name)]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown translator %q (available: %s)", name, strings.Join(r.Names(), ", "))
	}
	return factory(cfg)
}

// Names returns the registered names, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
