// Package archtest enforces the layering documented in AGENTS.md: a wrong
// import fails the build's tests instead of eroding the structure.
package archtest

import (
	"go/build"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

const module = "github.com/jooservices/go-jabledownloader/"

// rules maps a package path prefix to the internal packages it must not
// import (checked by prefix).
var rules = map[string][]string{
	"internal/domain":   {"internal/"},
	"internal/engine":   {"internal/app", "internal/ui", "internal/site", "internal/media", "internal/config", "internal/telemetry"},
	"internal/site":     {"internal/app", "internal/ui", "internal/engine", "internal/media", "internal/config", "internal/telemetry"},
	"internal/media":    {"internal/app", "internal/ui", "internal/engine", "internal/site", "internal/config", "internal/telemetry"},
	"internal/platform": {"internal/app", "internal/ui", "internal/engine", "internal/site", "internal/media", "internal/domain"},
	// The app depends on contracts only; concrete engines, sites,
	// translators, and the UI are wired in cmd.
	"internal/app": {"internal/ui", "internal/media", "internal/engine/", "internal/site/", "internal/platform/"},
	"internal/ui":  {"internal/engine", "internal/site", "internal/media", "internal/config", "internal/telemetry"},
}

// allowed carves exceptions out of a rule.
var allowed = map[string][]string{
	"internal/app": {"internal/platform/httpx"}, // page client for Sites
}

func TestLayering(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || d.Name() == "testdata" {
			return err
		}
		pkg, err := build.ImportDir(path, 0)
		if err != nil {
			return nil // no Go files
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		for prefix, forbidden := range rules {
			if rel != prefix && !strings.HasPrefix(rel, prefix+"/") {
				continue
			}
			checked++
			for _, imp := range pkg.Imports {
				dep, ok := strings.CutPrefix(imp, module)
				if !ok || isAllowed(prefix, dep) {
					continue
				}
				for _, f := range forbidden {
					if strings.HasPrefix(dep, f) && !strings.HasPrefix(dep, rel) {
						t.Errorf("%s must not import %s", rel, dep)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 15 {
		t.Fatalf("only %d packages checked; the walk is broken", checked)
	}
}

func isAllowed(prefix, dep string) bool {
	for _, a := range allowed[prefix] {
		if dep == a {
			return true
		}
	}
	return false
}
