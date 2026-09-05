package ui

import (
	"os"
	"strings"
	"testing"
)

func TestPickerNonTTYReturnsItems(t *testing.T) {
	items := []PickerItem{{ID: "a", Label: "A", Selected: true}}
	got, err := PickMulti("test", items)
	if err != nil {
		t.Fatalf("PickMulti: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestPickMultiEmpty(t *testing.T) {
	got, err := PickMulti("empty", nil)
	if err != nil {
		t.Fatalf("PickMulti: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 8, "hello w…"},
	}
	for _, tc := range cases {
		if got := truncate(tc.in, tc.w); got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
		}
	}
}

func TestVisibleAndSelectedCount(t *testing.T) {
	p := &picker{
		items: []PickerItem{
			{ID: "1", Label: "Alpha One", Selected: true},
			{ID: "2", Label: "Beta Two", Selected: false},
			{ID: "3", Label: "Alpha Three", Selected: true},
		},
		filter: "alpha",
	}
	vis := p.visible()
	if len(vis) != 2 {
		t.Fatalf("visible = %v, want 2", vis)
	}
	if p.selectedCount() != 2 {
		t.Fatalf("selectedCount = %d, want 2", p.selectedCount())
	}

	p.filter = ""
	if len(p.visible()) != 3 {
		t.Fatalf("unfiltered visible = %d", len(p.visible()))
	}
}

func TestStdWriterStripsColors(t *testing.T) {
	var sb stringsBuilder
	w := NewStdWriter(&sb, false)
	w.Println(ColorRed + "error" + ColorReset)

	if got := sb.String(); got != "error\n" {
		t.Fatalf("expected stripped output, got %q", got)
	}
}

func TestStdWriterPrintfPrintColor(t *testing.T) {
	var sb stringsBuilder
	w := NewStdWriter(&sb, true)
	if !w.Color() {
		t.Fatal("expected color enabled")
	}
	w.Printf("%s hi %s", ColorCyan, ColorReset)
	w.Print("x")
	out := sb.String()
	if !strings.Contains(out, ColorCyan) || !strings.Contains(out, "hi") || !strings.Contains(out, "x") {
		t.Fatalf("unexpected output: %q", out)
	}

	var sb2 stringsBuilder
	w2 := NewStdWriter(&sb2, false)
	w2.Printf(ColorRed + "nope" + ColorReset)
	if sb2.String() != "nope" {
		t.Fatalf("expected stripped printf, got %q", sb2.String())
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	value, set := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if set {
			_ = os.Setenv(key, value)
			return
		}
		_ = os.Unsetenv(key)
	})
}

func TestColorEnabled(t *testing.T) {
	t.Run("no_color", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		if ColorEnabled(os.Stdout) {
			t.Fatal("NO_COLOR should disable color")
		}
	})
	t.Run("term_dumb", func(t *testing.T) {
		t.Setenv("TERM", "dumb")
		unsetEnv(t, "NO_COLOR")
		unsetEnv(t, "FORCE_COLOR")
		if ColorEnabled(os.Stdout) {
			t.Fatal("TERM=dumb should disable color")
		}
	})
	t.Run("force_true", func(t *testing.T) {
		unsetEnv(t, "NO_COLOR")
		t.Setenv("TERM", "xterm")
		t.Setenv("FORCE_COLOR", "true")
		if !ColorEnabled(os.Stdout) {
			t.Fatal("FORCE_COLOR=true should enable color")
		}
	})
	t.Run("force_false", func(t *testing.T) {
		unsetEnv(t, "NO_COLOR")
		t.Setenv("TERM", "xterm")
		t.Setenv("FORCE_COLOR", "false")
		if ColorEnabled(os.Stdout) {
			t.Fatal("FORCE_COLOR=false should disable color")
		}
	})
}

type stringsBuilder struct {
	b []byte
}

func (s *stringsBuilder) Write(p []byte) (int, error) {
	s.b = append(s.b, p...)
	return len(p), nil
}

func (s *stringsBuilder) String() string { return string(s.b) }
