package domain

import "time"

// Source kinds a Subscription can follow. They map onto the two generic
// questions every site answers: "what is in this listing view" and "what
// matches this keyword" (an actor, tag, studio, or channel is a keyword or a
// named view on the site that supports it).
const (
	SubView   = "view"   // a named listing view such as "latest" or "hot"
	SubSearch = "search" // a keyword: actor, tag, title words, code prefix
)

// Subscription is one registered source to monitor. New items it yields are
// downloaded automatically when AutoDownload is set.
type Subscription struct {
	ID           string        `json:"id"`
	Site         string        `json:"site"` // "" = every site
	Kind         string        `json:"kind"` // SubView or SubSearch
	Query        string        `json:"query"`
	Enabled      bool          `json:"enabled"`
	AutoDownload bool          `json:"auto_download"`
	Backfill     bool          `json:"backfill,omitempty"` // download what already exists on the first poll
	Interval     time.Duration `json:"interval"`           // 0 = the monitor default
	MaxPerPoll   int           `json:"max_per_poll,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	LastPolled   time.Time     `json:"last_polled,omitempty"`
	LastError    string        `json:"last_error,omitempty"`
}

// Job states.
const (
	JobQueued  = "queued"
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"
)

// Job is one requested download, from a subscription or an API/MCP request.
type Job struct {
	ID         string    `json:"id"`
	Input      string    `json:"input"` // URL or code
	Site       string    `json:"site,omitempty"`
	Code       string    `json:"code,omitempty"`
	Title      string    `json:"title,omitempty"`
	Origin     string    `json:"origin"` // "api", "mcp", or "sub:<id>"
	State      string    `json:"state"`
	Error      string    `json:"error,omitempty"`
	Attempts   int       `json:"attempts"`
	CreatedAt  time.Time `json:"created_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}
