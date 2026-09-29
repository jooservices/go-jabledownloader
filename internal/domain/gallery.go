package domain

import "net/http"

// Gallery is a photo gallery resolved by a site.
type Gallery struct {
	Site   string  `json:"site"`
	Code   string  `json:"code"`
	Title  string  `json:"title"`
	Photos []Photo `json:"photos"`
}

// Photo is one image of a gallery at its largest available size.
type Photo struct {
	ID           string      `json:"id"`
	URL          string      `json:"url"`
	ThumbnailURL string      `json:"thumbnail_url,omitempty"`
	Headers      http.Header `json:"-"` // request headers the image host requires
}
