package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/jooservices/go-jabledownloader/internal/config"
	"github.com/jooservices/go-jabledownloader/internal/direct"
	"github.com/jooservices/go-jabledownloader/internal/format"
	"github.com/jooservices/go-jabledownloader/internal/hls"
	"github.com/jooservices/go-jabledownloader/internal/site"
	"github.com/jooservices/go-jabledownloader/internal/subtitle"
	"github.com/jooservices/go-jabledownloader/internal/telemetry"
	"github.com/jooservices/go-jabledownloader/internal/ui"
)

// Options are the user-level switches for a run.
type Options struct {
	DryRun       bool
	Yes          bool
	Quiet        bool
	Verbose      bool
	Force        bool
	TTY          bool
	Workers      int
	OutDir       string
	MaxHeight    int
	Count        int
	CheckOnly    bool
	Subtitle     bool
	SubtitleMode string // subtitle.ModeSoft or subtitle.ModeHard
	WhisperModel string
	// SpokenLanguage hints Whisper (e.g. "ja"). Empty = auto-detect.
	SpokenLanguage string
}

// Service runs the download use-cases.
type Service struct {
	Config *config.Config
	Sites  *Sites
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

	st, err := s.Sites.For(input)
	if err != nil {
		return err
	}

	videoURL, err := st.ResolveInput(ctx, input)
	if err != nil {
		return err
	}

	info, err := s.fetchInfo(ctx, st, videoURL)
	if err != nil {
		return err
	}

	videoDir := VideoDir(s.Config.OutputDir, info.Code)
	if !s.Opts.Quiet {
		s.Out.Printf("  %sTitle:%s   %s\n", ui.ColorDim, ui.ColorReset, info.Title)
		s.Out.Printf("  %sCode:%s    %s\n", ui.ColorDim, ui.ColorReset, info.Code)
		s.Out.Printf("  %sSite:%s    %s\n", ui.ColorDim, ui.ColorReset, st.Name())
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
			if s.Opts.Subtitle {
				return s.embedSubtitles(ctx, existing)
			}
			return nil
		}
	}

	result, err := s.downloadVideo(ctx, st, info, videoDir)
	if err != nil {
		return err
	}

	s.Out.Printf("\n  %s%s Downloaded: %s%s\n", ui.ColorGreen, ui.IconOk, result.Path, ui.ColorReset)
	s.Out.Printf("  %s%s Size: %s%s\n", ui.ColorDim, ui.IconDisk, format.Bytes(result.Size), ui.ColorReset)
	if s.Opts.Verbose {
		s.Out.Printf("  %sCodec:%s   %s\n", ui.ColorDim, ui.ColorReset, result.Codec)
	}
	if s.Opts.Subtitle {
		return s.embedSubtitles(ctx, result.Path)
	}
	return nil
}

// VideoFetcher is one page of listing results.
type VideoFetcher func(ctx context.Context, page int) ([]site.VideoEntry, error)

