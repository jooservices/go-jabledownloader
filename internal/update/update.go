// Package update implements self-update from GitHub releases.
package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
)

// Asset is a release attachment.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
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
	for i := range r.Assets {
		if strings.HasSuffix(r.Assets[i].Name, suffix) {
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

// Install downloads the asset archive, extracts the binary and atomically
// replaces the currently running executable (keeping a .old backup until the
// new binary is in place).
func Install(ctx context.Context, a *Asset) error {
	tmp, err := os.MkdirTemp("", "go-jabledownloader-update-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	archivePath := filepath.Join(tmp, "release.tar.gz")
	if err := downloadAsset(ctx, a, archivePath); err != nil {
		return err
	}

	binPath, err := extractBinary(archivePath, tmp)
	if err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current binary: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve current binary: %w", err)
	}

	backup := exe + ".old"
	os.Remove(backup)
	if err := os.Rename(exe, backup); err != nil {
		return fmt.Errorf("replace current binary: %w — hint: run from a writable location (e.g. not /usr/local/bin without sudo)", err)
	}

	if err := copyFile(binPath, exe); err != nil {
		_ = os.Rename(backup, exe)
		return fmt.Errorf("install new binary: %w", err)
	}
	if err := os.Chmod(exe, 0o755); err != nil {
		fmt.Printf("  warning: could not set permissions: %v\n", err)
	}
	os.Remove(backup)
	return nil
}

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

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("write archive: %w", err)
	}
	if a.Size > 0 {
		if st, err := out.Stat(); err == nil && st.Size() != a.Size {
			return fmt.Errorf("downloaded archive is %d bytes, expected %d — retry", st.Size(), a.Size)
		}
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
		binPath = filepath.Join(destDir, base)
		out, err := os.OpenFile(binPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", fmt.Errorf("extract binary: %w", err)
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return "", fmt.Errorf("extract binary: %w", err)
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
