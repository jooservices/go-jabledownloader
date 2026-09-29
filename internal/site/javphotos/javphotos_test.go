package javphotos

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
	ListCount     int    `json:"list_count"`
	GalleryURL    string `json:"gallery_url"`
	GalleryCode   string `json:"gallery_code"`
	GalleryTitle  string `json:"gallery_title"`
	PhotoCount    int    `json:"photo_count"`
	SearchKeyword string `json:"search_keyword"`
	SearchCount   int    `json:"search_count"`
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

func TestGalleryParsesRealPage(t *testing.T) {
	m := loadManifest(t)
	fetcher := &pageFetcher{html: readFixture(t, "gallery_page.html")}

	gallery, err := NewClient(fetcher).Gallery(context.Background(), m.GalleryURL)

	if err != nil {
		t.Fatal(err)
	}
	if gallery.Site != "javphotos" || gallery.Code != m.GalleryCode || gallery.Title != m.GalleryTitle || len(gallery.Photos) != m.PhotoCount {
		t.Fatalf("gallery = %+v (photos %d)", gallery, len(gallery.Photos))
	}
	if strings.Contains(gallery.Title, "Jav Photos Free") || strings.Contains(gallery.Title, "Pics Gallery") {
		t.Fatalf("title wrapper not stripped: %q", gallery.Title)
	}
	ids := map[string]bool{}
	for _, p := range gallery.Photos {
		if !strings.HasPrefix(p.URL, BaseURL+"/pictures/") || !strings.HasPrefix(p.ThumbnailURL, BaseURL+"/") ||
			!domain.ValidCode(p.ID) || ids[p.ID] || p.Headers.Get("Referer") != BaseURL+"/" {
			t.Fatalf("bad photo %+v", p)
		}
		ids[p.ID] = true
	}
	if fetcher.url != m.GalleryURL {
		t.Fatalf("fetched %q", fetcher.url)
	}
}

var photoLinkRe = regexp.MustCompile(`href="/pictures/[^"]*"`)

// A model or listing page (no /pictures/ links) is not a gallery.
func TestGalleryWithoutPhotosFails(t *testing.T) {
	m := loadManifest(t)
	stripped := photoLinkRe.ReplaceAllString(readFixture(t, "gallery_page.html"), `href="#"`)

	if _, err := NewClient(&pageFetcher{html: stripped}).Gallery(context.Background(), m.GalleryURL); err == nil || !strings.Contains(err.Error(), "no photos") {
		t.Fatalf("err = %v", err)
	}
}

func TestGalleryRejectsInvalidURLs(t *testing.T) {
	c := NewClient(&pageFetcher{html: readFixture(t, "gallery_page.html")})
	for _, bad := range []string{
		"caribbeancom-a",
		"ftp://jav.photos/free/a-b",
		"https://evil.test/free/a-b",
		"https://jav.photos/free/12",
		"https://jav.photos/pictures/a/b.jpg",
		"https://jav.photos/free/../etc",
	} {
		if _, err := c.Gallery(context.Background(), bad); err == nil {
			t.Errorf("Gallery(%q): expected error", bad)
		}
	}
	if _, err := NewClient(&pageFetcher{err: errors.New("boom")}).Gallery(context.Background(), loadManifest(t).GalleryURL); err == nil {
		t.Fatal("expected fetch error")
	}
}

func TestListParsesRealListing(t *testing.T) {
	m := loadManifest(t)
	fetcher := &pageFetcher{html: readFixture(t, "browse_page.html")}

	items, err := NewClient(fetcher).List(context.Background(), site.ListOptions{})

	if err != nil || len(items) != m.ListCount || items[0].Code != m.GalleryCode || items[0].URL != m.GalleryURL {
		t.Fatalf("items=%d first=%+v err=%v", len(items), items, err)
	}
	assertGalleryItems(t, items)
	if fetcher.url != BaseURL+"/" {
		t.Fatalf("fetched %q", fetcher.url)
	}
}

func TestSearchParsesRealModelPage(t *testing.T) {
	m := loadManifest(t)
	fetcher := &pageFetcher{html: readFixture(t, "search_page.html")}

	items, err := NewClient(fetcher).Search(context.Background(), m.SearchKeyword, 1)

	if err != nil || len(items) != m.SearchCount {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
	assertGalleryItems(t, items)
}

func assertGalleryItems(t *testing.T, items []domain.Item) {
	t.Helper()
	seen := map[string]bool{}
	for _, item := range items {
		if item.Site != "javphotos" || !domain.ValidCode(item.Code) || digitsRe.MatchString(item.Code) ||
			item.Title == "" || item.URL != BaseURL+"/free/"+item.Code || seen[item.Code] {
			t.Fatalf("bad item %+v", item)
		}
		seen[item.Code] = true
	}
}

func TestListAndSearchURLs(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*Client) error
		want string
	}{
		{"page 3", func(c *Client) error { _, err := c.List(context.Background(), site.ListOptions{Page: 3}); return err }, BaseURL + "/free/3"},
		{"search", func(c *Client) error { _, err := c.Search(context.Background(), " Yui  Hatano! ", 1); return err }, BaseURL + "/free/yui-hatano"},
		{"search page 2", func(c *Client) error { _, err := c.Search(context.Background(), "yui", 2); return err }, BaseURL + "/free/yui/2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fetcher := &pageFetcher{html: readFixture(t, "browse_page.html")}
			if err := tc.call(NewClient(fetcher)); err != nil || fetcher.url != tc.want {
				t.Fatalf("url=%q err=%v", fetcher.url, err)
			}
		})
	}
}

func TestListSearchAndDetailErrors(t *testing.T) {
	c := NewClient(&pageFetcher{err: errors.New("boom")})
	if _, err := c.List(context.Background(), site.ListOptions{View: "hot"}); err == nil || !strings.Contains(err.Error(), "valid: latest") {
		t.Fatalf("view err = %v", err)
	}
	if _, err := c.List(context.Background(), site.ListOptions{}); err == nil {
		t.Fatal("expected fetch error")
	}
	for _, bad := range []string{" ", "!!", "123"} {
		if _, err := c.Search(context.Background(), bad, 1); err == nil {
			t.Errorf("Search(%q): expected error", bad)
		}
	}
	if _, err := c.Detail(context.Background(), "x"); !errors.Is(err, site.ErrNotVideo) {
		t.Fatalf("Detail err = %v", err)
	}
	if c.Name() != "javphotos" {
		t.Fatal(c.Name())
	}
}

func TestHelpers(t *testing.T) {
	if absoluteURL("") != "" || absoluteURL("https://x.test/a") != "https://x.test/a" || absoluteURL("/thumbs/a.jpg") != BaseURL+"/thumbs/a.jpg" {
		t.Fatal("absoluteURL")
	}
}
