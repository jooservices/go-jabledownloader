package translate

import (
	"context"

	"github.com/jooservices/go-jabledownloader/internal/domain"
)

func init() {
	Register("none", func(Config) (Translator, error) { return noneTranslator{}, nil })
}

// noneTranslator keeps cues unchanged; it only accepts same-language pairs.
type noneTranslator struct{}

func (noneTranslator) Name() string { return "none" }

func (noneTranslator) Translate(ctx context.Context, cues []domain.Cue, from, to string) ([]domain.Cue, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if from != to {
		return nil, ErrUnsupportedPair
	}
	return append([]domain.Cue(nil), cues...), nil
}
