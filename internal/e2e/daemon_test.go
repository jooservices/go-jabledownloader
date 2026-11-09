package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/monitor"
	"github.com/jooservices/go-jabledownloader/internal/notify"
	"github.com/jooservices/go-jabledownloader/internal/server"
	"github.com/jooservices/go-jabledownloader/internal/store"
)

// override lets a test change what the fake site lists.
var override struct {
	sync.Mutex
	items []domain.Item
	set   bool
}

func setListing(items []domain.Item) {
	override.Lock()
	override.items, override.set = items, true
	override.Unlock()
}

func resetListing() {
	override.Lock()
	override.items, override.set = nil, false
	override.Unlock()
}

func listingOverride() ([]domain.Item, bool) {
	override.Lock()
	defer override.Unlock()
	return append([]domain.Item(nil), override.items...), override.set
}

// TestDaemonDetectsAndDownloadsNewVideos is the 24/7 story end to end: a
// subscription records a baseline, then new items on the site are queued,
// downloaded by the real engines, and announced on (mock) Telegram.
func TestDaemonDetectsAndDownloadsNewVideos(t *testing.T) {
	f := newFixtures(t)
	f.serve(t)
	t.Cleanup(resetListing)
	setListing(nil)

	var mu sync.Mutex
	var sent []string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Text string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		sent = append(sent, body.Text)
		mu.Unlock()
	}))
	defer tg.Close()

	out := outDir(t)
	svc := service(t, out, &recorder{})
	st, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	mon := &monitor.Monitor{
		Store: st, Discoverer: svc, Downloader: svc,
		Notifier: &notify.Telegram{Token: "T", ChatID: "C", BaseURL: tg.URL},
	}
	core := &server.Core{Queue: mon, Poller: mon, Store: st}
	sub, err := core.Subscribe(server.SubscribeRequest{Site: "e2e", Kind: domain.SubView, Query: "latest"})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := core.PollNow(ctx, sub.ID); err != nil { // baseline of an empty site
		t.Fatal(err)
	}
	setListing([]domain.Item{
		{Site: "e2e", Code: "hls-1", Title: "HLS video", URL: baseURL() + "/watch/hls-1"},
		{Site: "e2e", Code: "mp4-1", Title: "MP4 video", URL: baseURL() + "/watch/mp4-1"},
	})
	if err := core.PollNow(ctx, sub.ID); err != nil {
		t.Fatal(err)
	}
	mon.DrainQueue(ctx)

	for _, job := range core.Jobs() {
		if job.State != domain.JobDone {
			t.Fatalf("job %s (%s) = %s: %s", job.ID, job.Input, job.State, job.Error)
		}
	}
	if len(core.Jobs()) != 2 {
		t.Fatalf("expected two jobs, got %+v", core.Jobs())
	}
	probe(t, filepath.Join(out, "e2e", "hls-1", "hls-1-h264.mp4"))
	probe(t, filepath.Join(out, "e2e", "mp4-1", "mp4-1-h264.mp4"))

	// Nothing new: a further poll queues no more work.
	if err := core.PollNow(ctx, sub.ID); err != nil || len(core.Jobs()) != 2 {
		t.Fatalf("repeat poll: %v jobs=%d", err, len(core.Jobs()))
	}

	mu.Lock()
	defer mu.Unlock()
	all := strings.Join(sent, "\n")
	for _, want := range []string{"New on e2e: HLS video", "New on e2e: MP4 video", "Downloaded: HLS video", "Downloaded: MP4 video"} {
		if !strings.Contains(all, want) {
			t.Errorf("Telegram never received %q; got:\n%s", want, all)
		}
	}
}
