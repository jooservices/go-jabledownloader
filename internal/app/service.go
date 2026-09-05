package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/jooservices/go-jabledownloader/internal/config"
	"github.com/jooservices/go-jabledownloader/internal/format"
	"github.com/jooservices/go-jabledownloader/internal/hls"
	"github.com/jooservices/go-jabledownloader/internal/scraper"
	"github.com/jooservices/go-jabledownloader/internal/telemetry"
	"github.com/jooservices/go-jabledownloader/internal/ui"
)

// Options are the user-level switches for a run.
type Options struct {
	DryRun    bool
	Yes       bool
	Quiet     bool
	Verbose   bool
	Force     bool
	TTY       bool
	Workers   int
	OutDir    string
	MaxHeight int
	Count     int
	CheckOnly bool
}

// Service runs the download use-cases.
type Service struct {
	Config *config.Config
	Client *scraper.Client
	Out    ui.Writer
	Tel    *telemetry.T
	Opts   Options
}

// RunGet downloads a single video identified by a URL or code.
func (s *Service) RunGet(ctx context.Context, input string) error {
	ctx, span := s.span(ctx, "run.get", attribute.String("input", input))
	defer span()

	if !s.Opts.Quiet {
		ui.StartBanner(s.Out)
	}

	videoURL, err := scraper.ResolveInput(input)
	if err != nil {
		return err
	}

	if code := scraper.CodeFromURL(videoURL); code != "" && !s.Opts.Force {
		videoDir := VideoDir(s.Config.OutputDir, code)
		if existing := FindCompleteVideo(videoDir, code); existing != "" {
			s.Out.Printf("  %s%s Already downloaded (%s)%s\n", ui.ColorYellow, ui.IconSkip, filepath.Base(existing), ui.ColorReset)
			return nil
		}
	}

	info, err := s.fetchInfo(ctx, videoURL)
	if err != nil {
		return err
	}

	videoDir := VideoDir(s.Config.OutputDir, info.Code)
	if !s.Opts.Quiet {
		s.Out.Printf("  %sTitle:%s   %s\n", ui.ColorDim, ui.ColorReset, info.Title)
		s.Out.Printf("  %sCode:%s    %s\n", ui.ColorDim, ui.ColorReset, info.Code)
		s.Out.Printf("  %sOutput:%s  %s\n", ui.ColorDim, ui.ColorReset, s.Config.OutputDir)
		s.Out.Printf("  %sWorkers:%s %d\n", ui.ColorDim, ui.ColorReset, s.Config.WorkerCount)
		if s.Opts.Verbose {
			s.Out.Printf("  %sPage:%s    %s\n", ui.ColorDim, ui.ColorReset, videoURL)
		}
		s.Out.Println()
	}

	if s.Opts.DryRun {
		s.Out.Printf("  %s%s Dry run — nothing downloaded.%s\n", ui.ColorYellow, ui.IconSpark, ui.ColorReset)
		return nil
	}

	if !s.Opts.Force {
		if existing := FindCompleteVideo(videoDir, info.Code); existing != "" {
			s.Out.Printf("  %s%s Already downloaded (%s)%s\n", ui.ColorYellow, ui.IconSkip, filepath.Base(existing), ui.ColorReset)
			return nil
		}
	}

	result, err := s.downloadVideo(ctx, info, videoDir)
	if err != nil {
		return err
	}

	s.Out.Printf("\n  %s%s Downloaded: %s%s\n", ui.ColorGreen, ui.IconOk, result.Path, ui.ColorReset)
	s.Out.Printf("  %s%s Size: %s%s\n", ui.ColorDim, ui.IconDisk, format.Bytes(result.Size), ui.ColorReset)
	if s.Opts.Verbose {
		s.Out.Printf("  %sCodec:%s   %s\n", ui.ColorDim, ui.ColorReset, result.Codec)
	}
	return nil
}

// VideoFetcher is one page of listing results.
type VideoFetcher func(ctx context.Context, page int) ([]scraper.VideoEntry, error)

