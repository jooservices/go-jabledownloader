package eporner

// Fixtures in testdata/ are real pages fetched by fixtures_refresh_test.go;
// manifest.json records what was fetched. Negative cases are derived from
// those real pages in code, never hand-written.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/site"
)

type manifest struct {
	ListCount   int    `json:"list_count"`
	VideoURL    string `json:"video_url"`
	VideoCode   string `json:"video_code"`
	SourceCount int    `json:"source_count"`
}

func loadManifest(t *testing.T) manifest {
	t.Helper()
	var m manifest
	if err := json.Unmarshal([]byte(readFixture(t, "manifest.json")), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type pageFetcher struct {
	html string
	err  error
	url  string
}

func (f *pageFetcher) FetchHTML(_ context.Context, url string, _ site.FetchMode) (string, error) {
	f.url = url
	return f.html, f.err
}

func TestDetailParsesRealVideoPage(t *testing.T) {
	m := loadManifest(t)
	fetcher := &pageFetcher{html: readFixture(t, "video_page.html")}

	info, err := NewClient(fetcher).Detail(context.Background(), m.VideoURL)

	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if info.Site != "eporner" || info.Code != m.VideoCode || info.Title == "" || strings.Contains(info.Title, " - EPORNER") {
		t.Fatalf("detail = %+v", info)
	}
	if len(info.Sources) != m.SourceCount || fetcher.url != m.VideoURL {
		t.Fatalf("sources = %d, fetched %q", len(info.Sources), fetcher.url)
	}
	for i, src := range info.Sources {
		if src.Kind != domain.SourceProgressive || !strings.HasPrefix(src.URL, BaseURL+"/dload/") || src.Height <= 0 ||
			(src.Codec != "h264" && src.Codec != "av1") || src.Headers.Get("Referer") != BaseURL+"/" {
			t.Fatalf("source %d = %+v", i, src)
		}
		if i > 0 {
			prev := info.Sources[i-1]
			if prev.Height > src.Height || (prev.Height == src.Height && prev.Codec == "av1" && src.Codec == "h264") {
				t.Fatalf("sources not ordered by height then h264 first: %+v", info.Sources)
			}
		}
	}
}

func TestDetailSourcesHaveIndependentHeaders(t *testing.T) {
	m := loadManifest(t)
	info, err := NewClient(&pageFetcher{html: readFixture(t, "video_page.html")}).Detail(context.Background(), m.VideoURL)
	if err != nil {
		t.Fatal(err)
	}
	info.Sources[0].Headers.Set("Referer", "changed")
	if info.Sources[1].Headers.Get("Referer") != BaseURL+"/" || referer.Get("Referer") != BaseURL+"/" {
		t.Fatal("sources share a mutable header map")
	}
}

var dloadLinkRe = regexp.MustCompile(`href="/dload/[^"]*"`)

func TestDetailWithoutSourcesFails(t *testing.T) {
	m := loadManifest(t)
	stripped := dloadLinkRe.ReplaceAllString(readFixture(t, "video_page.html"), `href="#"`)

	if _, err := NewClient(&pageFetcher{html: stripped}).Detail(context.Background(), m.VideoURL); err == nil ||
		!strings.Contains(err.Error(), "no downloadable sources") {
		t.Fatalf("err = %v", err)
	}
}

func TestDetailRejectsInvalidInput(t *testing.T) {
	c := NewClient(&pageFetcher{html: readFixture(t, "video_page.html")})
	for _, bad := range []string{
		"SJvCzwESrxX",
		"https://en.jable.tv/videos/jur-827/",
		"https://www.eporner.com/search/q/",
		"http://www.eporner.com/video-ABC123/x/",
		"https://eporner.com.evil.test/video-ABC123/x/",
	} {
		if _, err := c.Detail(context.Background(), bad); err == nil {
			t.Errorf("Detail(%q): expected error", bad)
		}
	}
	if _, err := NewClient(&pageFetcher{err: errors.New("boom")}).Detail(context.Background(), loadManifest(t).VideoURL); err == nil {
		t.Fatal("expected fetch error")
	}
}

func TestListParsesRealListing(t *testing.T) {
	m := loadManifest(t)
	fetcher := &pageFetcher{html: readFixture(t, "browse_page.html")}

	items, err := NewClient(fetcher).List(context.Background(), site.ListOptions{})

	if err != nil {
		t.Fatal(err)
	}
	if len(items) != m.ListCount || items[0].Code != m.VideoCode || items[0].URL != m.VideoURL {
		t.Fatalf("items = %d, first = %+v", len(items), items[0])
	}
	seen := map[string]bool{}
	for _, item := range items {
		if item.Site != "eporner" || !domain.ValidCode(item.Code) || item.Title == "" || !strings.HasPrefix(item.URL, BaseURL+"/video-") ||
			!durationRe.MatchString(item.Duration) || seen[item.Code] {
			t.Fatalf("bad item %+v", item)
		}
		seen[item.Code] = true
	}
	if fetcher.url != BaseURL+"/" {
		t.Fatalf("fetched %q", fetcher.url)
	}
}

func TestListAndSearchURLs(t *testing.T) {
	html := readFixture(t, "browse_page.html")
	for _, tc := range []struct {
		name string
		call func(*Client) error
		want string
	}{
		{"all", func(c *Client) error {
			_, err := c.List(context.Background(), site.ListOptions{View: "all"})
			return err
		}, BaseURL + "/cat/all/"},
		{"top-rated page 2", func(c *Client) error {
			_, err := c.List(context.Background(), site.ListOptions{View: "top-rated", Page: 2})
			return err
		}, BaseURL + "/top-rated/?page=2"},
		{"search", func(c *Client) error { _, err := c.Search(context.Background(), "big tits", 3); return err },
			BaseURL + "/search?page=3&q=big+tits"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fetcher := &pageFetcher{html: html}
			if err := tc.call(NewClient(fetcher)); err != nil || fetcher.url != tc.want {
				t.Fatalf("url=%q err=%v", fetcher.url, err)
			}
		})
	}
}

func TestListAndSearchErrors(t *testing.T) {
	c := NewClient(&pageFetcher{err: errors.New("boom")})
	if _, err := c.List(context.Background(), site.ListOptions{View: "bogus"}); err == nil || !strings.Contains(err.Error(), "valid: latest, all, most-viewed, top-rated") {
		t.Fatalf("view err = %v", err)
	}
	if _, err := c.List(context.Background(), site.ListOptions{}); err == nil {
		t.Fatal("expected fetch error")
	}
	if _, err := c.Search(context.Background(), " ", 1); err == nil {
		t.Fatal("expected blank keyword error")
	}
}
