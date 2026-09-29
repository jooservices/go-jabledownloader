// Package srt reads and writes SubRip (.srt) subtitles.
package srt

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/domain"
)

var timestampPattern = regexp.MustCompile(`^(\d+):(\d{2}):(\d{2})[,.](\d{3})\s+-->\s+(\d+):(\d{2}):(\d{2})[,.](\d{3})$`)

// Parse reads an SRT stream. It accepts an optional UTF-8 BOM, CRLF line
// endings, and cue text spanning multiple lines.
func Parse(r io.Reader) ([]domain.Cue, error) {
	scanner := bufio.NewScanner(r)
	// Subtitle lines are normally short, but a large limit avoids an otherwise
	// surprising failure for a valid multiline cue.
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)

	var cues []domain.Cue
	var block []subtitleLine
	lineNumber := 0
	flush := func() error {
		if len(block) == 0 {
			return nil
		}
		cue, err := parseBlock(block)
		if err != nil {
			return err
		}
		cues = append(cues, cue)
		block = nil
		return nil
	}

	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if len(block) == 0 && len(cues) == 0 {
			line = strings.TrimPrefix(line, "\ufeff")
		}
		if strings.TrimSpace(line) == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		block = append(block, subtitleLine{number: lineNumber, text: line})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SRT: %w", err)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return cues, nil
}

type subtitleLine struct {
	number int
	text   string
}

func parseBlock(block []subtitleLine) (domain.Cue, error) {
	index, err := strconv.Atoi(strings.TrimSpace(block[0].text))
	if err != nil {
		return domain.Cue{}, fmt.Errorf("line %d: invalid cue index: %w", block[0].number, err)
	}
	if len(block) < 2 {
		return domain.Cue{}, fmt.Errorf("line %d: missing timestamp", block[0].number)
	}
	start, end, err := parseTimestamp(block[1].text)
	if err != nil {
		return domain.Cue{}, fmt.Errorf("line %d: %w", block[1].number, err)
	}
	if len(block) < 3 {
		return domain.Cue{}, fmt.Errorf("line %d: missing cue text", block[1].number)
	}
	text := make([]string, 0, len(block)-2)
	for _, line := range block[2:] {
		text = append(text, line.text)
	}
	return domain.Cue{Index: index, Start: start, End: end, Text: strings.Join(text, "\n")}, nil
}

func parseTimestamp(raw string) (time.Duration, time.Duration, error) {
	match := timestampPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if match == nil {
		return 0, 0, fmt.Errorf("invalid timestamp %q", raw)
	}
	values := make([]int64, 8)
	for i := range values {
		value, err := strconv.ParseInt(match[i+1], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid timestamp %q", raw)
		}
		values[i] = value
	}
	start, err := duration(values[0], values[1], values[2], values[3])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid timestamp %q: %w", raw, err)
	}
	end, err := duration(values[4], values[5], values[6], values[7])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid timestamp %q: %w", raw, err)
	}
	return start, end, nil
}

func duration(hours, minutes, seconds, milliseconds int64) (time.Duration, error) {
	if minutes > 59 || seconds > 59 || milliseconds > 999 {
		return 0, fmt.Errorf("timestamp component out of range")
	}
	const maxHours = int64((1<<63 - 1) / time.Hour)
	if hours > maxHours {
		return 0, fmt.Errorf("timestamp is too large")
	}
	return time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute +
		time.Duration(seconds)*time.Second + time.Duration(milliseconds)*time.Millisecond, nil
}

// Format writes cues in canonical SRT form. Cues without an index are
// numbered by position.
func Format(w io.Writer, cues []domain.Cue) error {
	buf := bufio.NewWriter(w)
	for i, cue := range cues {
		if cue.Index <= 0 {
			cue.Index = i + 1
		}
		if cue.Start < 0 || cue.End < 0 {
			return fmt.Errorf("cue %d: negative timestamp", cue.Index)
		}
		if _, err := fmt.Fprintf(buf, "%d\n%s --> %s\n%s\n\n", cue.Index, formatTimestamp(cue.Start), formatTimestamp(cue.End), cue.Text); err != nil {
			return fmt.Errorf("write cue %d: %w", cue.Index, err)
		}
	}
	if err := buf.Flush(); err != nil {
		return fmt.Errorf("write SRT: %w", err)
	}
	return nil
}

func formatTimestamp(value time.Duration) string {
	total := value / time.Millisecond
	hours := total / (60 * 60 * 1000)
	minutes := (total / (60 * 1000)) % 60
	seconds := (total / 1000) % 60
	milliseconds := total % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, minutes, seconds, milliseconds)
}
