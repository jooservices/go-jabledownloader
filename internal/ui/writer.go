package ui

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// Writer is the output abstraction used by services and commands. Tests use
// a strings.Builder-backed implementation; the CLI uses stdout.
type Writer interface {
	Printf(format string, a ...any)
	Println(a ...any)
	Print(a ...any)
}

// StdWriter writes to an io.Writer. Color codes are stripped when color is
// disabled, keeping piped output clean.
type StdWriter struct {
	mu    sync.Mutex
	out   io.Writer
	color bool
}

// NewStdWriter builds a Writer around out.
func NewStdWriter(out io.Writer, color bool) *StdWriter {
	return &StdWriter{out: out, color: color}
}

// Color reports whether colors are enabled for this writer.
func (w *StdWriter) Color() bool { return w.color }

func (w *StdWriter) sanitize(s string) string {
	if w.color {
		return s
	}
	return stripColors(s)
}

// Printf writes a formatted line through the writer.
func (w *StdWriter) Printf(format string, a ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fmt.Fprintf(w.out, w.sanitize(format), a...)
}

// Println writes arguments followed by a newline.
func (w *StdWriter) Println(a ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fmt.Fprintln(w.out, w.sanitize(fmt.Sprint(a...)))
}

// Print writes arguments without a newline.
func (w *StdWriter) Print(a ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fmt.Fprint(w.out, w.sanitize(fmt.Sprint(a...)))
}

var ansiReplacer = strings.NewReplacer(
	"\033[0m", "",
	"\033[1m", "",
	"\033[2m", "",
	"\033[31m", "",
	"\033[32m", "",
	"\033[33m", "",
	"\033[34m", "",
	"\033[36m", "",
	"\033[37m", "",
	"\033[H\033[2J", "",
	"\033[?25l", "",
	"\033[?25h", "",
	"\033[K", "",
)

func stripColors(s string) string {
	return ansiReplacer.Replace(s)
}
