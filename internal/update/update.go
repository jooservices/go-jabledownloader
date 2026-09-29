// Package update implements self-update from GitHub releases.
package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// Owner is the GitHub organization owning the releases.
	Owner = "jooservices"
	// Repo is the GitHub repository name.
	Repo = "go-jabledownloader"
	// BinName is the released binary name.
	BinName = "jabledownloader"

	maxUpdateSize   = 200 << 20
	maxChecksumSize = 1 << 20
)

// Asset is a release attachment.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	ChecksumURL        string `json:"-"`
}

// Release is GitHub release metadata.
type Release struct {
	TagName string  `json:"tag_name"`
	Name    string  `json:"name"`
	Assets  []Asset `json:"assets"`
}

var httpClient = &http.Client{Timeout: 120 * time.Second}

// LatestRelease fetches the newest release metadata from GitHub.
func LatestRelease(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", Owner, Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", BinName)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contact GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no releases found for %s/%s yet", Owner, Repo)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned status %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	return &rel, nil
}

// AssetFor returns the release asset matching this platform
// (jabledownloader_vX.Y.Z_{goos}_{goarch}.tar.gz), or nil.
func (r *Release) AssetFor() *Asset {
	suffix := fmt.Sprintf("_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	var checksumURL string
	for i := range r.Assets {
		if filepath.Base(r.Assets[i].Name) == "checksums.txt" {
			checksumURL = r.Assets[i].BrowserDownloadURL
			break
		}
	}
	for i := range r.Assets {
		if strings.HasSuffix(r.Assets[i].Name, suffix) {
			r.Assets[i].ChecksumURL = checksumURL
			return &r.Assets[i]
		}
	}
	return nil
}

// IsNewer reports whether latest is a newer version than current.
// Dev builds and empty versions always count as outdated. When the numeric
// parts are equal, prerelease ordering applies (e.g. 1.0.0-beta < 1.0.0).
func IsNewer(current, latest string) bool {
	if current == "" || current == "dev" {
		return true
	}
	pa, pb := parseVersion(current), parseVersion(latest)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	curPre := strings.Contains(current, "-")
	latPre := strings.Contains(latest, "-")
	if curPre && !latPre {
		return true
	}
	return strings.TrimPrefix(current, "v") < strings.TrimPrefix(latest, "v")
}

func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	parts := strings.Split(v, ".")
	for i := 0; i < 3 && i < len(parts); i++ {
		out[i], _ = strconv.Atoi(parts[i])
	}
	return out
}

// lookUpExecutable locates the running binary. Tests override it.
var lookUpExecutable = os.Executable

