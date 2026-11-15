package scip

import (
	"os"
	"path/filepath"
	"testing"
)

// The derived-index cache is gone; files earlier releases left in .ckb/ (the
// cache and the .tmp files of interrupted background writes) are removed, and
// nothing else in .ckb/ is touched.
func TestRemoveObsoleteDerivedCache(t *testing.T) {
	root := t.TempDir()
	ckb := filepath.Join(root, ".ckb")
	if err := os.MkdirAll(ckb, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"scip_derived.gob", "scip_derived.gob.tmp.20261111120540", "ckb.db", "index-meta.json"} {
		if err := os.WriteFile(filepath.Join(ckb, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	(&SCIPAdapter{repoRoot: root}).removeObsoleteDerivedCache()

	entries, err := os.ReadDir(ckb)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if len(left) != 2 || left[0] != "ckb.db" || left[1] != "index-meta.json" {
		t.Fatalf(".ckb/ after cleanup = %v, want [ckb.db index-meta.json]", left)
	}
}
