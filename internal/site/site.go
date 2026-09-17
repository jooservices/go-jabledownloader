// Package site defines the provider abstraction that lets the downloader
// support multiple video sites (Jable, EPORNER, ...) behind one CLI. Each
// site exposes input resolution and video-page parsing as typed domain data;
// the app layer picks a download strategy from the returned sources.
package site

import (
	"context"
	"errors"
)

// SourceKind distinguishes how a video stream must be fetched.
type SourceKind int

const (
	// SourceHLS marks an HLS master playlist (segment download + concat).
	SourceHLS SourceKind = iota
	// SourceDirect marks a progressive file downloaded over HTTP (Range).
	SourceDirect
)

// Source is one downloadable stream offered by a video page.
type Source struct {
	Kind   SourceKind
	URL    string
	Codec  string // h264, av1; empty when unknown
	Height int    // 0 when unknown
}

// VideoInfo is a fully resolved video page.
type VideoInfo struct {
	Code    string
	Title   string
	VideoID string
	Sources []Source
}

// VideoEntry is one result row in a listing page.
type VideoEntry struct {
	Code     string
	Title    string
	URL      string
	Duration string
}

// Site adapts one video provider. Fetchers are injected so tests stay
// network-free; implementations must not hold global browser state.
type Site interface {
	Name() string
	// ResolveInput maps a CLI argument (full URL or bare code) to a video
	// page URL, rejecting hosts that do not belong to this site.
	ResolveInput(ctx context.Context, input string) (string, error)
	// FetchInfo parses a video page into typed domain data.
	FetchInfo(ctx context.Context, videoURL string) (*VideoInfo, error)
}

// Lister is the optional discovery contract a site can implement
// (search / latest / hot listings).
type Lister interface {
	Latest(ctx context.Context, page int) ([]VideoEntry, error)
	Hot(ctx context.Context, page int) ([]VideoEntry, error)
	Search(ctx context.Context, query string, page int) ([]VideoEntry, error)
}

// Photo is one full-resolution image in a photo gallery.
type Photo struct {
	ID           string
	ImageURL     string // largest available resolution
	ThumbnailURL string
}

// Gallery is a photo gallery with its full photo set.
type Gallery struct {
	Code   string
	Title  string
	Photos []Photo
}

// GallerySite is the optional contract a photo-gallery provider implements
// (e.g. Jav Photos). It complements Site rather than replacing it.
type GallerySite interface {
	FetchGallery(ctx context.Context, url string) (*Gallery, error)
}

// ErrUnsupportedInput is returned when no registered site can resolve input.
var ErrUnsupportedInput = errors.New("unsupported input: provide a site URL or a recognized video code")
