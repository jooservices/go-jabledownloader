package hls

import "net/http"

// httpHint turns an HTTP status into actionable advice for this site.
func httpHint(status int) string {
	switch status {
	case http.StatusForbidden:
		return "the site may be geo-blocking or Cloudflare is blocking — retry later or try a different network"
	case http.StatusNotFound:
		return "the video or segment no longer exists — the video may have been removed"
	case http.StatusUnauthorized:
		return "access denied — the Cloudflare token may have expired, retry the download"
	case http.StatusTooManyRequests:
		return "rate-limited — backing off and retrying"
	case http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "server error — retrying usually helps"
	}
	return ""
}
