package hls

import (
	"bufio"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Variant is one quality level of a master playlist.
type Variant struct {
	URL        string
	Bandwidth  int64
	Resolution string
	Codec      string
}

// ResolveMasterPlaylist parses a master playlist and returns the variant
// with the highest bandwidth.
func ResolveMasterPlaylist(content string, baseURL string) (*Variant, error) {
	var variants []Variant
	scanner := bufio.NewScanner(strings.NewReader(content))

	var currentBW int64
	var currentRes string
	var currentCodec string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			currentBW = 0
			currentRes = ""
			currentCodec = ""
			attrs := strings.TrimPrefix(line, "#EXT-X-STREAM-INF:")

			for _, attr := range strings.Split(attrs, ",") {
				attr = strings.TrimSpace(attr)
				if strings.HasPrefix(attr, "BANDWIDTH=") {
					val := strings.TrimPrefix(attr, "BANDWIDTH=")
					if bw, err := strconv.ParseInt(val, 10, 64); err == nil {
						currentBW = bw
					}
				}
				if strings.HasPrefix(attr, "RESOLUTION=") {
					currentRes = strings.TrimPrefix(attr, "RESOLUTION=")
				}
				if strings.HasPrefix(attr, "CODECS=") {
					currentCodec = codecFromAttribute(strings.TrimPrefix(attr, "CODECS="))
				}
			}
			continue
		}

		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		resolvedURL, err := resolveSegmentURL(baseURL, line)
		if err != nil {
			continue
		}

		variants = append(variants, Variant{
			URL:        resolvedURL,
			Bandwidth:  currentBW,
			Resolution: currentRes,
			Codec:      currentCodec,
		})
	}

	if len(variants) == 0 {
		return nil, fmt.Errorf("no variants found in master playlist")
	}

	best := variants[0]
	for _, v := range variants {
		if v.Bandwidth > best.Bandwidth {
			best = v
		}
	}
	if best.Codec == "" {
		best.Codec = "h264"
	}

	return &best, nil
}

// codecFromAttribute maps the quoted CODECS value (e.g. "avc1.640028,mp4a.40.2")
// to a short codec name for filenames. The first entry is the video codec.
func codecFromAttribute(csv string) string {
	raw := strings.Trim(csv, `"`)
	first := raw
	if idx := strings.Index(raw, ","); idx >= 0 {
		first = raw[:idx]
	}

	switch {
	case strings.HasPrefix(first, "avc1"), strings.HasPrefix(first, "avc3"):
		return "h264"
	case strings.HasPrefix(first, "hvc1"), strings.HasPrefix(first, "hev1"):
		return "h265"
	case strings.HasPrefix(first, "av01"):
		return "av1"
	case strings.HasPrefix(first, "vp09"), strings.HasPrefix(first, "vp9"):
		return "vp9"
	default:
		return ""
	}
}

// resolveSegmentURL resolves a possibly relative playlist line against the
// playlist base URL.
func resolveSegmentURL(baseURL, line string) (string, error) {
	if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
		return line, nil
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	rel, err := url.Parse(line)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(rel).String(), nil
}
