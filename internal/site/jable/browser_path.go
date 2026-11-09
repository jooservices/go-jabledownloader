package jable

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// browserProbe abstracts the host so detection is testable on any OS.
type browserProbe struct {
	goos     string
	getenv   func(string) string
	exists   func(path string) bool
	lookPath func(name string) (string, error)
}

func hostProbe() browserProbe {
	return browserProbe{
		goos:   runtime.GOOS,
		getenv: os.Getenv,
		exists: func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && !info.IsDir()
		},
		lookPath: exec.LookPath,
	}
}

// detectBrowser finds a Chromium-family browser (Chrome, Chromium, Edge,
// Brave) so the tool also works on machines that only ship Edge. It returns
// "" when nothing is found; the caller then lets chromedp try its defaults.
func detectBrowser(p browserProbe) string {
	switch p.goos {
	case "windows":
		suffixes := []string{
			`Google\Chrome\Application\chrome.exe`,
			`Microsoft\Edge\Application\msedge.exe`,
			`BraveSoftware\Brave-Browser\Application\brave.exe`,
			`Chromium\Application\chrome.exe`,
		}
		for _, suffix := range suffixes {
			for _, root := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
				if base := p.getenv(root); base != "" {
					if path := filepath.Join(base, suffix); p.exists(path) {
						return path
					}
				}
			}
		}
	case "darwin":
		for _, app := range []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		} {
			if p.exists(app) {
				return app
			}
		}
	default:
		for _, name := range []string{
			"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
			"microsoft-edge", "microsoft-edge-stable", "brave-browser",
		} {
			if path, err := p.lookPath(name); err == nil {
				return path
			}
		}
	}
	return ""
}
