package ui

import (
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

func TestStdWriterStripsColors(t *testing.T) {
	var sb stringsBuilder
	w := NewStdWriter(&sb, false)
	w.Println(ColorRed + "error" + ColorReset)

	if got := sb.String(); got != "error\n" {
		t.Fatalf("expected stripped output, got %q", got)
	}
}

type stringsBuilder struct {
	b []byte
}

func (s *stringsBuilder) Write(p []byte) (int, error) {
	s.b = append(s.b, p...)
	return len(p), nil
}

func (s *stringsBuilder) String() string { return string(s.b) }
