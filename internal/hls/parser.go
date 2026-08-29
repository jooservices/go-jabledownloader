// Package hls implements HLS playlist parsing, variant resolution, segment
// downloading, and mp4 concatenation. It is a pure engine: it imports no UI,
// no config, and no telemetry. Observability is wired in by the caller via
// the Progress callback.
package hls

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// Segment is a single media segment of a media playlist.
type Segment struct {
	URL      string
	Duration float64
}

// Playlist is a parsed media playlist.
type Playlist struct {
	Segments       []Segment
	TargetDuration float64
	Encrypted      bool
	KeyURI         string
}

// ParsePlaylist parses a media playlist body. baseURL is used to resolve
// relative segment URLs.
func ParsePlaylist(content string, baseURL string) (*Playlist, error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	pl := &Playlist{}

	var currentDuration float64

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#EXTM3U") {
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-TARGETDURATION:") {
			val := strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:")
			if d, err := strconv.ParseFloat(val, 64); err == nil {
				pl.TargetDuration = d
			}
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			return nil, fmt.Errorf("master playlist detected, resolve quality first")
		}

		if strings.HasPrefix(line, "#EXT-X-KEY:") {
			pl.Encrypted = true
			if idx := strings.Index(line, `URI="`); idx >= 0 {
				start := idx + 5
				if end := strings.Index(line[start:], `"`); end >= 0 {
					pl.KeyURI = line[start : start+end]
				}
			}
			continue
		}

		if strings.HasPrefix(line, "#EXTINF:") {
			val := strings.TrimPrefix(line, "#EXTINF:")
			if idx := strings.Index(val, ","); idx >= 0 {
				val = val[:idx]
			}
			if d, err := strconv.ParseFloat(val, 64); err == nil {
				currentDuration = d
			}
			continue
		}

		if strings.HasPrefix(line, "#") {
			currentDuration = 0
			continue
		}

		segURL, err := resolveSegmentURL(baseURL, line)
		if err != nil {
			continue
		}
		pl.Segments = append(pl.Segments, Segment{URL: segURL, Duration: currentDuration})
		currentDuration = 0
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan playlist: %w", err)
	}

	if len(pl.Segments) == 0 {
		return nil, fmt.Errorf("no segments found in playlist")
	}

	return pl, nil
}
