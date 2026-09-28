package srt

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/domain"
)

func TestSRTParseFormatRoundTrip(t *testing.T) {
	input := "\ufeff1\r\n00:00:01,250 --> 00:00:03,500\r\nfirst line\r\nsecond line\r\n\r\n"
	want := []domain.Cue{{Index: 1, Start: 1250 * time.Millisecond, End: 3500 * time.Millisecond, Text: "first line\nsecond line"}}

	got, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	var output bytes.Buffer
	if err := Format(&output, got); err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(&output)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != len(want) || parsed[0] != want[0] {
		t.Fatalf("round trip got %#v, want %#v", parsed, want)
	}
}

func TestSRTParseReportsTimestampLine(t *testing.T) {
	_, err := Parse(strings.NewReader("1\n00:00:99,000 --> 00:00:01,000\ntext\n"))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("expected line-numbered timestamp error, got %v", err)
	}
}

func TestSRTFormatRejectsNegativeTimestamp(t *testing.T) {
	var output bytes.Buffer
	err := Format(&output, []domain.Cue{{Index: 1, Start: -time.Millisecond}})
	if err == nil || !strings.Contains(err.Error(), "negative timestamp") {
		t.Fatalf("expected negative timestamp error, got %v", err)
	}
}

func TestSRTParseRejectsMalformedCueBlocks(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "index", data: "x\n00:00:00,000 --> 00:00:01,000\ntext\n", want: "invalid cue index"},
		{name: "missing timestamp", data: "1\ntext\n", want: "invalid timestamp"},
		{name: "missing text", data: "1\n00:00:00,000 --> 00:00:01,000\n", want: "missing cue text"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestSRTFormatReportsWriterError(t *testing.T) {
	err := Format(failingWriter{}, []domain.Cue{{Index: 1, End: time.Second, Text: "text"}})
	if err == nil || !strings.Contains(err.Error(), "write SRT") {
		t.Fatalf("expected writer error, got %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errTestWriter }

var errTestWriter = errors.New("test writer failure")

func TestFormatNumbersUnindexedCues(t *testing.T) {
	var buf bytes.Buffer
	cues := []domain.Cue{{Start: 0, End: time.Second, Text: "a"}, {Start: time.Second, End: 2 * time.Second, Text: "b"}}
	if err := Format(&buf, cues); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "1\n") || !strings.Contains(buf.String(), "\n\n2\n") {
		t.Fatalf("output = %q", buf.String())
	}
}