// RunMulti downloads a batch of videos from a listing source.
func (s *Service) RunMulti(ctx context.Context, label string, count int, st site.Site, fetcher VideoFetcher) error {
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

	var allVideos []site.VideoEntry
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
				if s.Opts.Subtitle {
					if err := s.embedSubtitles(ctx, existing); err != nil {
						s.Out.Printf("    %s%s Subtitles: %v%s\n", ui.ColorRed, ui.IconErr, err, ui.ColorReset)
						failed++
						continue
					}
				}
				skipped++
				continue
			}
		}

		info, err := s.fetchInfo(ctx, st, entry.URL)
		if err != nil {
			s.Out.Printf("    %s%s Fetch info: %v%s\n", ui.ColorRed, ui.IconErr, err, ui.ColorReset)
			failed++
			continue
		}

		result, err := s.downloadVideo(ctx, st, info, videoDir)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			s.Out.Printf("    %s%s Download: %v%s\n", ui.ColorRed, ui.IconErr, err, ui.ColorReset)
			failed++
			continue
		}

		if s.Opts.Subtitle {
			if err := s.embedSubtitles(ctx, result.Path); err != nil {
				s.Out.Printf("    %s%s Subtitles: %v%s\n", ui.ColorRed, ui.IconErr, err, ui.ColorReset)
				failed++
				continue
			}
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

func (s *Service) fetchInfo(ctx context.Context, st site.Site, url string) (*site.VideoInfo, error) {
	ctx, span := s.span(ctx, "crawl.fetch_video_info", attribute.String("url", url))
	defer span()

	start := time.Now()
	info, err := st.FetchInfo(ctx, url)
	s.Tel.Record(ctx, "crawl.fetch_video_info.duration_ms", float64(time.Since(start).Milliseconds()))
	if err != nil {
		s.Tel.Count(ctx, "crawl.request.total", 1, attribute.String("status", "error"))
		s.Tel.Error(ctx, "fetch video info failed", attribute.String("url", url), attribute.String("error", err.Error()))
		return nil, fmt.Errorf("fetch video info: %w", err)
	}
	s.Tel.Count(ctx, "crawl.request.total", 1, attribute.String("status", "ok"))
	s.Tel.Info(ctx, "video info fetched",
		attribute.String("site", st.Name()),
		attribute.String("code", info.Code),
		attribute.String("title", info.Title),
		attribute.String("video_id", info.VideoID),
	)
	return info, nil
}

// pickSource returns the best source at or below the max height, preferring
// h264 over av1 at equal height. Zero maxHeight keeps the highest source.
func pickSource(sources []site.Source, maxHeight int) (site.Source, error) {
	if len(sources) == 0 {
		return site.Source{}, fmt.Errorf("no download sources available")
	}

	best := sources[0]
	for _, src := range sources {
		if maxHeight > 0 && src.Height > maxHeight {
			continue
		}
		if src.Height > best.Height ||
			(src.Height == best.Height && src.Codec == "h264" && best.Codec == "av1") {
			best = src
		}
	}
	if maxHeight > 0 && best.Height > maxHeight {
		return site.Source{}, fmt.Errorf("no source at or below %dp", maxHeight)
	}
	return best, nil
}

func (s *Service) downloadVideo(ctx context.Context, st site.Site, info *site.VideoInfo, videoDir string) (*hls.VideoFile, error) {
	ctx, span := s.span(ctx, "video.download",
		attribute.String("site", st.Name()),
		attribute.String("code", info.Code),
		attribute.String("video_id", info.VideoID),
	)
	defer span()

	if err := os.MkdirAll(videoDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	src, err := pickSource(info.Sources, s.Opts.MaxHeight)
	if err != nil {
		return nil, err
	}

	progress := ui.NewProgress(0)
	progress.SetLabel(info.Code)
	display := newProgressDisplay(s.Out, progress, s.Opts.Quiet, s.Opts.TTY)
	defer display.stop()

	if s.Opts.Verbose {
		s.Out.Printf("  %sSource:%s  %s\n", ui.ColorDim, ui.ColorReset, src.URL)
	}

	start := time.Now()
	var result *hls.VideoFile

	switch src.Kind {
	case site.SourceHLS:
		result, err = s.downloadHLS(ctx, info.Code, src.URL, videoDir, progress)
	case site.SourceDirect:
		result, err = s.downloadDirect(ctx, info.Code, src, videoDir, progress)
	default:
		err = fmt.Errorf("unsupported source kind %d", src.Kind)
	}
	s.Tel.Record(ctx, "video.download.duration_ms", float64(time.Since(start).Milliseconds()),
		attribute.String("code", info.Code), attribute.String("site", st.Name()))

	if err != nil {
		s.Tel.Count(ctx, "videos", 1, attribute.String("outcome", "failed"))
		return nil, fmt.Errorf("download: %w", err)
	}

	if progress.SegmentsUsed() && !s.Opts.Quiet {
		s.Out.Print(progress.Summary())
	}

	s.Tel.Count(ctx, "videos", 1, attribute.String("outcome", "completed"))
	s.Tel.Info(ctx, "video downloaded",
		attribute.String("site", st.Name()),
		attribute.String("code", info.Code),
		attribute.String("path", result.Path),
		attribute.String("codec", result.Codec),
		attribute.Int64("bytes", result.Size),
	)
	return result, nil
}

func (s *Service) downloadHLS(ctx context.Context, code, hlsURL, videoDir string, progress *ui.Progress) (*hls.VideoFile, error) {
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
	return dl.Download(ctx, code, hlsURL)
}

func (s *Service) downloadDirect(ctx context.Context, code string, src site.Source, videoDir string, progress *ui.Progress) (*hls.VideoFile, error) {
	dl := direct.NewDownloader(videoDir,
		direct.WithWorkers(s.Config.WorkerCount),
		direct.WithHTTPClient(s.Sites.httpClient()),
		direct.WithProgress(func(ev direct.Event) {
			h := toHLSEvent(ev)
			progress.Update(h)
			if ev.Kind == direct.EventResume && ev.Message != "" {
				s.Out.Printf("  %s%s %s%s\n", ui.ColorYellow, ui.IconClock, ev.Message, ui.ColorReset)
			}
		}),
	)
	vf, err := dl.Download(ctx, code, src.Codec, src.URL)
	if err != nil {
		return nil, err
	}
	return &hls.VideoFile{Path: vf.Path, Size: vf.Size, Codec: vf.Codec}, nil
}

// toHLSEvent adapts a direct engine event to the shared UI event shape.
func toHLSEvent(ev direct.Event) hls.Event {
	kind := hls.EventSegments
	switch ev.Kind {
	case direct.EventRetry:
		kind = hls.EventRetry
	case direct.EventResume:
		kind = hls.EventResume
	}
	return hls.Event{
		Kind:    kind,
		Done:    ev.Done,
		Total:   ev.Total,
		Bytes:   ev.Bytes,
		Failed:  ev.Failed,
		Seconds: ev.Seconds,
		Speed:   ev.Speed,
		Message: ev.Message,
	}
}

func (s *Service) embedSubtitles(ctx context.Context, videoPath string) error {
	ctx, span := s.span(ctx, "subtitle.embed_english",
		attribute.String("path", videoPath),
		attribute.String("mode", s.Opts.SubtitleMode),
	)
	defer span()

	mode, err := subtitle.ParseMode(s.Opts.SubtitleMode)
	if err != nil {
		return err
	}
	// Sidecar marks a prior successful embed. Re-running hard mode would burn
	// another layer into the stored pixels; soft re-mux is wasteful. Delete
	// the .en.srt to regenerate.
	srtPath := strings.TrimSuffix(videoPath, filepath.Ext(videoPath)) + ".en.srt"
	if _, err := os.Stat(srtPath); err == nil {
		if !s.Opts.Quiet {
			s.Out.Printf("  %s%s English subtitles already present (%s) — skip embed%s\n",
				ui.ColorYellow, ui.IconSkip, filepath.Base(srtPath), ui.ColorReset)
		}
		s.Tel.Count(ctx, "subtitle.embed", 1, attribute.String("outcome", "skipped"), attribute.String("mode", mode))
		return nil
	}

	if !s.Opts.Quiet {
		s.Out.Printf("  %s%s Embedding English subtitles (mode=%s, mlx_whisper)...%s\n",
			ui.ColorCyan, ui.IconSpark, mode, ui.ColorReset)
	}
	start := time.Now()
	err = embedEnglish(ctx, videoPath, subtitle.Options{
		Model:    s.Opts.WhisperModel,
		Language: s.Opts.SpokenLanguage,
		Mode:     mode,
		Verbose:  s.Opts.Verbose,
	})
	s.Tel.Record(ctx, "subtitle.embed.duration_ms", float64(time.Since(start).Milliseconds()),
		attribute.String("mode", mode))
	if err != nil {
		s.Tel.Count(ctx, "subtitle.embed", 1, attribute.String("outcome", "failed"), attribute.String("mode", mode))
		return fmt.Errorf("embed English subtitles: %w", err)
	}
	s.Tel.Count(ctx, "subtitle.embed", 1, attribute.String("outcome", "ok"), attribute.String("mode", mode))
	if !s.Opts.Quiet {
		switch mode {
		case subtitle.ModeHard:
			s.Out.Printf("  %s%s English hardsubs burned into %s%s\n",
				ui.ColorGreen, ui.IconOk, filepath.Base(videoPath), ui.ColorReset)
		default:
			s.Out.Printf("  %s%s English soft subs muxed into %s%s\n",
				ui.ColorGreen, ui.IconOk, filepath.Base(videoPath), ui.ColorReset)
		}
	}
	return nil
}

// embedEnglish is the subtitle seam (overridden in tests).
var embedEnglish = subtitle.EmbedEnglish

// progressDisplay renders the progress block at a fixed interval until stop.
// On a TTY it overwrites the previous multi-line block. On a non-TTY
// (pipes, docker logs) it prints newline-terminated snapshots instead.
type progressDisplay struct {
	w         ui.Writer
	p         *ui.Progress
	quiet     bool
	tty       bool
	done      chan struct{}
	closed    bool
	started   bool
	lastLines int
	wg        sync.WaitGroup
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
	d.started = true
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-d.done:
				return
			case <-ticker.C:
				if d.tty {
					n := d.p.LineCount()
					d.w.Print(d.p.RenderLine(d.lastLines))
					d.lastLines = n
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
	if d.started {
		d.wg.Wait()
	}
	if !d.quiet && d.tty {
		d.w.Print(ui.ClearBlock(d.lastLines))
	}
}

// pickVideos runs the interactive picker when appropriate.
func (s *Service) pickVideos(videos []site.VideoEntry) ([]site.VideoEntry, error) {
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

	selected := []site.VideoEntry{}
	for i, it := range picked {
		if it.Selected && i < len(videos) {
			selected = append(selected, videos[i])
		}
	}
	return selected, nil
}

func (s *Service) printPlan(selected []site.VideoEntry) {
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
