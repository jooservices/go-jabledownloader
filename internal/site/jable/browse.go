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

var videoLinkRe = regexp.MustCompile(`/videos/([^/]+)/`)

// Latest returns the latest-updates listing.
func (c *Client) Latest(ctx context.Context, page int) ([]site.VideoEntry, error) {
	return c.fetchBrowsePage(ctx, fmt.Sprintf("%s/latest-updates/?page=%d", BaseURL, page))
}

// Hot returns the hot listing.
func (c *Client) Hot(ctx context.Context, page int) ([]site.VideoEntry, error) {
	return c.fetchBrowsePage(ctx, fmt.Sprintf("%s/hot/?page=%d", BaseURL, page))
}

// Search returns search results for query.
func (c *Client) Search(ctx context.Context, query string, page int) ([]site.VideoEntry, error) {
	return c.fetchBrowsePage(ctx, fmt.Sprintf("%s/search/%s/?page=%d", BaseURL, url.PathEscape(query), page))
}

func (c *Client) fetchBrowsePage(ctx context.Context, pageURL string) ([]site.VideoEntry, error) {
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
func extractVideosFromDoc(doc *goquery.Document) []site.VideoEntry {
	var entries []site.VideoEntry
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

		entry := site.VideoEntry{
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
