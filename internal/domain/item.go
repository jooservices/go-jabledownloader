package domain

// Item is one result row in a listing page.
type Item struct {
	Site         string `json:"site"`
	Code         string `json:"code"`
	Title        string `json:"title"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	Duration     string `json:"duration,omitempty"`
}

// Detail is a fully resolved video page.
type Detail struct {
	Site    string   `json:"site"`
	Code    string   `json:"code"`
	Title   string   `json:"title"`
	VideoID string   `json:"video_id,omitempty"`
	Sources []Source `json:"sources"`
}
