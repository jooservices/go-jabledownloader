package site

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Factory builds a site implementation on top of an injected Fetcher.
type Factory func(fetcher Fetcher) Site

type descriptor struct {
	name   string
	hosts  []string
	codeRe *regexp.Regexp // bare-code heuristic; nil = URLs only
	newFn  Factory
}

var descriptors []descriptor

// Register adds a site to the registry. hosts are matched against the URL
// host (exact or subdomain); codeRe, when set, recognizes a bare code input.
func Register(name string, hosts []string, codeRe *regexp.Regexp, newFn Factory) {
	descriptors = append(descriptors, descriptor{name: name, hosts: hosts, codeRe: codeRe, newFn: newFn})
}

// New builds a site implementation by registered name.
func New(name string, fetcher Fetcher) (Site, error) {
	for _, d := range descriptors {
		if d.name == name {
			return d.newFn(fetcher), nil
		}
	}
	return nil, fmt.Errorf("unknown site %q", name)
}

// DetectName auto-detects the site from a CLI input: URL hosts select the
// site; bare inputs fall back to per-site code heuristics.
func DetectName(input string) (string, error) {
	if parsed, err := url.Parse(strings.TrimSpace(input)); err == nil && parsed.Host != "" {
		host := strings.ToLower(parsed.Hostname())
		for _, d := range descriptors {
			for _, h := range d.hosts {
				if HostMatches(host, h) {
					return d.name, nil
				}
			}
		}
		return "", fmt.Errorf("%w: no site handles host %q", ErrUnsupportedInput, host)
	}

	for _, d := range descriptors {
		if d.codeRe != nil && d.codeRe.MatchString(strings.TrimSpace(input)) {
			return d.name, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnsupportedInput, input)
}
