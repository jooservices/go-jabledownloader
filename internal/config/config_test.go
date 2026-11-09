package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.WorkerCount != DefaultWorkerCount {
		t.Fatalf("expected %d workers, got %d", DefaultWorkerCount, cfg.WorkerCount)
	}
	if cfg.OutputDir != "./videos" {
		t.Fatalf("unexpected output dir: %q", cfg.OutputDir)
	}
	if cfg.PathTemplate != "{site}/{code}" {
		t.Fatalf("unexpected path template: %q", cfg.PathTemplate)
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	isolate(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OutputDir != "./videos" {
		t.Fatalf("unexpected output dir: %q", cfg.OutputDir)
	}
}

func TestLoadExistingFile(t *testing.T) {
	dir := isolate(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(Config{OutputDir: "/tmp/videos", PathTemplate: "archive/{code}", WorkerCount: 4})
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OutputDir != "/tmp/videos" {
		t.Fatalf("unexpected output dir: %q", cfg.OutputDir)
	}
	if cfg.WorkerCount != 4 {
		t.Fatalf("unexpected worker count: %d", cfg.WorkerCount)
	}
	if cfg.PathTemplate != "archive/{code}" {
		t.Fatalf("unexpected path template: %q", cfg.PathTemplate)
	}
}

func TestPath(t *testing.T) {
	isolate(t)
	if filepath.Base(Path()) != "config.json" {
		t.Fatalf("Path() = %q", Path())
	}
}

func TestSaveAndReload(t *testing.T) {
	isolate(t)

	cfg := Defaults()
	cfg.OutputDir = "/tmp/save-test"
	cfg.PathTemplate = "saved/{site}/{code}"
	cfg.WorkerCount = 8
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.OutputDir != "/tmp/save-test" || reloaded.PathTemplate != "saved/{site}/{code}" || reloaded.WorkerCount != 8 {
		t.Fatalf("round-trip mismatch: %+v", reloaded)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	dir := isolate(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// isolate points the config at a private directory so tests never touch the
// real user config, on any OS.
func isolate(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cfg")
	t.Setenv(DirEnv, dir)
	return dir
}

func TestConfigDirPrecedence(t *testing.T) {
	t.Setenv(DirEnv, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("APPDATA", filepath.Join(home, "appdata"))

	native := configDir()
	if filepath.Base(native) != "jabledownloader" {
		t.Fatalf("native dir = %q", native)
	}
	legacy := filepath.Join(home, ".config", "jabledownloader")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := configDir(); got != legacy {
		t.Fatalf("legacy config not preferred: %q", got)
	}
	t.Setenv(DirEnv, "/custom")
	if got := configDir(); got != "/custom" {
		t.Fatalf("env override ignored: %q", got)
	}
}
