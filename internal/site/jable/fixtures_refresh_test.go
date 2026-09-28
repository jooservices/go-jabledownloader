//go:build fixtures

package jable

// Refreshes testdata from the real site; fixtures are never written by hand.
//
//	go test -tags fixtures -run TestRefreshFixtures ./internal/site/jable/
//
// cf_challenge.html is the interstitial Cloudflare serves to a plain HTTP
// client (no browser), fetched the same way.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/jooservices/go-jabledownloader/internal/platform/httpx"
	"github.com/jooservices/go-jabledownloader/internal/site"
)

func TestRefreshFixtures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	browser, err := NewBrowser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()

	listURL := BaseURL + "/latest-updates/?page=1"
	listing, err := browser.FetchHTML(ctx, listURL, site.FetchReady)
	if err != nil {
		t.Fatalf("fetch listing: %v", err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(listing))
	if err != nil {
		t.Fatal(err)
	}
	items := extractVideosFromDoc(doc)
	if len(items) == 0 {
		t.Fatal("real listing has no items; selectors may be outdated")
	}
	video, err := browser.FetchHTML(ctx, items[0].URL, site.FetchHLS)
	if err != nil {
		t.Fatalf("fetch video: %v", err)
	}

	write(t, "browse_page.html", listing)
	write(t, "video_page.html", video)
	write(t, "cf_challenge.html", fetchChallenge(ctx, t, items[0].URL))
	manifest, _ := json.MarshalIndent(map[string]any{
		"fetched_at": time.Now().UTC().Format(time.RFC3339),
		"list_url":   listURL,
		"list_count": len(items),
		"video_url":  items[0].URL,
		"video_code": items[0].Code,
	}, "", "  ")
	write(t, "manifest.json", string(manifest)+"\n")
}

func fetchChallenge(ctx context.Context, t *testing.T, url string) string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := httpx.NewClient(httpx.Options{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("fetch challenge: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !isChallenge(string(body)) {
		t.Fatalf("plain HTTP fetch was not challenged (status %d)", resp.StatusCode)
	}
	return string(body)
}

func write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join("testdata", name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("wrote testdata/%s (%d bytes)\n", name, len(content))
}
