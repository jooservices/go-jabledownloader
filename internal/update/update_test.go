package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"", "v1.0.0", true},
		{"dev", "v1.0.0", true},
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.1", "v1.0.0", false},
		{"v1.0.0", "v1.0.0", false},
		{"v1.2.0", "v1.10.0", true},
		{"v1.0.0", "v2.0.0", true},
		{"v1.0.0-beta", "v1.0.0", true},
	}
	for _, tc := range cases {
		if got := IsNewer(tc.current, tc.latest); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want [3]int
	}{
		{"v1.2.3", [3]int{1, 2, 3}},
		{"1.2", [3]int{1, 2, 0}},
		{"v1.10.0-rc.1", [3]int{1, 10, 0}},
		{"", [3]int{0, 0, 0}},
	}
	for _, tc := range cases {
		if got := parseVersion(tc.in); got != tc.want {
			t.Errorf("parseVersion(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestValidateAssetURLAndChecksum(t *testing.T) {
	for _, raw := range []string{"http://github.com/a", "https://evil.example/a", "https://github.com.evil/a", "not-url"} {
		if err := validateAssetURL(raw); err == nil {
			t.Errorf("expected URL rejection for %q", raw)
		}
	}
	if err := validateAssetURL("https://github.com/jooservices/go-jabledownloader/a"); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum(filepath.Join(t.TempDir(), "missing"), "archive", "release.tar.gz"); err == nil {
		t.Fatal("expected missing checksum error")
	}
	dir := t.TempDir()
	archive := filepath.Join(dir, "release.tar.gz")
	data := []byte("archive")
	if err := os.WriteFile(archive, data, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	checksums := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(checksums, []byte(digest+"  release.tar.gz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum(checksums, archive, "release.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checksums, []byte(strings.Repeat("0", 64)+"  release.tar.gz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum(checksums, archive, "release.tar.gz"); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

func TestAssetFor(t *testing.T) {
	rel := Release{Assets: []Asset{
		{Name: "jabledownloader_v1.0.0_linux_amd64.tar.gz"},
		{Name: "jabledownloader_v1.0.0_windows_amd64.tar.gz"},
		{Name: "jabledownloader_v1.0.0_linux_arm64.tar.gz"},
		{Name: "jabledownloader_v1.0.0_darwin_amd64.tar.gz"},
		{Name: "jabledownloader_v1.0.0_darwin_arm64.tar.gz"},
		{Name: "jabledownloader_v1.0.0_windows_arm64.tar.gz"},
	}}
	a := rel.AssetFor()
	if a == nil {
		t.Fatal("expected an asset for this platform")
	}
	wantSuffix := "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	if !strings.HasSuffix(a.Name, wantSuffix) {
		t.Fatalf("unexpected asset: %q want suffix %q", a.Name, wantSuffix)
	}
}

func TestAssetForCarriesChecksumURL(t *testing.T) {
	rel := Release{Assets: []Asset{
		{Name: "checksums.txt", BrowserDownloadURL: "https://github.com/jooservices/go-jabledownloader/releases/download/v1/checksums.txt"},
		{Name: fmt.Sprintf("jabledownloader_v1_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH), BrowserDownloadURL: "https://github.com/jooservices/go-jabledownloader/releases/download/v1/a.tar.gz"},
	}}
	a := rel.AssetFor()
	if a == nil || a.ChecksumURL == "" {
		t.Fatalf("asset=%+v", a)
	}
}

type rewriteTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.URL.Scheme = t.target.Scheme
	r.URL.Host = t.target.Host
	r.RequestURI = ""
	return t.base.RoundTrip(r)
}

func withTestClient(t *testing.T, handler http.Handler) {
	t.Helper()
	srv := httptest.NewServer(handler)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	old := httpClient
	httpClient = &http.Client{
		Transport: &rewriteTransport{base: http.DefaultTransport, target: u},
	}
	t.Cleanup(func() {
		httpClient = old
		srv.Close()
	})
}

func TestLatestRelease(t *testing.T) {
	body := fmt.Sprintf(`{
		"tag_name": "v9.9.9",
		"name": "v9.9.9",
		"assets": [{"name":"jabledownloader_v9.9.9_%s_%s.tar.gz","browser_download_url":"http://example.invalid/a.tar.gz","size":12}]
	}`, runtime.GOOS, runtime.GOARCH)

	withTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/jooservices/go-jabledownloader/releases/latest" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))

	rel, err := LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if rel.TagName != "v9.9.9" {
		t.Fatalf("tag = %q", rel.TagName)
	}
	if rel.AssetFor() == nil {
		t.Fatal("expected AssetFor match")
	}
}

func TestLatestReleaseNotFound(t *testing.T) {
	withTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	_, err := LatestRelease(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no releases") {
		t.Fatalf("expected no releases error, got %v", err)
	}
}

func TestLatestReleaseBadStatus(t *testing.T) {
	withTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	_, err := LatestRelease(context.Background())
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestDownloadAssetExtractCopy(t *testing.T) {
	archive := buildReleaseArchive(t, []byte("#!/bin/sh\necho ok\n"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	old := httpClient
	httpClient = &http.Client{Transport: &rewriteTransport{base: srv.Client().Transport, target: mustURL(t, srv.URL)}}
	defer func() { httpClient = old }()

	dir := t.TempDir()
	dest := filepath.Join(dir, "release.tar.gz")
	asset := &Asset{
		Name:               "release.tar.gz",
		BrowserDownloadURL: "https://github.com/jooservices/go-jabledownloader/releases/download/v1/release.tar.gz",
		Size:               int64(len(archive)),
	}
	if err := downloadAsset(context.Background(), asset, dest); err != nil {
		t.Fatalf("downloadAsset: %v", err)
	}

	bin, err := extractBinary(dest, dir)
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("echo ok")) {
		t.Fatalf("unexpected binary contents: %q", data)
	}

	copied := filepath.Join(dir, "copied")
	if err := copyFile(bin, copied); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	copiedData, err := os.ReadFile(copied)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, copiedData) {
		t.Fatal("copy mismatch")
	}
}

func TestDownloadAssetSizeMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tiny"))
	}))
	defer srv.Close()

	old := httpClient
	httpClient = &http.Client{Transport: &rewriteTransport{base: srv.Client().Transport, target: mustURL(t, srv.URL)}}
	defer func() { httpClient = old }()

	err := downloadAsset(context.Background(), &Asset{
		BrowserDownloadURL: "https://github.com/jooservices/go-jabledownloader/releases/download/v1/release.tar.gz",
		Size:               999,
	}, filepath.Join(t.TempDir(), "a.tar.gz"))
	if err == nil || !strings.Contains(err.Error(), "expected") {
		t.Fatalf("expected size mismatch, got %v", err)
	}
}

func TestDownloadAssetHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	old := httpClient
	httpClient = &http.Client{Transport: &rewriteTransport{base: srv.Client().Transport, target: mustURL(t, srv.URL)}}
	defer func() { httpClient = old }()

	err := downloadAsset(context.Background(), &Asset{BrowserDownloadURL: "https://github.com/jooservices/go-jabledownloader/releases/download/v1/release.tar.gz"}, filepath.Join(t.TempDir(), "a.tar.gz"))
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected http error, got %v", err)
	}
}

func TestExtractBinaryMissing(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "empty.tar.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "README.md", Mode: 0o644, Size: 4}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("docs")); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	if err := os.WriteFile(archivePath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := extractBinary(archivePath, dir)
	if err == nil || !strings.Contains(err.Error(), "binary not found") {
		t.Fatalf("expected missing binary, got %v", err)
	}
}

func TestLatestReleaseBadJSON(t *testing.T) {
	withTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{bad"))
	}))
	_, err := LatestRelease(context.Background())
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestInstallLocateError(t *testing.T) {
	archive := buildReleaseArchive(t, []byte("x"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			_, _ = fmt.Fprintf(w, "%x  dist/release.tar.gz\n", sha256.Sum256(archive))
			return
		}
		_, _ = w.Write(archive)
	}))
	defer srv.Close()
	oldClient := httpClient
	httpClient = &http.Client{Transport: &rewriteTransport{base: srv.Client().Transport, target: mustURL(t, srv.URL)}}
	defer func() { httpClient = oldClient }()

	oldLookup := lookUpExecutable
	lookUpExecutable = func() (string, error) { return "", fmt.Errorf("no exe") }
	defer func() { lookUpExecutable = oldLookup }()

	_, err := Install(context.Background(), &Asset{
		Name:               "release.tar.gz",
		BrowserDownloadURL: "https://github.com/jooservices/go-jabledownloader/releases/download/v1/release.tar.gz",
		ChecksumURL:        "https://github.com/jooservices/go-jabledownloader/releases/download/v1/checksums.txt",
		Size:               int64(len(archive)),
	})
	if err == nil || !strings.Contains(err.Error(), "locate") {
		t.Fatalf("expected locate error, got %v", err)
	}
}

