package site_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/site"

	// Register the built-in sites so DetectName/New resolve them.
	_ "github.com/jooservices/go-jabledownloader/internal/site/eporner"
	_ "github.com/jooservices/go-jabledownloader/internal/site/jable"
)

func TestHostMatches(t *testing.T) {
	cases := []struct {
		host, pattern string
		want          bool
	}{
		{"www.eporner.com", "eporner.com", true},
		{"eporner.com", "eporner.com", true},
		{"en.jable.tv", "jable.tv", true},
		{"jable.tv", "jable.tv", true},
		{"example.com", "eporner.com", false},
		{"eporner.com.evil.test", "eporner.com", false},
	}
	for _, tc := range cases {
		if got := site.HostMatches(tc.host, tc.pattern); got != tc.want {
			t.Errorf("HostMatches(%q, %q) = %v, want %v", tc.host, tc.pattern, got, tc.want)
		}
	}
}

func TestDetectName(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"jur-827", "jable", false},
		{"https://en.jable.tv/videos/jur-827/", "jable", false},
		{"https://www.eporner.com/video-1XrYk0gaMpV/daisy-f-x/", "eporner", false},
		{"not a code", "", true},
		{"https://example.com/videos/jur-827/", "", true},
	}
	for _, tc := range cases {
		got, err := site.DetectName(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("DetectName(%q): expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("DetectName(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("DetectName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

type stubFetcher struct{}

func (stubFetcher) FetchHTML(context.Context, string, site.FetchMode) (string, error) {
	return "<html></html>", nil
}

func TestNewUnknownSite(t *testing.T) {
	if _, err := site.New("nope", stubFetcher{}); err == nil {
		t.Fatal("expected error for unknown site")
	}
}

func TestNewKnownSites(t *testing.T) {
	for _, name := range []string{"jable", "eporner"} {
		st, err := site.New(name, stubFetcher{})
		if err != nil {
			t.Fatalf("New(%q): %v", name, err)
		}
		if st.Name() != name {
			t.Fatalf("site name = %q, want %q", st.Name(), name)
		}
	}
}

func TestNewHeaderClientSetsHeaders(t *testing.T) {
	var ua, ref, accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua, ref, accept = r.Header.Get("User-Agent"), r.Header.Get("Referer"), r.Header.Get("Accept")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := site.NewHeaderClient("https://example.test/")
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()

	if ua == "" || ref != "https://example.test/" || accept == "" {
		t.Fatalf("headers ua=%q ref=%q accept=%q", ua, ref, accept)
	}
}

func TestNewHeaderClientRedirectEncodesSpaces(t *testing.T) {
	var seenQuery string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/f.mp4?dload=hello world x.mp4", http.StatusFound)
	}))
	defer src.Close()

	c := site.NewHeaderClient("https://example.test/")
	resp, err := c.Get(src.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()

	if seenQuery != "dload=hello%20world%20x.mp4" {
		t.Fatalf("redirect query = %q, want space-encoded", seenQuery)
	}
}

func TestHTTPFetcher(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			fmt.Fprint(w, "<html>ok</html>")
		case "/err":
			http.Error(w, "nope", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	f := site.NewHTTPFetcher(&http.Client{})
	body, err := f.FetchHTML(context.Background(), srv.URL+"/ok", site.FetchReady)
	if err != nil || body != "<html>ok</html>" {
		t.Fatalf("FetchHTML = %q, %v", body, err)
	}
	if _, err := f.FetchHTML(context.Background(), srv.URL+"/err", site.FetchReady); err == nil {
		t.Fatal("expected error for non-200")
	}
	if _, err := f.FetchHTML(context.Background(), "http://127.0.0.1:1/x", site.FetchReady); err == nil {
		t.Fatal("expected error for unreachable")
	}
}
