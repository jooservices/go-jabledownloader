// Package contracttest contains the shared translator contract suite.
package contracttest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/media/translate"
)

// Run verifies the behavior required of every translator implementation. The
// factory must support the en-to-en pair and reject xx-to-yy.
func Run(t testing.TB, factory translate.Factory) {
	t.Helper()
	translator, err := factory(translate.Config{})
	if err != nil {
		t.Fatalf("construct translator: %v", err)
	}
	cues := []domain.Cue{{Index: 7, Start: time.Second, End: 2 * time.Second, Text: "contract"}}

	got, err := translator.Translate(context.Background(), cues, "en", "en")
	if err != nil {
		t.Fatalf("supported pair: %v", err)
	}
	if len(got) != len(cues) || got[0].Index != cues[0].Index || got[0].Start != cues[0].Start || got[0].End != cues[0].End || got[0].Text == "" {
		t.Fatalf("translator changed cue shape: got %#v", got)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := translator.Translate(cancelled, cues, "en", "en"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: got %v", err)
	}

	if _, err := translator.Translate(context.Background(), cues, "xx", "yy"); !errors.Is(err, translate.ErrUnsupportedPair) {
		t.Fatalf("unsupported pair: got %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, callErr := translator.Translate(context.Background(), cues, "en", "en")
			if callErr != nil || len(result) != 1 || result[0].Text == "" {
				t.Errorf("concurrent translation: result=%#v error=%v", result, callErr)
			}
		}()
	}
	wg.Wait()
}
