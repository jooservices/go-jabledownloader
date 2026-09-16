package jable

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/jooservices/go-jabledownloader/internal/site"
)

var (
	hlsURLRe  = regexp.MustCompile(`var\s+hlsUrl\s*=\s*'([^']+)'`)
	videoIDRe = regexp.MustCompile(`videoId\s*[:=]\s*'(\d+)'`)
	codeRe    = regexp.MustCompile(`(?i)[a-z]+-\d+`)
)

func init() {
	site.Register("jable", []string{"jable.tv"}, codeRe,
		func(f site.Fetcher) site.Site { return NewClient(f) })
}

// Client adapts Jable.TV to the site.Site contract.
type Client struct {
	fetcher site.Fetcher
}

// NewClient builds the Jable site client on top of a HTML fetcher.
func NewClient(fetcher site.Fetcher) *Client {
	return &Client{fetcher: fetcher}
}

// Name returns the site identifier.
func (c *Client) Name() string {
	return "jable"
}

// ResolveInput maps a CLI argument to a video page URL, accepting either a
// full Jable URL or a bare video code such as "jur-827".
func (c *Client) ResolveInput(_ context.Context, input string) (string, error) {
	return ResolveInput(input)
}

// FetchInfo loads a video page and extracts code, title and the HLS source.
func (c *Client) FetchInfo(ctx context.Context, videoURL string) (*site.VideoInfo, error) {
	htmlContent, err := c.fetcher.FetchHTML(ctx, videoURL, site.FetchHLS)
	if err != nil {
		return nil, fmt.Errorf("fetch page: %w", err)
	}

	info := &site.VideoInfo{}

	if m := hlsURLRe.FindStringSubmatch(htmlContent); len(m) >= 2 && strings.TrimSpace(m[1]) != "" {
		info.Sources = append(info.Sources, site.Source{Kind: site.SourceHLS, URL: strings.TrimSpace(m[1])})
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

	if len(info.Sources) == 0 {
		return nil, fmt.Errorf("no HLS URL found on page")
	}

	return info, nil
}

// ResolveInput maps a CLI argument to a video page URL, accepting either a
// full Jable URL or a bare video code such as "jur-827".
func ResolveInput(input string) (string, error) {
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		if CodeFromURL(input) == "" {
			return "", fmt.Errorf("unsupported Jable URL: %s", input)
		}
		return input, nil
	}
	if codeRe.MatchString(input) {
		return BaseURL + "/videos/" + input + "/", nil
	}
	return "", fmt.Errorf("invalid input: provide a full Jable URL or a video code (e.g. jur-827)")
}

// CodeFromURL extracts the video code from a Jable video page URL.
// Non-Jable hosts and malformed codes return an empty string.
func CodeFromURL(videoURL string) string {
	u, err := url.Parse(videoURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host != "jable.tv" && !strings.HasSuffix(host, ".jable.tv") {
		return ""
	}
	m := videoLinkRe.FindStringSubmatch(u.Path)
	if len(m) < 2 || !codeRe.MatchString(m[1]) {
		return ""
	}
	return m[1]
}
