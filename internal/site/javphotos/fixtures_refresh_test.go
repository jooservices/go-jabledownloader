//go:build fixtures

package javphotos

// Refreshes testdata from the real site; fixtures are never written by hand.
//
//	go test -tags fixtures -run TestRefreshFixtures ./internal/site/javphotos/

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

const searchKeyword = "yui hatano"

func TestRefreshFixtures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fetcher := site.NewHTTPFetcher(httpx.NewClient(httpx.Options{Timeout: 60 * time.Second}), referer)

	listing := fetch(ctx, t, fetcher, BaseURL+"/")
	items, err := NewClient(staticFetcher(listing)).List(ctx, site.ListOptions{})
	if err != nil || len(items) == 0 {
		t.Fatalf("real listing has no galleries (err %v); selectors may be outdated", err)
	}
	galleryPage := fetch(ctx, t, fetcher, items[0].URL)
	gallery, err := NewClient(staticFetcher(galleryPage)).Gallery(ctx, items[0].URL)
	if err != nil {
		t.Fatalf("parse real gallery: %v", err)
	}
	searchURL := &urlRecorder{}
	_, _ = NewClient(searchURL).Search(ctx, searchKeyword, 1)
	searchPage := fetch(ctx, t, fetcher, searchURL.url)
	found, err := NewClient(staticFetcher(searchPage)).Search(ctx, searchKeyword, 1)
	if err != nil || len(found) == 0 {
		t.Fatalf("real search has no galleries (err %v)", err)
	}

	write(t, "browse_page.html", listing)
	write(t, "gallery_page.html", galleryPage)
	write(t, "search_page.html", searchPage)
	manifest, _ := json.MarshalIndent(map[string]any{
		"fetched_at":     time.Now().UTC().Format(time.RFC3339),
		"list_count":     len(items),
		"gallery_url":    items[0].URL,
		"gallery_code":   items[0].Code,
		"gallery_title":  gallery.Title,
		"photo_count":    len(gallery.Photos),
		"search_keyword": searchKeyword,
		"search_count":   len(found),
	}, "", "  ")
	write(t, "manifest.json", string(manifest)+"\n")
}

// urlRecorder captures the URL the client would request.
type urlRecorder struct{ url string }

func (r *urlRecorder) FetchHTML(_ context.Context, url string, _ site.FetchMode) (string, error) {
	r.url = url
	return "", nil
}

type staticFetcher string

func (s staticFetcher) FetchHTML(context.Context, string, site.FetchMode) (string, error) {
	return string(s), nil
}

func fetch(ctx context.Context, t *testing.T, f site.Fetcher, url string) string {
	t.Helper()
	html, err := f.FetchHTML(ctx, url, site.FetchReady)
	if err != nil {
		t.Fatalf("fetch %s: %v", url, err)
	}
	return html
}

func write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("testdata", name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("wrote testdata/%s (%d bytes)\n", name, len(content))
}
