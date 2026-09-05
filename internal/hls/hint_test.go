package hls

import (
	"net/http"
	"strings"
	"testing"
)

func TestHTTPHint(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusForbidden, "geo-blocking"},
		{http.StatusNotFound, "no longer exists"},
		{http.StatusUnauthorized, "Cloudflare token"},
		{http.StatusTooManyRequests, "rate-limited"},
		{http.StatusInternalServerError, "server error"},
		{http.StatusBadGateway, "server error"},
		{http.StatusServiceUnavailable, "server error"},
		{http.StatusGatewayTimeout, "server error"},
		{http.StatusOK, ""},
		{418, ""},
	}
	for _, tc := range cases {
		got := httpHint(tc.status)
		if tc.want == "" {
			if got != "" {
				t.Errorf("httpHint(%d) = %q, want empty", tc.status, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("httpHint(%d) = %q, want substring %q", tc.status, got, tc.want)
		}
	}
}
