package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/config"
	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/engine"
	"github.com/jooservices/go-jabledownloader/internal/site"
)

// world is the behaviour of the fake sites registered below.
type fakeWorld struct {
	mu        sync.Mutex
	details   map[string]*domain.Detail // by ref
	items     map[string][]domain.Item  // by site
	listErr   map[string]error
	listDelay time.Duration
}

var world = &fakeWorld{}

func resetWorld(t *testing.T) *fakeWorld {
	t.Helper()
	world = &fakeWorld{details: map[string]*domain.Detail{}, items: map[string][]domain.Item{}, listErr: map[string]error{}}
	return world
}

type fakeSite struct{ name string }

func (f fakeSite) Name() string { return f.name }

func (f fakeSite) List(ctx context.Context, _ site.ListOptions) ([]domain.Item, error) {
	return f.Search(ctx, "", 1)
}

func (f fakeSite) Search(ctx context.Context, _ string, _ int) ([]domain.Item, error) {
	world.mu.Lock()
	delay, items, err := world.listDelay, world.items[f.name], world.listErr[f.name]
	world.mu.Unlock()
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return append([]domain.Item(nil), items...), err
}

func (f fakeSite) Detail(_ context.Context, ref string) (*domain.Detail, error) {
	world.mu.Lock()
	defer world.mu.Unlock()
	if d, ok := world.details[ref]; ok {
		copied := *d
		return &copied, nil
	}
	return nil, fmt.Errorf("no such video %q", ref)
}

func init() {
	for _, name := range []string{"alpha", "beta"} {
		site.Register(site.Descriptor{
			Name: name, Hosts: []string{name + ".test"}, CodeRe: regexp.MustCompile(`^` + name[:1] + `-\d+$`),
			New: func(site.Fetcher) site.Site { return fakeSite{name: name} },
		})
	}
}

type fakeEngine struct {
	mu   sync.Mutex
	reqs []engine.Request
	err  error
}

func (e *fakeEngine) Download(_ context.Context, req engine.Request, sink domain.EventSink) (*engine.Result, error) {
	e.mu.Lock()
	e.reqs = append(e.reqs, req)
	e.mu.Unlock()
	sink(domain.Event{Kind: domain.EventProgress, Done: 1, Total: 1, Bytes: 5})
	if e.err != nil {
		return nil, e.err
	}
	path := filepath.Join(req.Dir, req.OutputName("h264"))
	if err := os.MkdirAll(req.Dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte("video"), 0o644); err != nil {
		return nil, err
	}
	return &engine.Result{Path: path, Size: 5, Codec: "h264"}, nil
}

func (e *fakeEngine) requests() []engine.Request {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]engine.Request(nil), e.reqs...)
}

type recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *recorder) Report(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

// kinds returns the event type names, dropping progress for readability.
func (r *recorder) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, ev := range r.events {
		if _, ok := ev.(DownloadProgress); ok {
			continue
		}
		out = append(out, reflect.TypeOf(ev).Name())
	}
	return out
}

func find[T Event](r *recorder) (T, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range r.events {
		if typed, ok := ev.(T); ok {
			return typed, true
		}
	}
	var zero T
	return zero, false
}

type fakePrompter struct {
	pick      []domain.Item
	pickErr   error
	confirm   bool
	confirmed int
}

func (p *fakePrompter) Pick(items []domain.Item) ([]domain.Item, error) {
	if p.pick != nil || p.pickErr != nil {
		return p.pick, p.pickErr
	}
	return items, nil
}

func (p *fakePrompter) Confirm(string) (bool, error) {
	p.confirmed++
	return p.confirm, nil
}

type fakeSubtitler struct {
	videos  []string
	skipped bool
	err     error
}

func (f *fakeSubtitler) Run(_ context.Context, video string) (string, bool, error) {
	f.videos = append(f.videos, video)
	return strings.TrimSuffix(video, ".mp4") + ".en.srt", f.skipped, f.err
}

