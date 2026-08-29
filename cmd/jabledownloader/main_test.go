package main

import "testing"

func TestRootCommandHasCommands(t *testing.T) {
	root := newRootCmd()
	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"get", "search", "latest", "hot", "update", "completion"} {
		if !names[want] {
			t.Errorf("missing command %q", want)
		}
	}
}

func TestCompletionRejectsUnknownShell(t *testing.T) {
	root := newRootCmd()
	cmd := newCompletionCmd(root)
	cmd.SetArgs([]string{"nope"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for unsupported shell")
	}
}
