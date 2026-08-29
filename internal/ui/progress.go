package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/format"
	"github.com/jooservices/go-jabledownloader/internal/hls"
)

const barWidth = 40

// FailEntry describes one failed segment.
type FailEntry struct {
	ID  string
	URL string
	Err string
}

// Progress accumulates downloader events into a renderable state. All
// methods are safe for concurrent use.
type Progress struct {
	total   int64
	done    int64
	failed  int64
	bytes   int64
	start   time.Time
	ffmpeg  bool
	seconds float64
	speed   float64
	fails   []string
	mu      sync.Mutex
}

// NewProgress starts tracking a segment download of total segments.
func NewProgress(total int) *Progress {
	return &Progress{total: int64(total), start: time.Now()}
}

// Update consumes an hls.Event and refreshes the state.
func (p *Progress) Update(ev hls.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch ev.Kind {
	case hls.EventSegments:
		p.total = ev.Total
		p.done = ev.Done
		p.failed = ev.Failed
		p.bytes = ev.Bytes
	case hls.EventFFmpeg:
		p.ffmpeg = true
		p.seconds = ev.Seconds
		p.speed = ev.Speed
	case hls.EventRetry:
		if ev.Message != "" && (len(p.fails) == 0 || p.fails[len(p.fails)-1] != ev.Message) {
			p.fails = append(p.fails, ev.Message)
		}
	}
}

// Render returns the current one-line progress string.
func (p *Progress) Render() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ffmpeg {
		return p.renderFFmpeg()
	}
	return p.renderSegments()
}

func (p *Progress) renderSegments() string {
	var b strings.Builder

	pct := 0.0
	if p.total > 0 {
		pct = float64(p.done) / float64(p.total) * 100
	}

	b.WriteString("  ")
	filled := int(float64(barWidth) * pct / 100)
	if filled < 0 {
		filled = 0
	}
	if filled > barWidth {
		filled = barWidth
	}
	empty := barWidth - filled

	b.WriteString(ColorCyan + "[")
	b.WriteString(ColorGreen + strings.Repeat("━", filled))
	if filled < barWidth {
		b.WriteString(ColorYellow + "▶")
		empty--
	}
	b.WriteString(ColorDim + strings.Repeat("─", max(empty, 0)))
	b.WriteString(ColorCyan + "]" + ColorReset)

	b.WriteString(fmt.Sprintf(" %s%3.0f%%%s", ColorBold, pct, ColorReset))
	b.WriteString(fmt.Sprintf("  %s%d%s/%s%d%s",
		ColorGreen, p.done, ColorReset,
		ColorCyan, p.total, ColorReset,
	))

	if p.failed > 0 {
		b.WriteString(fmt.Sprintf("  %s✗ %d failed%s", ColorRed, p.failed, ColorReset))
	}

	elapsed := time.Since(p.start)
	if p.done > 0 && elapsed >= 200*time.Millisecond && p.bytes > 0 {
		speed := float64(p.bytes) / elapsed.Seconds()
		eta := time.Duration(float64(p.total-p.done) / float64(p.done) * elapsed.Seconds() * float64(time.Second))
		if eta > 0 && p.done < p.total {
			b.WriteString(fmt.Sprintf("  %s%s %seta %s%s%s",
				ColorDim, IconClock, ColorReset,
				ColorYellow, format.Duration(eta), ColorReset,
			))
		}
		b.WriteString(fmt.Sprintf("  %s%s/s%s", ColorDim, format.Bytes(int64(speed)), ColorReset))
	}

	return b.String()
}

func (p *Progress) renderFFmpeg() string {
	var b strings.Builder
	b.WriteString("  " + ColorCyan + IconVideo + ColorReset + " " + ColorBold + "Downloading" + ColorReset)
	b.WriteString(fmt.Sprintf("  %s%s%s", ColorBold, format.Duration(time.Duration(p.seconds)*time.Second), ColorReset))

	if p.speed > 0 {
		b.WriteString(fmt.Sprintf("  %s%.1fx%s", ColorDim, p.speed, ColorReset))
		if p.seconds > 0 {
			elapsed := time.Since(p.start).Seconds()
			etaSec := p.seconds/p.speed - elapsed
			if etaSec > 0 {
				b.WriteString(fmt.Sprintf("  %s%s %seta %s%s",
					ColorDim, IconClock,
					ColorYellow, format.Duration(time.Duration(etaSec)*time.Second), ColorReset))
			}
		}
	}
	return b.String()
}

// Summary renders the final multi-line download summary.
func (p *Progress) Summary() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(Bordered("Download Complete", ColorGreen))
	b.WriteString(fmt.Sprintf("\n  %s%s Elapsed:%s %s\n",
		ColorDim, IconClock, ColorReset, format.Duration(time.Since(p.start))))

	ok := ColorGreen + fmt.Sprintf("%s %d", IconOk, p.done) + ColorReset
	fail := ColorRed + fmt.Sprintf("%s %d", IconErr, p.failed) + ColorReset
	b.WriteString(fmt.Sprintf("  %-20s %s", ok, fail))

	if p.bytes > 0 {
		b.WriteString(fmt.Sprintf("  %s%s %s%s",
			ColorDim, IconDisk, format.Bytes(p.bytes), ColorReset,
		))
	}

	if len(p.fails) > 0 {
		b.WriteString(fmt.Sprintf("\n\n  %s%s Failed:%s\n", ColorRed, IconErr, ColorReset))
		for _, f := range p.fails {
			b.WriteString(fmt.Sprintf("    %s%s — %s%s\n", ColorDim, IconErr, f, ColorReset))
		}
	}

	b.WriteString("\n")
	return b.String()
}

// Bordered draws text inside a unicode box.
func Bordered(text, color string) string {
	width := runeCount(text) + 4
	top := "╔" + strings.Repeat("═", width) + "╗"
	mid := "║  " + text + "  ║"
	bot := "╚" + strings.Repeat("═", width) + "╝"
	return color + top + "\n" + mid + "\n" + bot + ColorReset
}

// Phase renders a phase header line.
func Phase(label string) string {
	return fmt.Sprintf("%s%s %s%s%s", ColorCyan, IconArrow, ColorBold, label, ColorReset)
}

// StartBanner renders the CLI banner.
func StartBanner(w Writer) {
	w.Println()
	w.Println(Bordered("Jable Downloader", ColorCyan))
	w.Println()
}

func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
