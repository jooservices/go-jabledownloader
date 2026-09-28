package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/app"
	"github.com/jooservices/go-jabledownloader/internal/domain"
)

type syncBuffer struct {
	mu sync.Mutex
	sb strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.String()
}

func render(opts ReporterOptions, events ...app.Event) string {
	var out syncBuffer
	r := NewReporter(&out, opts)
	for _, ev := range events {
		r.Report(ev)
	}
	return out.String()
}

func TestReporterRendersSingleDownload(t *testing.T) {
	got := render(ReporterOptions{Verbose: true},
		app.RunStarted{},
		app.VideoResolved{Site: "jable", Code: "abc-1", Title: "Title", Input: "abc-1", Dir: "/out/jable/abc-1", Workers: 4},
		app.DownloadStarted{Code: "abc-1", SourceURL: "https://cdn.test/a.m3u8"},
		app.DownloadProgress{Code: "abc-1", Event: domain.Event{Kind: domain.EventPlan, Total: 2}},
		app.DownloadProgress{Code: "abc-1", Event: domain.Event{Kind: domain.EventProgress, Done: 2, Total: 2, Bytes: 2048}},
		app.DownloadStopped{Code: "abc-1"},
		app.VideoDownloaded{Code: "abc-1", Path: "/out/jable/abc-1/abc-1-h264.mp4", Codec: "h264", Size: 2048},
		app.SubtitleStarted{Video: "/out/abc-1-h264.mp4"},
		app.SubtitleDone{SRT: "/out/abc-1-h264.en.srt"},
	)
	for _, want := range []string{
		"Jable Downloader", "Title:   Title", "Site:    jable", "Output:  /out/jable/abc-1", "Workers: 4", "Input:   abc-1",
		"Source:  https://cdn.test/a.m3u8", "Download Complete", "Downloaded: /out/jable/abc-1/abc-1-h264.mp4",
		"Size: 2.0 KiB", "Codec:   h264", "Adding subtitles to abc-1-h264.mp4", "Subtitles added (abc-1-h264.en.srt)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\033[") {
		t.Fatal("colors must be stripped when Color is false")
	}
}

func TestReporterQuietShowsOnlyResults(t *testing.T) {
	got := render(ReporterOptions{Quiet: true},
		app.RunStarted{},
		app.VideoResolved{Title: "Title"},
		app.DownloadStarted{Code: "abc-1"},
		app.DownloadProgress{Event: domain.Event{Kind: domain.EventProgress, Done: 1, Total: 1}},
		app.DownloadStopped{},
		app.VideoSkipped{Path: "/out/old.mp4"},
		app.VideoDownloaded{Path: "/out/new.mp4"},
		app.ItemFailed{Code: "x-1", Err: errors.New("boom")},
		app.DiscoveryStarted{Site: "jable"},
	)
	for _, want := range []string{"Already downloaded (old.mp4)", "Downloaded: /out/new.mp4", "Download x-1: boom"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	for _, hidden := range []string{"Jable Downloader", "Title", "Loading", "Download Complete"} {
		if strings.Contains(got, hidden) {
			t.Errorf("quiet output contains %q", hidden)
		}
	}
}

func TestReporterRendersDiscoveryPlanAndStates(t *testing.T) {
	got := render(ReporterOptions{},
		app.DiscoveryStarted{Site: "jable", Action: "latest"},
		app.DiscoveryDone{Site: "jable", Count: 3},
		app.DiscoveryDone{Site: "eporner", Err: errors.New("blocked")},
		app.PlanReady{Items: []domain.Item{{Title: "One", Duration: "1:00:00"}, {Title: "Two"}}},
		app.DryRun{},
		app.Cancelled{},
		app.SubtitleDone{SRT: "/x.en.srt", Skipped: true},
	)
	for _, want := range []string{
		"Loading latest from jable", "Found 3 result(s) from jable", "eporner failed",
		"One", "One  1:00:00 · ~1.3 GiB", "Total: 2 videos · ~1.3 GiB estimated", "Dry run", "Cancelled", "already present (x.en.srt)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestReporterDrawsProgressWhileDownloading(t *testing.T) {
	var out syncBuffer
	r := NewReporter(&out, ReporterOptions{TTY: true, Color: true})
	r.Report(app.DownloadStarted{Code: "abc-1"})
	r.Report(app.DownloadProgress{Event: domain.Event{Kind: domain.EventProgress, Done: 1, Total: 4, Bytes: 100}})
	time.Sleep(300 * time.Millisecond)
	r.Report(app.DownloadStopped{Err: errors.New("failed")})

	got := out.String()
	if !strings.Contains(got, "Segs") || !strings.Contains(got, "\033[K") {
		t.Fatalf("expected in-place progress block: %q", got)
	}
	if strings.Contains(got, "Download Complete") {
		t.Fatal("failed download must not print the completion summary")
	}
}

// Engine workers report concurrently.
func TestReporterIsSafeForConcurrentProgress(_ *testing.T) {
	r := NewReporter(&syncBuffer{}, ReporterOptions{})
	r.Report(app.DownloadStarted{Code: "abc-1"})
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 50 {
				r.Report(app.DownloadProgress{Event: domain.Event{Kind: domain.EventProgress, Done: int64(j), Total: 50, Bytes: int64(i * j)}})
			}
		}()
	}
	wg.Wait()
	r.Report(app.DownloadStopped{})
}

