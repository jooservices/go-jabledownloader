package monitor

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/notify"
	"github.com/jooservices/go-jabledownloader/internal/store"
)

type fakeDiscoverer struct {
	mu    sync.Mutex
	items []domain.Item
	err   error
	calls int
}

func (f *fakeDiscoverer) Discover(context.Context, domain.Subscription) ([]domain.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.items, f.err
}

func (f *fakeDiscoverer) set(items ...domain.Item) {
	f.mu.Lock()
	f.items = items
	f.mu.Unlock()
}

type fakeDownloader struct {
	mu    sync.Mutex
	got   []string
	fails map[string]int // input -> remaining failures
	block chan struct{}
}

func (f *fakeDownloader) Download(ctx context.Context, input string) error {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, input)
	if f.fails[input] > 0 {
		f.fails[input]--
		return errors.New("boom")
	}
	return nil
}

type recorder struct {
	mu   sync.Mutex
	msgs []notify.Message
	err  error
}

func (r *recorder) Notify(_ context.Context, m notify.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, m)
	return r.err
}

func (r *recorder) titles() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, m := range r.msgs {
		out = append(out, m.Title)
	}
	return out
}

type rig struct {
	m    *Monitor
	disc *fakeDiscoverer
	dl   *fakeDownloader
	rec  *recorder
	st   *store.Store
	now  time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{disc: &fakeDiscoverer{}, dl: &fakeDownloader{fails: map[string]int{}}, rec: &recorder{}, st: st, now: time.Unix(1_700_000_000, 0)}
	r.m = &Monitor{Store: st, Discoverer: r.disc, Downloader: r.dl, Notifier: r.rec, Interval: time.Hour, Now: func() time.Time { return r.now }}
	return r
}

func item(code string) domain.Item {
	return domain.Item{Site: "jable", Code: code, Title: "Title " + code, URL: "https://jable.example/" + code}
}

