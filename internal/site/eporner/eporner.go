// Package eporner adapts EPORNER to the site.Site contract. Video pages are
// server-rendered, so a plain HTTP fetcher is enough; the direct MP4 links
// (/dload/...) map to site.Source values the direct engine downloads.
package eporner

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/jooservices/go-jabledownloader/internal/site"
)

// BaseURL is the site root used to absolutize dload links.
const BaseURL = "https://www.eporner.com"

var (
	videoIDRe       = regexp.MustCompile(`/video-([A-Za-z0-9]+)/`)
	fullVideoPathRe = regexp.MustCompile(`^/video-([A-Za-z0-9]+)/[^/]+/$`)
	dloadRe         = regexp.MustCompile(`/dload/[^/]+/(\d+)/([^"]+?)-(\d+)p(-av1)?\.mp4`)
)

func init() {
	site.Register("eporner", []string{"eporner.com"}, nil,
		func(f site.Fetcher) site.Site { return NewClient(f) })
}

// Client adapts EPORNER to the site.Site contract.
type Client struct {
	fetcher site.Fetcher
}

// NewClient builds the EPORNER site client on top of a HTML fetcher.
func NewClient(fetcher site.Fetcher) *Client {
	return &Client{fetcher: fetcher}
}

// Name returns the site identifier.
func (c *Client) Name() string {
	return "eporner"
}

// ResolveInput accepts a full EPORNER video URL only (no bare codes).
func (c *Client) ResolveInput(_ context.Context, input string) (string, error) {
	if !strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://") {
		return "", fmt.Errorf("EPORNER requires a full video URL (e.g. %s/video-1XrYk0gaMpV/daisy-f-x/)", BaseURL)
	}
	u, err := url.Parse(input)
	if err != nil || !site.HostMatches(u.Hostname(), "eporner.com") {
		return "", fmt.Errorf("unsupported EPORNER URL: %s", input)
	}
	if !fullVideoPathRe.MatchString(u.Path) {
		return "", fmt.Errorf("unsupported EPORNER URL: %s (expected /video-<id>/<slug>/)", input)
	}
	return input, nil
}

// FetchInfo loads a video page and extracts the direct MP4 download sources.
func (c *Client) FetchInfo(ctx context.Context, videoURL string) (*site.VideoInfo, error) {
	htmlContent, err := c.fetcher.FetchHTML(ctx, videoURL, site.FetchReady)
	if err != nil {
		return nil, fmt.Errorf("fetch page: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	code := videoID(videoURL)
	if code == "" {
		return nil, fmt.Errorf("no EPORNER video id found on page")
	}

	info := &site.VideoInfo{Code: code}

	title := strings.TrimSpace(doc.Find("title").First().Text())
	if idx := strings.Index(title, " - EPORNER"); idx >= 0 {
		title = strings.TrimSpace(title[:idx])
	}
	info.Title = title

	seen := make(map[string]bool)
	doc.Find("a[href*='/dload/']").Each(func(_ int, s *goquery.Selection) {
		href, ok := s.Attr("href")
		if !ok {
			return
		}
		m := dloadRe.FindStringSubmatch(href)
		if len(m) < 4 {
			return
		}
		height, err := strconv.Atoi(m[1])
		if err != nil || height <= 0 {
			return
		}
		codec := "h264"
		if m[4] == "-av1" {
			codec = "av1"
		}
		key := strconv.Itoa(height) + ":" + codec
		if seen[key] {
			return
		}
		seen[key] = true
		info.Sources = append(info.Sources, site.Source{
			Kind:   site.SourceDirect,
			URL:    BaseURL + m[0],
			Codec:  codec,
			Height: height,
		})
	})

	if len(info.Sources) == 0 {
		return nil, fmt.Errorf("no downloadable sources found on page")
	}

	sort.Slice(info.Sources, func(i, j int) bool {
		if info.Sources[i].Height != info.Sources[j].Height {
			return info.Sources[i].Height < info.Sources[j].Height
		}
		if info.Sources[i].Codec == info.Sources[j].Codec {
			return false
		}
		// Prefer h264 over av1 at equal height.
		return info.Sources[i].Codec == "h264"
	})

	return info, nil
}

func videoID(path string) string {
	m := videoIDRe.FindStringSubmatch(path)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}
