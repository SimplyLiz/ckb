package repos

import (
	"path/filepath"
	"testing"
)

// The registry lives under CKB_HOME like the rest of CKB's global state;
// ignoring it put sandboxed runs' temp repos into the real ~/.ckb/repos.json.
func TestRegistryPath_HonorsCKBHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CKB_HOME", home)

	reg, err := GetRegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "repos.json"); reg != want {
		t.Fatalf("registry path = %s, want %s", reg, want)
	}
	lock, err := GetLockPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "repos.lock"); lock != want {
		t.Fatalf("lock path = %s, want %s", lock, want)
	}
}
