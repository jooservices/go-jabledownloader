// Package javphotos adapts Jav Photos (jav.photos) to the site contracts.
// It hosts photo galleries only: listings and searches return gallery rows,
// and Gallery resolves a gallery page into its full-size photos. Pages are
// server-rendered, so a plain HTTP fetcher is enough.
package javphotos

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/site"
)

// BaseURL is the site root.
const BaseURL = "https://jav.photos"

var (
	// galleryPathRe matches /free/<slug>; numeric slugs are listing pages.
	galleryPathRe = regexp.MustCompile(`^/free/([A-Za-z0-9][A-Za-z0-9-]*)/?$`)
	digitsRe      = regexp.MustCompile(`^\d+$`)
	nonSlugRe     = regexp.MustCompile(`[^a-z0-9]+`)
	views         = []string{"latest"}
	// referer satisfies the image host's hotlink protection.
	referer = http.Header{"Referer": {BaseURL + "/"}}
)

func init() {
	site.Register(site.Descriptor{
		Name: "javphotos", Hosts: []string{"jav.photos"},
		Fetcher: site.FetcherHTTP, Views: views, Headers: referer,
		New: func(f site.Fetcher) site.Site { return NewClient(f) },
	})
}

// Client adapts Jav Photos to site.Site and site.GallerySite.
type Client struct {
	fetcher site.Fetcher
}

var (
	_ site.Site        = (*Client)(nil)
	_ site.GallerySite = (*Client)(nil)
)

// NewClient builds the Jav Photos client on top of an HTML fetcher.
func NewClient(fetcher site.Fetcher) *Client {
	return &Client{fetcher: fetcher}
}

// Name returns the site identifier.
func (c *Client) Name() string { return "javphotos" }

// Detail is unsupported: Jav Photos has no videos.
func (c *Client) Detail(context.Context, string) (*domain.Detail, error) {
	return nil, site.ErrNotVideo
}

// List returns the latest galleries; page 1 is the home page.
func (c *Client) List(ctx context.Context, opts site.ListOptions) ([]domain.Item, error) {
	if _, err := site.CheckView(c.Name(), views, opts.View); err != nil {
		return nil, err
	}
	pageURL := BaseURL + "/"
	if opts.Page > 1 {
		pageURL = BaseURL + "/free/" + strconv.Itoa(opts.Page)
	}
	return c.fetchGalleries(ctx, pageURL)
}

// Search returns the galleries of a model. The site's search form
// redirects to /free/<model-slug>, which is fetched directly so paging works.
func (c *Client) Search(ctx context.Context, keyword string, page int) ([]domain.Item, error) {
	slug := strings.Trim(nonSlugRe.ReplaceAllString(strings.ToLower(keyword), "-"), "-")
	if slug == "" || digitsRe.MatchString(slug) {
		return nil, fmt.Errorf("search keyword must contain letters (a model name such as \"yui hatano\")")
	}
	pageURL := BaseURL + "/free/" + slug
	if page > 1 {
		pageURL += "/" + strconv.Itoa(page)
	}
	return c.fetchGalleries(ctx, pageURL)
}

// Gallery resolves a gallery URL into its photos at the largest size.
func (c *Client) Gallery(ctx context.Context, ref string) (*domain.Gallery, error) {
	code, err := galleryCode(ref)
	if err != nil {
		return nil, err
	}
	doc, err := c.fetch(ctx, ref)
	if err != nil {
		return nil, err
	}
	gallery := &domain.Gallery{Site: c.Name(), Code: code, Title: pageTitle(doc)}
	seen := make(map[string]bool)
	doc.Find(`div.pinbox.pinimg > a[href^="/pictures/"]`).Each(func(_ int, link *goquery.Selection) {
		href, _ := link.Attr("href")
		if seen[href] {
			return
		}
		seen[href] = true
		thumb, _ := link.Find("img").First().Attr("src")
		gallery.Photos = append(gallery.Photos, domain.Photo{
			ID:           strings.TrimSuffix(path.Base(href), path.Ext(href)),
			URL:          BaseURL + href,
			ThumbnailURL: absoluteURL(thumb),
			Headers:      referer.Clone(),
		})
	})
	if len(gallery.Photos) == 0 {
		return nil, fmt.Errorf("no photos on %s (model pages list galleries; use search)", ref)
	}
	return gallery, nil
}

// fetchGalleries reads the gallery cards (div.pinbox without .pinimg) of a
// listing, search, or model page.
func (c *Client) fetchGalleries(ctx context.Context, pageURL string) ([]domain.Item, error) {
	doc, err := c.fetch(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	items := make([]domain.Item, 0)
	seen := make(map[string]bool)
	doc.Find("div.pinbox").Not(".pinimg").Each(func(_ int, card *goquery.Selection) {
		link := card.ChildrenFiltered(`a[href^="/free/"]`).First()
		href, _ := link.Attr("href")
		m := galleryPathRe.FindStringSubmatch(href)
		if m == nil || digitsRe.MatchString(m[1]) || seen[m[1]] {
			return
		}
		seen[m[1]] = true
		thumb, _ := link.Find("img").First().Attr("src")
		title := strings.TrimSpace(link.Find("p").First().Text())
		if title == "" {
			title = m[1]
		}
		items = append(items, domain.Item{
			Site: c.Name(), Code: m[1], Title: title,
			URL: BaseURL + "/free/" + m[1], ThumbnailURL: absoluteURL(thumb),
		})
	})
	return items, nil
}

func (c *Client) fetch(ctx context.Context, pageURL string) (*goquery.Document, error) {
	html, err := c.fetcher.FetchHTML(ctx, pageURL, site.FetchReady)
	if err != nil {
		return nil, fmt.Errorf("fetch page: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}
	return doc, nil
}

// galleryCode validates a gallery URL and returns its slug.
func galleryCode(ref string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !site.HostMatches(u.Hostname(), "jav.photos") {
		return "", fmt.Errorf("jav.photos needs a gallery URL such as %s/free/<gallery>", BaseURL)
	}
	m := galleryPathRe.FindStringSubmatch(u.Path)
	if m == nil || digitsRe.MatchString(m[1]) {
		return "", fmt.Errorf("unsupported Jav Photos URL %s (expected /free/<gallery>)", ref)
	}
	return m[1], nil
}

// pageTitle strips the site's "Jav Photos Free … HD Porn Pics Gallery"
// wrapper from <title>.
func pageTitle(doc *goquery.Document) string {
	title := strings.TrimSpace(doc.Find("title").First().Text())
	title = strings.TrimSpace(strings.TrimPrefix(title, "Jav Photos Free"))
	title = strings.TrimSpace(strings.TrimSuffix(title, "HD Porn Pics Gallery"))
	if title == "" {
		return "Untitled"
	}
	return title
}

func absoluteURL(ref string) string {
	if ref == "" || strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	return BaseURL + "/" + strings.TrimLeft(ref, "/")
}
