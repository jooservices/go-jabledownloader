// Package app holds the use-cases: resolve and download a video, discover
// listings, and download a selection. It talks to sites, engines, and the
// subtitle pipeline through their contracts and to the user only through
// the Reporter and Prompter ports, so any UI can drive it.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"

	"github.com/jooservices/go-jabledownloader/internal/config"
	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/engine"
	"github.com/jooservices/go-jabledownloader/internal/site"
	"github.com/jooservices/go-jabledownloader/internal/telemetry"
)

// maxConcurrentDiscovery bounds how many sites are queried at once.
const maxConcurrentDiscovery = 4

// Options are the user-level switches for a run.
type Options struct {
	DryRun    bool
	Yes       bool // skip the picker and confirmation
	Quiet     bool // implies Yes
	Force     bool // re-download even when a complete file exists
	Subtitle  bool
	FileName  string // custom output name (--name)
	MaxHeight int    // 0 = best
}

// Service runs the use-cases. Reporter is required; Prompter and
// Subtitler are needed only for interactive batches and --subtitle.
type Service struct {
	Config    *config.Config
	Sites     *Sites
	Tel       *telemetry.T
	Reporter  Reporter
	Prompter  Prompter
	Subtitler Subtitler
	Opts      Options
}

// PlanError reports a batch where some videos failed (exit code 2).
type PlanError struct{ Failed int }

func (e *PlanError) Error() string { return fmt.Sprintf("%d video(s) failed", e.Failed) }

// DiscoveryResult holds listing or search rows, grouped in site
// registration order (the CLI never invents cross-site recency).
type DiscoveryResult struct {
	Items    []domain.Item
	Failures []DiscoveryFailure
}

// DiscoveryFailure identifies a site that could not answer.
type DiscoveryFailure struct {
	Site string
	Err  error
}

// DiscoveryError reports that at least one site failed (exit code 2);
// results from the other sites are still returned.
type DiscoveryError struct{ Failures []DiscoveryFailure }

func (e *DiscoveryError) Error() string {
	return fmt.Sprintf("discovery failed for %d site(s)", len(e.Failures))
}

// RunGet downloads one video identified by a URL or code.
func (s *Service) RunGet(ctx context.Context, input string) error {
	if err := ValidateFileName(s.Opts.FileName); err != nil {
		return err
	}
	s.emit(RunStarted{})
	return s.get(ctx, input)
}

// RunInspect resolves one video without downloading it.
func (s *Service) RunInspect(ctx context.Context, input string) (*domain.Detail, error) {
	st, err := s.Sites.For(input)
	if err != nil {
		return nil, err
	}
	return s.detail(ctx, st, input)
}

// Latest lists the newest videos of the given sites (all when empty).
func (s *Service) Latest(ctx context.Context, siteNames []string, page, count int) (DiscoveryResult, error) {
	return s.discover(ctx, siteNames, count, "latest", func(ctx context.Context, st site.Site) ([]domain.Item, error) {
		return st.List(ctx, site.ListOptions{Page: page})
	})
}

// Search finds keyword on the given sites (all when empty).
func (s *Service) Search(ctx context.Context, siteNames []string, keyword string, page, count int) (DiscoveryResult, error) {
	return s.discover(ctx, siteNames, count, "search "+keyword, func(ctx context.Context, st site.Site) ([]domain.Item, error) {
		return st.Search(ctx, keyword, page)
	})
}

// List returns one named listing view of a single site.
func (s *Service) List(ctx context.Context, name, view string, page, count int) (DiscoveryResult, error) {
	return s.discover(ctx, []string{name}, count, view, func(ctx context.Context, st site.Site) ([]domain.Item, error) {
		return st.List(ctx, site.ListOptions{View: view, Page: page})
	})
}

