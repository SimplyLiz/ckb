package tier

import (
	"os"
	"path/filepath"
	"testing"
)

// scip_dart lives in the pub cache, which `dart pub global activate` never
// puts on PATH; without the alternative path Dart showed as basic tier.
func TestFindAltPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PUB_CACHE", "")
	bin := filepath.Join(home, ".pub-cache", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := []string{"$PUB_CACHE/bin/scip_dart", "~/.pub-cache/bin/scip_dart"}
	if got := findAltPath(paths); got != "" {
		t.Fatalf("findAltPath = %q before install, want none", got)
	}
	exe := filepath.Join(bin, "scip_dart")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findAltPath(paths); got != exe {
		t.Errorf("findAltPath = %q, want %q", got, exe)
	}
	custom := t.TempDir()
	t.Setenv("PUB_CACHE", custom)
	if err := os.MkdirAll(filepath.Join(custom, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	cexe := filepath.Join(custom, "bin", "scip_dart")
	if err := os.WriteFile(cexe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findAltPath(paths); got != cexe {
		t.Errorf("findAltPath = %q, want $PUB_CACHE's %q", got, cexe)
	}
}
