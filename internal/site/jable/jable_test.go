package jable

// Fixtures in testdata/ are real pages fetched by fixtures_refresh_test.go;
// manifest.json records what was fetched. Negative cases are derived from
// those real pages in code, never hand-written.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/site"
)

type manifest struct {
	ListCount int    `json:"list_count"`
	VideoURL  string `json:"video_url"`
	VideoCode string `json:"video_code"`
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

// pageFetcher serves fixed HTML and records the requested URL and mode.
type pageFetcher struct {
	html string
	err  error
	url  string
	mode site.FetchMode
	ua   string
}

func (f *pageFetcher) FetchHTML(_ context.Context, url string, mode site.FetchMode) (string, error) {
	f.url, f.mode = url, mode
	return f.html, f.err
}

// browserLikeFetcher also exposes a User-Agent, like *Browser.
type browserLikeFetcher struct{ pageFetcher }

func (f *browserLikeFetcher) UserAgent() string { return f.ua }

func TestDetailParsesRealVideoPage(t *testing.T) {
	m := loadManifest(t)
	fetcher := &pageFetcher{html: readFixture(t, "video_page.html")}

	info, err := NewClient(fetcher).Detail(context.Background(), m.VideoURL)

	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if info.Site != "jable" || info.Code != m.VideoCode || info.Title == "" || strings.Contains(info.Title, "Jable.TV") {
		t.Fatalf("detail = %+v", info)
	}
	if info.VideoID == "" || len(info.Sources) != 1 {
		t.Fatalf("detail = %+v", info)
	}
	src := info.Sources[0]
	if src.Kind != domain.SourceHLS || !strings.HasSuffix(src.URL, ".m3u8") || src.Headers.Get("Referer") != BaseURL+"/" {
		t.Fatalf("source = %+v", src)
	}
	if fetcher.url != m.VideoURL || fetcher.mode != site.FetchHLS {
		t.Fatalf("fetched %q mode %v", fetcher.url, fetcher.mode)
	}
}

func TestDetailBareCodeBuildsVideoURL(t *testing.T) {
	m := loadManifest(t)
	fetcher := &pageFetcher{html: readFixture(t, "video_page.html")}

	if _, err := NewClient(fetcher).Detail(context.Background(), m.VideoCode); err != nil {
		t.Fatal(err)
	}
	if fetcher.url != BaseURL+"/videos/"+m.VideoCode+"/" {
		t.Fatalf("requested %q", fetcher.url)
	}
}

func TestDetailReusesBrowserUserAgentForMedia(t *testing.T) {
	m := loadManifest(t)
	fetcher := &browserLikeFetcher{pageFetcher{html: readFixture(t, "video_page.html"), ua: "Mozilla/5.0 Chrome/140.0"}}

	info, err := NewClient(fetcher).Detail(context.Background(), m.VideoCode)

	if err != nil || info.Sources[0].Headers.Get("User-Agent") != "Mozilla/5.0 Chrome/140.0" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
}

var canonicalRe = regexp.MustCompile(`<link rel="canonical" href="[^"]*"`)

// The page is untrusted and its code becomes a directory name: a hostile
// canonical link (derived from the real page) must not override it.
func TestDetailIgnoresUnsafeCanonicalCode(t *testing.T) {
	m := loadManifest(t)
	page := readFixture(t, "video_page.html")
	for name, html := range map[string]string{
		"traversal": canonicalRe.ReplaceAllString(page, `<link rel="canonical" href="https://en.jable.tv/videos/../"`),
		"missing":   canonicalRe.ReplaceAllString(page, ""),
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(page, `rel="canonical"`) {
				t.Skip("real page has no canonical link")
			}
			info, err := NewClient(&pageFetcher{html: html}).Detail(context.Background(), m.VideoCode)
			if err != nil || info.Code != m.VideoCode {
				t.Fatalf("info=%+v err=%v", info, err)
			}
		})
	}
}

func TestDetailErrors(t *testing.T) {
	if _, err := NewClient(&pageFetcher{err: errors.New("boom")}).Detail(context.Background(), "jur-827"); err == nil {
		t.Fatal("expected fetch error")
	}
	if _, err := NewClient(&pageFetcher{html: readFixture(t, "browse_page.html")}).Detail(context.Background(), "jur-827"); err == nil {
		t.Fatal("expected missing HLS error on a listing page")
	}
	if _, err := NewClient(&pageFetcher{}).Detail(context.Background(), "not a code"); err == nil {
		t.Fatal("expected invalid input error")
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
		if item.Site != "jable" || !codeRe.MatchString(item.Code) || item.Title == "" || seen[item.Code] {
			t.Fatalf("bad item %+v", item)
		}
		seen[item.Code] = true
	}
	if fetcher.url != BaseURL+"/latest-updates/?page=1" || fetcher.mode != site.FetchReady {
		t.Fatalf("fetched %q mode %v", fetcher.url, fetcher.mode)
	}
}

