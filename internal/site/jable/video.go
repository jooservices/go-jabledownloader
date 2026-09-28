package jable

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/site"
)

var (
	hlsURLRe  = regexp.MustCompile(`var\s+hlsUrl\s*=\s*'([^']+)'`)
	videoIDRe = regexp.MustCompile(`videoId\s*[:=]\s*'(\d+)'`)
	codeRe    = regexp.MustCompile(`(?i)^[a-z]+-\d+$`)
	views     = []string{"latest", "hot"}
)

func init() {
	site.Register(site.Descriptor{
		Name: "jable", Hosts: []string{"jable.tv"}, CodeRe: codeRe,
		Fetcher: site.FetcherBrowser, Views: views,
		New: func(f site.Fetcher) site.Site { return NewClient(f) },
	})
}

// userAgentProvider is implemented by fetchers (the browser) whose requests
// carry a specific User-Agent. The CDN sees the same agent for media.
type userAgentProvider interface {
	UserAgent() string
}

// Client adapts Jable.TV to the site.Site contract.
type Client struct {
	fetcher site.Fetcher
}

var _ site.Site = (*Client)(nil)

// NewClient builds the Jable site client on top of an HTML fetcher.
func NewClient(fetcher site.Fetcher) *Client {
	return &Client{fetcher: fetcher}
}

// Name returns the site identifier.
func (c *Client) Name() string { return "jable" }

// Detail resolves ref (full URL or bare code such as "jur-827") into the
// video detail with its HLS source.
func (c *Client) Detail(ctx context.Context, ref string) (*domain.Detail, error) {
	videoURL, code, err := resolveInput(ref)
	if err != nil {
		return nil, err
	}
	return c.fetchDetail(ctx, videoURL, code)
}

// fetchDetail loads a video page and extracts its detail; code is the code
// requested by the caller, used when the page offers no valid canonical one.
func (c *Client) fetchDetail(ctx context.Context, videoURL, code string) (*domain.Detail, error) {
	htmlContent, err := c.fetcher.FetchHTML(ctx, videoURL, site.FetchHLS)
	if err != nil {
		return nil, fmt.Errorf("fetch page: %w", err)
	}
	m := hlsURLRe.FindStringSubmatch(htmlContent)
	if len(m) < 2 || strings.TrimSpace(m[1]) == "" {
		return nil, fmt.Errorf("no HLS URL found on page")
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	info := &domain.Detail{
		Site:    c.Name(),
		Code:    canonicalCode(doc, code),
		Title:   pageTitle(doc),
		Sources: []domain.Source{{Kind: domain.SourceHLS, URL: strings.TrimSpace(m[1]), Headers: c.mediaHeaders()}},
	}
	if m := videoIDRe.FindStringSubmatch(htmlContent); len(m) >= 2 {
		info.VideoID = m[1]
	}
	return info, nil
}

func (c *Client) mediaHeaders() http.Header {
	headers := http.Header{"Referer": {BaseURL + "/"}}
	if p, ok := c.fetcher.(userAgentProvider); ok && p.UserAgent() != "" {
		headers.Set("User-Agent", p.UserAgent())
	}
	return headers
}

func pageTitle(doc *goquery.Document) string {
	title := strings.TrimSpace(doc.Find("title").First().Text())
	if before, _, found := strings.Cut(title, " - Jable.TV"); found {
		title = strings.TrimSpace(before)
	}
	return title
}

// canonicalCode prefers the page's canonical code, but only when it is a
// well-formed code: the page is untrusted and the code becomes a path.
func canonicalCode(doc *goquery.Document, requested string) string {
	canonical, ok := doc.Find(`link[rel="canonical"]`).Attr("href")
	if !ok {
		return requested
	}
	parts := strings.Split(strings.Trim(canonical, "/"), "/")
	if last := parts[len(parts)-1]; codeRe.MatchString(last) {
		return last
	}
	return requested
}

// resolveInput maps a CLI argument (full Jable URL or bare code) to the
// video page URL and the requested code.
func resolveInput(input string) (string, string, error) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		code := CodeFromURL(input)
		if code == "" {
			return "", "", fmt.Errorf("unsupported Jable URL: %s", input)
		}
		return input, code, nil
	}
	if codeRe.MatchString(input) {
		return BaseURL + "/videos/" + input + "/", input, nil
	}
	return "", "", fmt.Errorf("invalid input: provide a full Jable URL or a video code (e.g. jur-827)")
}

// CodeFromURL extracts the video code from a Jable video page URL.
// Non-Jable hosts and malformed codes return an empty string.
func CodeFromURL(videoURL string) string {
	u, err := url.Parse(videoURL)
	if err != nil || !site.HostMatches(u.Hostname(), "jable.tv") {
		return ""
	}
	m := videoLinkRe.FindStringSubmatch(u.Path)
	if len(m) < 2 || !codeRe.MatchString(m[1]) {
		return ""
	}
	return m[1]
}
