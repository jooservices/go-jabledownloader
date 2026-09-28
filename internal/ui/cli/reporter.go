package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"sync"

	"github.com/jooservices/go-jabledownloader/internal/app"
	"github.com/jooservices/go-jabledownloader/internal/format"
)

// ReporterOptions controls how much the reporter prints.
type ReporterOptions struct {
	Color   bool // emit ANSI colors (otherwise they are stripped)
	TTY     bool // redraw progress in place
	Quiet   bool // only results, skips, and failures
	Verbose bool // also inputs, source URLs, and codecs
}

// Reporter renders app events to a terminal. It is safe for concurrent use.
type Reporter struct {
	mu      sync.Mutex
	w       *StdWriter
	opts    ReporterOptions
	display *progressDisplay
}

var _ app.Reporter = (*Reporter)(nil)

// NewReporter writes to out.
func NewReporter(out io.Writer, opts ReporterOptions) *Reporter {
	return &Reporter{w: NewStdWriter(out, opts.Color), opts: opts}
}

// Report renders one event.
func (r *Reporter) Report(ev app.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch e := ev.(type) {
	case app.DownloadProgress:
		if r.display != nil {
			r.display.progress.Update(e.Event)
		}
	case app.DownloadStarted:
		r.downloadStarted(e)
	case app.DownloadStopped:
		r.downloadStopped(e)
	case app.VideoSkipped:
		r.w.Printf("  %s%s Already downloaded (%s)%s\n", ColorYellow, IconSkip, filepath.Base(e.Path), ColorReset)
	case app.VideoDownloaded:
		r.w.Printf("\n  %s%s Downloaded: %s%s\n", ColorGreen, IconOk, e.Path, ColorReset)
		r.w.Printf("  %s%s Size: %s%s\n", ColorDim, IconDisk, format.Bytes(e.Size), ColorReset)
		r.verbosef("  %sCodec:%s   %s\n", ColorDim, ColorReset, e.Codec)
	case app.ItemFailed:
		r.w.Printf("  %s%s Download %s: %v%s\n", ColorRed, IconErr, e.Code, e.Err, ColorReset)
	default:
		if !r.opts.Quiet {
			r.reportDetail(ev)
		}
	}
}

// reportDetail renders the events hidden by --quiet.
func (r *Reporter) reportDetail(ev app.Event) {
	switch e := ev.(type) {
	case app.RunStarted:
		r.w.Println()
		r.w.Println(Bordered("Jable Downloader", ColorCyan))
		r.w.Println()
	case app.VideoResolved:
		r.w.Printf("  %sTitle:%s   %s\n", ColorDim, ColorReset, e.Title)
		r.w.Printf("  %sCode:%s    %s\n", ColorDim, ColorReset, e.Code)
		r.w.Printf("  %sSite:%s    %s\n", ColorDim, ColorReset, e.Site)
		r.w.Printf("  %sOutput:%s  %s\n", ColorDim, ColorReset, e.Dir)
		r.w.Printf("  %sWorkers:%s %d\n", ColorDim, ColorReset, e.Workers)
		r.verbosef("  %sInput:%s   %s\n", ColorDim, ColorReset, e.Input)
		r.w.Println()
	case app.DryRun:
		r.w.Printf("  %s%s Dry run — nothing downloaded.%s\n", ColorYellow, IconSpark, ColorReset)
	case app.Cancelled:
		r.w.Printf("  %sCancelled — nothing downloaded.%s\n", ColorYellow, ColorReset)
	case app.SubtitleStarted:
		r.w.Printf("  %s%s Adding subtitles to %s...%s\n", ColorCyan, IconSpark, filepath.Base(e.Video), ColorReset)
	case app.SubtitleDone:
		if e.Skipped {
			r.w.Printf("  %s%s Subtitles already present (%s) — skipped%s\n", ColorYellow, IconSkip, filepath.Base(e.SRT), ColorReset)
		} else {
			r.w.Printf("  %s%s Subtitles added (%s)%s\n", ColorGreen, IconOk, filepath.Base(e.SRT), ColorReset)
		}
	case app.DiscoveryStarted:
		r.w.Printf("  %s%s Loading %s from %s...%s\n", ColorCyan, IconArrow, e.Action, e.Site, ColorReset)
	case app.DiscoveryDone:
		if e.Err != nil {
			r.w.Printf("  %s%s %s failed%s\n", ColorRed, IconErr, e.Site, ColorReset)
		} else {
			r.w.Printf("  %s%s Found %d result(s) from %s%s\n", ColorGreen, IconOk, e.Count, e.Site, ColorReset)
		}
	case app.PlanReady:
		r.plan(e)
	}
}

func (r *Reporter) downloadStarted(e app.DownloadStarted) {
	r.verbosef("  %sSource:%s  %s\n", ColorDim, ColorReset, e.SourceURL)
	if r.opts.Quiet || r.display != nil {
		return
	}
	progress := NewProgress(0)
	progress.SetLabel(e.Code)
	r.display = startDisplay(r.w, progress, r.opts.TTY)
}

func (r *Reporter) downloadStopped(e app.DownloadStopped) {
	if r.display == nil {
		return
	}
	r.display.stop()
	progress := r.display.progress
	r.display = nil
	if e.Err == nil && progress.SegmentsUsed() {
		r.w.Print(progress.Summary())
	}
}

func (r *Reporter) plan(e app.PlanReady) {
	var total int64
	r.w.Printf("\n  %sVideos:%s\n", ColorBold, ColorReset)
	for _, item := range e.Items {
		detail := item.Duration
		if estimate := app.EstimateVideoBytes(item.Duration); estimate > 0 {
			detail += " · ~" + format.Bytes(estimate)
			total += estimate
		}
		r.w.Printf("    %s%s%s  %s  %s%s%s\n", ColorCyan, IconVideo, ColorReset, item.Title, ColorDim, detail, ColorReset)
	}
	summary := fmt.Sprintf("%d videos", len(e.Items))
	if total > 0 {
		summary += " · ~" + format.Bytes(total) + " estimated"
	}
	r.w.Printf("\n  %sTotal:%s %s\n", ColorBold, ColorReset, summary)
}

func (r *Reporter) verbosef(format string, a ...any) {
	if r.opts.Verbose {
		r.w.Printf(format, a...)
	}
}