// RunMulti downloads a batch of videos from a listing source.
func (s *Service) RunMulti(ctx context.Context, label string, count int, fetcher VideoFetcher) error {
	ctx, span := s.span(ctx, "run.multi", attribute.String("source", label))
	defer span()

	if count <= 0 {
		count = 10
	}

	if !s.Opts.Quiet {
		ui.StartBanner(s.Out)
	}

	s.Out.Printf("  %sSource:%s   %s\n", ui.ColorDim, ui.ColorReset, label)
	s.Out.Printf("  %sTarget:%s   %d videos\n", ui.ColorDim, ui.ColorReset, count)
	s.Out.Printf("  %sOutput:%s  %s\n", ui.ColorDim, ui.ColorReset, s.Config.OutputDir)
	s.Out.Printf("  %sWorkers:%s %d\n", ui.ColorDim, ui.ColorReset, s.Config.WorkerCount)
	s.Out.Println()

	var allVideos []scraper.VideoEntry
	page := 1
	for len(allVideos) < count {
		s.Out.Printf("\r\033[K  %sScanning page %d...%s", ui.ColorDim, page, ui.ColorReset)
		videos, err := fetcher(ctx, page)
		if err != nil {
			s.Out.Printf("\r\033[K  %s%s Scan page %d: %v%s\n", ui.ColorRed, ui.IconErr, page, err, ui.ColorReset)
			s.Tel.Warn(ctx, "scan page failed", attribute.Int("page", page), attribute.String("error", err.Error()))
			break
		}
		if len(videos) == 0 {
			break
		}
		allVideos = append(allVideos, videos...)
		page++
	}
	s.Out.Print("\r\033[K")
	if len(allVideos) > count {
		allVideos = allVideos[:count]
	}

	s.Out.Printf("  %sFound %d videos%s\n", ui.ColorCyan, len(allVideos), ui.ColorReset)

	selected, err := s.pickVideos(allVideos)
	if err != nil {
		return err
	}

	s.printPlan(selected)

	if s.Opts.DryRun {
		s.Out.Printf("\n  %s%s Dry run — nothing downloaded.%s\n", ui.ColorYellow, ui.IconSpark, ui.ColorReset)
		return nil
	}

	if len(selected) == 0 {
		s.Out.Printf("  %sNo videos selected — nothing to download.%s\n", ui.ColorYellow, ui.ColorReset)
		return nil
	}

	if !s.Opts.Yes && !s.Opts.DryRun && !s.Opts.Quiet && !confirm("Start download?") {
		s.Out.Printf("  %sCancelled — nothing downloaded.%s\n", ui.ColorYellow, ui.ColorReset)
		return nil
	}

	var totalSize int64
	success, skipped, failed := 0, 0, 0

	for i, entry := range selected {
		s.Out.Printf("  %s[%d/%d]%s %s\n", ui.ColorCyan, i+1, len(selected), ui.ColorReset, entry.Title)

		videoDir := VideoDir(s.Config.OutputDir, entry.Code)
		if err := os.MkdirAll(videoDir, 0o755); err != nil {
			s.Out.Printf("    %s%s create dir: %v%s\n", ui.ColorRed, ui.IconErr, err, ui.ColorReset)
			failed++
			continue
		}

		if !s.Opts.Force {
			if existing := FindCompleteVideo(videoDir, entry.Code); existing != "" {
				s.Out.Printf("    %s%s Already downloaded (%s)%s\n", ui.ColorYellow, ui.IconSkip, filepath.Base(existing), ui.ColorReset)
				skipped++
				continue
			}
		}

		info, err := s.fetchInfo(ctx, entry.URL)
		if err != nil {
			s.Out.Printf("    %s%s Fetch info: %v%s\n", ui.ColorRed, ui.IconErr, err, ui.ColorReset)
			failed++
			continue
		}

		result, err := s.downloadVideo(ctx, info, videoDir)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			s.Out.Printf("    %s%s Download: %v%s\n", ui.ColorRed, ui.IconErr, err, ui.ColorReset)
			failed++
			continue
		}

		totalSize += result.Size
		success++
		s.Out.Printf("    %s%s %s%s\n", ui.ColorGreen, ui.IconOk, format.Bytes(result.Size), ui.ColorReset)
	}

	s.Out.Printf("\n  %s%s Done: %s%d ok%s  %s%d skip%s  %s%d fail%s  %s%s total%s\n",
		ui.ColorBold, ui.IconSpark,
		ui.ColorGreen, success, ui.ColorReset,
		ui.ColorYellow, skipped, ui.ColorReset,
		ui.ColorRed, failed, ui.ColorReset,
		ui.ColorDim, format.Bytes(totalSize), ui.ColorReset,
	)

	s.Tel.Count(ctx, "run.videos", int64(success), attribute.String("outcome", "ok"))
	s.Tel.Count(ctx, "run.videos", int64(skipped), attribute.String("outcome", "skip"))
	s.Tel.Count(ctx, "run.videos", int64(failed), attribute.String("outcome", "fail"))

	if failed > 0 {
		return &PlanError{Failed: failed}
	}
	return nil
}

