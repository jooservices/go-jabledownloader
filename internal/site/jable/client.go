// Package jable adapts Jable.TV to the site.Site contract. Pages sit behind
// Cloudflare, so they are fetched with headless Chrome (chromedp) and parsed
// with goquery; tests inject fixture HTML through site.Fetcher instead.
package jable

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"

	"github.com/jooservices/go-jabledownloader/internal/site"
)

// BaseURL is the site root.
const BaseURL = "https://en.jable.tv"

const (
	pageTimeout  = 30 * time.Second // navigation, and separately the challenge wait
	hlsTimeout   = 15 * time.Second
	pollInterval = 500 * time.Millisecond
)

// ErrCloudflareChallenge means the Cloudflare challenge did not clear.
var ErrCloudflareChallenge = errors.New("cloudflare challenge did not clear")

// errPollTimeout is returned by poll when the condition never became true.
var errPollTimeout = errors.New("timed out")

// Browser implements site.Fetcher with one headless Chrome process; each
// fetch opens a tab, so Cloudflare clearance cookies are shared.
type Browser struct {
	allocCancel   context.CancelFunc
	browserCtx    context.Context
	browserCancel context.CancelFunc
	ua            string
	pageTimeout   time.Duration
	// SandboxFallback reports that Chrome only started with --no-sandbox.
	SandboxFallback bool
}

// NewBrowser starts Chrome. The sandbox stays on unless CHROME_NO_SANDBOX=1;
// when a sandboxed launch fails (for example a restricted container) it
// retries once without the sandbox unless CHROME_NO_SANDBOX=0.
func NewBrowser(ctx context.Context) (*Browser, error) {
	var lastErr error
	for i, noSandbox := range launchPlan(os.Getenv("CHROME_NO_SANDBOX")) {
		b, err := launch(ctx, allocatorOptions(noSandbox, os.Getenv("CHROME_PATH"), os.Getenv("JABLE_CHROME_PROFILE")))
		if err == nil {
			b.SandboxFallback = i > 0
			return b, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("chrome not available: %w", lastErr)
}

// launchPlan lists the --no-sandbox settings to try, in order.
func launchPlan(env string) []bool {
	switch strings.TrimSpace(env) {
	case "1":
		return []bool{true}
	case "0":
		return []bool{false}
	default:
		return []bool{false, true}
	}
}

// allocatorOptions builds the Chrome flags. Automation signals Cloudflare
// checks (navigator.webdriver) are disabled; chromedp itself adds
// --no-sandbox when running as root.
func allocatorOptions(noSandbox bool, chromePath, profileDir string) []chromedp.ExecAllocatorOption {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-crash-reporter", true),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)
	if noSandbox {
		opts = append(opts, chromedp.NoSandbox)
	}
	if path := strings.TrimSpace(chromePath); path != "" {
		opts = append(opts, chromedp.ExecPath(path))
	}
	if dir := strings.TrimSpace(profileDir); dir != "" {
		opts = append(opts, chromedp.UserDataDir(dir))
	}
	return opts
}

func launch(ctx context.Context, opts []chromedp.ExecAllocatorOption) (*Browser, error) {
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	var product string
	err := chromedp.Run(browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, _, _, userAgent, _, err := browser.GetVersion().Do(ctx)
		product = userAgent
		return err
	}))
	if err != nil {
		browserCancel()
		allocCancel()
		return nil, err
	}
	return &Browser{
		allocCancel:   allocCancel,
		browserCtx:    browserCtx,
		browserCancel: browserCancel,
		ua:            userAgentFrom(product),
		pageTimeout:   pageTimeout,
	}, nil
}

// userAgentFrom turns the browser's own User-Agent into a regular desktop
// one, so it matches the real Chrome version instead of a stale constant.
func userAgentFrom(browserUA string) string {
	return strings.Replace(browserUA, "HeadlessChrome/", "Chrome/", 1)
}

// Close releases the browser process.
func (b *Browser) Close() {
	b.browserCancel()
	b.allocCancel()
}

// UserAgent returns the User-Agent used for pages; media requests reuse it.
func (b *Browser) UserAgent() string { return b.ua }

// FetchHTML navigates to url and returns the rendered HTML once any
// Cloudflare challenge has cleared (and, for FetchHLS, once the player has
// injected its stream URL).
func (b *Browser) FetchHTML(ctx context.Context, url string, mode site.FetchMode) (string, error) {
	tabCtx, cancelTab := chromedp.NewContext(b.browserCtx)
	defer cancelTab()
	stop := context.AfterFunc(ctx, cancelTab)
	defer stop()
	// Budget: navigation, then the challenge wait, then the HLS wait, so the
	// challenge wait ends first and reports ErrCloudflareChallenge.
	tabCtx, cancelTimeout := context.WithTimeout(tabCtx, 2*b.pageTimeout+hlsTimeout)
	defer cancelTimeout()

	var html string
	actions := []chromedp.Action{
		emulation.SetUserAgentOverride(b.ua),
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			err := poll(ctx, b.pageTimeout, pollInterval, func() (bool, error) {
				var challenged bool
				err := chromedp.Evaluate(challengeScript, &challenged).Do(ctx)
				return !challenged, err
			})
			if errors.Is(err, errPollTimeout) {
				return ErrCloudflareChallenge
			}
			return err
		}),
	}
	if mode == site.FetchHLS {
		actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
			err := poll(ctx, hlsTimeout, pollInterval, func() (bool, error) {
				var ready bool
				err := chromedp.Evaluate(`typeof hlsUrl !== 'undefined' && hlsUrl.length > 0`, &ready).Do(ctx)
				return ready, err
			})
			if errors.Is(err, errPollTimeout) {
				return errors.New("the player did not expose an HLS stream URL")
			}
			return err
		}))
	}
	actions = append(actions, chromedp.OuterHTML("html", &html))

	if err := chromedp.Run(tabCtx, actions...); err != nil {
		if errors.Is(err, ErrCloudflareChallenge) {
			return "", fmt.Errorf("%w — hint: retry, update Chrome, or set JABLE_CHROME_PROFILE to reuse clearance", err)
		}
		return "", fmt.Errorf("chromedp: %w — hint: the site may be slow or blocking; retry later", err)
	}
	if isChallenge(html) {
		return "", ErrCloudflareChallenge
	}
	return html, nil
}

// challengeScript detects an active Cloudflare interstitial in the page.
const challengeScript = `document.title.toLowerCase().startsWith('just a moment') || typeof window._cf_chl_opt !== 'undefined'`

// isChallenge reports whether html is a Cloudflare interstitial. It only
// matches markers unique to the challenge page: normal pages behind
// Cloudflare also load /cdn-cgi/challenge-platform/ scripts.
func isChallenge(html string) bool {
	lower := strings.ToLower(html)
	return strings.Contains(lower, "<title>just a moment") ||
		strings.Contains(lower, "window._cf_chl_opt") ||
		strings.Contains(lower, `id="challenge-form"`)
}

// poll calls ready every interval until it reports true, ctx ends, or
// timeout passes (errPollTimeout). Errors from ready are retried: pages
// navigate while Cloudflare clears.
func poll(ctx context.Context, timeout, interval time.Duration, ready func() (bool, error)) error {
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ok, err := ready(); err == nil && ok {
			return nil
		}
		select {
		case <-deadline.Done():
			if err := ctx.Err(); err != nil {
				return err
			}
			return errPollTimeout
		case <-ticker.C:
		}
	}
}
