package scraper

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var (
	hlsURLRe  = regexp.MustCompile(`var\s+hlsUrl\s*=\s*'([^']+)'`)
	videoIDRe = regexp.MustCompile(`videoId\s*[:=]\s*'(\d+)'`)
)

// VideoInfo is a fully resolved video page.
type VideoInfo struct {
	Code    string
	Title   string
	HLSURL  string
	VideoID string
}

// VideoEntry is one result row in a listing page.
type VideoEntry struct {
	Code     string
	Title    string
	URL      string
	Duration string
}

// Client wraps the site behind a Fetcher.
type Client struct {
	fetcher Fetcher
}

// NewClient builds a site client on top of a HTML fetcher.
func NewClient(fetcher Fetcher) *Client {
	return &Client{fetcher: fetcher}
}

// FetchVideoInfo loads a video page and extracts code, title and HLS URL.
func (c *Client) FetchVideoInfo(ctx context.Context, videoURL string) (*VideoInfo, error) {
	htmlContent, err := c.fetcher.FetchHTML(ctx, videoURL, FetchHLS)
	if err != nil {
		return nil, fmt.Errorf("fetch page: %w", err)
	}

	info := &VideoInfo{}

	if m := hlsURLRe.FindStringSubmatch(htmlContent); len(m) >= 2 {
		info.HLSURL = m[1]
	}
	if m := videoIDRe.FindStringSubmatch(htmlContent); len(m) >= 2 {
		info.VideoID = m[1]
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	title := doc.Find("title").First().Text()
	info.Title = strings.TrimSpace(title)
	if idx := strings.Index(info.Title, " - Jable.TV"); idx >= 0 {
		info.Title = strings.TrimSpace(info.Title[:idx])
	}

	canonical, exists := doc.Find(`link[rel="canonical"]`).Attr("href")
	if exists {
		parts := strings.Split(strings.Trim(canonical, "/"), "/")
		if len(parts) > 0 {
			info.Code = parts[len(parts)-1]
		}
	}
	if info.Code == "" {
		parts := strings.Split(strings.TrimRight(videoURL, "/"), "/")
		if len(parts) > 0 {
			info.Code = parts[len(parts)-1]
		}
	}

	if info.HLSURL == "" {
		return nil, fmt.Errorf("no HLS URL found on page")
	}

	return info, nil
}

// ResolveInput maps a CLI argument to a video page URL, accepting either a
// full URL or a bare video code such as "jur-827".
func ResolveInput(input string) (string, error) {
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		return input, nil
	}
	if codeRe.MatchString(input) {
		return BaseURL + "/videos/" + input + "/", nil
	}
	return "", fmt.Errorf("invalid input: provide a full URL or a video code (e.g. jur-827)")
}

// CodeFromURL extracts the video code from a Jable video page URL.
func CodeFromURL(videoURL string) string {
	m := videoLinkRe.FindStringSubmatch(videoURL)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
