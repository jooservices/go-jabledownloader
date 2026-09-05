package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/hls"
)

func TestProgressSegmentsFlow(t *testing.T) {
	p := NewProgress(10)
	p.SetLabel("demo-h264.mp4")
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

	p.Update(hls.Event{Kind: hls.EventSegments, Done: 3, Total: 10, Bytes: 3_000_000, Failed: 1})
	p.Update(hls.Event{Kind: hls.EventRetry, Message: "rate-limited"})
	p.Update(hls.Event{Kind: hls.EventRetry, Message: "rate-limited"}) // duplicate ignored
	p.Update(hls.Event{Kind: hls.EventRetry, Message: "server error"})

	// Age the start and samples so speed/eta branch can fire.
	p.mu.Lock()
	p.start = time.Now().Add(-2 * time.Second)
	if len(p.samples) > 0 {
		p.samples[0].at = time.Now().Add(-2 * time.Second)
		p.samples[0].bytes = 500_000
	}
	p.samples = append(p.samples, speedSample{at: time.Now(), bytes: 3_000_000})
	p.mu.Unlock()

	line := p.Render()
	for _, want := range []string{"%", "Segs", "Size", "left", "Speed", "eta", "demo-h264.mp4"} {
		if !strings.Contains(line, want) {
			t.Fatalf("render missing %q: %q", want, line)
		}
	}
	if !strings.Contains(line, "failed") {
		t.Fatalf("expected failed marker: %q", line)
	}
	if p.LineCount() < 4 {
		t.Fatalf("expected multi-line block, lines=%d", p.LineCount())
	}

	rl := p.RenderLine(0)
	if !strings.Contains(rl, "\033[K") {
		t.Fatalf("RenderLine clear missing: %q", rl)
	}
	n := p.LineCount()
	rl2 := p.RenderLine(n)
	wantUp := fmt.Sprintf("\033[%dA", n-1)
	if n > 1 && !strings.Contains(rl2, wantUp) {
		t.Fatalf("expected cursor up %q in %q", wantUp, rl2)
	}
	if block := ClearBlock(3); !strings.Contains(block, "\033[2A") {
		t.Fatalf("ClearBlock should move up lines-1: %q", block)
	}
	if block := ClearBlock(1); strings.Contains(block, "A") {
		t.Fatalf("single-line ClearBlock needs no cursor-up: %q", block)
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
	if !strings.Contains(line, "ffmpeg") {
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
