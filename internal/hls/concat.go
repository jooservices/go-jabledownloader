package hls

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ConcatSegments muxes the downloaded segments into an mp4 at outPath via
// ffmpeg.
func ConcatSegments(ctx context.Context, segDir string, totalSegments int, codec, outPath string) (*VideoFile, error) {
	listPath := filepath.Join(filepath.Dir(outPath), ".concat.txt")
	listFile, err := os.Create(listPath)
	if err != nil {
		return nil, fmt.Errorf("create concat list: %w", err)
	}

	for i := 0; i < totalSegments; i++ {
		segPath := filepath.Join(segDir, fmt.Sprintf("seg_%06d.ts", i))
		if _, err := os.Stat(segPath); err != nil {
			listFile.Close()
			return nil, fmt.Errorf("segment %d missing: %w", i, err)
		}
		fmt.Fprintf(listFile, "file '%s'\n", segPath)
	}
	listFile.Close()
	defer os.Remove(listPath)

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg is required to produce a valid mp4 file")
	}

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-f", "concat",
		"-safe", "0",
		"-i", listPath,
		"-c", "copy",
		"-movflags", "faststart",
		"-y",
		outPath,
	)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg concat failed: %w", err)
	}

	fi, err := os.Stat(outPath)
	if err != nil {
		return nil, fmt.Errorf("stat mp4: %w", err)
	}
	return &VideoFile{Path: outPath, Size: fi.Size(), Codec: codec}, nil
}
