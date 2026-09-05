package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
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

func TestAssetFor(t *testing.T) {
	rel := Release{Assets: []Asset{
		{Name: "jabledownloader_v1.0.0_linux_amd64.tar.gz"},
		{Name: "jabledownloader_v1.0.0_windows_amd64.tar.gz"},
		{Name: "jabledownloader_v1.0.0_linux_arm64.tar.gz"},
	}}
	a := rel.AssetFor()
	if a == nil {
		t.Fatal("expected an asset for this platform")
	}
	if a.Name != "jabledownloader_v1.0.0_linux_arm64.tar.gz" && a.Name != "jabledownloader_v1.0.0_linux_amd64.tar.gz" {
		t.Fatalf("unexpected asset: %q", a.Name)
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
	httpClient = srv.Client()
	defer func() { httpClient = old }()

	dir := t.TempDir()
	dest := filepath.Join(dir, "release.tar.gz")
	asset := &Asset{
		Name:               "release.tar.gz",
		BrowserDownloadURL: srv.URL + "/asset",
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
	httpClient = srv.Client()
	defer func() { httpClient = old }()

	err := downloadAsset(context.Background(), &Asset{
		BrowserDownloadURL: srv.URL,
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
	httpClient = srv.Client()
	defer func() { httpClient = old }()

	err := downloadAsset(context.Background(), &Asset{BrowserDownloadURL: srv.URL}, filepath.Join(t.TempDir(), "a.tar.gz"))
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()
	oldClient := httpClient
	httpClient = srv.Client()
	defer func() { httpClient = oldClient }()

	oldLookup := lookUpExecutable
	lookUpExecutable = func() (string, error) { return "", fmt.Errorf("no exe") }
	defer func() { lookUpExecutable = oldLookup }()

	err := Install(context.Background(), &Asset{BrowserDownloadURL: srv.URL, Size: int64(len(archive))})
	if err == nil || !strings.Contains(err.Error(), "locate") {
		t.Fatalf("expected locate error, got %v", err)
	}
}

func TestInstall(t *testing.T) {
	archive := buildReleaseArchive(t, []byte("#!/bin/sh\necho new\n"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	oldClient := httpClient
	httpClient = srv.Client()
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
		BrowserDownloadURL: srv.URL,
		Size:               int64(len(archive)),
	}
	if err := Install(context.Background(), asset); err != nil {
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
