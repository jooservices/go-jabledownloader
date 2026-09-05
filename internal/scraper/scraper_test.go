package scraper

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// fileFetcher serves fixture HTML from testdata/.
type fileFetcher struct {
	file string
}

func (f *fileFetcher) FetchHTML(_ context.Context, _ string, _ FetchMode) (string, error) {
	data, err := os.ReadFile(filepath.Join("testdata", f.file))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// recordingFetcher serves fixture HTML and records the last requested URL.
type recordingFetcher struct {
	file string
	url  string
}

func (f *recordingFetcher) FetchHTML(_ context.Context, url string, _ FetchMode) (string, error) {
	f.url = url
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

func TestHotVideos(t *testing.T) {
	fetcher := &recordingFetcher{file: "browse_page.html"}
	c := NewClient(fetcher)
	entries, err := c.HotVideos(context.Background(), 1)
	if err != nil {
		t.Fatalf("HotVideos: %v", err)
	}
	wantURL := BaseURL + "/hot/?page=1"
	if fetcher.url != wantURL {
		t.Fatalf("requested URL=%q want %q", fetcher.url, wantURL)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
}

func TestBrowseFetcherError(t *testing.T) {
	c := NewClient(errFetcher{})
	if _, err := c.LatestVideos(context.Background(), 1); err == nil {
		t.Fatal("expected error")
	}
	if _, err := c.SearchVideos(context.Background(), "q", 1); err == nil {
		t.Fatal("expected error")
	}
}

func TestSearchVideos(t *testing.T) {
	fetcher := &recordingFetcher{file: "browse_page.html"}
	c := NewClient(fetcher)
	entries, err := c.SearchVideos(context.Background(), "jur", 1)
	if err != nil {
		t.Fatalf("SearchVideos: %v", err)
	}
	wantURL := BaseURL + "/search/jur/?page=1"
	if fetcher.url != wantURL {
		t.Fatalf("requested URL=%q want %q", fetcher.url, wantURL)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
}

func TestSearchVideosPathEscapesQuery(t *testing.T) {
	fetcher := &recordingFetcher{file: "browse_page.html"}
	c := NewClient(fetcher)
	if _, err := c.SearchVideos(context.Background(), "cute girl", 2); err != nil {
		t.Fatalf("SearchVideos: %v", err)
	}
	wantURL := BaseURL + "/search/cute%20girl/?page=2"
	if fetcher.url != wantURL {
		t.Fatalf("requested URL=%q want %q", fetcher.url, wantURL)
	}
}

func TestFetchVideoInfoFetcherError(t *testing.T) {
	c := NewClient(errFetcher{})
	_, err := c.FetchVideoInfo(context.Background(), "https://en.jable.tv/videos/x/")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFetchVideoInfoMissingHLS(t *testing.T) {
	c := NewClient(&fileFetcher{file: "browse_page.html"})
	_, err := c.FetchVideoInfo(context.Background(), "https://en.jable.tv/videos/jur-001/")
	if err == nil {
		t.Fatal("expected missing HLS error")
	}
}

type errFetcher struct{}

func (errFetcher) FetchHTML(context.Context, string, FetchMode) (string, error) {
	return "", fmt.Errorf("boom")
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

	if got := CodeFromURL("https://en.jable.tv/videos/jur-827/"); got != "jur-827" {
		t.Fatalf("CodeFromURL = %q", got)
	}
	if got := CodeFromURL("https://example.com/videos/pred-840/"); got != "" {
		t.Fatalf("CodeFromURL foreign host want empty, got %q", got)
	}
	if got := CodeFromURL("https://en.jable.tv/videos/not-a-code/"); got != "" {
		t.Fatalf("CodeFromURL bad code want empty, got %q", got)
	}
}
