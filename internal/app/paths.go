package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jooservices/go-jabledownloader/internal/domain"
)

// DefaultPathTemplate places each video in "<out>/<site>/<code>".
const DefaultPathTemplate = "{site}/{code}"

// Layout resolves per-video directories below the output directory.
type Layout struct {
	Base     string
	Template string // "" = DefaultPathTemplate
}

// VideoDir resolves the directory for one site's video. Code comes from an
// untrusted page, so it is validated before it becomes a path.
func (l Layout) VideoDir(site, code string) (string, error) {
	if !domain.ValidCode(code) {
		return "", fmt.Errorf("unsafe video code %q from %s", code, site)
	}
	template := l.template()
	if err := ValidatePathTemplate(template); err != nil {
		return "", err
	}
	base := filepath.Clean(l.Base)
	dir := filepath.Join(base, strings.NewReplacer("{site}", site, "{code}", code).Replace(template))
	rel, err := filepath.Rel(base, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path template %q escapes output dir", template)
	}
	return dir, nil
}

// SeparatesSites reports whether different sites always get different
// directories; otherwise equal codes from two sites would collide.
func (l Layout) SeparatesSites() bool {
	return strings.Contains(l.template(), "{site}")
}

// FindComplete returns a finished video for code: in the configured layout
// first, then in the pre-v5 "<out>/<code>" layout. fileName is the custom
// --name, if any.
func (l Layout) FindComplete(site, code, fileName string) (string, error) {
	dir, err := l.VideoDir(site, code)
	if err != nil {
		return "", err
	}
	for _, candidate := range []string{dir, filepath.Join(filepath.Clean(l.Base), code)} {
		if found := findCompleteInDir(candidate, code, fileName); found != "" {
			return found, nil
		}
	}
	return "", nil
}

func (l Layout) template() string {
	if l.Template == "" {
		return DefaultPathTemplate
	}
	return l.Template
}

// ValidatePathTemplate accepts relative templates made of {site}, {code},
// and literal path text, and requires {code} (resume state is per video).
func ValidatePathTemplate(template string) error {
	if !strings.Contains(template, "{code}") {
		return fmt.Errorf("path template %q must contain {code}", template)
	}
	resolved := filepath.Clean(strings.NewReplacer("{site}", "site", "{code}", "code").Replace(template))
	if filepath.IsAbs(resolved) || resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path template %q escapes output dir", template)
	}
	for rest := template; ; {
		start := strings.IndexByte(rest, '{')
		if start < 0 {
			return nil
		}
		end := strings.IndexByte(rest[start:], '}')
		if end < 0 {
			return fmt.Errorf("path template %q has an unclosed placeholder", template)
		}
		if placeholder := rest[start : start+end+1]; placeholder != "{site}" && placeholder != "{code}" {
			return fmt.Errorf("unknown placeholder %s (valid: {site}, {code})", placeholder)
		}
		rest = rest[start+end+1:]
	}
}

// ValidateFileName accepts a plain file name for --name (no directories).
func ValidateFileName(name string) error {
	if name == "" {
		return nil
	}
	if name != filepath.Base(name) || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return fmt.Errorf("--name must be a file name, not a path: %q", name)
	}
	return nil
}

func findCompleteInDir(dir, code, fileName string) string {
	if hasResumeState(dir) {
		return ""
	}
	if fileName != "" {
		path := filepath.Join(dir, fileName)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
		return ""
	}
	return findVideo(dir, code)
}

// findVideo returns "<code>-<codec>.mp4" in dir, falling back to any other
// "<code>-*.mp4" that is not a derived or hidden file.
func findVideo(dir, code string) string {
	matches, err := filepath.Glob(filepath.Join(dir, code+"-*.mp4"))
	if err != nil {
		return ""
	}
	fallback := ""
	for _, match := range matches {
		base := filepath.Base(match)
		lower := strings.ToLower(base)
		if strings.HasPrefix(lower, ".") || strings.Contains(lower, ".hard.") || strings.Contains(lower, ".withsubs.") {
			continue
		}
		rest := strings.TrimPrefix(strings.TrimSuffix(base, ".mp4"), code+"-")
		if rest != "" && !strings.ContainsAny(rest, ".-") {
			return match
		}
		if fallback == "" {
			fallback = match
		}
	}
	return fallback
}

// hasResumeState reports an interrupted download in dir.
func hasResumeState(dir string) bool {
	entries, err := os.ReadDir(filepath.Join(dir, ".segments"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "seg_") || e.Name() == ".source" {
			return true
		}
	}
	return false
}
