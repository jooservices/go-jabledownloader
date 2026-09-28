// Package eporner adapts EPORNER to the site.Site contract. Video pages are
// server-rendered, so a plain HTTP fetcher is enough; the direct MP4 links
// (/dload/...) map to progressive sources.
package eporner

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/site"
)

// BaseURL is the site root used to absolutize dload links.
const BaseURL = "https://www.eporner.com"

var (
	videoIDRe       = regexp.MustCompile(`/video-([A-Za-z0-9]+)/`)
	fullVideoPathRe = regexp.MustCompile(`^/video-([A-Za-z0-9]+)/[^/]+/$`)
	dloadRe         = regexp.MustCompile(`/dload/[^/]+/(\d+)/([^"]+?)-(\d+)p(-av1)?\.mp4`)
	durationRe      = regexp.MustCompile(`\b\d{1,3}:\d{2}\b`)
	views           = []string{"latest", "all", "most-viewed", "top-rated"}
	viewPaths       = map[string]string{"latest": "/", "all": "/cat/all/", "most-viewed": "/most-viewed/", "top-rated": "/top-rated/"}
)

// referer is sent with page and media requests; the CDN requires it.
var referer = http.Header{"Referer": {BaseURL + "/"}}

func init() {
	site.Register(site.Descriptor{
		Name: "eporner", Hosts: []string{"eporner.com"},
		Fetcher: site.FetcherHTTP, Views: views, Headers: referer,
		New: func(f site.Fetcher) site.Site { return NewClient(f) },
	})
}

// Client adapts EPORNER to the site.Site contract.
type Client struct {
	fetcher site.Fetcher
}

var _ site.Site = (*Client)(nil)

// NewClient builds the EPORNER site client on top of a HTML fetcher.
func NewClient(fetcher site.Fetcher) *Client {
	return &Client{fetcher: fetcher}
}

// Name returns the site identifier.
func (c *Client) Name() string {
	return "eporner"
}

// Detail resolves ref (a full EPORNER video URL) into the video detail. Bare
// codes are not supported for EPORNER.
func (c *Client) Detail(ctx context.Context, ref string) (*domain.Detail, error) {
	if !strings.HasPrefix(ref, "https://") {
		return nil, fmt.Errorf("EPORNER requires a full HTTPS video URL (e.g. %s/video-1XrYk0gaMpV/daisy-f-x/)", BaseURL)
	}
	u, err := url.Parse(ref)
	if err != nil || !site.HostMatches(u.Hostname(), "eporner.com") {
		return nil, fmt.Errorf("unsupported EPORNER URL: %s", ref)
	}
	if !fullVideoPathRe.MatchString(u.Path) {
		return nil, fmt.Errorf("unsupported EPORNER URL: %s (expected /video-<id>/<slug>/)", ref)
	}
	return c.fetchInfo(ctx, ref)
}

// List returns one EPORNER listing page. Empty selects the newest videos.
func (c *Client) List(ctx context.Context, opts site.ListOptions) ([]domain.Item, error) {
	view, err := site.CheckView(c.Name(), views, opts.View)
	if err != nil {
		return nil, err
	}
	return c.fetchBrowsePage(ctx, pageURL(viewPaths[view], opts.Page))
}

// Search returns video search results for keyword. EPORNER publishes this
// query URL in its SearchAction metadata; a plain GET keeps the fetcher
// contract read-only and testable.
func (c *Client) Search(ctx context.Context, keyword string, page int) ([]domain.Item, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, fmt.Errorf("search keyword is required")
	}
	path := "/search?" + url.Values{"q": []string{keyword}}.Encode()
	return c.fetchBrowsePage(ctx, pageURL(path, page))
}

func pageURL(path string, page int) string {
	u, err := url.Parse(BaseURL + path)
	if err != nil {
		return BaseURL + path
	}
	if page > 1 {
		query := u.Query()
		query.Set("page", strconv.Itoa(page))
		u.RawQuery = query.Encode()
	}
	return u.String()
}

func (c *Client) fetchBrowsePage(ctx context.Context, pageURL string) ([]domain.Item, error) {
	htmlContent, err := c.fetcher.FetchHTML(ctx, pageURL, site.FetchReady)
	if err != nil {
		return nil, fmt.Errorf("fetch listing: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("parse listing html: %w", err)
	}
	return extractBrowseItems(doc), nil
}

func extractBrowseItems(doc *goquery.Document) []domain.Item {
	items := make([]domain.Item, 0)
	seen := make(map[string]struct{})
	doc.Find(`a[href*="/video-"]`).Each(func(_ int, link *goquery.Selection) {
		href, ok := link.Attr("href")
		if !ok {
			return
		}
		code := videoID(href)
		if code == "" {
			return
		}
		if _, exists := seen[code]; exists {
			return
		}
		seen[code] = struct{}{}

		card := link.ParentsFiltered("article, li, .video-item, .video-box, .mb").First()
		if card.Length() == 0 {
			card = link.Parent()
		}
		title := firstNonEmpty(
			attr(link, "title"),
			attr(card.Find("img").First(), "alt"),
			strings.TrimSpace(link.Text()),
		)
		if title == "" {
			title = code
		}
		thumbnail := firstNonEmpty(
			attr(card.Find("img").First(), "data-src"),
			attr(card.Find("img").First(), "data-original"),
			attr(card.Find("img").First(), "src"),
		)
		items = append(items, domain.Item{
			Site:         "eporner",
			Code:         code,
			Title:        title,
			URL:          absoluteURL(href),
			ThumbnailURL: thumbnail,
			Duration:     durationRe.FindString(card.Text()),
		})
	})
	return items
}

func absoluteURL(href string) string {
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	return BaseURL + "/" + strings.TrimLeft(href, "/")
}

func attr(s *goquery.Selection, name string) string {
	value, _ := s.Attr(name)
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// fetchInfo loads a video page and extracts the direct MP4 download sources.
func (c *Client) fetchInfo(ctx context.Context, videoURL string) (*domain.Detail, error) {
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

	info := &domain.Detail{Site: c.Name(), Code: code}

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
		info.Sources = append(info.Sources, domain.Source{
			Kind:    domain.SourceProgressive,
			URL:     BaseURL + m[0],
			Codec:   codec,
			Height:  height,
			Headers: referer.Clone(),
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
