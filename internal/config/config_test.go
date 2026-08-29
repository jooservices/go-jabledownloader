package config

import (
	"encoding/json"
	"os"
	"path/filepath"
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
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OutputDir != "./videos" {
		t.Fatalf("unexpected output dir: %q", cfg.OutputDir)
	}
}

func TestLoadExistingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".config", "jabledownloader")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(Config{OutputDir: "/tmp/videos", WorkerCount: 4})
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
}

func TestSaveAndReload(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := Defaults()
	cfg.OutputDir = "/tmp/save-test"
	cfg.WorkerCount = 8
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.OutputDir != "/tmp/save-test" || reloaded.WorkerCount != 8 {
		t.Fatalf("round-trip mismatch: %+v", reloaded)
	}
}
