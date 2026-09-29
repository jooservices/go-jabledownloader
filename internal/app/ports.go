package app

import (
	"context"
	"errors"

	"github.com/jooservices/go-jabledownloader/internal/domain"
)

// Reporter receives use-case events; UI adapters implement it. It must be
// safe for concurrent use: download progress arrives from engine workers.
type Reporter interface {
	Report(Event)
}

// Prompter asks the user for decisions.
type Prompter interface {
	// Pick returns the chosen items, or ErrCancelled.
	Pick(items []domain.Item) ([]domain.Item, error)
	// Confirm answers a yes/no question.
	Confirm(prompt string) (bool, error)
}

// Subtitler adds subtitles to a downloaded video. It reports skipped when
// an earlier run already produced the sidecar.
type Subtitler interface {
	Run(ctx context.Context, video string) (srt string, skipped bool, err error)
}

// ErrCancelled is returned by a Prompter when the user aborts.
var ErrCancelled = errors.New("cancelled by user")
