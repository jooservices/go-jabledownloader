// Package domain contains provider-independent downloader values and events.
package domain

import "regexp"

var validCodeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidCode reports whether a provider code is safe to use in a path.
func ValidCode(code string) bool {
	return code != "." && code != ".." && validCodeRe.MatchString(code)
}
