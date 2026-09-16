package eporner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/site"
)

type fileFetcher struct {
	file string
}

func (f *fileFetcher) FetchHTML(_ context.Context, _ string, _ site.FetchMode) (string, error) {
	data, err := os.ReadFile(filepath.Join("testdata", f.file))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func TestFetchInfo(t *testing.T) {
	c := NewClient(&fileFetcher{file: "video_page.html"})

	info, err := c.FetchInfo(context.Background(), "https://www.eporner.com/video-1XrYk0gaMpV/daisy-f-x/")
	if err != nil {
		t.Fatalf("FetchInfo: %v", err)
	}
	if info.Code != "1XrYk0gaMpV" {
		t.Fatalf("code = %q", info.Code)
	}
	if info.Title != "Daisy F💀x - Daisy Fox" {
		t.Fatalf("title = %q", info.Title)
	}
	if len(info.Sources) != 6 {
		t.Fatalf("expected 6 sources, got %d: %+v", len(info.Sources), info.Sources)
	}

	first := info.Sources[0]
	if first.Kind != site.SourceDirect || first.Height != 240 || first.Codec != "h264" {
		t.Fatalf("first source = %+v", first)
	}
	if first.URL != "https://www.eporner.com/dload/1XrYk0gaMpV/240/18152538-240p.mp4" {
		t.Fatalf("first url = %q", first.URL)
	}

	var heights []int
	for _, s := range info.Sources {
		heights = append(heights, s.Height)
	}
	want := []int{240, 360, 480, 720, 720, 1080}
	for i := range want {
		if heights[i] != want[i] {
			t.Fatalf("heights = %v, want %v", heights, want)
		}
	}
	if info.Sources[3].Codec != "h264" || info.Sources[4].Codec != "av1" {
		t.Fatalf("720p codecs = %q, %q", info.Sources[3].Codec, info.Sources[4].Codec)
	}
}

func TestFetchInfoMissingSources(t *testing.T) {
	c := NewClient(&fileFetcher{file: "no_sources.html"})
	if _, err := c.FetchInfo(context.Background(), "https://www.eporner.com/video-ABC123/x/"); err == nil {
		t.Fatal("expected missing sources error")
	}
}

func TestResolveInput(t *testing.T) {
	c := NewClient(&fileFetcher{file: "video_page.html"})
	ok := "https://www.eporner.com/video-1XrYk0gaMpV/daisy-f-x/"
	if got, err := c.ResolveInput(context.Background(), ok); err != nil || got != ok {
		t.Fatalf("ResolveInput(%q) = %q, %v", ok, got, err)
	}
	for _, bad := range []string{"1XrYk0gaMpV", "https://en.jable.tv/videos/jur-827/", "https://www.eporner.com/search/q/"} {
		if _, err := c.ResolveInput(context.Background(), bad); err == nil {
			t.Errorf("ResolveInput(%q): expected error", bad)
		}
	}
}
