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

// speedSample records downloaded bytes at a point in time for instant speed.
type speedSample struct {
	at    time.Time
	bytes int64
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
	resume  string
	label   string
	samples []speedSample
	mu      sync.Mutex
}

// NewProgress starts tracking a segment download of total segments.
func NewProgress(total int) *Progress {
	return &Progress{total: int64(total), start: time.Now()}
}

// SetLabel sets an optional file/title label shown above the progress block.
func (p *Progress) SetLabel(label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.label = label
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
		p.recordSampleLocked(ev.Bytes)
	case hls.EventFFmpeg:
		p.ffmpeg = true
		p.seconds = ev.Seconds
		p.speed = ev.Speed
	case hls.EventRetry:
		if ev.Message != "" && (len(p.fails) == 0 || p.fails[len(p.fails)-1] != ev.Message) {
			p.fails = append(p.fails, ev.Message)
		}
	case hls.EventResume:
		p.resume = ev.Message
	}
}

func (p *Progress) recordSampleLocked(bytes int64) {
	now := time.Now()
	p.samples = append(p.samples, speedSample{at: now, bytes: bytes})
	cutoff := now.Add(-3 * time.Second)
	i := 0
	for i < len(p.samples) && p.samples[i].at.Before(cutoff) {
		i++
	}
	if i > 0 && i < len(p.samples) {
		p.samples = p.samples[i:]
	} else if i >= len(p.samples) && len(p.samples) > 1 {
		p.samples = p.samples[len(p.samples)-1:]
	}
}

// Render returns the current progress string (multi-line for segments).
func (p *Progress) Render() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ffmpeg {
		return p.renderFFmpeg()
	}
	return p.renderSegments()
}

