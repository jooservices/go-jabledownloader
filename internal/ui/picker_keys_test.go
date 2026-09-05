package ui

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestReadKey(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want int
	}{
		{"letter", []byte{'a'}, 'a'},
		{"enter", []byte{'\r'}, keyEnter},
		{"esc_alone", []byte{27}, keyEsc},
		{"up", []byte{27, '[', 'A'}, keyUp},
		{"down", []byte{27, '[', 'B'}, keyDown},
		{"pgup", []byte{27, '[', '5'}, keyPageUp},
		{"pgdn", []byte{27, '[', '6'}, keyPageDn},
		{"esc_other", []byte{27, '[', 'Z'}, keyEsc},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(tc.in); err != nil {
				t.Fatal(err)
			}
			_ = w.Close()

			got, err := readKey(r)
			_ = r.Close()
			if err != nil && tc.want != keyEsc {
				// esc_alone may timeout reading rest; still returns keyEsc
				if got != tc.want {
					t.Fatalf("readKey error %v got %d want %d", err, got, tc.want)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}

func TestRedraw(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()

	p := &picker{
		title:  "Pick",
		height: 10,
		width:  80,
		items: []PickerItem{
			{ID: "1", Label: "Alpha", Detail: "d1", Selected: true},
			{ID: "2", Label: "Beta", Selected: false},
		},
		cursor: 0,
	}
	p.redraw()

	p.filtering = true
	p.filter = "al"
	p.redraw()

	p.filtering = false
	p.filter = "zzz"
	p.redraw()

	_ = w.Close()
	out, _ := io.ReadAll(r)
	s := string(out)
	if !strings.Contains(s, "Pick") || !strings.Contains(s, "Alpha") {
		t.Fatalf("unexpected redraw output: %q", s)
	}
	if !strings.Contains(s, "no matches") {
		t.Fatalf("expected no matches: %q", s)
	}
}

func TestReadKeyEmpty(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	_, err = readKey(r)
	_ = r.Close()
	if err == nil {
		t.Fatal("expected EOF error")
	}
}

func TestReadKeyEscTimeout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte{27}); err != nil {
		t.Fatal(err)
	}
	// Do not close immediately — allow deadline on trailing read.
	time.AfterFunc(50*time.Millisecond, func() { _ = w.Close() })
	got, err := readKey(r)
	_ = r.Close()
	if got != keyEsc {
		t.Fatalf("got %d want esc (%v)", got, err)
	}
}
