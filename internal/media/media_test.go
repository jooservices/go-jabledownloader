package media

import (
	"context"
	"strings"
	"testing"
)

func TestOSRunnerExecutesHostBinaries(t *testing.T) {
	path, err := OSRunner{}.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	out, err := OSRunner{}.Run(context.Background(), path, "env", "GOOS")
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestOSRunnerStreamsOutput(t *testing.T) {
	path, err := OSRunner{}.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	var out strings.Builder
	if err := (OSRunner{}).RunStreaming(context.Background(), &out, path, "env", "GOOS"); err != nil || strings.TrimSpace(out.String()) == "" {
		t.Fatalf("out=%q err=%v", out.String(), err)
	}
}
