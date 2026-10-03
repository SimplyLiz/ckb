package scip

import (
	"os"
	"path/filepath"
	"testing"
)

// A truncated index — what a server sees when it loads while the indexer is
// still writing — must come back as an error. It used to panic the parser
// goroutine (slice bounds out of range), taking the whole process down.
func TestLoadSCIPIndex_TruncatedFileIsAnError(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "go", ".scip", "index.scip"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	for _, cut := range []int{1, 2, len(fixture) / 3, len(fixture) / 2, len(fixture) - 1} {
		path := filepath.Join(t.TempDir(), "index.scip")
		if err := os.WriteFile(path, fixture[:cut], 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := LoadSCIPIndex(path); err == nil {
			t.Errorf("cut at %d/%d bytes: loaded without error", cut, len(fixture))
		}
	}
}

func TestLoadSCIPIndex_GarbageIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.scip")
	if err := os.WriteFile(path, []byte("not a scip index"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadSCIPIndex(path); err == nil {
		t.Fatal("garbage loaded without error")
	}
}
