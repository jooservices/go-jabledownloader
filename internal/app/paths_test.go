package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLayoutVideoDir(t *testing.T) {
	base := t.TempDir()
	tests := []struct {
		name     string
		template string
		want     string
	}{
		{name: "default", template: "", want: filepath.Join(base, "jable", "abc-1")},
		{name: "legacy", template: "{code}", want: filepath.Join(base, "abc-1")},
		{name: "nested", template: "videos/{site}-{code}", want: filepath.Join(base, "videos", "jable-abc-1")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := (Layout{Base: base, Template: test.template}).VideoDir("jable", "abc-1")
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("VideoDir = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLayoutVideoDirRejectsInvalidTemplates(t *testing.T) {
	base := t.TempDir()
	for _, test := range []struct {
		template string
		want     string
	}{
		{template: "../{code}", want: "escapes output dir"},
		{template: "{site}", want: "must contain {code}"},
		{template: "{foo}/{code}", want: "unknown placeholder {foo}"},
	} {
		t.Run(test.template, func(t *testing.T) {
			_, err := (Layout{Base: base, Template: test.template}).VideoDir("jable", "abc-1")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLayoutFindCompleteChecksConfiguredThenLegacy(t *testing.T) {
	base := t.TempDir()
	layout := Layout{Base: base, Template: "{site}/{code}"}
	legacy := filepath.Join(base, "abc-1")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "abc-1-h264.mp4"
	if err := os.WriteFile(filepath.Join(legacy, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := layout.FindComplete("jable", "abc-1", name)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(legacy, name) {
		t.Fatalf("FindComplete = %q, want legacy file", got)
	}

	current := filepath.Join(base, "jable", "abc-1")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = layout.FindComplete("jable", "abc-1", name)
	if err != nil || got != filepath.Join(current, name) {
		t.Fatalf("FindComplete = %q, %v; want configured file", got, err)
	}
}

func TestLayoutFindCompleteIgnoresResumeState(t *testing.T) {
	base := t.TempDir()
	layout := Layout{Base: base, Template: "{site}/{code}"}
	dir := filepath.Join(base, "abc-1")
	if err := os.MkdirAll(filepath.Join(dir, ".segments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".segments", "seg_000000.ts"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "abc-1-h264.mp4"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := layout.FindComplete("jable", "abc-1", name); err != nil || got != "" {
		t.Fatalf("FindComplete = %q, %v; want no complete file", got, err)
	}
}

func TestLayoutRejectsUnsafeCodesAndBadTemplates(t *testing.T) {
	layout := Layout{Base: t.TempDir()}
	for _, code := range []string{"..", ".", "a/b", ""} {
		if _, err := layout.VideoDir("jable", code); err == nil || !strings.Contains(err.Error(), "unsafe video code") {
			t.Errorf("VideoDir(%q) err = %v", code, err)
		}
	}
	if err := ValidatePathTemplate("{site/{code}"); err == nil {
		t.Fatal("expected placeholder error")
	}
	if err := ValidatePathTemplate("{code"); err == nil || !strings.Contains(err.Error(), "must contain {code}") {
		t.Fatalf("err = %v", err)
	}
	if err := ValidatePathTemplate("x/{code}/{"); err == nil || !strings.Contains(err.Error(), "unclosed") {
		t.Fatalf("err = %v", err)
	}
	for _, escaping := range []string{"../{code}", "/abs/{code}", "a/../../{code}"} {
		if err := ValidatePathTemplate(escaping); err == nil || !strings.Contains(err.Error(), "escapes output dir") {
			t.Errorf("ValidatePathTemplate(%q) = %v", escaping, err)
		}
	}
}

func TestLayoutSeparatesSites(t *testing.T) {
	if !(Layout{}).SeparatesSites() || (Layout{Template: "{code}"}).SeparatesSites() {
		t.Fatal("SeparatesSites mismatch")
	}
}

func TestValidateFileName(t *testing.T) {
	for _, ok := range []string{"", "my.mp4", "video name.mp4"} {
		if err := ValidateFileName(ok); err != nil {
			t.Errorf("ValidateFileName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"../x.mp4", "a/b.mp4", `a\b.mp4`, "..", "."} {
		if err := ValidateFileName(bad); err == nil {
			t.Errorf("ValidateFileName(%q): expected error", bad)
		}
	}
}

func TestFindVideoPrefersPrimaryName(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"abc-1-h264.hard.mp4", ".abc-1-h264.mp4", "abc-1-extra-name.mp4", "abc-1-h264.mp4"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := findVideo(dir, "abc-1"); filepath.Base(got) != "abc-1-h264.mp4" {
		t.Fatalf("findVideo = %q", got)
	}
	if err := os.Remove(filepath.Join(dir, "abc-1-h264.mp4")); err != nil {
		t.Fatal(err)
	}
	if got := findVideo(dir, "abc-1"); filepath.Base(got) != "abc-1-extra-name.mp4" {
		t.Fatalf("fallback = %q", got)
	}
	if got := findVideo(t.TempDir(), "abc-1"); got != "" {
		t.Fatalf("empty dir = %q", got)
	}
}

func TestLayoutFindCompleteWithoutCustomName(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "jable", "abc-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "abc-1-h264.mp4"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := (Layout{Base: base}).FindComplete("jable", "abc-1", "")
	if err != nil || filepath.Base(got) != "abc-1-h264.mp4" {
		t.Fatalf("FindComplete = %q, %v", got, err)
	}
	if _, err := (Layout{Base: base}).FindComplete("jable", "..", ""); err == nil {
		t.Fatal("expected unsafe code error")
	}
}
