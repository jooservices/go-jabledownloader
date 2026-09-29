//go:build e2e

package jable

// Drives real Chrome against the real pages in testdata/, served locally
// (a CSP blocks their external resources so the run stays offline).
//
//	CHROME_PATH=... go test -tags e2e ./internal/site/jable/

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/site"
)

func servePages(t *testing.T, agents chan<- string) string {
	t.Helper()
	pages := map[string]string{
		"/latest-updates/":   readFixture(t, "browse_page.html"),
		"/videos/fixture/":   readFixture(t, "video_page.html"),
		"/cdn-cgi/challenge": readFixture(t, "cf_challenge.html"),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		select {
		case agents <- r.UserAgent():
		default:
		}
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestBrowserFixtureE2E(t *testing.T) {
	if os.Getenv("CHROME_PATH") == "" {
		t.Setenv("CHROME_PATH", "/usr/bin/chromium")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	browser, err := NewBrowser(ctx)
	if err != nil {
		t.Fatalf("NewBrowser: %v", err)
	}
	t.Cleanup(browser.Close)
	browser.pageTimeout = 3 * time.Second
	agents := make(chan string, 1)
	base := servePages(t, agents)
	m := loadManifest(t)

	listing, err := browser.FetchHTML(ctx, base+"/latest-updates/?page=1", site.FetchReady)
	if err != nil {
		t.Fatalf("FetchHTML listing: %v", err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(listing))
	if err != nil {
		t.Fatal(err)
	}
	if items := extractVideosFromDoc(doc); len(items) != m.ListCount || items[0].Code != m.VideoCode {
		t.Fatalf("rendered listing items = %d", len(items))
	}
	if ua := <-agents; ua != browser.UserAgent() || strings.Contains(ua, "Headless") {
		t.Fatalf("page UA %q, browser UA %q", ua, browser.UserAgent())
	}

	info, err := NewClient(browser).fetchDetail(ctx, base+"/videos/fixture/", m.VideoCode)
	if err != nil {
		t.Fatalf("fetchDetail: %v", err)
	}
	if info.Code != m.VideoCode || len(info.Sources) != 1 || info.Sources[0].Kind != domain.SourceHLS ||
		info.Sources[0].Headers.Get("User-Agent") != browser.UserAgent() {
		t.Fatalf("detail = %+v", info)
	}

	if _, err := browser.FetchHTML(ctx, base+"/cdn-cgi/challenge", site.FetchReady); !errors.Is(err, ErrCloudflareChallenge) {
		t.Fatalf("challenge page err = %v", err)
	}
}
