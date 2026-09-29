//go:build fixtures

package eporner

// Refreshes testdata from the real site; fixtures are never written by hand.
//
//	go test -tags fixtures -run TestRefreshFixtures ./internal/site/eporner/

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/platform/httpx"
	"github.com/jooservices/go-jabledownloader/internal/site"
)

func TestRefreshFixtures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fetcher := site.NewHTTPFetcher(httpx.NewClient(httpx.Options{Timeout: 60 * time.Second}), referer)

	listURL := pageURL(viewPaths["latest"], 1)
	listing, err := fetcher.FetchHTML(ctx, listURL, site.FetchReady)
	if err != nil {
		t.Fatalf("fetch listing: %v", err)
	}
	items, err := NewClient(staticFetcher(listing)).List(ctx, site.ListOptions{})
	if err != nil || len(items) == 0 {
		t.Fatalf("real listing has no items (err %v); selectors may be outdated", err)
	}
	video, err := fetcher.FetchHTML(ctx, items[0].URL, site.FetchReady)
	if err != nil {
		t.Fatalf("fetch video: %v", err)
	}
	detail, err := NewClient(staticFetcher(video)).Detail(ctx, items[0].URL)
	if err != nil {
		t.Fatalf("parse real video page: %v", err)
	}

	write(t, "browse_page.html", listing)
	write(t, "video_page.html", video)
	manifest, _ := json.MarshalIndent(map[string]any{
		"fetched_at":   time.Now().UTC().Format(time.RFC3339),
		"list_url":     listURL,
		"list_count":   len(items),
		"video_url":    items[0].URL,
		"video_code":   items[0].Code,
		"source_count": len(detail.Sources),
	}, "", "  ")
	write(t, "manifest.json", string(manifest)+"\n")
}

type staticFetcher string

func (s staticFetcher) FetchHTML(context.Context, string, site.FetchMode) (string, error) {
	return string(s), nil
}

func write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join("testdata", name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("wrote testdata/%s (%d bytes)\n", name, len(content))
}
