package site_test

import (
	"context"
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
