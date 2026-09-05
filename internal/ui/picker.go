package ui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// ErrPickerCancelled is returned by PickMulti when the user aborts (q/Ctrl-C).
var ErrPickerCancelled = errors.New("cancelled")

// PickerItem is one selectable entry in the interactive picker.
type PickerItem struct {
	ID       string
	Label    string
	Detail   string
	Selected bool
}

const (
	keyEnter  = '\r'
	keyEsc    = 27
	keySpace  = ' '
	keyCtrlC  = 3
	keyCtrlU  = 21
	keyCtrlW  = 23
	keyBack   = 127
	keyUp     = 1001
	keyDown   = 1002
	keyPageUp = 1003
	keyPageDn = 1004
)

func readKey(f *os.File) (int, error) {
	var b [3]byte
	n, err := f.Read(b[:1])
	if err != nil || n == 0 {
		return 0, err
	}
	if b[0] != keyEsc {
		return int(b[0]), nil
	}
	_ = f.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	n, _ = f.Read(b[1:])
	_ = f.SetReadDeadline(time.Time{})
	if n == 0 {
		return keyEsc, nil
	}
	if b[1] == '[' {
		switch b[2] {
		case 'A':
			return keyUp, nil
		case 'B':
			return keyDown, nil
		case '5':
			return keyPageUp, nil
		case '6':
			return keyPageDn, nil
		}
	}
	return keyEsc, nil
}

type picker struct {
	items     []PickerItem
	title     string
	filter    string
	filtering bool
	cursor    int
	scroll    int
	height    int
	width     int
}

func (p *picker) visible() []int {
	var idx []int
	for i, it := range p.items {
		if p.filter == "" ||
			strings.Contains(strings.ToLower(it.Label), strings.ToLower(p.filter)) {
			idx = append(idx, i)
		}
	}
	return idx
}

func (p *picker) selectedCount() int {
	n := 0
	for _, it := range p.items {
		if it.Selected {
			n++
		}
	}
	return n
}

func truncate(s string, w int) string {
	if utf8.RuneCountInString(s) > w {
		runes := []rune(s)
		return string(runes[:max(w-1, 1)]) + "…"
	}
	return s
}

func (p *picker) redraw() {
	vis := p.visible()

	var b strings.Builder
	b.WriteString("\033[H\033[2J")
	b.WriteString(ColorCyan + ColorBold)
	fmt.Fprintf(&b, "  %s  %s(%d/%d selected)%s",
		p.title, ColorDim, p.selectedCount(), len(p.items), ColorReset)
	b.WriteString(ColorReset)
	b.WriteString("\r\n")

	if p.filtering {
		b.WriteString("  " + ColorBold + "Filter: " + ColorReset + p.filter + ColorDim + "▌" + ColorReset + "\r\n")
	} else if p.filter != "" {
		b.WriteString("  " + ColorDim + "Filter: " + p.filter + ColorReset + "\r\n")
	}

	listH := p.height - 3

	start := p.scroll
	if len(vis) == 0 {
		b.WriteString("  " + ColorDim + "(no matches)" + ColorReset + "\r\n")
	} else {
		if p.cursor >= len(vis) {
			p.cursor = len(vis) - 1
		}
		if p.cursor < start {
			start = p.cursor
		}
		if p.cursor >= start+listH {
			start = p.cursor - listH + 1
		}
		p.scroll = start

		for row := 0; row < listH && start+row < len(vis); row++ {
			it := p.items[vis[start+row]]
			mark := IconSkip
			col := ColorDim
			if it.Selected {
				mark = IconOk
				col = ColorGreen
			}
			cursor := "  "
			if vis[start+row] == vis[p.cursor] {
				cursor = "▶ "
			}
			b.WriteString(" " + cursor + col + mark + ColorReset + " " + ColorBold + truncate(it.Label, max(p.width-30, 10)) + ColorReset)
			if it.Detail != "" {
				b.WriteString("  " + ColorDim + it.Detail + ColorReset)
			}
			b.WriteString("\r\n")
		}
	}

	b.WriteString("\r\n  " + ColorDim + "↑/↓ move  space toggle  a all  n none  / filter  enter confirm  q cancel" + ColorReset)
	fmt.Print(b.String())
}

// Terminal seams — production uses golang.org/x/term; tests override these
// to exercise the picker without a real TTY.
var (
	stdinTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	makeRaw       = term.MakeRaw
	restoreTerm   = term.Restore
	terminalSize  = term.GetSize
)

// PickMulti shows an interactive multi-select list and blocks until the user
// confirms or cancels. When stdin is not a TTY, the items are returned
// unchanged. Returns the (possibly edited) items and an error on cancel.
func PickMulti(title string, items []PickerItem) ([]PickerItem, error) {
	if len(items) == 0 || !stdinTerminal() {
		return items, nil
	}

	oldState, err := makeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return items, nil
	}
	defer func() { _ = restoreTerm(int(os.Stdin.Fd()), oldState) }()

	w, h := pickerWindowSize()
	p := &picker{items: items, title: title, height: max(h-3, 5), width: w}
	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")

	for {
		p.redraw()

		key, err := readKey(os.Stdin)
		if err != nil {
			return nil, err
		}

		if p.filtering {
			if err := p.handleFilterKey(key); err != nil {
				return nil, err
			}
			continue
		}

		done, err := p.handleBrowseKey(key)
		if err != nil {
			return nil, err
		}
		if done {
			return p.items, nil
		}
	}
}

func pickerWindowSize() (width, height int) {
	w, h, err := terminalSize(int(os.Stdin.Fd()))
	if err != nil || w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	return w, h
}

func (p *picker) handleFilterKey(key int) error {
	switch key {
	case keyEnter:
		p.filtering = false
	case keyEsc, keyCtrlC:
		if key == keyCtrlC {
			return ErrPickerCancelled
		}
		p.filter = ""
		p.filtering = false
	case keyBack, keyCtrlW:
		if p.filter != "" {
			_, size := utf8.DecodeLastRuneInString(p.filter)
			p.filter = p.filter[:len(p.filter)-size]
		}
	case keyCtrlU:
		p.filter = ""
	default:
		if key >= 32 && key < 127 {
			p.filter += string(rune(key))
		}
	}
	p.cursor = 0
	p.scroll = 0
	return nil
}

func (p *picker) handleBrowseKey(key int) (done bool, err error) {
	vis := p.visible()
	switch key {
	case keyCtrlC, 'q':
		return false, ErrPickerCancelled
	case keyEnter:
		return true, nil
	case keyUp, 'k':
		if p.cursor > 0 {
			p.cursor--
		}
	case keyDown, 'j':
		if p.cursor < len(vis)-1 {
			p.cursor++
		}
	case keyPageUp:
		p.cursor = max(p.cursor-10, 0)
	case keyPageDn:
		p.cursor = min(p.cursor+10, len(vis)-1)
	case keySpace:
		if len(vis) > 0 {
			it := &p.items[vis[p.cursor]]
			it.Selected = !it.Selected
		}
	case 'a':
		for i := range p.items {
			p.items[i].Selected = true
		}
	case 'n':
		for i := range p.items {
			p.items[i].Selected = false
		}
	case '/':
		p.filtering = true
	}
	return false, nil
}
