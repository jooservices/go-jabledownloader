package jable

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/platform/httpx"
)

func TestDetectBrowser(t *testing.T) {
	env := map[string]string{"ProgramFiles": `C:\PF`, "LOCALAPPDATA": `C:\Local`}
	edge := filepath.Join(`C:\PF`, `Microsoft\Edge\Application\msedge.exe`)
	chrome := filepath.Join(`C:\Local`, `Google\Chrome\Application\chrome.exe`)

	cases := []struct {
		name  string
		probe browserProbe
		want  string
	}{
		{"windows edge only", browserProbe{goos: "windows", getenv: func(k string) string { return env[k] },
			exists: func(p string) bool { return p == edge }}, edge},
		{"windows prefers chrome", browserProbe{goos: "windows", getenv: func(k string) string { return env[k] },
			exists: func(p string) bool { return p == edge || p == chrome }}, chrome},
		{"windows none", browserProbe{goos: "windows", getenv: func(string) string { return "" },
			exists: func(string) bool { return true }}, ""},
		{"darwin edge", browserProbe{goos: "darwin",
			exists: func(p string) bool { return p == "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge" }},
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"},
		{"darwin none", browserProbe{goos: "darwin", exists: func(string) bool { return false }}, ""},
		{"linux chromium", browserProbe{goos: "linux", lookPath: func(n string) (string, error) {
			if n == "chromium" {
				return "/usr/bin/chromium", nil
			}
			return "", errors.New("not found")
		}}, "/usr/bin/chromium"},
		{"linux none", browserProbe{goos: "linux", lookPath: func(string) (string, error) { return "", errors.New("no") }}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectBrowser(tc.probe); got != tc.want {
				t.Fatalf("detectBrowser = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHostProbeIsUsable(t *testing.T) {
	p := hostProbe()
	if p.goos == "" || p.getenv == nil || p.exists == nil || p.lookPath == nil {
		t.Fatalf("incomplete probe: %+v", p)
	}
	if p.exists(t.TempDir()) {
		t.Fatal("directories must not count as browsers")
	}
	_ = detectBrowser(p) // must not panic on the real host
}

func TestResolverRules(t *testing.T) {
	doh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"Status":0,"Answer":[{"type":1,"data":"198.51.100.7"}]}`)
	}))
	defer doh.Close()
	ctx := context.Background()

	// .invalid never resolves, so the system answer is "blocked" offline too.
	if got := resolverRules(ctx, httpx.NewResolver(doh.URL), "https://blocked.invalid/x"); got != "MAP blocked.invalid 198.51.100.7" {
		t.Fatalf("rules = %q", got)
	}
	if got := resolverRules(ctx, httpx.NewResolver("off"), "https://blocked.invalid"); got != "" {
		t.Fatalf("off must not pin, got %q", got)
	}
	if got := resolverRules(ctx, httpx.NewResolver(doh.URL), "https://127.0.0.1"); got != "" {
		t.Fatalf("literal IPs must not pin, got %q", got)
	}
	if got := resolverRules(ctx, httpx.NewResolver(doh.URL), "://bad"); got != "" {
		t.Fatalf("bad URL must not pin, got %q", got)
	}
	dead := httpx.NewResolver("http://127.0.0.1:1/x")
	if got := resolverRules(ctx, dead, "https://blocked.invalid"); got != "" {
		t.Fatalf("failed DoH must not pin, got %q", got)
	}
}
