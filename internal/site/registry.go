package site

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// FetcherKind selects how a site's pages are fetched.
type FetcherKind int

const (
	// FetcherHTTP fetches server-rendered pages with a plain HTTP client.
	FetcherHTTP FetcherKind = iota
	// FetcherBrowser fetches pages with the shared headless browser.
	FetcherBrowser
)

// Descriptor declares how a site is detected, built, and fetched.
type Descriptor struct {
	Name    string
	Hosts   []string       // URL hosts handled (exact or subdomain)
	CodeRe  *regexp.Regexp // recognises a bare code input; nil = URLs only
	Fetcher FetcherKind
	Views   []string    // valid ListOptions.View values
	Headers http.Header // extra headers for page requests (FetcherHTTP)
	New     func(Fetcher) Site
}

var descriptors []Descriptor

// Register adds a site. It panics on programmer errors (missing name or
// factory, duplicate name) because registration happens in init.
func Register(d Descriptor) {
	if d.Name == "" || d.New == nil {
		panic("site.Register requires a name and a factory")
	}
	if _, err := Lookup(d.Name); err == nil {
		panic(fmt.Sprintf("duplicate site registration %q", d.Name))
	}
	descriptors = append(descriptors, d)
}

// Lookup returns the descriptor registered under name.
func Lookup(name string) (Descriptor, error) {
	for _, d := range descriptors {
		if d.Name == name {
			return d, nil
		}
	}
	return Descriptor{}, fmt.Errorf("unknown site %q (available: %s)", name, strings.Join(Names(), ", "))
}

// Names lists the registered site names in registration order.
func Names() []string {
	names := make([]string, 0, len(descriptors))
	for _, d := range descriptors {
		names = append(names, d.Name)
	}
	return names
}

// Detect returns the site for a CLI input: URL hosts select the site; bare
// inputs fall back to each site's code pattern.
func Detect(input string) (Descriptor, error) {
	input = strings.TrimSpace(input)
	if parsed, err := url.Parse(input); err == nil && parsed.Host != "" {
		host := parsed.Hostname()
		for _, d := range descriptors {
			for _, h := range d.Hosts {
				if HostMatches(host, h) {
					return d, nil
				}
			}
		}
		return Descriptor{}, fmt.Errorf("%w: no site handles host %q", ErrUnsupportedInput, host)
	}
	for _, d := range descriptors {
		if d.CodeRe != nil && d.CodeRe.MatchString(input) {
			return d, nil
		}
	}
	return Descriptor{}, fmt.Errorf("%w: %q", ErrUnsupportedInput, input)
}

// HostMatches reports whether host equals or is a subdomain of pattern.
func HostMatches(host, pattern string) bool {
	host, pattern = strings.ToLower(host), strings.ToLower(pattern)
	return host == pattern || strings.HasSuffix(host, "."+pattern)
}
