package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/config"
	"github.com/jooservices/go-jabledownloader/internal/scraper"
	"github.com/jooservices/go-jabledownloader/internal/ui"
)

func TestFindExistingVideo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "jur-001-h264.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := FindExistingVideo(dir, "jur-001"); got == "" {
		t.Fatal("expected existing video to be found")
	}
	if got := FindExistingVideo(dir, "jur-002"); got != "" {
		t.Fatalf("expected no match, got %q", got)
	}
}

func TestPickVideosNonInteractiveSelectsAll(t *testing.T) {
	videos := []scraper.VideoEntry{
		{Code: "jur-001", Title: "One", Duration: "1:00:00"},
		{Code: "abc-002", Title: "Two"},
	}
	svc := &Service{
		Config: config.Defaults(),
		Out:    ui.NewStdWriter(ioDiscard{}, false),
		Opts:   Options{Yes: true},
	}

	got, err := svc.pickVideos(videos)
	if err != nil {
		t.Fatalf("pickVideos: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected all videos, got %d", len(got))
	}
}

func TestPlanError(t *testing.T) {
	err := &PlanError{Failed: 3}
	if !strings.Contains(err.Error(), "3 video(s) failed") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
