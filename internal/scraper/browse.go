package scraper

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var (
	videoLinkRe = regexp.MustCompile(`/videos/([^/]+)/`)
	codeRe      = regexp.MustCompile(`(?i)[a-z]+-\d+`)
)

// LatestVideos returns the latest-updates listing.
func (c *Client) LatestVideos(ctx context.Context, page int) ([]VideoEntry, error) {
	return c.fetchBrowsePage(ctx, fmt.Sprintf("%s/latest-updates/?page=%d", BaseURL, page))
}

// HotVideos returns the hot listing.
func (c *Client) HotVideos(ctx context.Context, page int) ([]VideoEntry, error) {
	return c.fetchBrowsePage(ctx, fmt.Sprintf("%s/hot/?page=%d", BaseURL, page))
}

// SearchVideos returns search results for query.
func (c *Client) SearchVideos(ctx context.Context, query string, page int) ([]VideoEntry, error) {
	return c.fetchBrowsePage(ctx, fmt.Sprintf("%s/search/%s/?page=%d", BaseURL, query, page))
}

func (c *Client) fetchBrowsePage(ctx context.Context, url string) ([]VideoEntry, error) {
	htmlContent, err := c.fetcher.FetchHTML(ctx, url)
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
func extractVideosFromDoc(doc *goquery.Document) []VideoEntry {
	var entries []VideoEntry
	seen := make(map[string]bool)

	doc.Find(".video-img-box").Each(func(_ int, s *goquery.Selection) {
		link := s.Find("a[href*='/videos/']").First()
		href, exists := link.Attr("href")
		if !exists {
			return
		}

		m := videoLinkRe.FindStringSubmatch(href)
		if len(m) < 2 {
			return
		}
		code := m[1]
		if seen[code] {
			return
		}
		seen[code] = true

		entry := VideoEntry{
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

		entries = append(entries, entry)
	})

	return entries
}
