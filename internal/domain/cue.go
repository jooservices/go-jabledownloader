package domain

import "time"

// Cue is one timed subtitle cue.
type Cue struct {
	Index int
	Start time.Duration
	End   time.Duration
	Text  string
}
