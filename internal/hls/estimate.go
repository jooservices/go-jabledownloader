package hls

import (
	"strconv"
	"strings"
)

// assumedVideoBitrate is used to estimate download sizes from a video's
// duration before downloading. Jable streams are typically 720p–1080p H.264,
// roughly 2–5 Mbps; 3 Mbps is a reasonable middle ground.
const assumedVideoBitrate = 3_000_000 // bits per second

// ParseDurationSeconds parses "h:mm:ss", "mm:ss" or "ss" into seconds.
// Returns 0 when the value cannot be parsed.
func ParseDurationSeconds(d string) int64 {
	parts := strings.Split(strings.TrimSpace(d), ":")
	if len(parts) == 0 || len(parts) > 3 {
		return 0
	}
	var total int64
	for _, p := range parts {
		if p == "" {
			return 0
		}
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 {
			return 0
		}
		total = total*60 + n
	}
	return total
}

// EstimateVideoBytes guesses the download size of a video from its duration
// label (e.g. "1:23:45"), or returns 0 when the duration is unknown.
func EstimateVideoBytes(duration string) int64 {
	secs := ParseDurationSeconds(duration)
	if secs <= 0 {
		return 0
	}
	return secs * assumedVideoBitrate / 8
}
