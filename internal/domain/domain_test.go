package domain

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestValidCode(t *testing.T) {
	long := make([]byte, 129)
	for i := range long {
		long[i] = 'a'
	}
	for _, tc := range []struct {
		code string
		want bool
	}{
		{"abc-123", true},
		{"1XrYk0gaMpV", true},
		{"a.b_c-1", true},
		{"..", false},
		{".", false},
		{"a/b", false},
		{`a\b`, false},
		{"", false},
		{".hidden", false},
		{string(long), false},
	} {
		if got := ValidCode(tc.code); got != tc.want {
			t.Errorf("ValidCode(%q) = %v, want %v", tc.code, got, tc.want)
		}
	}
}

// The `get --json` output is a CLI contract: keys must match v4.3.
func TestDetailJSONMatchesV43Contract(t *testing.T) {
	detail := Detail{
		Site: "jable", Code: "abc-1", Title: "Title", VideoID: "42",
		Sources: []Source{{
			Kind: SourceHLS, URL: "https://cdn.test/a.m3u8",
			Headers: http.Header{"Referer": {"https://jable.test/"}},
		}},
	}

	got, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}

	want := `{"site":"jable","code":"abc-1","title":"Title","video_id":"42",` +
		`"sources":[{"Kind":0,"URL":"https://cdn.test/a.m3u8","Codec":"","Height":0}]}`
	if string(got) != want {
		t.Fatalf("JSON =\n%s\nwant\n%s", got, want)
	}
}
