package scip

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	indexmeta "github.com/SimplyLiz/CodeMCP/internal/index"
)

func TestFileStamp_Equal(t *testing.T) {
	now := time.Now()
	a := fileStamp{exists: true, size: 10, modTime: now}
	cases := []struct {
		name string
		b    fileStamp
		want bool
	}{
		{"same", fileStamp{exists: true, size: 10, modTime: now}, true},
		{"size differs", fileStamp{exists: true, size: 11, modTime: now}, false},
		{"mtime differs", fileStamp{exists: true, size: 10, modTime: now.Add(time.Second)}, false},
		{"missing", fileStamp{}, false},
	}
	for _, c := range cases {
		if got := a.equal(c.b); got != c.want {
			t.Errorf("%s: equal = %v, want %v", c.name, got, c.want)
		}
	}
	if !(fileStamp{}).equal(fileStamp{}) {
		t.Error("two missing-file stamps must be equal")
	}
}

func TestIndexFileChanged(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.scip")
	a := &SCIPAdapter{indexPath: indexPath}

	if a.IndexFileChanged() {
		t.Fatal("missing file reported as changed")
	}

	if err := os.WriteFile(indexPath, []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}
	if !a.IndexFileChanged() {
		t.Fatal("new file with nothing loaded not reported as changed")
	}

	// Pretend v1 was loaded.
	a.loadedStamp = statFile(indexPath)
	if a.IndexFileChanged() {
		t.Fatal("loaded file reported as changed")
	}

	// Rewrite with a different size (mtime granularity may be coarse).
	if err := os.WriteFile(indexPath, []byte("v2 longer"), 0644); err != nil {
		t.Fatal(err)
	}
	if !a.IndexFileChanged() {
		t.Fatal("rewritten file not reported as changed")
	}

	// A file that failed to load is not reported again.
	a.failedStamp = statFile(indexPath)
	if a.IndexFileChanged() {
		t.Fatal("failed file reported as changed again")
	}

	// Deleted: not a change (IsAvailable handles it).
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	if a.IndexFileChanged() {
		t.Fatal("deleted file reported as changed")
	}
}

// scip-typescript (and others) write no commit into the SCIP metadata; the
// commit 'ckb index' recorded in .ckb/index-meta.json fills the gap so
// freshness is not "unknown" for those languages.
func TestLoadIndex_FallsBackToIndexMetaCommit(t *testing.T) {
	dir := t.TempDir()
	fixture, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "go", ".scip", "index.scip"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), fixture, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ckb"), 0755); err != nil {
		t.Fatal(err)
	}
	meta := &indexmeta.IndexMeta{CreatedAt: time.Now(), CommitHash: "abc123def"}
	if err := meta.Save(filepath.Join(dir, ".ckb")); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.RepoRoot = dir
	a, err := NewSCIPAdapter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if !a.IsAvailable() {
		t.Fatal("index not loaded")
	}
	info := a.GetIndexInfo()
	if info.IndexedCommit != "abc123def" && extractCommitFromToolInfo(a.GetIndex().Metadata.ToolInfo) == "" {
		t.Fatalf("IndexedCommit = %q, want abc123def from index-meta.json", info.IndexedCommit)
	}
}