func TestListAndSearchURLs(t *testing.T) {
	html := readFixture(t, "browse_page.html")
	for _, tc := range []struct {
		name string
		call func(*Client) error
		want string
	}{
		{"hot", func(c *Client) error {
			_, err := c.List(context.Background(), site.ListOptions{View: "hot", Page: 3})
			return err
		}, BaseURL + "/hot/?page=3"},
		{"search", func(c *Client) error { _, err := c.Search(context.Background(), "cute girl", 2); return err },
			BaseURL + "/search/cute%20girl/?page=2"},
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
	if _, err := c.List(context.Background(), site.ListOptions{View: "../admin"}); err == nil || !strings.Contains(err.Error(), "valid: latest, hot") {
		t.Fatalf("view err = %v", err)
	}
	if _, err := c.List(context.Background(), site.ListOptions{}); err == nil {
		t.Fatal("expected fetch error")
	}
	if _, err := c.Search(context.Background(), " ", 1); err == nil {
		t.Fatal("expected empty keyword error")
	}
}

func TestResolveInput(t *testing.T) {
	cases := []struct {
		in, wantURL, wantCode string
		wantErr               bool
	}{
		{"jur-827", "https://en.jable.tv/videos/jur-827/", "jur-827", false},
		{"https://en.jable.tv/videos/jur-827/", "https://en.jable.tv/videos/jur-827/", "jur-827", false},
		{"not a code", "", "", true},
		{"https://www.eporner.com/video-abc123/x/", "", "", true},
	}
	for _, tc := range cases {
		gotURL, gotCode, err := resolveInput(tc.in)
		if (err != nil) != tc.wantErr || gotURL != tc.wantURL || gotCode != tc.wantCode {
			t.Errorf("resolveInput(%q) = %q, %q, %v", tc.in, gotURL, gotCode, err)
		}
	}
	for in, want := range map[string]string{
		"https://en.jable.tv/videos/jur-827/":     "jur-827",
		"https://example.com/videos/pred-840/":    "",
		"https://en.jable.tv/videos/not-a-code/":  "",
		"https://jable.tv.evil.test/videos/a-1/":  "",
		"https://en.jable.tv/videos/%zz/":         "",
		"https://en.jable.tv/categories/jur-827/": "",
	} {
		if got := CodeFromURL(in); got != want {
			t.Errorf("CodeFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// Both pages are real: the interstitial served to a plain HTTP client and a
// rendered page after Cloudflare cleared.
func TestIsChallenge(t *testing.T) {
	for file, want := range map[string]bool{
		"cf_challenge.html": true,
		"video_page.html":   false,
		"browse_page.html":  false,
	} {
		if got := isChallenge(readFixture(t, file)); got != want {
			t.Errorf("isChallenge(%s) = %v, want %v", file, got, want)
		}
	}
}

func TestLaunchPlan(t *testing.T) {
	for env, want := range map[string]string{"": "[false true]", "1": "[true]", "0": "[false]", " 1 ": "[true]"} {
		if got := fmt.Sprint(launchPlan(env)); got != want {
			t.Errorf("launchPlan(%q) = %s, want %s", env, got, want)
		}
	}
}

func TestAllocatorOptionsAddsOptionalFlags(t *testing.T) {
	base := len(allocatorOptions(false, "", ""))
	if got := len(allocatorOptions(true, "/chrome", "/profile")); got != base+3 {
		t.Fatalf("options = %d, want %d", got, base+3)
	}
}

func TestUserAgentFrom(t *testing.T) {
	got := userAgentFrom("Mozilla/5.0 (Macintosh) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/140.0.7339.80 Safari/537.36")
	if got != "Mozilla/5.0 (Macintosh) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.7339.80 Safari/537.36" {
		t.Fatal(got)
	}
}

func TestPoll(t *testing.T) {
	calls := 0
	err := poll(context.Background(), time.Second, time.Millisecond, func() (bool, error) {
		calls++
		if calls == 1 {
			return false, errors.New("navigating")
		}
		return calls == 3, nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	if err := poll(context.Background(), 20*time.Millisecond, time.Millisecond, func() (bool, error) { return false, nil }); !errors.Is(err, errPollTimeout) {
		t.Fatalf("timeout err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := poll(ctx, time.Second, time.Millisecond, func() (bool, error) { return false, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err = %v", err)
	}
}
