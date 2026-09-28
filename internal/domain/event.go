package domain

// EventKind classifies download engine events.
type EventKind int

const (
	// EventPlan announces the unit total before a transfer starts.
	EventPlan EventKind = iota
	// EventProgress reports unit-based progress (segments or chunks).
	EventProgress
	// EventTimeProgress reports progress measured in media seconds, used when
	// ffmpeg downloads a stream directly.
	EventTimeProgress
	// EventRetry reports a retry.
	EventRetry
	// EventResume reports resumed work.
	EventResume
	// EventDone reports a finished transfer.
	EventDone
)

// Event is transport-neutral download progress emitted by engines.
type Event struct {
	Kind    EventKind
	Done    int64   // units finished (segments or chunks)
	Total   int64   // units planned
	Bytes   int64   // bytes written so far
	Failed  int64   // units that failed
	Seconds float64 // media seconds processed (EventTimeProgress)
	Speed   float64 // ffmpeg speed multiplier (EventTimeProgress)
	Message string  // human-readable detail for retry/resume
}

// EventSink receives engine events. Engines may call it concurrently.
type EventSink func(Event)
