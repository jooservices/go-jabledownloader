package scraper

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// fileFetcher serves fixture HTML from testdata/, keyed by URL suffix.
type fileFetcher struct {
	file string
}

func (f *fileFetcher) FetchHTML(_ context.Context, _ string) (string, error) {
	data, err := os.ReadFile(filepath.Join("testdata", f.file))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func TestFetchVideoInfo(t *testing.T) {
	c := NewClient(&fileFetcher{file: "video_page.html"})

	info, err := c.FetchVideoInfo(context.Background(), "https://en.jable.tv/videos/pred-840/")
	if err != nil {
		t.Fatalf("FetchVideoInfo: %v", err)
	}
	if info.Code != "pred-840" {
		t.Fatalf("expected code pred-840, got %q", info.Code)
	}
	if info.Title != "PRED-840 First Sample Title" {
		t.Fatalf("unexpected title: %q", info.Title)
	}
	if info.HLSURL != "https://stream.jable.tv/m3u8/422608.m3u8" {
		t.Fatalf("unexpected hls url: %q", info.HLSURL)
	}
	if info.VideoID != "422608" {
		t.Fatalf("unexpected video id: %q", info.VideoID)
	}
}

func TestFetchVideoInfoCodeFromURLFallback(t *testing.T) {
	c := NewClient(&fileFetcher{file: "video_page_no_id.html"})

	info, err := c.FetchVideoInfo(context.Background(), "https://en.jable.tv/videos/dsod-001/")
	if err != nil {
		t.Fatalf("FetchVideoInfo: %v", err)
	}
	if info.Code != "dsod-001" {
		t.Fatalf("expected code dsod-001, got %q", info.Code)
	}
	if info.VideoID != "" {
		t.Fatalf("expected empty video id, got %q", info.VideoID)
	}
	if info.HLSURL != "https://stream.jable.tv/m3u8/900001.m3u8" {
		t.Fatalf("unexpected hls url: %q", info.HLSURL)
	}
}

func TestBrowseEntries(t *testing.T) {
	c := NewClient(&fileFetcher{file: "browse_page.html"})

	entries, err := c.LatestVideos(context.Background(), 1)
	if err != nil {
		t.Fatalf("LatestVideos: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 deduped entries, got %d", len(entries))
	}
	if entries[0].Code != "jur-001" || entries[0].Title != "JUR-001 First Entry" {
		t.Fatalf("unexpected first entry: %+v", entries[0])
	}
	if entries[0].URL != "https://en.jable.tv/videos/jur-001/" {
		t.Fatalf("unexpected first url: %q", entries[0].URL)
	}
	if entries[1].Duration != "10:00" {
		t.Fatalf("unexpected duration: %q", entries[1].Duration)
	}
}

func TestResolveInput(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"jur-827", "https://en.jable.tv/videos/jur-827/", false},
		{"https://en.jable.tv/videos/jur-827/", "https://en.jable.tv/videos/jur-827/", false},
		{"not a code", "", true},
	}
	for _, tc := range cases {
		got, err := ResolveInput(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ResolveInput(%q): expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ResolveInput(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ResolveInput(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
