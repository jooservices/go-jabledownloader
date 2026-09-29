// Package site defines the provider contract and registry that let the
// downloader support many video sites. A site only resolves pages into
// domain values; downloading is the job of the engines.
package site

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jooservices/go-jabledownloader/internal/domain"
)

// ListOptions selects a listing. View is one of the site's Descriptor.Views;
// empty selects "latest".
type ListOptions struct {
	View string
	Page int
}

// Site adapts one video provider. Fetchers are injected so tests stay
// network-free; implementations must not hold global browser state.
type Site interface {
	Name() string
	// List returns one page of a named listing.
	List(ctx context.Context, opts ListOptions) ([]domain.Item, error)
	// Search returns one page of results for keyword.
	Search(ctx context.Context, keyword string, page int) ([]domain.Item, error)
	// Detail resolves a video URL or bare code into its downloadable sources.
	Detail(ctx context.Context, ref string) (*domain.Detail, error)
}

// GallerySite is the optional contract of photo-gallery providers (for
// example Jav Photos): a gallery URL resolves to all of its photos, which
// the app downloads instead of a video.
type GallerySite interface {
	Gallery(ctx context.Context, ref string) (*domain.Gallery, error)
}

// ErrNotVideo is returned by Detail on sites that only host photo galleries.
var ErrNotVideo = errors.New("this site hosts photo galleries, not videos; use download with a gallery URL")

// ErrUnsupportedInput is returned when no registered site can resolve input.
var ErrUnsupportedInput = errors.New("unsupported input: provide a site URL or a recognized video code")

// CheckView normalises view ("" → "latest") and rejects views the site does
// not declare.
func CheckView(siteName string, views []string, view string) (string, error) {
	view = strings.TrimSpace(view)
	if view == "" {
		view = "latest"
	}
	if !slices.Contains(views, view) {
		return "", fmt.Errorf("unsupported %s list view %q (valid: %s)", siteName, view, strings.Join(views, ", "))
	}
	return view, nil
}
