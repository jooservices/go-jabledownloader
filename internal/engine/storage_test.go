package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkOpenTracksFingerprintAndResetsSegments(t *testing.T) {
	dir := t.TempDir()
	w, err := WorkDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })

	fresh, err := w.Open("fp1")
	if err != nil || !fresh {
		t.Fatalf("first Open = fresh %v, err %v", fresh, err)
	}
	segment := filepath.Join(w.SegmentsDir(), "seg_000001")
	if err := os.WriteFile(segment, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh, err = w.Open("fp1")
	if err != nil || fresh {
		t.Fatalf("matching Open = fresh %v, err %v", fresh, err)
	}
	fresh, err = w.Open("fp2")
	if err != nil || !fresh {
		t.Fatalf("changed Open = fresh %v, err %v", fresh, err)
	}
	if _, err := os.Stat(segment); !os.IsNotExist(err) {
		t.Fatalf("old segment still exists, err=%v", err)
	}
}

func TestFinalizeAndPartialPath(t *testing.T) {
	dir := t.TempDir()
	partial := PartialPath(dir)
	final := filepath.Join(dir, "nested", "video.mp4")
	if err := os.WriteFile(partial, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Finalize(partial, final); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(final)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "video" {
		t.Fatalf("final content = %q", data)
	}
}

func TestWorkHelpersValidateInputs(t *testing.T) {
	if _, err := WorkDir(""); err == nil {
		t.Fatal("expected empty work directory error")
	}
	if got := Fingerprint("value"); len(got) != 64 {
		t.Fatalf("fingerprint length=%d", len(got))
	}
	if got := (&Work{dir: "/tmp/work"}).Dir(); got != "/tmp/work" {
		t.Fatal(got)
	}
	if err := (&Work{}).Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Work{}).Open("fp"); err == nil {
		t.Fatal("expected closed work error")
	}
	if err := Finalize("", "x"); err == nil {
		t.Fatal("expected finalize validation")
	}
}

func TestWorkDiscardRemovesSegments(t *testing.T) {
	w, err := WorkDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := w.Discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.SegmentsDir()); !os.IsNotExist(err) {
		t.Fatalf("segments dir still present: %v", err)
	}
}

func TestWorkErrorsOnUnwritableDirectories(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	dir := t.TempDir()
	w, err := WorkDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if _, err := w.Open("fp"); err == nil {
		t.Fatal("expected error creating segments in a read-only dir")
	}
	if err := Finalize(filepath.Join(dir, "missing.part"), filepath.Join(dir, "sub", "final.mp4")); err == nil {
		t.Fatal("expected error finalizing into a read-only dir")
	}
	if err := Finalize(filepath.Join(t.TempDir(), "missing.part"), filepath.Join(t.TempDir(), "final.mp4")); err == nil {
		t.Fatal("expected error for a missing partial file")
	}
	if _, err := WorkDir(filepath.Join(dir, "child")); err == nil {
		t.Fatal("expected error creating a work dir under a read-only dir")
	}
}

func TestWorkOpenRejectsEmptyFingerprint(t *testing.T) {
	w, err := WorkDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if _, err := w.Open(" "); err == nil {
		t.Fatal("expected fingerprint error")
	}
}
