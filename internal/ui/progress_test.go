package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/hls"
)

func TestProgressSegmentsFlow(t *testing.T) {
	p := NewProgress(10)
	if !p.SegmentsUsed() {
		t.Fatal("expected SegmentsUsed true when total > 0")
	}
	if p.ResumeMessage() != "" {
		t.Fatal("expected empty resume")
	}

	p.Update(hls.Event{Kind: hls.EventResume, Message: "resuming 2 of 10"})
	if p.ResumeMessage() != "resuming 2 of 10" {
		t.Fatalf("resume = %q", p.ResumeMessage())
	}

	p.Update(hls.Event{Kind: hls.EventSegments, Done: 3, Total: 10, Bytes: 1024, Failed: 1})
	p.Update(hls.Event{Kind: hls.EventRetry, Message: "rate-limited"})
	p.Update(hls.Event{Kind: hls.EventRetry, Message: "rate-limited"}) // duplicate ignored
	p.Update(hls.Event{Kind: hls.EventRetry, Message: "server error"})

	// Age the start so speed/eta branch can fire.
	p.mu.Lock()
	p.start = time.Now().Add(-time.Second)
	p.mu.Unlock()

	line := p.Render()
	if !strings.Contains(line, "%") || !strings.Contains(line, "3") {
		t.Fatalf("unexpected render: %q", line)
	}
	if !strings.Contains(line, "failed") {
		t.Fatalf("expected failed marker: %q", line)
	}

	rl := p.RenderLine()
	if !strings.HasPrefix(rl, "\r\033[K") {
		t.Fatalf("RenderLine prefix missing: %q", rl)
	}

	sum := p.Summary()
	if !strings.Contains(sum, "Download Complete") {
		t.Fatalf("summary missing banner: %q", sum)
	}
	if !strings.Contains(sum, "Failed:") {
		t.Fatalf("summary missing fails: %q", sum)
	}
}

func TestProgressFFmpegFlow(t *testing.T) {
	p := NewProgress(0)
	p.Update(hls.Event{Kind: hls.EventFFmpeg, Seconds: 12, Speed: 1.5})
	p.mu.Lock()
	p.start = time.Now().Add(-2 * time.Second)
	p.mu.Unlock()

	line := p.Render()
	if !strings.Contains(line, "Downloading") {
		t.Fatalf("expected ffmpeg render: %q", line)
	}
	if !strings.Contains(line, "1.5x") {
		t.Fatalf("expected speed: %q", line)
	}
}

func TestBorderedPhaseStartBanner(t *testing.T) {
	box := Bordered("Hello", ColorGreen)
	if !strings.Contains(box, "Hello") || !strings.Contains(box, "╔") {
		t.Fatalf("unexpected bordered: %q", box)
	}
	ph := Phase("Fetching")
	if !strings.Contains(ph, "Fetching") {
		t.Fatalf("unexpected phase: %q", ph)
	}

	var sb stringsBuilder
	w := NewStdWriter(&sb, false)
	StartBanner(w)
	out := sb.String()
	if !strings.Contains(out, "Jable Downloader") {
		t.Fatalf("banner missing: %q", out)
	}
}

func TestProgressZeroTotalRender(t *testing.T) {
	p := NewProgress(0)
	if p.SegmentsUsed() {
		t.Fatal("expected SegmentsUsed false")
	}
	_ = p.Render()
}
