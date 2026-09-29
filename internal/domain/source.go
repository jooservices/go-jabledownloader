package domain

import "net/http"

// SourceKind identifies the transport required for a stream.
type SourceKind int

const (
	// SourceHLS is an HLS stream.
	SourceHLS SourceKind = iota
	// SourceProgressive is a progressive HTTP stream.
	SourceProgressive
)

// Source is one downloadable stream offered by a video page. The JSON keys
// match the v4.3 `get --json` output and are part of the CLI contract.
type Source struct {
	Kind   SourceKind `json:"Kind"`
	URL    string     `json:"URL"`
	Codec  string     `json:"Codec"`
	Height int        `json:"Height"`
	// Headers are the request headers the CDN requires (Referer,
	// User-Agent). They are set by the site and never serialized.
	Headers http.Header `json:"-"`
}
