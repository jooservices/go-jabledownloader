package contracttest

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/media/translate"
)

// The suite must accept the reference "none" translator.
func TestRunAcceptsNoneTranslator(t *testing.T) {
	Run(t, func(cfg translate.Config) (translate.Translator, error) {
		return translate.Default.New("none", cfg)
	})
}

// recordingTB captures failures instead of failing the outer test.
type recordingTB struct {
	testing.TB
	mu       sync.Mutex
	failures []string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	runtime.Goexit()
}

func runRecorded(factory translate.Factory) []string {
	rec := &recordingTB{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(rec, factory)
	}()
	<-done
	return rec.failures
}

// brokenTranslator violates one rule of the contract.
type brokenTranslator struct {
	dropCues, ignoreCtx, rejectAll, acceptAll bool
}

func (brokenTranslator) Name() string { return "broken" }

func (b brokenTranslator) Translate(ctx context.Context, cues []domain.Cue, from, to string) ([]domain.Cue, error) {
	if !b.ignoreCtx && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	switch {
	case b.rejectAll:
		return nil, translate.ErrUnsupportedPair
	case b.dropCues:
		return nil, nil
	case !b.acceptAll && from != to:
		return nil, translate.ErrUnsupportedPair
	}
	return append([]domain.Cue(nil), cues...), nil
}

// The suite must catch every broken implementation.
func TestRunDetectsContractViolations(t *testing.T) {
	for name, factory := range map[string]translate.Factory{
		"constructor error": func(translate.Config) (translate.Translator, error) { return nil, errors.New("no key") },
		"drops cues":        func(translate.Config) (translate.Translator, error) { return brokenTranslator{dropCues: true}, nil },
		"ignores context":   func(translate.Config) (translate.Translator, error) { return brokenTranslator{ignoreCtx: true}, nil },
		"rejects en->en":    func(translate.Config) (translate.Translator, error) { return brokenTranslator{rejectAll: true}, nil },
		"accepts any pair":  func(translate.Config) (translate.Translator, error) { return brokenTranslator{acceptAll: true}, nil },
	} {
		if failures := runRecorded(factory); len(failures) == 0 {
			t.Errorf("%s: contract suite accepted a broken translator", name)
		}
	}
}
