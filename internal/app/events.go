package app

import "github.com/jooservices/go-jabledownloader/internal/domain"

// Event is a typed notification from a use-case to its UI. The UI decides
// how (and whether) to render each one; the app never prints.
type Event interface{ appEvent() }

// RunStarted opens a single-video run (the CLI shows its banner).
type RunStarted struct{}

// VideoResolved reports the resolved video before any download.
type VideoResolved struct {
	Site, Code, Title, Input, Dir string
	Workers                       int
}

// DryRun reports that nothing will be downloaded.
type DryRun struct{}

// VideoSkipped reports an already complete download.
type VideoSkipped struct{ Code, Path string }

// DownloadStarted opens a transfer; DownloadProgress events follow until
// DownloadStopped.
type DownloadStarted struct{ Code, SourceURL string }

// DownloadProgress carries one engine event. It may arrive concurrently.
type DownloadProgress struct {
	Code  string
	Event domain.Event
}

// DownloadStopped closes a transfer; Err is nil on success.
type DownloadStopped struct {
	Code string
	Err  error
}

// VideoDownloaded reports the finished file.
type VideoDownloaded struct {
	Code, Path, Codec string
	Size              int64
}

// SubtitleStarted opens subtitle generation for a video.
type SubtitleStarted struct{ Video string }

// SubtitleDone reports the sidecar; Skipped means it already existed.
type SubtitleDone struct {
	Video, SRT string
	Skipped    bool
}

// DiscoveryStarted and DiscoveryDone bracket one site's listing or search.
type DiscoveryStarted struct{ Site, Action string }

// DiscoveryDone reports a site's result count or failure.
type DiscoveryDone struct {
	Site  string
	Count int
	Err   error
}

// PlanReady lists the videos a batch will download.
type PlanReady struct{ Items []domain.Item }

// Cancelled reports that the user declined; nothing was downloaded.
type Cancelled struct{}

// ItemFailed reports one failed video in a batch; the batch continues.
type ItemFailed struct {
	Code string
	Err  error
}

func (RunStarted) appEvent()       {}
func (VideoResolved) appEvent()    {}
func (DryRun) appEvent()           {}
func (VideoSkipped) appEvent()     {}
func (DownloadStarted) appEvent()  {}
func (DownloadProgress) appEvent() {}
func (DownloadStopped) appEvent()  {}
func (VideoDownloaded) appEvent()  {}
func (SubtitleStarted) appEvent()  {}
func (SubtitleDone) appEvent()     {}
func (DiscoveryStarted) appEvent() {}
func (DiscoveryDone) appEvent()    {}
func (PlanReady) appEvent()        {}
func (Cancelled) appEvent()        {}
func (ItemFailed) appEvent()       {}
