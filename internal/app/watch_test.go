package app

import (
	"context"
	"errors"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/domain"
)

func TestDiscoverBySubscriptionKind(t *testing.T) {
	f := newFixture(t)
	f.world.items["alpha"] = []domain.Item{{Code: "a-1"}, {Code: "a-2"}}
	f.world.items["beta"] = []domain.Item{{Code: "b-1"}}

	// A view subscription pinned to one site.
	items, err := f.svc.Discover(context.Background(), domain.Subscription{Site: "alpha", Kind: domain.SubView, Query: "latest"})
	if err != nil || len(items) != 2 || items[0].Site != "alpha" {
		t.Fatalf("view: %+v %v", items, err)
	}
	// A search subscription with no site asks every site.
	items, err = f.svc.Discover(context.Background(), domain.Subscription{Kind: domain.SubSearch, Query: "anything"})
	if err != nil || len(items) != 3 {
		t.Fatalf("search: %+v %v", items, err)
	}
}

func TestDiscoverReturnsPartialItemsWithTheError(t *testing.T) {
	f := newFixture(t)
	f.world.items["alpha"] = []domain.Item{{Code: "a-1"}}
	f.world.listErr["beta"] = errors.New("beta down")
	items, err := f.svc.Discover(context.Background(), domain.Subscription{Kind: domain.SubSearch, Query: "x"})
	var derr *DiscoveryError
	if !errors.As(err, &derr) || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
}

func TestDiscoverRejectsBadSubscriptions(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Discover(context.Background(), domain.Subscription{Kind: domain.SubSearch, Query: "  "}); err == nil {
		t.Fatal("empty search query")
	}
	if _, err := f.svc.Discover(context.Background(), domain.Subscription{Kind: "bogus"}); err == nil {
		t.Fatal("unknown kind")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.svc.Discover(ctx, domain.Subscription{Kind: domain.SubView, Query: "latest"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context must surface: %v", err)
	}
}

func TestDownloadRunsGet(t *testing.T) {
	f := newFixture(t)
	f.addVideo("a-1", "alpha", "a-1")
	if err := f.svc.Download(context.Background(), "a-1"); err != nil {
		t.Fatal(err)
	}
	if len(f.engine.requests()) != 1 {
		t.Fatal("Download must reach the engine")
	}
}