func (r *rig) subscribe(t *testing.T, sub domain.Subscription) domain.Subscription {
	t.Helper()
	sub.Enabled = true
	got, err := r.st.AddSubscription(sub)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestFirstPollOnlyRecordsABaseline(t *testing.T) {
	r := newRig(t)
	r.disc.set(item("A-1"), item("A-2"))
	r.subscribe(t, domain.Subscription{AutoDownload: true})

	r.m.PollOnce(context.Background())
	r.m.DrainQueue(context.Background())

	if len(r.dl.got) != 0 || len(r.rec.titles()) != 0 {
		t.Fatalf("baseline must not download or notify: %v %v", r.dl.got, r.rec.titles())
	}
	if !r.st.IsSeen("jable", "A-1") {
		t.Fatal("baseline items must be recorded as seen")
	}
}

func TestNewItemsAreQueuedDownloadedAndAnnounced(t *testing.T) {
	r := newRig(t)
	r.disc.set(item("A-1"))
	r.subscribe(t, domain.Subscription{AutoDownload: true})
	r.m.PollOnce(context.Background()) // baseline

	r.now = r.now.Add(2 * time.Hour)
	r.disc.set(item("A-2"), item("A-1"))
	r.m.PollOnce(context.Background())
	r.m.DrainQueue(context.Background())

	if len(r.dl.got) != 1 || r.dl.got[0] != "https://jable.example/A-2" {
		t.Fatalf("downloaded = %v", r.dl.got)
	}
	titles := r.rec.titles()
	if len(titles) != 2 || titles[0] != "New on jable: Title A-2" || titles[1] != "Downloaded: Title A-2" {
		t.Fatalf("notifications = %v", titles)
	}
	// Nothing new on the next poll: no duplicate download.
	r.now = r.now.Add(2 * time.Hour)
	r.m.PollOnce(context.Background())
	r.m.DrainQueue(context.Background())
	if len(r.dl.got) != 1 {
		t.Fatal("seen items must not download again")
	}
}

func TestBackfillDownloadsExistingItemsOnFirstPoll(t *testing.T) {
	r := newRig(t)
	r.disc.set(item("A-1"), item("A-2"), item("A-3"))
	r.subscribe(t, domain.Subscription{AutoDownload: true, Backfill: true, MaxPerPoll: 2})
	r.m.PollOnce(context.Background())
	r.m.DrainQueue(context.Background())
	if len(r.dl.got) != 2 {
		t.Fatalf("MaxPerPoll must cap the batch: %v", r.dl.got)
	}
}

func TestNotifyOnlyWithoutAutoDownload(t *testing.T) {
	r := newRig(t)
	r.disc.set()
	r.subscribe(t, domain.Subscription{})
	r.m.PollOnce(context.Background())
	r.now = r.now.Add(2 * time.Hour)
	r.disc.set(item("A-9"))
	r.m.PollOnce(context.Background())
	r.m.DrainQueue(context.Background())
	if len(r.dl.got) != 0 || len(r.rec.titles()) != 1 {
		t.Fatalf("got downloads %v, notes %v", r.dl.got, r.rec.titles())
	}
}

func TestScheduleHonoursIntervalAndEnabled(t *testing.T) {
	r := newRig(t)
	r.disc.set(item("A-1"))
	on := r.subscribe(t, domain.Subscription{Interval: 10 * time.Minute})
	_, _ = r.st.AddSubscription(domain.Subscription{Enabled: false})

	r.m.PollOnce(context.Background())
	if r.disc.calls != 1 {
		t.Fatalf("calls = %d (disabled subscriptions must be skipped)", r.disc.calls)
	}
	r.now = r.now.Add(5 * time.Minute)
	r.m.PollOnce(context.Background())
	if r.disc.calls != 1 {
		t.Fatal("not due yet")
	}
	r.now = r.now.Add(6 * time.Minute)
	r.m.PollOnce(context.Background())
	if r.disc.calls != 2 {
		t.Fatalf("due after the interval, calls = %d", r.disc.calls)
	}
	if err := r.m.Poll(context.Background(), on.ID); err != nil || r.disc.calls != 3 {
		t.Fatalf("manual poll ignores the schedule: %v calls=%d", err, r.disc.calls)
	}
	if err := r.m.Poll(context.Background(), "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestDiscoveryErrorsAreRecorded(t *testing.T) {
	r := newRig(t)
	r.disc.err = errors.New("site down")
	sub := r.subscribe(t, domain.Subscription{})
	if err := r.m.Poll(context.Background(), sub.ID); err == nil {
		t.Fatal("expected the discovery error")
	}
	got := r.st.Subscriptions()[0]
	if got.LastError != "site down" || got.LastPolled.IsZero() {
		t.Fatalf("subscription = %+v", got)
	}
}

func TestPartialDiscoveryStillProcessesItems(t *testing.T) {
	r := newRig(t)
	r.disc.set()
	sub := r.subscribe(t, domain.Subscription{AutoDownload: true})
	_ = r.m.Poll(context.Background(), sub.ID) // baseline
	r.disc.set(item("B-1"))
	r.disc.err = errors.New("one site failed")
	if err := r.m.Poll(context.Background(), sub.ID); err == nil {
		t.Fatal("partial failure is still reported")
	}
	r.m.DrainQueue(context.Background())
	if len(r.dl.got) != 1 {
		t.Fatalf("items from healthy sites must still download: %v", r.dl.got)
	}
}

func TestDownloadRetriesThenFails(t *testing.T) {
	r := newRig(t)
	r.dl.fails["https://x/1"] = 99
	r.m.MaxAttempts = 2
	job, added, err := r.m.Enqueue("https://x/1", "api")
	if err != nil || !added {
		t.Fatal(err)
	}
	r.m.DrainQueue(context.Background()) // attempt 1 -> requeued
	if got, _ := r.st.Job(job.ID); got.State != domain.JobQueued || got.Attempts != 1 || got.Error != "boom" {
		t.Fatalf("after attempt 1: %+v", got)
	}
	r.m.DrainQueue(context.Background()) // attempt 2 -> failed
	got, _ := r.st.Job(job.ID)
	if got.State != domain.JobFailed || got.Attempts != 2 || got.FinishedAt.IsZero() {
		t.Fatalf("after attempt 2: %+v", got)
	}
	titles := r.rec.titles()
	if len(titles) != 1 || titles[0] != "Download failed: https://x/1" {
		t.Fatalf("a failure notice is sent once: %v", titles)
	}
}

func TestDownloadSucceedsAfterRetry(t *testing.T) {
	r := newRig(t)
	r.dl.fails["https://x/1"] = 1
	job, _, _ := r.m.Enqueue("https://x/1", "mcp")
	r.m.DrainQueue(context.Background())
	r.m.DrainQueue(context.Background())
	if got, _ := r.st.Job(job.ID); got.State != domain.JobDone || got.Error != "" {
		t.Fatalf("job = %+v", got)
	}
}

func TestEnqueueDeduplicates(t *testing.T) {
	r := newRig(t)
	a, added, _ := r.m.Enqueue("https://x/1", "api")
	b, added2, _ := r.m.Enqueue("https://x/1", "mcp")
	if !added || added2 || a.ID != b.ID {
		t.Fatalf("dedupe: %v %v", added, added2)
	}
}

func TestCancelledDownloadStaysQueued(t *testing.T) {
	r := newRig(t)
	r.dl.block = make(chan struct{})
	job, _, _ := r.m.Enqueue("https://x/1", "api")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.m.DrainQueue(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	got, _ := r.st.Job(job.ID)
	if got.State != domain.JobQueued || got.Attempts != 0 {
		t.Fatalf("shutdown must not burn an attempt: %+v", got)
	}
}

func TestConcurrentClaims(t *testing.T) {
	r := newRig(t)
	r.m.Concurrency = 3
	for _, in := range []string{"a", "b", "c", "d"} {
		_, _, _ = r.m.Enqueue(in, "api")
	}
	r.m.DrainQueue(context.Background())
	if len(r.dl.got) != 4 {
		t.Fatalf("downloaded = %v", r.dl.got)
	}
}

func TestNotifierFailureDoesNotBreakFlow(t *testing.T) {
	r := newRig(t)
	r.rec.err = errors.New("telegram down")
	job, _, _ := r.m.Enqueue("https://x/1", "api")
	r.m.DrainQueue(context.Background())
	if got, _ := r.st.Job(job.ID); got.State != domain.JobDone {
		t.Fatalf("job = %+v", got)
	}
}

func TestRunRecoversPollsAndStops(t *testing.T) {
	r := newRig(t)
	r.m.Tick = 10 * time.Millisecond
	stuck, _, _ := r.st.AddJob(domain.Job{Input: "https://x/stuck"})
	_, _ = r.st.UpdateJob(stuck.ID, func(j *domain.Job) { j.State = domain.JobRunning })
	r.subscribe(t, domain.Subscription{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- r.m.Run(ctx) }()
	deadline := time.After(time.Second)
	for {
		if got, _ := r.st.Job(stuck.ID); got.State == domain.JobDone {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the interrupted job was not resumed")
		case <-time.After(10 * time.Millisecond):
		}
	}
	// A request made while running wakes the loop.
	if _, _, err := r.m.Enqueue("https://x/live", "api"); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
	if r.disc.calls == 0 {
		t.Fatal("Run must poll subscriptions")
	}
}

func TestLabelOf(t *testing.T) {
	if labelOf(domain.Job{Title: "T", Code: "C", Input: "I"}) != "T" ||
		labelOf(domain.Job{Code: "C", Input: "I"}) != "C" ||
		labelOf(domain.Job{Input: "I"}) != "I" {
		t.Fatal("label precedence")
	}
}

func TestDefaultsApplied(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "s.json"))
	m := &Monitor{Store: st}
	m.init()
	if m.Interval != DefaultInterval || m.Tick != DefaultTick || m.MaxAttempts != DefaultMaxAttempts || m.Concurrency != 1 {
		t.Fatalf("defaults: %+v", m)
	}
}
