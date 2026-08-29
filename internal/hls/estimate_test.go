package hls

import "testing"

func TestParseDurationSeconds(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"1:00:00", 3600},
		{"10:00", 600},
		{"45", 45},
		{"", 0},
		{"1:2:3:4", 0},
		{"ab:cd", 0},
		{"-5", 0},
	}
	for _, tc := range cases {
		if got := ParseDurationSeconds(tc.in); got != tc.want {
			t.Errorf("ParseDurationSeconds(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestEstimateVideoBytes(t *testing.T) {
	if got := EstimateVideoBytes("1:00:00"); got != 3600*3_000_000/8 {
		t.Fatalf("unexpected estimate: %d", got)
	}
	if got := EstimateVideoBytes(""); got != 0 {
		t.Fatalf("expected 0 for unknown duration, got %d", got)
	}
}
