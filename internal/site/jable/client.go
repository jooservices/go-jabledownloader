// Package jable adapts Jable.TV to the site.Site contract. It wraps
// chromedp (Cloudflare bypass) and goquery (parsing); the HTML source is
// behind a site.Fetcher so tests inject fixture content instead of Chrome.
package jable

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/jooservices/go-jabledownloader/internal/site"
)

// BaseURL is the site root.
const BaseURL = "https://en.jable.tv"

// Browser implements site.Fetcher with a headless Chrome instance that
// bypasses Cloudflare and waits for the player to inject its stream URL.
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
	if chromePath := strings.TrimSpace(os.Getenv("CHROME_PATH")); chromePath != "" {
		opts = append(opts, chromedp.ExecPath(chromePath))
	}

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

// FetchHTML navigates to url and returns the fully rendered HTML.
// FetchHLS waits for the player hlsUrl; FetchReady returns after body is ready.
func (b *Browser) FetchHTML(_ context.Context, url string, mode site.FetchMode) (string, error) {
	tabCtx, tabCancel := chromedp.NewContext(b.allocCtx)
	defer tabCancel()

	tabCtx, tabCancel = context.WithTimeout(tabCtx, 30*time.Second)
	defer tabCancel()

	actions := []chromedp.Action{
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
	}
	if mode == site.FetchHLS {
		actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
			for i := 0; i < 30; i++ {
				var hasHLS bool
				if err := chromedp.Evaluate(`typeof hlsUrl !== 'undefined' && hlsUrl.length > 0`, &hasHLS).Do(ctx); err == nil && hasHLS {
					return nil
				}
				time.Sleep(500 * time.Millisecond)
			}
			return nil
		}))
	}

	var htmlContent string
	actions = append(actions, chromedp.OuterHTML("html", &htmlContent))
	if err := chromedp.Run(tabCtx, actions...); err != nil {
		return "", fmt.Errorf("chromedp: %w — hint: Cloudflare may be blocking or the site is slow; make sure Chrome is up to date, or retry later", err)
	}
	return htmlContent, nil
}

// userAgent mirrors a desktop Chrome build.
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
