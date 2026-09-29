package cli

import (
	"sync"
	"time"
)

// progressDisplay redraws a Progress at a fixed interval until stop. On a
// TTY it overwrites the previous block; otherwise (pipes, docker logs) it
// prints newline-terminated snapshots at a slower pace.
type progressDisplay struct {
	w         *StdWriter
	progress  *Progress
	tty       bool
	done      chan struct{}
	wg        sync.WaitGroup
	lastLines int
}

func startDisplay(w *StdWriter, p *Progress, tty bool) *progressDisplay {
	d := &progressDisplay{w: w, progress: p, tty: tty, done: make(chan struct{})}
	interval := 800 * time.Millisecond
	if tty {
		interval = 120 * time.Millisecond
	}
	d.wg.Add(1)
	go d.loop(interval)
	return d
}

func (d *progressDisplay) loop(interval time.Duration) {
	defer d.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			if d.tty {
				lines := d.progress.LineCount()
				d.w.Print(d.progress.RenderLine(d.lastLines))
				d.lastLines = lines
			} else {
				d.w.Println(d.progress.Render())
			}
		}
	}
}

// stop ends the redraw loop and clears the TTY block.
func (d *progressDisplay) stop() {
	close(d.done)
	d.wg.Wait()
	if d.tty {
		d.w.Print(ClearBlock(d.lastLines))
	}
}
