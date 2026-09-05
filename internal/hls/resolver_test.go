package hls

import "testing"

func TestResolveMasterPlaylistPicksHighestBandwidth(t *testing.T) {
	content := `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360,CODECS="avc1.4D401E,mp4a.40.2"
low.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2800000,RESOLUTION=1280x720,CODECS="avc1.640028,mp4a.40.2"
high.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1400000,RESOLUTION=960x540,CODECS="avc1.4D401F,mp4a.40.2"
mid.m3u8
`
	v, err := ResolveMasterPlaylist(content, "https://cdn.example.com/master/", 0)
	if err != nil {
		t.Fatalf("ResolveMasterPlaylist: %v", err)
	}
	if v.URL != "https://cdn.example.com/master/high.m3u8" {
		t.Fatalf("expected highest bandwidth variant, got %q", v.URL)
	}
	if v.Bandwidth != 2800000 {
		t.Fatalf("expected bandwidth 2800000, got %d", v.Bandwidth)
	}
	if v.Codec != "h264" {
		t.Fatalf("expected codec h264, got %q", v.Codec)
	}
}

func TestResolveMasterPlaylistCodecMapping(t *testing.T) {
	cases := []struct {
		attr string
		want string
	}{
		{`"avc1.640028,mp4a.40.2"`, "h264"},
		{`"hvc1.1.6.L93.B0,mp4a.40.2"`, "h265"},
		{`"hev1.1.6.L93.B0,mp4a.40.2"`, "h265"},
		{`"av01.0.04M.08,opus"`, "av1"},
		{`"vp09.00.40.08,opus"`, "vp9"},
		{`"mystery-codec,mp4a.40.2"`, ""},
	}
	for _, tc := range cases {
		if got := codecFromAttribute(tc.attr); got != tc.want {
			t.Errorf("codecFromAttribute(%s) = %q, want %q", tc.attr, got, tc.want)
		}
	}
}

func TestResolveMasterPlaylistNoVariants(t *testing.T) {
	_, err := ResolveMasterPlaylist("#EXTM3U\n", "https://cdn.example.com/", 0)
	if err == nil {
		t.Fatal("expected error for empty master playlist")
	}
}

func TestResolveMasterPlaylistMaxHeight(t *testing.T) {
	content := `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360,CODECS="avc1.4D401E,mp4a.40.2"
low.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2800000,RESOLUTION=1280x720,CODECS="avc1.640028,mp4a.40.2"
high.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1400000,RESOLUTION=960x540,CODECS="avc1.4D401F,mp4a.40.2"
mid.m3u8
`
	v, err := ResolveMasterPlaylist(content, "https://cdn.example.com/master/", 540)
	if err != nil {
		t.Fatalf("ResolveMasterPlaylist: %v", err)
	}
	if v.URL != "https://cdn.example.com/master/mid.m3u8" {
		t.Fatalf("expected mid variant for maxHeight=540, got %q", v.URL)
	}

	v, err = ResolveMasterPlaylist(content, "https://cdn.example.com/master/", 240)
	if err != nil {
		t.Fatalf("ResolveMasterPlaylist: %v", err)
	}
	if v.URL != "https://cdn.example.com/master/low.m3u8" {
		t.Fatalf("expected lowest when none fit, got %q", v.URL)
	}
}
