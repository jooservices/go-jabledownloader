package javphotos

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

func TestFetchGallery(t *testing.T) {
	c := NewClient(&fileFetcher{file: "gallery.html"})

	gallery, err := c.FetchGallery(context.Background(), "https://jav.photos/free/caribbeancom-ai-uehara-elite-3xpl")
	if err != nil {
		t.Fatalf("FetchGallery: %v", err)
	}
	if gallery.Code != "caribbeancom-ai-uehara-elite-3xpl" {
		t.Fatalf("code = %q", gallery.Code)
	}
	if gallery.Title != "上原愛 Ai Uehara Caribbeancom Elite 3xpl" {
		t.Fatalf("title = %q", gallery.Title)
	}
	if len(gallery.Photos) != 3 {
		t.Fatalf("photos = %d, want 3 (related pins excluded)", len(gallery.Photos))
	}

	first := gallery.Photos[0]
	if first.ID != "ai-uehara-1" {
		t.Fatalf("id = %q", first.ID)
	}
	if first.ImageURL != "https://jav.photos/pictures/caribbeancom/ai-uehara/052716-172/ai-uehara-1.jpg" {
		t.Fatalf("image_url = %q", first.ImageURL)
	}
	if first.ThumbnailURL != "https://jav.photos/pics/caribbeancom/ai-uehara/052716-172/hd-ai-uehara-1.jpg" {
		t.Fatalf("thumb = %q", first.ThumbnailURL)
	}
}

func TestFetchGalleryEmpty(t *testing.T) {
	c := NewClient(&fileFetcher{file: "empty.html"})
	if _, err := c.FetchGallery(context.Background(), "https://jav.photos/free/empty-gallery"); err == nil {
		t.Fatal("expected error for empty gallery")
	}
}

func TestResolveInput(t *testing.T) {
	c := NewClient(&fileFetcher{file: "gallery.html"})
	ok := "https://jav.photos/free/caribbeancom-ai-uehara-elite-3xpl"
	if got, err := c.ResolveInput(context.Background(), ok); err != nil || got != ok {
		t.Fatalf("ResolveInput(%q) = %q, %v", ok, got, err)
	}
	for _, bad := range []string{
		"caribbeancom-ai-uehara-elite-3xpl",
		"https://www.eporner.com/video-ABC123/slug/",
		"https://jav.photos/free/5",
		"https://jav.photos/",
	} {
		if _, err := c.ResolveInput(context.Background(), bad); err == nil {
			t.Errorf("ResolveInput(%q): expected error", bad)
		}
	}
}

func TestFetchInfoUnsupported(t *testing.T) {
	c := NewClient(&fileFetcher{file: "gallery.html"})
	if _, err := c.FetchInfo(context.Background(), "https://jav.photos/free/x"); err == nil {
		t.Fatal("expected unsupported video error")
	}
}
