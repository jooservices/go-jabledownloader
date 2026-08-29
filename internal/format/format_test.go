package format

import (
	"testing"
	"time"
)

func TestBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{10 * 1024 * 1024, "10.0 MiB"},
		{2 * 1024 * 1024 * 1024, "2.0 GiB"},
	}
	for _, tc := range cases {
		if got := Bytes(tc.in); got != tc.want {
			t.Errorf("Bytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{3*time.Minute + 20*time.Second, "3m20s"},
		{1*time.Hour + 2*time.Minute, "1h02m"},
	}
	for _, tc := range cases {
		if got := Duration(tc.in); got != tc.want {
			t.Errorf("Duration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
