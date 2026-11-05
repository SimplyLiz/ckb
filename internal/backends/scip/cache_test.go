package scip

import (
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"
)

// A derived cache written by a release without (or with a different)
// DerivedCacheVersion must be discarded even though the .scip file is
// unchanged; otherwise an upgrade keeps old kinds and an empty Alias.
func TestLoadDerivedCache_RejectsOtherVersion(t *testing.T) {
	dir := t.TempDir()
	scipPath := filepath.Join(dir, "index.scip")
	if err := os.WriteFile(scipPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(scipPath)

	write := func(version int) string {
		p := filepath.Join(dir, "cache.gob")
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		c := derivedCache{Version: version, ScipModTime: fi.ModTime().UnixNano(), ScipSize: fi.Size()}
		if err := gob.NewEncoder(f).Encode(&c); err != nil {
			t.Fatal(err)
		}
		return p
	}

	if loadDerivedCache(write(0), scipPath) != nil {
		t.Error("cache without a version (pre-9.4 file) was accepted")
	}
	if loadDerivedCache(write(DerivedCacheVersion+1), scipPath) != nil {
		t.Error("cache from another version was accepted")
	}
	if loadDerivedCache(write(DerivedCacheVersion), scipPath) == nil {
		t.Error("current-version cache was rejected")
	}
}