// RunItems downloads listed items after the user picks and confirms them.
func (s *Service) RunItems(ctx context.Context, items []domain.Item) error {
	if err := ValidateFileName(s.Opts.FileName); err != nil {
		return err
	}
	selected, err := s.pick(items)
	if errors.Is(err, ErrCancelled) {
		s.emit(Cancelled{})
		return nil
	}
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		s.emit(Cancelled{})
		return nil
	}
	s.emit(PlanReady{Items: selected})
	if s.Opts.DryRun {
		s.emit(DryRun{})
		return nil
	}
	if ok, err := s.confirm("Start download?"); err != nil || !ok {
		if err == nil {
			s.emit(Cancelled{})
		}
		return err
	}

	failed := 0
	codes := make(map[string]string) // code → site, for layouts without {site}
	for _, item := range selected {
		if !s.layout().SeparatesSites() {
			if other, seen := codes[item.Code]; seen && other != item.Site {
				s.emit(ItemFailed{Code: item.Code, Err: fmt.Errorf("code also listed by %s and the path template has no {site}; download it by URL", other)})
				failed++
				continue
			}
			codes[item.Code] = item.Site
		}
		if err := s.get(ctx, item.URL); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			s.emit(ItemFailed{Code: item.Code, Err: err})
			failed++
		}
	}
	s.Tel.Count(ctx, "run.videos", int64(len(selected)-failed), attribute.String("outcome", "ok"))
	s.Tel.Count(ctx, "run.videos", int64(failed), attribute.String("outcome", "fail"))
	if failed > 0 {
		return &PlanError{Failed: failed}
	}
	return nil
}

// get resolves and downloads one video, then applies subtitles if asked.
func (s *Service) get(ctx context.Context, input string) error {
	st, err := s.Sites.For(input)
	if err != nil {
		return err
	}
	info, err := s.detail(ctx, st, input)
	if err != nil {
		return err
	}
	layout := s.layout()
	dir, err := layout.VideoDir(st.Name(), info.Code)
	if err != nil {
		return err
	}
	s.emit(VideoResolved{Site: st.Name(), Code: info.Code, Title: info.Title, Input: input, Dir: dir, Workers: s.Config.WorkerCount})
	if s.Opts.DryRun {
		s.emit(DryRun{})
		return nil
	}
	if !s.Opts.Force {
		existing, err := layout.FindComplete(st.Name(), info.Code, s.Opts.FileName)
		if err != nil {
			return err
		}
		if existing != "" {
			s.emit(VideoSkipped{Code: info.Code, Path: existing})
			return s.subtitle(ctx, existing)
		}
	}
	result, err := s.download(ctx, st.Name(), info, dir)
	if err != nil {
		return err
	}
	s.emit(VideoDownloaded{Code: info.Code, Path: result.Path, Codec: result.Codec, Size: result.Size})
	return s.subtitle(ctx, result.Path)
}

func (s *Service) detail(ctx context.Context, st site.Site, ref string) (*domain.Detail, error) {
	ctx, end := s.Tel.StartSpan(ctx, "site.detail", attribute.String("site", st.Name()))
	defer end()
	start := time.Now()
	info, err := st.Detail(ctx, ref)
	s.Tel.Record(ctx, "site.detail.duration_ms", float64(time.Since(start).Milliseconds()), attribute.String("site", st.Name()))
	if err != nil {
		s.Tel.Count(ctx, "site.detail", 1, attribute.String("site", st.Name()), attribute.String("outcome", "error"))
		return nil, fmt.Errorf("fetch video info: %w", err)
	}
	s.Tel.Count(ctx, "site.detail", 1, attribute.String("site", st.Name()), attribute.String("outcome", "ok"))
	return info, nil
}

func (s *Service) download(ctx context.Context, siteName string, info *domain.Detail, dir string) (*engine.Result, error) {
	ctx, end := s.Tel.StartSpan(ctx, "video.download", attribute.String("site", siteName), attribute.String("code", info.Code))
	defer end()

	src, err := pickSource(info.Sources, s.Opts.MaxHeight)
	if err != nil {
		return nil, err
	}
	eng, err := engine.For(src.Kind)
	if err != nil {
		return nil, err
	}
	req := engine.Request{Source: src, Code: info.Code, Dir: dir, FileName: s.Opts.FileName,
		Workers: s.Config.WorkerCount, MaxHeight: s.Opts.MaxHeight}

	s.emit(DownloadStarted{Code: info.Code, SourceURL: src.URL})
	start := time.Now()
	result, err := eng.Download(ctx, req, func(ev domain.Event) {
		s.emit(DownloadProgress{Code: info.Code, Event: ev})
	})
	s.emit(DownloadStopped{Code: info.Code, Err: err})

	attrs := []attribute.KeyValue{attribute.String("site", siteName), attribute.String("outcome", outcome(err))}
	s.Tel.Record(ctx, "video.download.duration_ms", float64(time.Since(start).Milliseconds()), attrs...)
	s.Tel.Count(ctx, "video.download", 1, attrs...)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	s.Tel.Count(ctx, "video.download.bytes", result.Size, attribute.String("site", siteName), attribute.String("codec", result.Codec))
	return result, nil
}