func TestInstall(t *testing.T) {
	archive := buildReleaseArchive(t, []byte("#!/bin/sh\necho new\n"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			_, _ = fmt.Fprintf(w, "%x  dist/release.tar.gz\n", sha256.Sum256(archive))
			return
		}
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	oldClient := httpClient
	httpClient = &http.Client{Transport: &rewriteTransport{base: srv.Client().Transport, target: mustURL(t, srv.URL)}}
	defer func() { httpClient = oldClient }()

	dir := t.TempDir()
	current := filepath.Join(dir, "current-bin")
	if err := os.WriteFile(current, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldLookup := lookUpExecutable
	lookUpExecutable = func() (string, error) { return current, nil }
	defer func() { lookUpExecutable = oldLookup }()

	asset := &Asset{
		Name:               "release.tar.gz",
		BrowserDownloadURL: "https://github.com/jooservices/go-jabledownloader/releases/download/v1/release.tar.gz",
		ChecksumURL:        "https://github.com/jooservices/go-jabledownloader/releases/download/v1/checksums.txt",
		Size:               int64(len(archive)),
	}
	if _, err := Install(context.Background(), asset); err != nil {
		t.Fatalf("Install: %v", err)
	}
	data, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("echo new")) {
		t.Fatalf("binary not replaced: %q", data)
	}
	if _, err := os.Stat(current + ".old"); !os.IsNotExist(err) {
		t.Fatalf("expected .old backup removed, err=%v", err)
	}
}

func TestInstallChecksumMismatchPreservesOriginal(t *testing.T) {
	archive := buildReleaseArchive(t, []byte("new"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			_, _ = fmt.Fprintln(w, strings.Repeat("0", sha256.Size*2)+"  dist/release.tar.gz")
			return
		}
		_, _ = w.Write(archive)
	}))
	defer srv.Close()
	oldClient := httpClient
	httpClient = &http.Client{Transport: &rewriteTransport{base: srv.Client().Transport, target: mustURL(t, srv.URL)}}
	defer func() { httpClient = oldClient }()

	dir := t.TempDir()
	current := filepath.Join(dir, "current-bin")
	original := []byte("original")
	if err := os.WriteFile(current, original, 0o755); err != nil {
		t.Fatal(err)
	}
	oldLookup := lookUpExecutable
	lookUpExecutable = func() (string, error) { return current, nil }
	defer func() { lookUpExecutable = oldLookup }()

	_, err := Install(context.Background(), &Asset{
		Name:               "release.tar.gz",
		BrowserDownloadURL: "https://github.com/jooservices/go-jabledownloader/releases/download/v1/release.tar.gz",
		ChecksumURL:        "https://github.com/jooservices/go-jabledownloader/releases/download/v1/checksums.txt",
		Size:               int64(len(archive)),
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
	got, readErr := os.ReadFile(current)
	if readErr != nil || !bytes.Equal(got, original) {
		t.Fatalf("original binary changed: %q, err=%v", got, readErr)
	}
}

func TestInstallRequiresChecksums(t *testing.T) {
	_, err := Install(context.Background(), &Asset{
		Name:               "release.tar.gz",
		BrowserDownloadURL: "https://github.com/jooservices/go-jabledownloader/releases/download/v1/release.tar.gz",
	})
	if err == nil || !strings.Contains(err.Error(), "no checksums") {
		t.Fatalf("expected no checksums error, got %v", err)
	}
}

func TestInstallRejectsUntrustedAssetURL(t *testing.T) {
	_, err := Install(context.Background(), &Asset{
		Name:               "release.tar.gz",
		BrowserDownloadURL: "http://evil/x.tar.gz",
		ChecksumURL:        "https://github.com/jooservices/go-jabledownloader/releases/download/v1/checksums.txt",
	})
	if err == nil || !strings.Contains(err.Error(), "must use https") {
		t.Fatalf("expected URL rejection, got %v", err)
	}
}

func TestExtractBinaryTooLarge(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "large.tar.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: BinName, Mode: 0o755, Size: maxUpdateSize + 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(tw, zeroReader{}, maxUpdateSize+1); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := extractBinary(archivePath, dir)
	if err == nil || !strings.Contains(err.Error(), "binary too large") {
		t.Fatalf("expected binary size error, got %v", err)
	}
}

func TestExtractBinarySkipsDirs(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "subdir/", Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
		t.Fatal(err)
	}
	payload := []byte("bin")
	if err := tw.WriteHeader(&tar.Header{Name: "subdir/" + BinName, Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	path := filepath.Join(dir, "a.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	bin, err := extractBinary(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(bin) != BinName {
		t.Fatalf("bin=%q", bin)
	}
}

func TestExtractBinaryExeName(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte("bin")
	hdr := &tar.Header{Name: BinName + ".exe", Mode: 0o755, Size: int64(len(payload))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	archivePath := filepath.Join(dir, "a.tar.gz")
	if err := os.WriteFile(archivePath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	bin, err := extractBinary(archivePath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(bin) != BinName+".exe" {
		t.Fatalf("bin=%q", bin)
	}
}

func TestCopyFileMissingSrc(t *testing.T) {
	if err := copyFile(filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "dst")); err == nil {
		t.Fatal("expected error")
	}
}

func TestAssetForNil(t *testing.T) {
	rel := Release{Assets: []Asset{{Name: "notes.txt"}}}
	if rel.AssetFor() != nil {
		t.Fatal("expected nil")
	}
}

func buildReleaseArchive(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name: BinName,
		Mode: 0o755,
		Size: int64(len(payload)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func validAsset(size int64) *Asset {
	return &Asset{
		Name:               "release.tar.gz",
		BrowserDownloadURL: "https://github.com/jooservices/go-jabledownloader/releases/download/v1/release.tar.gz",
		ChecksumURL:        "https://github.com/jooservices/go-jabledownloader/releases/download/v1/checksums.txt",
		Size:               size,
	}
}

func TestInstallRejectsInvalidAssetsBeforeDownloading(t *testing.T) {
	requests := 0
	withTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	bad := func(mutate func(*Asset)) *Asset {
		a := validAsset(1)
		mutate(a)
		return a
	}
	for name, asset := range map[string]*Asset{
		"nil":             nil,
		"no checksums":    bad(func(a *Asset) { a.ChecksumURL = "" }),
		"http asset":      bad(func(a *Asset) { a.BrowserDownloadURL = "http://github.com/x.tar.gz" }),
		"foreign sums":    bad(func(a *Asset) { a.ChecksumURL = "https://evil.test/checksums.txt" }),
		"oversized asset": bad(func(a *Asset) { a.Size = maxUpdateSize + 1 }),
	} {
		if _, err := Install(context.Background(), asset); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if requests != 0 {
		t.Fatalf("made %d requests for invalid assets", requests)
	}
}

func TestInstallChecksumDownloadFailures(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"missing": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
		"too large declared": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprint(maxChecksumSize+1))
			_, _ = w.Write(make([]byte, maxChecksumSize+1))
		},
		"too large streamed": func(w http.ResponseWriter, _ *http.Request) {
			w.(http.Flusher).Flush() // chunked: no Content-Length
			_, _ = w.Write(make([]byte, maxChecksumSize+1))
		},
	} {
		t.Run(name, func(t *testing.T) {
			withTestClient(t, handler)
			if _, err := Install(context.Background(), validAsset(1)); err == nil || !strings.Contains(err.Error(), "checksums") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestInstallArchiveDownloadFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		handler func(http.ResponseWriter)
		want    string
	}{
		"declared too large": {func(w http.ResponseWriter) {
			w.Header().Set("Content-Length", fmt.Sprint(maxUpdateSize+1))
			w.WriteHeader(http.StatusOK)
		}, "too large"},
		"http error": {func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) }, "http status 502"},
	} {
		t.Run(name, func(t *testing.T) {
			withTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "checksums.txt") {
					_, _ = fmt.Fprintln(w, strings.Repeat("0", 64)+"  release.tar.gz")
					return
				}
				tc.handler(w)
			}))
			if _, err := Install(context.Background(), validAsset(0)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestInstallTransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	old := httpClient
	httpClient = &http.Client{Transport: &rewriteTransport{base: http.DefaultTransport, target: mustURL(t, srv.URL)}}
	t.Cleanup(func() { httpClient = old })

	if _, err := Install(context.Background(), validAsset(1)); err == nil || !strings.Contains(err.Error(), "download checksums") {
		t.Fatalf("err = %v", err)
	}
}

// A read-only install directory must leave the current binary untouched.
func TestInstallReadOnlyLocationKeepsBinary(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission semantics differ")
	}
	archive := buildReleaseArchive(t, []byte("new"))
	withTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			_, _ = fmt.Fprintf(w, "%x  dist/release.tar.gz\n", sha256.Sum256(archive))
			return
		}
		_, _ = w.Write(archive)
	}))
	dir := t.TempDir()
	current := filepath.Join(dir, "current-bin")
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	oldLookup := lookUpExecutable
	lookUpExecutable = func() (string, error) { return current, nil }
	t.Cleanup(func() { lookUpExecutable = oldLookup })

	_, err := Install(context.Background(), validAsset(int64(len(archive))))

	if err == nil || !strings.Contains(err.Error(), "writable location") {
		t.Fatalf("err = %v", err)
	}
	if data, _ := os.ReadFile(current); string(data) != "old" {
		t.Fatalf("binary changed: %q", data)
	}
}