type fixture struct {
	svc    *Service
	rec    *recorder
	engine *fakeEngine
	world  *fakeWorld
	out    string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	w := resetWorld(t)
	eng := &fakeEngine{}
	engine.Register(domain.SourceHLS, eng)
	t.Cleanup(func() { engine.Register(domain.SourceHLS, nil) })
	out := t.TempDir()
	rec := &recorder{}
	svc := &Service{
		Config:   &config.Config{OutputDir: out, WorkerCount: 3},
		Sites:    NewSites(nil),
		Reporter: rec,
		Opts:     Options{Yes: true},
	}
	return fixture{svc: svc, rec: rec, engine: eng, world: w, out: out}
}

func (f fixture) addVideo(ref, siteName, code string) {
	f.world.details[ref] = &domain.Detail{Site: siteName, Code: code, Title: "Title " + code,
		Sources: []domain.Source{{Kind: domain.SourceHLS, URL: "https://cdn.test/" + code + ".m3u8", Headers: http.Header{"Referer": {"https://" + siteName + ".test/"}}}}}
}

func TestRunGetDownloadsThroughEngine(t *testing.T) {
	f := newFixture(t)
	f.addVideo("a-1", "alpha", "a-1")
	f.svc.Opts.FileName = "custom.mp4"
	f.svc.Opts.MaxHeight = 720

	if err := f.svc.RunGet(context.Background(), "a-1"); err != nil {
		t.Fatal(err)
	}

	reqs := f.engine.requests()
	if len(reqs) != 1 {
		t.Fatalf("engine calls = %d", len(reqs))
	}
	req := reqs[0]
	wantDir := filepath.Join(f.out, "alpha", "a-1")
	if req.Dir != wantDir || req.Workers != 3 || req.FileName != "custom.mp4" || req.MaxHeight != 720 ||
		req.Source.Headers.Get("Referer") != "https://alpha.test/" {
		t.Fatalf("request = %+v", req)
	}
	want := []string{"RunStarted", "VideoResolved", "DownloadStarted", "DownloadStopped", "VideoDownloaded"}
	if got := f.rec.kinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if done, _ := find[VideoDownloaded](f.rec); done.Path != filepath.Join(wantDir, "custom.mp4") || done.Size != 5 {
		t.Fatalf("downloaded = %+v", done)
	}
	if progress, ok := find[DownloadProgress](f.rec); !ok || progress.Code != "a-1" || progress.Event.Bytes != 5 {
		t.Fatalf("progress = %+v", progress)
	}
}

func TestRunGetDryRunDoesNotDownload(t *testing.T) {
	f := newFixture(t)
	f.addVideo("a-1", "alpha", "a-1")
	f.svc.Opts.DryRun = true

	if err := f.svc.RunGet(context.Background(), "a-1"); err != nil {
		t.Fatal(err)
	}
	if len(f.engine.requests()) != 0 {
		t.Fatal("dry run downloaded")
	}
	if _, ok := find[DryRun](f.rec); !ok {
		t.Fatal("missing DryRun event")
	}
}

func TestRunGetSkipsCompleteVideoUnlessForced(t *testing.T) {
	f := newFixture(t)
	f.addVideo("a-1", "alpha", "a-1")
	sub := &fakeSubtitler{}
	f.svc.Subtitler = sub
	f.svc.Opts.Subtitle = true
	existing := filepath.Join(f.out, "a-1", "a-1-h264.mp4") // legacy layout
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := f.svc.RunGet(context.Background(), "a-1"); err != nil {
		t.Fatal(err)
	}
	if skipped, ok := find[VideoSkipped](f.rec); !ok || skipped.Path != existing || len(f.engine.requests()) != 0 {
		t.Fatalf("skipped=%+v ok=%v engine=%d", skipped, ok, len(f.engine.requests()))
	}
	if len(sub.videos) != 1 || sub.videos[0] != existing {
		t.Fatalf("subtitles applied to %v", sub.videos)
	}

	f.svc.Opts.Force = true
	if err := f.svc.RunGet(context.Background(), "a-1"); err != nil {
		t.Fatal(err)
	}
	if len(f.engine.requests()) != 1 {
		t.Fatal("--force did not download")
	}
}

