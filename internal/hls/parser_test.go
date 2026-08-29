package hls

import (
	"strings"
	"testing"
)

func TestParsePlaylist(t *testing.T) {
	content := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:10
#EXTINF:10.0,
seg_000.ts
#EXTINF:10.0,
seg_001.ts
#EXT-X-ENDLIST
`

	pl, err := ParsePlaylist(content, "https://cdn.example.com/video/")
	if err != nil {
		t.Fatalf("ParsePlaylist: %v", err)
	}
	if len(pl.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(pl.Segments))
	}
	if pl.TargetDuration != 10 {
		t.Fatalf("expected target duration 10, got %v", pl.TargetDuration)
	}
	if pl.Encrypted {
		t.Fatal("expected unencrypted playlist")
	}
	want := "https://cdn.example.com/video/seg_000.ts"
	if pl.Segments[0].URL != want {
		t.Fatalf("expected %q, got %q", want, pl.Segments[0].URL)
	}
	if pl.Segments[1].Duration != 10 {
		t.Fatalf("expected duration 10, got %v", pl.Segments[1].Duration)
	}
}

func TestParsePlaylistAbsoluteSegments(t *testing.T) {
	content := `#EXTM3U
#EXTINF:5.0,
https://other.example.com/a.ts
`
	pl, err := ParsePlaylist(content, "https://cdn.example.com/video/")
	if err != nil {
		t.Fatalf("ParsePlaylist: %v", err)
	}
	if pl.Segments[0].URL != "https://other.example.com/a.ts" {
		t.Fatalf("expected absolute URL kept, got %q", pl.Segments[0].URL)
	}
}

func TestParsePlaylistMasterRejected(t *testing.T) {
	content := `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1280000
low.m3u8
`
	_, err := ParsePlaylist(content, "https://cdn.example.com/")
	if err == nil {
		t.Fatal("expected master playlist error")
	}
	if !strings.Contains(err.Error(), "master playlist") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePlaylistEncrypted(t *testing.T) {
	content := `#EXTM3U
#EXT-X-KEY:METHOD=AES-128,URI="https://cdn.example.com/key.bin"
#EXTINF:5.0,
seg_000.ts
`
	pl, err := ParsePlaylist(content, "https://cdn.example.com/video/")
	if err != nil {
		t.Fatalf("ParsePlaylist: %v", err)
	}
	if !pl.Encrypted {
		t.Fatal("expected encrypted playlist")
	}
	if pl.KeyURI != "https://cdn.example.com/key.bin" {
		t.Fatalf("unexpected key URI: %q", pl.KeyURI)
	}
}

func TestParsePlaylistEmpty(t *testing.T) {
	_, err := ParsePlaylist("#EXTM3U\n", "https://cdn.example.com/")
	if err == nil {
		t.Fatal("expected error for empty playlist")
	}
}
