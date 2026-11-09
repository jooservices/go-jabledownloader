package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/domain"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return st, path
}

func TestSubscriptionsCRUDAndPersistence(t *testing.T) {
	st, path := open(t)
	a, err := st.AddSubscription(domain.Subscription{Site: "jable", Kind: domain.SubSearch, Query: "ABC", Enabled: true})
	if err != nil || a.ID == "" || a.CreatedAt.IsZero() {
		t.Fatalf("add: %+v %v", a, err)
	}
	b, _ := st.AddSubscription(domain.Subscription{Kind: domain.SubView, Query: "latest"})
	if a.ID == b.ID {
		t.Fatal("ids must be unique")
	}
	if _, err := st.UpdateSubscription(a.ID, func(s *domain.Subscription) { s.Enabled = false; s.LastError = "boom" }); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveSubscription(b.ID); err != nil {
		t.Fatal(err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	subs := again.Subscriptions()
	if len(subs) != 1 || subs[0].Enabled || subs[0].LastError != "boom" {
		t.Fatalf("not persisted: %+v", subs)
	}
	c, _ := again.AddSubscription(domain.Subscription{})
	if c.ID == a.ID || c.ID == b.ID {
		t.Fatalf("id reused: %s", c.ID)
	}
	if _, err := again.UpdateSubscription("nope", func(*domain.Subscription) {}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update unknown: %v", err)
	}
	if err := again.RemoveSubscription("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove unknown: %v", err)
	}
}

func TestSeenLedger(t *testing.T) {
	st, path := open(t)
	items := []domain.Item{{Site: "jable", Code: "A-1"}, {Site: "jable", Code: "A-2"}, {Site: "jable", Code: "A-1"}}
	n, err := st.MarkSeen(items...)
	if err != nil || n != 2 {
		t.Fatalf("new = %d, %v", n, err)
	}
	if n, _ := st.MarkSeen(items...); n != 0 {
		t.Fatal("re-marking must add nothing")
	}
	if !st.IsSeen("jable", "A-1") || st.IsSeen("eporner", "A-1") {
		t.Fatal("seen is keyed by site and code")
	}
	again, _ := Open(path)
	if !again.IsSeen("jable", "A-2") {
		t.Fatal("ledger not persisted")
	}
}

func TestJobsLifecycle(t *testing.T) {
	st, path := open(t)
	j, added, err := st.AddJob(domain.Job{Input: "https://x/1", Origin: "api"})
	if err != nil || !added || j.State != domain.JobQueued || j.ID == "" {
		t.Fatalf("add: %+v %v %v", j, added, err)
	}
	dup, added, _ := st.AddJob(domain.Job{Input: "https://x/1"})
	if added || dup.ID != j.ID {
		t.Fatal("same input must not be queued twice")
	}
	if _, err := st.UpdateJob(j.ID, func(j *domain.Job) { j.State = domain.JobFailed }); err != nil {
		t.Fatal(err)
	}
	if _, added, _ := st.AddJob(domain.Job{Input: "https://x/1"}); !added {
		t.Fatal("a failed job may be requeued")
	}
	if got, err := st.Job(j.ID); err != nil || got.State != domain.JobFailed {
		t.Fatalf("job: %+v %v", got, err)
	}
	if _, err := st.Job("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown job")
	}
	if _, err := st.UpdateJob("nope", func(*domain.Job) {}); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown job update")
	}
	if len(st.Jobs()) != 2 {
		t.Fatal("jobs")
	}

	running, _, _ := st.AddJob(domain.Job{Input: "https://x/2"})
	_, _ = st.UpdateJob(running.ID, func(j *domain.Job) { j.State = domain.JobRunning })
	again, _ := Open(path)
	if n, err := again.RecoverInterrupted(); err != nil || n != 1 {
		t.Fatalf("recover = %d, %v", n, err)
	}
	if got, _ := again.Job(running.ID); got.State != domain.JobQueued {
		t.Fatal("running job must be requeued")
	}
	if n, _ := again.RecoverInterrupted(); n != 0 {
		t.Fatal("nothing left to recover")
	}
}

func TestOpenErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte("{not json"), 0o644)
	if _, err := Open(bad); err == nil {
		t.Fatal("corrupt file must fail")
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("directory must fail")
	}
	legacy := filepath.Join(dir, "null.json")
	_ = os.WriteFile(legacy, []byte(`{"version":1,"seen":null}`), 0o644)
	st, err := Open(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.MarkSeen(domain.Item{Site: "s", Code: "c"}); err != nil {
		t.Fatalf("nil seen map must be usable: %v", err)
	}
}

func TestSaveFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	_ = os.WriteFile(blocker, nil, 0o644)
	st, _ := Open(filepath.Join(blocker, "state.json")) // parent is a file
	if _, err := st.AddSubscription(domain.Subscription{}); err == nil {
		t.Fatal("expected save error")
	}
}
