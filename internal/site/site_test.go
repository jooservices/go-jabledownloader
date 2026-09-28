package site

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/domain"
)

type stubSite struct{ name string }

func (s stubSite) Name() string { return s.name }
func (stubSite) List(context.Context, ListOptions) ([]domain.Item, error) {
	return nil, nil
}
func (stubSite) Search(context.Context, string, int) ([]domain.Item, error) { return nil, nil }
func (stubSite) Detail(context.Context, string) (*domain.Detail, error)     { return nil, nil }

// withRegistry isolates the package registry for one test.
func withRegistry(t *testing.T, ds ...Descriptor) {
	t.Helper()
	saved := descriptors
	descriptors = nil
	t.Cleanup(func() { descriptors = saved })
	for _, d := range ds {
		Register(d)
	}
}

func descriptor(name string, hosts []string, code string) Descriptor {
	d := Descriptor{Name: name, Hosts: hosts, New: func(Fetcher) Site { return stubSite{name: name} }}
	if code != "" {
		d.CodeRe = regexp.MustCompile(code)
	}
	return d
}

func TestDetect(t *testing.T) {
	withRegistry(t,
		descriptor("alpha", []string{"alpha.test"}, `^[a-z]+-\d+$`),
		descriptor("beta", []string{"beta.test"}, ""),
	)
	for _, tc := range []struct {
		input, want string
	}{
		{"abc-123", "alpha"},
		{" https://www.alpha.test/videos/abc-1/ ", "alpha"},
		{"https://BETA.test/video-1/", "beta"},
	} {
		d, err := Detect(tc.input)
		if err != nil || d.Name != tc.want {
			t.Errorf("Detect(%q) = %q, %v; want %q", tc.input, d.Name, err, tc.want)
		}
	}
	for _, bad := range []string{"not a code", "https://evil.test/abc-1", "https://alpha.test.evil.test/"} {
		if _, err := Detect(bad); !errors.Is(err, ErrUnsupportedInput) {
			t.Errorf("Detect(%q) err = %v", bad, err)
		}
	}
}

func TestRegisterAndLookup(t *testing.T) {
	withRegistry(t, descriptor("alpha", nil, ""), descriptor("beta", nil, ""))

	if got := strings.Join(Names(), ","); got != "alpha,beta" {
		t.Fatalf("Names = %s", got)
	}
	d, err := Lookup("beta")
	if err != nil || d.New(nil).Name() != "beta" {
		t.Fatalf("Lookup = %+v, %v", d, err)
	}
	if _, err := Lookup("gamma"); err == nil || !strings.Contains(err.Error(), "available: alpha, beta") {
		t.Fatalf("unknown Lookup err = %v", err)
	}
}

func TestRegisterPanicsOnProgrammerErrors(t *testing.T) {
	withRegistry(t, descriptor("alpha", nil, ""))
	for name, d := range map[string]Descriptor{
		"duplicate":  descriptor("alpha", nil, ""),
		"no name":    {New: func(Fetcher) Site { return nil }},
		"no factory": {Name: "gamma"},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			Register(d)
		})
	}
}

func TestHostMatches(t *testing.T) {
	for _, tc := range []struct {
		host, pattern string
		want          bool
	}{
		{"www.eporner.com", "eporner.com", true},
		{"EPORNER.com", "eporner.com", true},
		{"eporner.com.evil.test", "eporner.com", false},
		{"noteporner.com", "eporner.com", false},
	} {
		if got := HostMatches(tc.host, tc.pattern); got != tc.want {
			t.Errorf("HostMatches(%q, %q) = %v", tc.host, tc.pattern, got)
		}
	}
}

func TestCheckView(t *testing.T) {
	views := []string{"latest", "hot"}
	if got, err := CheckView("alpha", views, ""); err != nil || got != "latest" {
		t.Fatalf("empty view = %q, %v", got, err)
	}
	if got, err := CheckView("alpha", views, " hot "); err != nil || got != "hot" {
		t.Fatalf("hot = %q, %v", got, err)
	}
	if _, err := CheckView("alpha", views, "../admin"); err == nil || !strings.Contains(err.Error(), "valid: latest, hot") {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTPFetcherSendsHeadersAndReturnsBody(t *testing.T) {
	var referer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referer = r.Referer()
		_, _ = w.Write([]byte("<html>ok</html>"))
	}))
	defer srv.Close()
	f := NewHTTPFetcher(srv.Client(), http.Header{"Referer": {"https://site.test/"}})

	body, err := f.FetchHTML(context.Background(), srv.URL, FetchReady)

	if err != nil || body != "<html>ok</html>" || referer != "https://site.test/" {
		t.Fatalf("body=%q referer=%q err=%v", body, referer, err)
	}
}

func TestHTTPFetcherErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/big" {
			_, _ = w.Write(make([]byte, maxHTMLSize+1))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	f := NewHTTPFetcher(srv.Client(), nil)

	if _, err := f.FetchHTML(context.Background(), srv.URL+"/missing", FetchReady); err == nil || !strings.Contains(err.Error(), "http 404") {
		t.Fatalf("404 err = %v", err)
	}
	if _, err := f.FetchHTML(context.Background(), srv.URL+"/big", FetchReady); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize err = %v", err)
	}
	if _, err := f.FetchHTML(context.Background(), "://bad", FetchReady); err == nil {
		t.Fatal("expected bad URL error")
	}
	srv.Close()
	if _, err := f.FetchHTML(context.Background(), srv.URL, FetchReady); err == nil {
		t.Fatal("expected transport error")
	}
}