// Install downloads the asset archive, verifies its SHA-256 against the
// release checksums.txt, extracts the binary and replaces the running
// executable (keeping a .old backup until the new binary is in place). The
// returned warnings are non-fatal and meant for the user.
func Install(ctx context.Context, a *Asset) ([]string, error) {
	var warnings []string
	if a == nil {
		return warnings, fmt.Errorf("release asset is nil")
	}
	if a.ChecksumURL == "" {
		return warnings, fmt.Errorf("no checksums asset in release")
	}
	if err := validateAssetURL(a.BrowserDownloadURL); err != nil {
		return warnings, err
	}
	if err := validateAssetURL(a.ChecksumURL); err != nil {
		return warnings, err
	}
	if a.Size > maxUpdateSize {
		return warnings, fmt.Errorf("archive is too large: %d bytes", a.Size)
	}

	tmp, err := os.MkdirTemp("", "go-jabledownloader-update-")
	if err != nil {
		return warnings, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	checksumPath := filepath.Join(tmp, "checksums.txt")
	if err := downloadChecksums(ctx, a.ChecksumURL, checksumPath); err != nil {
		return warnings, err
	}

	archivePath := filepath.Join(tmp, "release.tar.gz")
	if err := downloadAsset(ctx, a, archivePath); err != nil {
		return warnings, err
	}
	if err := verifyChecksum(checksumPath, archivePath, a.Name); err != nil {
		return warnings, err
	}

	binPath, err := extractBinary(archivePath, tmp)
	if err != nil {
		return warnings, err
	}

	exe, err := lookUpExecutable()
	if err != nil {
		return warnings, fmt.Errorf("locate current binary: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return warnings, fmt.Errorf("resolve current binary: %w", err)
	}

	backup := exe + ".old"
	os.Remove(backup)
	if err := os.Rename(exe, backup); err != nil {
		return warnings, fmt.Errorf("replace current binary: %w — hint: run from a writable location (e.g. not /usr/local/bin without sudo)", err)
	}

	if err := copyFile(binPath, exe); err != nil {
		_ = os.Rename(backup, exe)
		return warnings, fmt.Errorf("install new binary: %w", err)
	}
	if err := os.Remove(backup); err != nil {
		warnings = append(warnings, fmt.Sprintf("could not remove old binary backup: %v", err))
	}
	return warnings, nil
}

// downloadAsset fetches a validated asset (see Install) to dest.
func downloadAsset(ctx context.Context, a *Asset, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.BrowserDownloadURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", BinName)
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download release: %w — hint: check your connection", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download release: http status %d", resp.StatusCode)
	}
	if resp.ContentLength > maxUpdateSize {
		return fmt.Errorf("archive is too large: %d bytes", resp.ContentLength)
	}

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer out.Close()

	n, err := io.Copy(out, io.LimitReader(resp.Body, maxUpdateSize+1))
	if err != nil {
		return fmt.Errorf("write archive: %w", err)
	}
	if n > maxUpdateSize {
		return fmt.Errorf("archive is too large: more than %d bytes", maxUpdateSize)
	}
	if a.Size > 0 && n != a.Size {
		return fmt.Errorf("downloaded archive is %d bytes, expected %d — retry", n, a.Size)
	}
	return nil
}

func validateAssetURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || (u.Hostname() != "github.com" && u.Hostname() != "objects.githubusercontent.com") {
		return fmt.Errorf("release asset URL must use https on github.com or objects.githubusercontent.com")
	}
	return nil
}

func downloadChecksums(ctx context.Context, rawURL, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("create checksum request: %w", err)
	}
	req.Header.Set("User-Agent", BinName)
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download checksums: http status %d", resp.StatusCode)
	}
	if resp.ContentLength > maxChecksumSize {
		return fmt.Errorf("checksums file is too large")
	}
	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create checksums file: %w", err)
	}
	defer out.Close()
	n, err := io.Copy(out, io.LimitReader(resp.Body, maxChecksumSize+1))
	if err != nil {
		return fmt.Errorf("write checksums: %w", err)
	}
	if n > maxChecksumSize {
		return fmt.Errorf("checksums file is too large")
	}
	return nil
}

func verifyChecksum(checksumPath, archivePath, assetName string) error {
	data, err := os.ReadFile(checksumPath)
	if err != nil {
		return fmt.Errorf("read checksums: %w", err)
	}
	want := ""
	base := filepath.Base(assetName)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && filepath.Base(fields[1]) == base {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return fmt.Errorf("no checksum for %s", base)
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive for checksum: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash archive: %w", err)
	}
	if !strings.EqualFold(want, fmt.Sprintf("%x", h.Sum(nil))) {
		return fmt.Errorf("checksum mismatch for %s", base)
	}
	return nil
}

func extractBinary(archivePath, destDir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("decompress archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	binPath := ""
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read archive: %w", err)
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(hdr.Name)
		if base != BinName && base != BinName+".exe" {
			continue
		}
		if hdr.Size > maxUpdateSize {
			return "", fmt.Errorf("binary too large: %d bytes", hdr.Size)
		}
		binPath = filepath.Join(destDir, base)
		out, err := os.OpenFile(binPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", fmt.Errorf("extract binary: %w", err)
		}
		n, err := io.Copy(out, io.LimitReader(tr, maxUpdateSize+1))
		if err != nil {
			out.Close()
			return "", fmt.Errorf("extract binary: %w", err)
		}
		if n > maxUpdateSize {
			out.Close()
			return "", fmt.Errorf("binary too large: more than %d bytes", maxUpdateSize)
		}
		out.Close()
		break
	}

	if binPath == "" {
		return "", fmt.Errorf("binary not found in archive — the release layout may have changed")
	}
	return binPath, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
