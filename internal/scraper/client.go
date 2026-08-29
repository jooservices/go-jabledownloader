// Package scraper adapts the Jable.TV site into the domain types the app
// works with. It wraps chromedp (Cloudflare bypass) and goquery (parsing).
// The HTML source is behind the Fetcher interface so tests can inject
// fixture content instead of launching Chrome.
package scraper

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
)

// BaseURL is the site root.
const BaseURL = "https://en.jable.tv"

// userAgent mirrors a desktop Chrome build.
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// Fetcher returns the rendered HTML of a page. Browser implements it with
// Chrome; tests implement it with fixtures.
type Fetcher interface {
	FetchHTML(ctx context.Context, url string) (string, error)
}

// Browser launches and drives a headless Chrome instance.
type Browser struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc
}

// NewBrowser starts Chrome with the flags required to bypass Cloudflare,
// verifying availability with a short probe.
func NewBrowser(ctx context.Context) (*Browser, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.UserAgent(userAgent),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)

	testCtx, testCancel := context.WithTimeout(allocCtx, 15*time.Second)
	defer testCancel()
	tabCtx, _ := chromedp.NewContext(testCtx)
	if err := chromedp.Run(tabCtx, chromedp.Navigate("about:blank")); err != nil {
		allocCancel()
		return nil, fmt.Errorf("chrome not available: %w", err)
	}

	return &Browser{allocCtx: allocCtx, allocCancel: allocCancel}, nil
}

// Close releases the browser process.
func (b *Browser) Close() {
	b.allocCancel()
}

// FetchHTML navigates to url and returns the fully rendered HTML, waiting
// for the hlsUrl variable the player injects.
func (b *Browser) FetchHTML(_ context.Context, url string) (string, error) {
	tabCtx, tabCancel := chromedp.NewContext(b.allocCtx)
	defer tabCancel()

	tabCtx, tabCancel = context.WithTimeout(tabCtx, 30*time.Second)
	defer tabCancel()

	var htmlContent string
	err := chromedp.Run(tabCtx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			for i := 0; i < 30; i++ {
				var hasHLS bool
				if err := chromedp.Evaluate(`typeof hlsUrl !== 'undefined' && hlsUrl.length > 0`, &hasHLS).Do(ctx); err == nil && hasHLS {
					return nil
				}
				time.Sleep(500 * time.Millisecond)
			}
			return nil
		}),
		chromedp.OuterHTML("html", &htmlContent),
	)
	if err != nil {
		return "", fmt.Errorf("chromedp: %w — hint: Cloudflare may be blocking or the site is slow; make sure Chrome is up to date, or retry later", err)
	}
	return htmlContent, nil
}
