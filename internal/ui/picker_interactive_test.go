package ui

import (
	"io"
	"os"
	"testing"

	"golang.org/x/term"
)

func TestPickMultiInteractive(t *testing.T) {
	oldTerm := stdinTerminal
	oldRaw := makeRaw
	oldRestore := restoreTerm
	oldSize := terminalSize
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() {
		stdinTerminal = oldTerm
		makeRaw = oldRaw
		restoreTerm = oldRestore
		terminalSize = oldSize
		os.Stdin = oldStdin
		os.Stdout = oldStdout
	}()

	stdinTerminal = func() bool { return true }
	makeRaw = func(int) (*term.State, error) { return &term.State{}, nil }
	restoreTerm = func(int, *term.State) error { return nil }
	terminalSize = func(int) (int, int, error) { return 0, 0, io.EOF } // force defaults

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	os.Stdout = devNull

	// Script ends with none selected, then select only the first item.
	script := []byte{
		'j',      // down
		' ',      // toggle
		'a',      // all
		'/', 'A', // filter
		keyEnter, // leave filter
		27, '[', '5',
		27, '[', '6',
		'k', // up
		'n', // none
		' ', // toggle first (cursor 0)
		keyEnter,
	}

	go func() {
		_, _ = w.Write(script)
		_ = w.Close()
	}()

	items := []PickerItem{
		{ID: "1", Label: "Alpha", Selected: true},
		{ID: "2", Label: "Beta", Selected: true},
	}
	got, err := PickMulti("title", items)
	if err != nil {
		t.Fatalf("PickMulti: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if !got[0].Selected || got[1].Selected {
		t.Fatalf("Selected=[%v,%v] want [true,false]", got[0].Selected, got[1].Selected)
	}
}

func TestPickMultiCancel(t *testing.T) {
	oldTerm := stdinTerminal
	oldRaw := makeRaw
	oldRestore := restoreTerm
	oldSize := terminalSize
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() {
		stdinTerminal = oldTerm
		makeRaw = oldRaw
		restoreTerm = oldRestore
		terminalSize = oldSize
		os.Stdin = oldStdin
		os.Stdout = oldStdout
	}()

	stdinTerminal = func() bool { return true }
	makeRaw = func(int) (*term.State, error) { return &term.State{}, nil }
	restoreTerm = func(int, *term.State) error { return nil }
	terminalSize = func(int) (int, int, error) { return 80, 24, nil }

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	devNull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	defer devNull.Close()
	os.Stdout = devNull

	go func() {
		_, _ = w.Write([]byte{'q'})
		_ = w.Close()
	}()

	_, err = PickMulti("t", []PickerItem{{ID: "1", Label: "A"}})
	if err != ErrPickerCancelled {
		t.Fatalf("expected cancel, got %v", err)
	}
}

func TestPickMultiFilterBackspaceAndCancel(t *testing.T) {
	oldTerm := stdinTerminal
	oldRaw := makeRaw
	oldRestore := restoreTerm
	oldSize := terminalSize
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() {
		stdinTerminal = oldTerm
		makeRaw = oldRaw
		restoreTerm = oldRestore
		terminalSize = oldSize
		os.Stdin = oldStdin
		os.Stdout = oldStdout
	}()

	stdinTerminal = func() bool { return true }
	makeRaw = func(int) (*term.State, error) { return &term.State{}, nil }
	restoreTerm = func(int, *term.State) error { return nil }
	terminalSize = func(int) (int, int, error) { return 80, 24, nil }

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	devNull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	defer devNull.Close()
	os.Stdout = devNull

	go func() {
		_, _ = w.Write([]byte{
			'/', 'x', 'y', keyBack, keyCtrlW, keyCtrlU, 'z',
			27, // esc clears filter
			'/', 'a',
			3, // ctrl-c cancel while filtering
		})
		_ = w.Close()
	}()

	_, err = PickMulti("t", []PickerItem{{ID: "1", Label: "Alpha"}})
	if err != ErrPickerCancelled {
		t.Fatalf("expected cancel, got %v", err)
	}
}

func TestPickMultiMakeRawFallback(t *testing.T) {
	oldTerm := stdinTerminal
	oldRaw := makeRaw
	defer func() {
		stdinTerminal = oldTerm
		makeRaw = oldRaw
	}()
	stdinTerminal = func() bool { return true }
	makeRaw = func(int) (*term.State, error) { return nil, io.ErrClosedPipe }

	items := []PickerItem{{ID: "1", Label: "A"}}
	got, err := PickMulti("t", items)
	if err != nil || len(got) != 1 {
		t.Fatalf("got=%v err=%v", got, err)
	}
}