func (s *Service) subtitle(ctx context.Context, video string) error {
	if !s.Opts.Subtitle {
		return nil
	}
	if s.Subtitler == nil {
		return errors.New("subtitles requested but no subtitle pipeline is configured")
	}
	s.emit(SubtitleStarted{Video: video})
	start := time.Now()
	srtPath, skipped, err := s.Subtitler.Run(ctx, video)
	s.Tel.Record(ctx, "subtitle.duration_ms", float64(time.Since(start).Milliseconds()), attribute.String("outcome", outcome(err)))
	if err != nil {
		return fmt.Errorf("subtitles: %w", err)
	}
	s.emit(SubtitleDone{Video: video, SRT: srtPath, Skipped: skipped})
	return nil
}

func (s *Service) discover(ctx context.Context, siteNames []string, count int, action string,
	fetch func(context.Context, site.Site) ([]domain.Item, error),
) (DiscoveryResult, error) {
	if len(siteNames) == 0 {
		siteNames = site.Names()
	}
	items := make([][]domain.Item, len(siteNames))
	failures := make([]error, len(siteNames))
	g := new(errgroup.Group)
	g.SetLimit(maxConcurrentDiscovery)
	for i, name := range siteNames {
		g.Go(func() error {
			s.emit(DiscoveryStarted{Site: name, Action: action})
			items[i], failures[i] = s.discoverSite(ctx, name, count, fetch)
			s.emit(DiscoveryDone{Site: name, Count: len(items[i]), Err: failures[i]})
			return nil
		})
	}
	_ = g.Wait()
	if err := ctx.Err(); err != nil {
		return DiscoveryResult{}, err
	}

	var result DiscoveryResult
	for i, name := range siteNames {
		if failures[i] != nil {
			result.Failures = append(result.Failures, DiscoveryFailure{Site: name, Err: failures[i]})
			continue
		}
		result.Items = append(result.Items, items[i]...)
	}
	if len(result.Failures) > 0 {
		return result, &DiscoveryError{Failures: result.Failures}
	}
	return result, nil
}

func (s *Service) discoverSite(ctx context.Context, name string, count int,
	fetch func(context.Context, site.Site) ([]domain.Item, error),
) ([]domain.Item, error) {
	st, err := s.Sites.ByName(name)
	if err != nil {
		return nil, err
	}
	items, err := fetch(ctx, st)
	if err != nil {
		return nil, err
	}
	if count > 0 && len(items) > count {
		items = items[:count]
	}
	for i := range items {
		if items[i].Site == "" {
			items[i].Site = st.Name()
		}
	}
	return items, nil
}

// pick lets the user choose, unless the run is non-interactive by flag.
func (s *Service) pick(items []domain.Item) ([]domain.Item, error) {
	if s.Opts.DryRun || s.Opts.Yes || s.Opts.Quiet || s.Prompter == nil {
		return items, nil
	}
	return s.Prompter.Pick(items)
}

func (s *Service) confirm(prompt string) (bool, error) {
	if s.Opts.Yes || s.Opts.Quiet {
		return true, nil
	}
	if s.Prompter == nil {
		return false, errors.New("confirmation required: re-run with --yes")
	}
	return s.Prompter.Confirm(prompt)
}

func (s *Service) layout() Layout {
	return Layout{Base: s.Config.OutputDir, Template: s.Config.PathTemplate}
}

func (s *Service) emit(ev Event) {
	if s.Reporter != nil {
		s.Reporter.Report(ev)
	}
}

// pickSource returns the tallest source within maxHeight (0 = no cap),
// preferring h264 over other codecs at equal height. Sources of unknown
// height (HLS masters, which cap height themselves) are the fallback.
func pickSource(sources []domain.Source, maxHeight int) (domain.Source, error) {
	if len(sources) == 0 {
		return domain.Source{}, errors.New("no download sources available")
	}
	var best, unknown *domain.Source
	for i := range sources {
		src := &sources[i]
		switch {
		case src.Height <= 0:
			if unknown == nil {
				unknown = src
			}
		case maxHeight > 0 && src.Height > maxHeight:
		case best == nil || src.Height > best.Height ||
			(src.Height == best.Height && src.Codec == "h264" && best.Codec != "h264"):
			best = src
		}
	}
	switch {
	case best != nil:
		return *best, nil
	case unknown != nil:
		return *unknown, nil
	default:
		return domain.Source{}, fmt.Errorf("no source at or below %dp", maxHeight)
	}
}

func outcome(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}
