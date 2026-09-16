// Package javphotos adapts Jav Photos to the site contracts. Gallery pages
// are server-rendered, so a plain HTTP fetcher is enough; each photo exposes
// its full-resolution /pictures/ URL (the largest available size) plus a
// /pics/ thumbnail. Jav Photos has no video pages, so FetchInfo is
// unsupported and only the gallery capability is exposed.
package javphotos

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jooservices/go-jabledownloader/internal/site"
)

// BaseURL is the site root.
const BaseURL = "https://jav.photos"

var (
	fullGalleryPathRe = regexp.MustCompile(`^/free/([a-zA-Z0-9-]+)$`)
	digitOnlyRe       = regexp.MustCompile(`^\d+$`)
	photoRe           = regexp.MustCompile(`<a[^>]+href="(/pictures/[^"]+)"[^>]*>\s*<img[^>]+src="([^"]+)"`)
)

func init() {
	site.Register("javphotos", []string{"jav.photos"}, nil,
		func(f site.Fetcher) site.Site { return NewClient(f) })
}

// Client adapts Jav Photos to the site.Site and site.GallerySite contracts.
type Client struct {
	fetcher site.Fetcher
}

// NewClient builds the Jav Photos client on top of a HTML fetcher.
func NewClient(fetcher site.Fetcher) *Client {
	return &Client{fetcher: fetcher}
}

// Name returns the site identifier.
func (c *Client) Name() string {
	return "javphotos"
}

// ResolveInput accepts a full Jav Photos gallery URL only (no bare codes).
func (c *Client) ResolveInput(_ context.Context, input string) (string, error) {
	if !strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://") {
		return "", fmt.Errorf("jav.photos requires a full gallery URL (e.g. %s/free/caribbeancom-ai-uehara-elite-3xpl)", BaseURL)
	}
	u, err := url.Parse(input)
	if err != nil || !site.HostMatches(u.Hostname(), "jav.photos") {
		return "", fmt.Errorf("unsupported Jav Photos URL: %s", input)
	}
	m := fullGalleryPathRe.FindStringSubmatch(u.Path)
	if len(m) < 2 || digitOnlyRe.MatchString(m[1]) {
		return "", fmt.Errorf("unsupported Jav Photos URL: %s (expected /free/<slug>)", input)
	}
	return input, nil
}

// FetchGallery parses a gallery page into its full photo set.
func (c *Client) FetchGallery(ctx context.Context, url string) (*site.Gallery, error) {
	htmlContent, err := c.fetcher.FetchHTML(ctx, url, site.FetchReady)
	if err != nil {
		return nil, fmt.Errorf("fetch page: %w", err)
	}

	gallery := &site.Gallery{Code: galleryCode(url)}

	title := cleanTitle(extractTitle(htmlContent))
	gallery.Title = title

	seen := make(map[string]bool)
	for _, m := range photoRe.FindAllStringSubmatch(htmlContent, -1) {
		if len(m) < 3 {
			continue
		}
		imageURL := BaseURL + m[1]
		if seen[imageURL] {
			continue
		}
		seen[imageURL] = true
		gallery.Photos = append(gallery.Photos, site.Photo{
			ID:           photoID(m[1]),
			ImageURL:     imageURL,
			ThumbnailURL: BaseURL + m[2],
		})
	}

	if gallery.Code == "" || len(gallery.Photos) == 0 {
		return nil, fmt.Errorf("no Jav Photos gallery photos found on page")
	}

	return gallery, nil
}

// FetchInfo is unsupported: Jav Photos has no video pages.
func (c *Client) FetchInfo(context.Context, string) (*site.VideoInfo, error) {
	return nil, fmt.Errorf("jav.photos is a photo-gallery site; videos are not supported")
}

func galleryCode(u string) string {
	path, err := url.Parse(u)
	if err != nil {
		return ""
	}
	m := fullGalleryPathRe.FindStringSubmatch(path.Path)
	if len(m) >= 2 && !digitOnlyRe.MatchString(m[1]) {
		return m[1]
	}
	return ""
}

func extractTitle(html string) string {
	m := regexp.MustCompile(`(?i)<title[^>]*>([^<]*)</title>`).FindStringSubmatch(html)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

// cleanTitle strips the "Jav Photos Free " prefix and " HD Porn Pics Gallery"
// suffix from the <title> tag.
func cleanTitle(title string) string {
	title = regexp.MustCompile(`(?i)^Jav Photos Free\s*`).ReplaceAllString(title, "")
	title = regexp.MustCompile(`(?i)\s*HD Porn Pics Gallery\s*$`).ReplaceAllString(title, "")
	title = strings.TrimSpace(title)
	if title == "" {
		return "Untitled"
	}
	return title
}

func photoID(p string) string {
	base := p[strings.LastIndex(p, "/")+1:]
	base = strings.TrimSuffix(base, filepathExt(base))
	return base
}

func filepathExt(name string) string {
	i := strings.LastIndex(name, ".")
	if i < 0 {
		return ""
	}
	return name[i:]
}