// LineCount returns how many terminal lines Render() occupies.
func (p *Progress) LineCount() int {
	s := p.Render()
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// RenderLine returns the progress block positioned to overwrite the previous
// TTY block (move cursor up + clear each line).
func (p *Progress) RenderLine(prevLines int) string {
	block := p.Render()
	lines := strings.Split(block, "\n")
	var b strings.Builder
	if prevLines > 0 {
		fmt.Fprintf(&b, "\033[%dA", prevLines)
	}
	for i, line := range lines {
		b.WriteString("\r\033[K")
		b.WriteString(line)
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// ClearBlock clears the last rendered TTY progress block.
func ClearBlock(lines int) string {
	if lines <= 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\033[%dA", lines)
	for i := 0; i < lines; i++ {
		b.WriteString("\r\033[K")
		if i < lines-1 {
			b.WriteByte('\n')
		}
	}
	if lines > 1 {
		fmt.Fprintf(&b, "\033[%dA", lines-1)
	}
	return b.String()
}

// SegmentsUsed reports whether segment-phase progress was recorded.
func (p *Progress) SegmentsUsed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.total > 0
}

// ResumeMessage returns the resume event message, if any.
func (p *Progress) ResumeMessage() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.resume
}

func (p *Progress) renderSegments() string {
	var b strings.Builder

	pct := 0.0
	if p.total > 0 {
		pct = float64(p.done) / float64(p.total) * 100
	}

	label := p.label
	if label == "" {
		label = "download"
	}
	fmt.Fprintf(&b, "  %s%s Download%s  %s\n", ColorCyan, IconArrow, ColorReset, label)

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
	fmt.Fprintf(&b, "  %s%3.0f%%%s\n", ColorBold, pct, ColorReset)

	fmt.Fprintf(&b, "  Segs     %s%d%s / %s%d%s",
		ColorGreen, p.done, ColorReset,
		ColorCyan, p.total, ColorReset,
	)
	if p.failed > 0 {
		fmt.Fprintf(&b, "  %s✗ %d failed%s", ColorRed, p.failed, ColorReset)
	}
	b.WriteByte('\n')

	estTotal := int64(0)
	if p.done > 0 && p.total > 0 && p.bytes > 0 {
		estTotal = p.bytes * p.total / p.done
	}
	left := int64(0)
	if estTotal > p.bytes {
		left = estTotal - p.bytes
	}

	if p.bytes > 0 {
		fmt.Fprintf(&b, "  Size     %s", format.Bytes(p.bytes))
		if estTotal > 0 {
			fmt.Fprintf(&b, "  /  ~%s     left  %s", format.Bytes(estTotal), format.Bytes(left))
		}
		b.WriteByte('\n')
	} else {
		b.WriteString("  Size     —\n")
	}

	curSpeed := p.currentSpeedLocked()
	elapsed := time.Since(p.start)
	avgSpeed := 0.0
	if elapsed >= 200*time.Millisecond && p.bytes > 0 {
		avgSpeed = float64(p.bytes) / elapsed.Seconds()
	}

	b.WriteString("  Speed    ")
	if curSpeed > 0 {
		fmt.Fprintf(&b, "%s/s", format.Bytes(int64(curSpeed)))
	} else {
		b.WriteString("—")
	}
	if avgSpeed > 0 {
		fmt.Fprintf(&b, "   (avg %s/s)", format.Bytes(int64(avgSpeed)))
	}
	b.WriteByte('\n')

	fmt.Fprintf(&b, "  Time     elapsed %s", format.Duration(elapsed))
	etaSpeed := curSpeed
	if etaSpeed <= 0 {
		etaSpeed = avgSpeed
	}
	if etaSpeed > 0 && left > 0 && p.done < p.total {
		eta := time.Duration(float64(left)/etaSpeed) * time.Second
		fmt.Fprintf(&b, "    eta %s%s%s", ColorYellow, format.Duration(eta), ColorReset)
	} else if etaSpeed > 0 && p.done > 0 && p.done < p.total {
		// Fallback: segment-ratio ETA when size estimate is unavailable.
		eta := time.Duration(float64(p.total-p.done) / float64(p.done) * elapsed.Seconds() * float64(time.Second))
		if eta > 0 {
			fmt.Fprintf(&b, "    eta %s%s%s", ColorYellow, format.Duration(eta), ColorReset)
		}
	}

	return b.String()
}

func (p *Progress) currentSpeedLocked() float64 {
	if len(p.samples) < 2 {
		return 0
	}
	first := p.samples[0]
	last := p.samples[len(p.samples)-1]
	dt := last.at.Sub(first.at).Seconds()
	if dt < 0.2 {
		return 0
	}
	db := float64(last.bytes - first.bytes)
	if db < 0 {
		return 0
	}
	return db / dt
}

func (p *Progress) renderFFmpeg() string {
	var b strings.Builder
	label := p.label
	if label == "" {
		label = "ffmpeg"
	}
	fmt.Fprintf(&b, "  %s%s Download%s  %s (ffmpeg direct)\n", ColorCyan, IconArrow, ColorReset, label)
	fmt.Fprintf(&b, "  Time     %s%s%s", ColorBold, format.Duration(time.Duration(p.seconds)*time.Second), ColorReset)

	if p.speed > 0 {
		fmt.Fprintf(&b, "  %s%.1fx%s", ColorDim, p.speed, ColorReset)
		if p.seconds > 0 {
			elapsed := time.Since(p.start).Seconds()
			etaSec := p.seconds/p.speed - elapsed
			if etaSec > 0 {
				fmt.Fprintf(&b, "    eta %s%s%s",
					ColorYellow, format.Duration(time.Duration(etaSec)*time.Second), ColorReset)
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
	fmt.Fprintf(&b, "\n  %s%s Elapsed:%s %s\n",
		ColorDim, IconClock, ColorReset, format.Duration(time.Since(p.start)))

	ok := ColorGreen + fmt.Sprintf("%s %d", IconOk, p.done) + ColorReset
	fail := ColorRed + fmt.Sprintf("%s %d", IconErr, p.failed) + ColorReset
	fmt.Fprintf(&b, "  %-20s %s", ok, fail)

	if p.bytes > 0 {
		fmt.Fprintf(&b, "  %s%s %s%s",
			ColorDim, IconDisk, format.Bytes(p.bytes), ColorReset,
		)
	}

	if len(p.fails) > 0 {
		fmt.Fprintf(&b, "\n\n  %s%s Failed:%s\n", ColorRed, IconErr, ColorReset)
		for _, f := range p.fails {
			fmt.Fprintf(&b, "    %s%s — %s%s\n", ColorDim, IconErr, f, ColorReset)
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