func (s *Service) fetchInfo(ctx context.Context, url string) (*scraper.VideoInfo, error) {
	ctx, span := s.span(ctx, "crawl.fetch_video_info", attribute.String("url", url))
	defer span()

	start := time.Now()
	info, err := s.Client.FetchVideoInfo(ctx, url)
	s.Tel.Record(ctx, "crawl.fetch_video_info.duration_ms", float64(time.Since(start).Milliseconds()))
	if err != nil {
		s.Tel.Count(ctx, "crawl.request.total", 1, attribute.String("status", "error"))
		s.Tel.Error(ctx, "fetch video info failed", attribute.String("url", url), attribute.String("error", err.Error()))
		return nil, fmt.Errorf("fetch video info: %w", err)
	}
	s.Tel.Count(ctx, "crawl.request.total", 1, attribute.String("status", "ok"))
	s.Tel.Info(ctx, "video info fetched",
		attribute.String("code", info.Code),
		attribute.String("title", info.Title),
		attribute.String("video_id", info.VideoID),
	)
	return info, nil
}

func (s *Service) downloadVideo(ctx context.Context, info *scraper.VideoInfo, videoDir string) (*hls.VideoFile, error) {
	ctx, span := s.span(ctx, "video.download",
		attribute.String("code", info.Code),
		attribute.String("video_id", info.VideoID),
	)
	defer span()

	if err := os.MkdirAll(videoDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	progress := ui.NewProgress(0)
	display := newProgressDisplay(s.Out, progress, s.Opts.Quiet, s.Opts.TTY)
	defer display.stop()

	dl := hls.NewDownloader(videoDir,
		hls.WithWorkers(s.Config.WorkerCount),
		hls.WithMaxHeight(s.Opts.MaxHeight),
		hls.WithProgress(func(ev hls.Event) {
			progress.Update(ev)
			if ev.Kind == hls.EventResume && ev.Message != "" {
				s.Out.Printf("  %s%s %s%s\n", ui.ColorYellow, ui.IconClock, ev.Message, ui.ColorReset)
			}
		}),
	)

	if s.Opts.Verbose {
		s.Out.Printf("  %sHLS:%s     %s\n", ui.ColorDim, ui.ColorReset, info.HLSURL)
	}

	start := time.Now()
	result, err := dl.Download(ctx, info.Code, info.HLSURL)
	s.Tel.Record(ctx, "hls.video.duration_ms", float64(time.Since(start).Milliseconds()),
		attribute.String("code", info.Code))

	if err != nil {
		s.Tel.Count(ctx, "hls.videos", 1, attribute.String("outcome", "failed"))
		return nil, fmt.Errorf("download: %w", err)
	}

	if progress.SegmentsUsed() && !s.Opts.Quiet {
		s.Out.Print(progress.Summary())
	}

	s.Tel.Count(ctx, "hls.videos", 1, attribute.String("outcome", "completed"))
	s.Tel.Info(ctx, "video downloaded",
		attribute.String("code", info.Code),
		attribute.String("path", result.Path),
		attribute.String("codec", result.Codec),
		attribute.Int64("bytes", result.Size),
	)
	return result, nil
}

// progressDisplay renders the progress line at a fixed interval until stop.
// On a TTY it overwrites a single line (carriage return). On a non-TTY
// (pipes, docker logs) it prints newline-terminated snapshots instead, since
// log collectors buffer output that lacks newlines.
type progressDisplay struct {
	w      ui.Writer
	p      *ui.Progress
	quiet  bool
	tty    bool
	done   chan struct{}
	closed bool
}

func newProgressDisplay(w ui.Writer, p *ui.Progress, quiet, tty bool) *progressDisplay {
	d := &progressDisplay{w: w, p: p, quiet: quiet, tty: tty, done: make(chan struct{})}
	if quiet {
		return d
	}
	interval := 120 * time.Millisecond
	if !tty {
		interval = 800 * time.Millisecond
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-d.done:
				return
			case <-ticker.C:
				if d.tty {
					d.w.Print(d.p.RenderLine())
				} else {
					d.w.Printf("%s\n", d.p.Render())
				}
			}
		}
	}()
	return d
}