func TestRunGetSubtitles(t *testing.T) {
	f := newFixture(t)
	f.addVideo("a-1", "alpha", "a-1")
	f.svc.Opts.Subtitle = true

	if err := f.svc.RunGet(context.Background(), "a-1"); err == nil || !strings.Contains(err.Error(), "no subtitle pipeline") {
		t.Fatalf("missing subtitler err = %v", err)
	}

	f.svc.Subtitler = &fakeSubtitler{skipped: true}
	f.svc.Opts.Force = true
	if err := f.svc.RunGet(context.Background(), "a-1"); err != nil {
		t.Fatal(err)
	}
	if done, ok := find[SubtitleDone](f.rec); !ok || !done.Skipped || !strings.HasSuffix(done.SRT, ".en.srt") {
		t.Fatalf("subtitle done = %+v", done)
	}

	f.svc.Subtitler = &fakeSubtitler{err: errors.New("whisper missing")}
	if err := f.svc.RunGet(context.Background(), "a-1"); err == nil || !strings.Contains(err.Error(), "subtitles: whisper missing") {
		t.Fatalf("subtitler err = %v", err)
	}
}

func TestRunGetErrors(t *testing.T) {
	f := newFixture(t)
	f.addVideo("a-6", "alpha", "..")
	f.addVideo("a-7", "alpha", "a-7")

	for name, tc := range map[string]struct {
		input, fileName string
		engineErr       error
		want            string
	}{
		"unsafe code":     {input: "a-6", want: "unsafe video code"},
		"bad name":        {input: "a-7", fileName: "../x.mp4", want: "--name must be a file name"},
		"unknown input":   {input: "zzz", want: "unsupported input"},
		"detail failure":  {input: "a-9", want: "fetch video info"},
		"engine failure":  {input: "a-7", engineErr: errors.New("cdn down"), want: "download: cdn down"},
		"invalid quality": {input: "a-7", want: "no source at or below 144p"},
	} {
		t.Run(name, func(t *testing.T) {
			f.svc.Opts = Options{Yes: true, FileName: tc.fileName, Force: true}
			if name == "invalid quality" {
				f.world.details["a-7"].Sources[0].Height = 720
				defer func() { f.world.details["a-7"].Sources[0].Height = 0 }()
				f.svc.Opts.MaxHeight = 144
			}
			f.engine.err = tc.engineErr
			defer func() { f.engine.err = nil }()

			err := f.svc.RunGet(context.Background(), tc.input)

			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if stopped, ok := find[DownloadStopped](f.rec); !ok || stopped.Err == nil {
		t.Fatalf("DownloadStopped = %+v", stopped)
	}
}

func TestRunGetReportsMissingEngine(t *testing.T) {
	f := newFixture(t)
	f.addVideo("a-1", "alpha", "a-1")
	f.world.details["a-1"].Sources[0].Kind = domain.SourceProgressive

	if err := f.svc.RunGet(context.Background(), "a-1"); err == nil || !strings.Contains(err.Error(), "no engine") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunInspect(t *testing.T) {
	f := newFixture(t)
	f.addVideo("https://beta.test/v/1", "beta", "b-1")

	info, err := f.svc.RunInspect(context.Background(), "https://beta.test/v/1")
	if err != nil || info.Code != "b-1" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	if _, err := f.svc.RunInspect(context.Background(), "nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestDiscoveryQueriesSitesConcurrentlyInRegistrationOrder(t *testing.T) {
	f := newFixture(t)
	f.world.listDelay = 150 * time.Millisecond
	f.world.items["alpha"] = []domain.Item{{Code: "a-1"}, {Code: "a-2"}, {Code: "a-3"}}
	f.world.items["beta"] = []domain.Item{{Code: "b-1", Site: "beta"}}

	start := time.Now()
	result, err := f.svc.Latest(context.Background(), nil, 1, 2)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, item := range result.Items {
		codes = append(codes, item.Site+"/"+item.Code)
	}
	if strings.Join(codes, ",") != "alpha/a-1,alpha/a-2,beta/b-1" {
		t.Fatalf("items = %v", codes)
	}
	if elapsed >= 280*time.Millisecond {
		t.Fatalf("discovery took %s; sites were not queried concurrently", elapsed)
	}
	if done, ok := find[DiscoveryDone](f.rec); !ok || done.Err != nil {
		t.Fatalf("DiscoveryDone = %+v", done)
	}
}

func TestDiscoveryReportsFailuresAndCancellation(t *testing.T) {
	f := newFixture(t)
	f.world.items["alpha"] = []domain.Item{{Code: "a-1"}}
	f.world.listErr["beta"] = errors.New("blocked")

	result, err := f.svc.Search(context.Background(), nil, "kw", 1, 0)
	var discoveryErr *DiscoveryError
	if !errors.As(err, &discoveryErr) || len(result.Items) != 1 || result.Failures[0].Site != "beta" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !strings.Contains(err.Error(), "1 site(s)") {
		t.Fatal(err)
	}

	if _, err := f.svc.List(context.Background(), "gamma", "latest", 1, 0); !errors.As(err, &discoveryErr) {
		t.Fatalf("unknown site err = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.svc.Latest(ctx, nil, 1, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled err = %v", err)
	}
}

func TestRunItems(t *testing.T) {
	items := []domain.Item{{Site: "alpha", Code: "a-1", URL: "a-1"}, {Site: "alpha", Code: "a-2", URL: "a-2"}}
	setup := func(t *testing.T, opts Options, p *fakePrompter) fixture {
		f := newFixture(t)
		f.addVideo("a-1", "alpha", "a-1")
		f.addVideo("a-2", "alpha", "a-2")
		f.svc.Opts = opts
		if p != nil {
			f.svc.Prompter = p
		}
		return f
	}

	t.Run("picked and confirmed", func(t *testing.T) {
		p := &fakePrompter{pick: items[:1], confirm: true}
		f := setup(t, Options{}, p)
		if err := f.svc.RunItems(context.Background(), items); err != nil {
			t.Fatal(err)
		}
		if len(f.engine.requests()) != 1 || p.confirmed != 1 {
			t.Fatalf("downloads=%d confirmed=%d", len(f.engine.requests()), p.confirmed)
		}
		if plan, _ := find[PlanReady](f.rec); len(plan.Items) != 1 {
			t.Fatalf("plan = %+v", plan)
		}
		if _, ok := find[RunStarted](f.rec); ok {
			t.Fatal("batch items must not repeat the run banner")
		}
	})
	t.Run("picker cancelled", func(t *testing.T) {
		f := setup(t, Options{}, &fakePrompter{pickErr: ErrCancelled})
		if err := f.svc.RunItems(context.Background(), items); err != nil {
			t.Fatal(err)
		}
		if _, ok := find[Cancelled](f.rec); !ok || len(f.engine.requests()) != 0 {
			t.Fatal("expected cancellation without downloads")
		}
	})
	t.Run("nothing picked", func(t *testing.T) {
		f := setup(t, Options{}, &fakePrompter{pick: []domain.Item{}})
		if err := f.svc.RunItems(context.Background(), items); err != nil || len(f.engine.requests()) != 0 {
			t.Fatalf("err=%v downloads=%d", err, len(f.engine.requests()))
		}
		if _, ok := find[Cancelled](f.rec); !ok {
			t.Fatal("expected Cancelled event")
		}
	})
	t.Run("confirm declined", func(t *testing.T) {
		f := setup(t, Options{}, &fakePrompter{confirm: false})
		if err := f.svc.RunItems(context.Background(), items); err != nil || len(f.engine.requests()) != 0 {
			t.Fatalf("err=%v downloads=%d", err, len(f.engine.requests()))
		}
	})
	t.Run("no prompter requires --yes", func(t *testing.T) {
		f := setup(t, Options{}, nil)
		if err := f.svc.RunItems(context.Background(), items); err == nil || !strings.Contains(err.Error(), "--yes") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("yes downloads all", func(t *testing.T) {
		f := setup(t, Options{Yes: true}, &fakePrompter{})
		if err := f.svc.RunItems(context.Background(), items); err != nil || len(f.engine.requests()) != 2 {
			t.Fatalf("err=%v downloads=%d", err, len(f.engine.requests()))
		}
	})
	t.Run("dry run", func(t *testing.T) {
		f := setup(t, Options{DryRun: true}, nil)
		if err := f.svc.RunItems(context.Background(), items); err != nil || len(f.engine.requests()) != 0 {
			t.Fatalf("err=%v downloads=%d", err, len(f.engine.requests()))
		}
	})
	t.Run("failure continues and returns PlanError", func(t *testing.T) {
		f := setup(t, Options{Yes: true}, nil)
		broken := append([]domain.Item{{Site: "alpha", Code: "a-9", URL: "a-9"}}, items...)
		err := f.svc.RunItems(context.Background(), broken)
		var planErr *PlanError
		if !errors.As(err, &planErr) || planErr.Failed != 1 || len(f.engine.requests()) != 2 || err.Error() != "1 video(s) failed" {
			t.Fatalf("err=%v downloads=%d", err, len(f.engine.requests()))
		}
		if failed, ok := find[ItemFailed](f.rec); !ok || failed.Code != "a-9" {
			t.Fatalf("ItemFailed = %+v", failed)
		}
	})
	t.Run("code collision without site in template", func(t *testing.T) {
		f := setup(t, Options{Yes: true}, nil)
		f.svc.Config.PathTemplate = "{code}"
		clash := []domain.Item{items[0], {Site: "beta", Code: "a-1", URL: "https://beta.test/a-1"}}
		err := f.svc.RunItems(context.Background(), clash)
		var planErr *PlanError
		if !errors.As(err, &planErr) || len(f.engine.requests()) != 1 {
			t.Fatalf("err=%v downloads=%d", err, len(f.engine.requests()))
		}
	})
	t.Run("cancelled context stops the batch", func(t *testing.T) {
		f := setup(t, Options{Yes: true}, nil)
		ctx, cancel := context.WithCancel(context.Background())
		f.engine.err = context.Canceled
		cancel()
		if err := f.svc.RunItems(ctx, items); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("invalid name", func(t *testing.T) {
		f := setup(t, Options{Yes: true, FileName: "a/b.mp4"}, nil)
		if err := f.svc.RunItems(context.Background(), items); err == nil {
			t.Fatal("expected --name error")
		}
	})
}

func TestPickSource(t *testing.T) {
	src := func(h int, codec string) domain.Source {
		return domain.Source{Height: h, Codec: codec, URL: fmt.Sprint(h, codec)}
	}
	for _, tc := range []struct {
		name    string
		sources []domain.Source
		max     int
		want    string
		wantErr string
	}{
		{"descending order capped", []domain.Source{src(1080, "h264"), src(720, "h264")}, 720, "720h264", ""},
		{"ascending order capped", []domain.Source{src(720, "h264"), src(1080, "h264")}, 720, "720h264", ""},
		{"prefer h264 at equal height", []domain.Source{src(1080, "av1"), src(1080, "h264")}, 0, "1080h264", ""},
		{"unknown height fallback", []domain.Source{src(0, "")}, 720, "0", ""},
		{"known beats unknown", []domain.Source{src(0, ""), src(480, "h264")}, 0, "480h264", ""},
		{"nothing fits", []domain.Source{src(1080, "h264")}, 480, "", "no source at or below 480p"},
		{"empty", nil, 0, "", "no download sources"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pickSource(tc.sources, tc.max)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil || got.URL != tc.want {
				t.Fatalf("got %q err %v, want %q", got.URL, err, tc.want)
			}
		})
	}
}

func TestSitesBuildsBrowserLazilyOnce(t *testing.T) {
	calls, closed := 0, 0
	sites := NewSites(func(context.Context) (site.Fetcher, func(), error) {
		calls++
		return site.NewHTTPFetcher(http.DefaultClient, nil), func() { closed++ }, nil
	})
	d := site.Descriptor{Name: "browser", Fetcher: site.FetcherBrowser, New: func(site.Fetcher) site.Site { return fakeSite{name: "browser"} }}
	for range 2 {
		if _, err := sites.build(d); err != nil {
			t.Fatal(err)
		}
	}
	sites.Close()
	sites.Close()
	if calls != 1 || closed != 1 {
		t.Fatalf("browser starts=%d closes=%d", calls, closed)
	}

	failing := NewSites(func(context.Context) (site.Fetcher, func(), error) { return nil, nil, errors.New("no chrome") })
	if _, err := failing.build(d); err == nil {
		t.Fatal("expected browser error")
	}
	if _, err := NewSites(nil).build(d); err == nil {
		t.Fatal("expected missing browser error")
	}
	if _, err := NewSites(nil).ByName("gamma"); err == nil {
		t.Fatal("expected unknown site error")
	}
}
