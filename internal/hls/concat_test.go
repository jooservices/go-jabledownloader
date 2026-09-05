package hls

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestConcatSegments(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}

	dir := t.TempDir()
	segDir := filepath.Join(dir, "segs")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		out := filepath.Join(segDir, fmt.Sprintf("seg_%06d.ts", i))
		cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "color=c=black:s=160x120:d=1",
			"-c:v", "libx264", "-pix_fmt", "yuv420p", "-bsf:v", "h264_mp4toannexb",
			"-f", "mpegts", "-y", out)
		if outBytes, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generate segment %d: %v\n%s", i, err, outBytes)
		}
	}

	mp4 := filepath.Join(dir, "out.mp4")
	vf, err := ConcatSegments(context.Background(), segDir, 2, "h264", mp4)
	if err != nil {
		t.Fatalf("ConcatSegments: %v", err)
	}
	if vf.Path != mp4 {
		t.Fatalf("path = %q, want %q", vf.Path, mp4)
	}
	if vf.Codec != "h264" {
		t.Fatalf("codec = %q", vf.Codec)
	}
	if vf.Size <= 0 {
		t.Fatal("expected non-zero size")
	}
	if _, err := os.Stat(mp4); err != nil {
		t.Fatalf("mp4 missing: %v", err)
	}
}

func TestConcatSegmentsMissingSegment(t *testing.T) {
	dir := t.TempDir()
	segDir := filepath.Join(dir, "segs")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ConcatSegments(context.Background(), segDir, 1, "h264", filepath.Join(dir, "out.mp4"))
	if err == nil {
		t.Fatal("expected error for missing segment")
	}
}