func (d *progressDisplay) stop() {
	if d.closed {
		return
	}
	d.closed = true
	close(d.done)
	if !d.quiet && d.tty {
		d.w.Print("\r\033[K")
	}
}

// pickVideos runs the interactive picker when appropriate.
func (s *Service) pickVideos(videos []scraper.VideoEntry) ([]scraper.VideoEntry, error) {
	if s.Opts.DryRun || s.Opts.Yes || s.Opts.Quiet || !interactive() {
		return videos, nil
	}

	items := make([]ui.PickerItem, 0, len(videos))
	for _, v := range videos {
		detail := v.Duration
		if est := hls.EstimateVideoBytes(v.Duration); est > 0 {
			detail += fmt.Sprintf(" · ~%s", format.Bytes(est))
		}
		items = append(items, ui.PickerItem{ID: v.Code, Label: v.Title, Detail: detail, Selected: true})
	}

	picked, err := ui.PickMulti("Select videos to download", items)
	if err != nil {
		if err == ui.ErrPickerCancelled {
			s.Out.Printf("  %sCancelled — nothing downloaded.%s\n", ui.ColorYellow, ui.ColorReset)
			return nil, nil
		}
		return nil, fmt.Errorf("video picker: %w", err)
	}

	selected := []scraper.VideoEntry{}
	for i, it := range picked {
		if it.Selected && i < len(videos) {
			selected = append(selected, videos[i])
		}
	}
	return selected, nil
}

func (s *Service) printPlan(selected []scraper.VideoEntry) {
	var totalEst int64
	s.Out.Printf("\n  %sVideos:%s\n", ui.ColorBold, ui.ColorReset)
	for _, v := range selected {
		detail := v.Duration
		if est := hls.EstimateVideoBytes(v.Duration); est > 0 {
			detail += fmt.Sprintf(" · ~%s", format.Bytes(est))
			totalEst += est
		}
		s.Out.Printf("    %s%s %s  %s%s\n",
			ui.ColorCyan, ui.IconVideo, ui.ColorReset, v.Title,
			ui.ColorDim+detail+ui.ColorReset)
	}
	total := fmt.Sprintf("%d videos", len(selected))
	if totalEst > 0 {
		total += fmt.Sprintf(" · ~%s estimated", format.Bytes(totalEst))
	}
	s.Out.Printf("\n  %sTotal:%s %s\n", ui.ColorBold, ui.ColorReset, total)
}

func (s *Service) span(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, func()) {
	if s.Tel == nil {
		return ctx, func() {}
	}
	return s.Tel.StartSpan(ctx, name, attrs...)
}

func interactive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func confirm(prompt string) bool {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("\n  %s%s [Y/n]%s ", ui.ColorBold, prompt, ui.ColorReset)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.TrimSpace(line)
	return line == "" || strings.EqualFold(line, "y") || strings.EqualFold(line, "yes")
}