func TestPrompterConfirm(t *testing.T) {
	var out syncBuffer
	p := NewPrompter(strings.NewReader("\nn\nyes\n"), NewStdWriter(&out, false), true)
	for i, want := range []bool{true, false, true, false} {
		got, err := p.Confirm("Start?")
		if err != nil || got != want {
			t.Fatalf("answer %d = %v, %v; want %v", i, got, err, want)
		}
	}
	if !strings.Contains(out.String(), "Start? [Y/n]") {
		t.Fatalf("prompt = %q", out.String())
	}
}

func TestPrompterPick(t *testing.T) {
	items := []domain.Item{
		{Site: "jable", Code: "abc-1", Title: "One", Duration: "10:00"},
		{Site: "eporner", Code: "abc-1", Title: "Two"},
	}
	var shown []PickerItem
	pickMulti = func(_ string, choices []PickerItem) ([]PickerItem, error) {
		shown = choices
		out := append([]PickerItem(nil), choices...)
		out[1].Selected = true
		return out, nil
	}
	t.Cleanup(func() { pickMulti = PickMulti })
	p := NewPrompter(strings.NewReader(""), NewStdWriter(&syncBuffer{}, false), true)

	got, err := p.Pick(items)

	if err != nil || len(got) != 1 || got[0].Site != "eporner" {
		t.Fatalf("picked %+v err %v (same code on two sites must stay distinct)", got, err)
	}
	if shown[0].Selected || shown[1].Selected {
		t.Fatal("nothing must be preselected")
	}
	if shown[0].Label != "abc-1 · One" || shown[0].Detail != "jable · 10:00 · ~214.6 MiB" || shown[1].Detail != "eporner · duration unknown" {
		t.Fatalf("choices = %+v", shown)
	}

	pickMulti = func(string, []PickerItem) ([]PickerItem, error) { return nil, ErrPickerCancelled }
	if _, err := p.Pick(items); !errors.Is(err, app.ErrCancelled) {
		t.Fatalf("cancel err = %v", err)
	}
	pickMulti = func(string, []PickerItem) ([]PickerItem, error) { return nil, errors.New("tty gone") }
	if _, err := p.Pick(items); err == nil || errors.Is(err, app.ErrCancelled) {
		t.Fatalf("picker err = %v", err)
	}
	if got, _ := NewPrompter(strings.NewReader(""), NewStdWriter(&syncBuffer{}, false), false).Pick(items); len(got) != 2 {
		t.Fatal("non-interactive Pick must return all items")
	}
}

// Regression: colors passed as format arguments leaked into piped output.
func TestStdWriterStripsColorArguments(t *testing.T) {
	var out syncBuffer
	NewStdWriter(&out, false).Printf("%sred%s\n", ColorRed, ColorReset)
	if out.String() != "red\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestColorEnabledHonoursEnvironment(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, tc := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"NO_COLOR": "1"}, false},
		{map[string]string{"TERM": "dumb"}, false},
		{map[string]string{"FORCE_COLOR": "true"}, true},
		{map[string]string{"FORCE_COLOR": "0"}, false},
		{map[string]string{}, false}, // a regular file is not a terminal
	} {
		t.Run(fmt.Sprint(tc.env), func(t *testing.T) {
			for _, key := range []string{"NO_COLOR", "TERM", "FORCE_COLOR"} {
				if value, ok := tc.env[key]; ok {
					t.Setenv(key, value)
				} else {
					t.Setenv(key, "xterm")
					if key != "TERM" {
						os.Unsetenv(key)
					}
				}
			}
			if got := ColorEnabled(file); got != tc.want {
				t.Fatalf("ColorEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPickerLabelWithoutTitle(t *testing.T) {
	if got := pickerLabel(domain.Item{Code: "abc-1"}); got != "abc-1" {
		t.Fatal(got)
	}
}
