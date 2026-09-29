package jable

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/site"
)

var videoLinkRe = regexp.MustCompile(`/videos/([^/]+)/`)

// viewPaths maps list views to Jable listing paths.
var viewPaths = map[string]string{"latest": "latest-updates", "hot": "hot"}

// List returns a listing page as general-info items.
func (c *Client) List(ctx context.Context, opts site.ListOptions) ([]domain.Item, error) {
	view, err := site.CheckView(c.Name(), views, opts.View)
	if err != nil {
		return nil, err
	}
	return c.fetchBrowsePage(ctx, fmt.Sprintf("%s/%s/?page=%d", BaseURL, viewPaths[view], pageOrOne(opts.Page)))
}

// Search returns search results for query. Query parameters live in the URL.
func (c *Client) Search(ctx context.Context, query string, page int) ([]domain.Item, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("search keyword is required")
	}
	return c.fetchBrowsePage(ctx, fmt.Sprintf("%s/search/%s/?page=%d",
		BaseURL, url.PathEscape(query), pageOrOne(page)))
}

func (c *Client) fetchBrowsePage(ctx context.Context, pageURL string) ([]domain.Item, error) {
	htmlContent, err := c.fetcher.FetchHTML(ctx, pageURL, site.FetchReady)
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}
	return extractVideosFromDoc(doc), nil
}

// extractVideosFromDoc pulls video entries out of a listing page.
func extractVideosFromDoc(doc *goquery.Document) []domain.Item {
	var entries []domain.Item
	seen := make(map[string]bool)

	doc.Find(".video-img-box").Each(func(_ int, s *goquery.Selection) {
		link := s.Find("a[href*='/videos/']").First()
		href, exists := link.Attr("href")
		if !exists {
			return
		}

		m := videoLinkRe.FindStringSubmatch(href)
		if len(m) < 2 || !codeRe.MatchString(m[1]) {
			return
		}
		code := m[1]
		if seen[code] {
			return
		}
		seen[code] = true

		entry := domain.Item{
			Site: "jable",
			Code: code,
			URL:  BaseURL + "/videos/" + code + "/",
		}

		titleLink := s.Find("h6.title a").First()
		if titleLink.Length() > 0 {
			entry.Title = strings.TrimSpace(titleLink.Text())
		}
		if entry.Title == "" {
			entry.Title = code
		}

		duration := s.Find(".label").First()
		if duration.Length() > 0 {
			entry.Duration = strings.TrimSpace(duration.Text())
		}
		if image := s.Find("img").First(); image.Length() > 0 {
			entry.ThumbnailURL = firstAttr(image, "data-src", "data-original", "src")
		}

		entries = append(entries, entry)
	})

	return entries
}

func pageOrOne(page int) int {
	if page < 1 {
		return 1
	}
	return page
}

func firstAttr(s *goquery.Selection, names ...string) string {
	for _, name := range names {
		if value, ok := s.Attr(name); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
