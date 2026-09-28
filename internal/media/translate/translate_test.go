package translate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/media/translate"
	"github.com/jooservices/go-jabledownloader/internal/media/translate/contracttest"
)

func TestNoneTranslatorContract(t *testing.T) {
	contracttest.Run(t, func(cfg translate.Config) (translate.Translator, error) {
		return translate.Default.New("none", cfg)
	})
}

func TestFakeTranslatorContract(t *testing.T) {
	contracttest.Run(t, func(translate.Config) (translate.Translator, error) { return uppercaseTranslator{}, nil })
}

func TestRegistryNamesSortedAndLookup(t *testing.T) {
	r := translate.NewRegistry()
	r.Register("zeta", fakeFactory)
	r.Register("alpha", fakeFactory)

	if got := strings.Join(r.Names(), ","); got != "alpha,zeta" {
		t.Fatalf("Names = %s", got)
	}
	if tr, err := r.New(" zeta ", translate.Config{}); err != nil || tr.Name() != "uppercase" {
		t.Fatalf("New = %v, %v", tr, err)
	}
	if _, err := r.New("missing", translate.Config{}); err == nil || !strings.Contains(err.Error(), "available: alpha, zeta") {
		t.Fatalf("missing err = %v", err)
	}
}

func TestRegistryPanicsOnProgrammerErrors(t *testing.T) {
	for name, register := range map[string]func(*translate.Registry){
		"duplicate":   func(r *translate.Registry) { r.Register("a", fakeFactory); r.Register("a", fakeFactory) },
		"empty name":  func(r *translate.Registry) { r.Register(" ", fakeFactory) },
		"nil factory": func(r *translate.Registry) { r.Register("a", nil) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			register(translate.NewRegistry())
		})
	}
}

func TestDefaultRegistryHasNone(t *testing.T) {
	if names := translate.Default.Names(); len(names) == 0 || names[0] != "none" {
		t.Fatalf("Default names = %v", names)
	}
}

func fakeFactory(translate.Config) (translate.Translator, error) { return uppercaseTranslator{}, nil }

type uppercaseTranslator struct{}

func (uppercaseTranslator) Name() string { return "uppercase" }

func (uppercaseTranslator) Translate(ctx context.Context, cues []domain.Cue, from, to string) ([]domain.Cue, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if from != "en" || to != "en" {
		return nil, translate.ErrUnsupportedPair
	}
	result := append([]domain.Cue(nil), cues...)
	for i := range result {
		result[i].Text = strings.ToUpper(result[i].Text)
	}
	return result, nil
}
