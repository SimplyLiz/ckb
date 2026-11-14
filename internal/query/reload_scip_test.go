package query

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// An engine built before the first index exists — the normal case for
// 'ckb mcp --watch' on a fresh project, where the agent's first tool call
// beats the background indexer — must pick the index up once it is written.
func TestReloadSCIP_PicksUpIndexBuiltAfterStartup(t *testing.T) {
	dir := t.TempDir()
	engine := newTestEngineWithGit(t, dir)

	scip := engine.GetScipBackend()
	if scip == nil {
		t.Fatal("SCIP backend not registered")
	}
	if scip.IsAvailable() {
		t.Fatal("SCIP available before any index exists")
	}

	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "go", ".scip", "index.scip"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), fixture, 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	if err := engine.ReloadSCIP(context.Background()); err != nil {
		t.Fatalf("ReloadSCIP: %v", err)
	}
	if !scip.IsAvailable() {
		t.Fatal("SCIP still unavailable after ReloadSCIP")
	}
	if got := engine.ActiveBackendName(); got != "scip" {
		t.Errorf("ActiveBackendName = %q, want scip", got)
	}
}

// A failed reload must not throw away an index that was already loaded.
func TestReloadSCIP_FailedLoadKeepsPreviousIndex(t *testing.T) {
	dir := t.TempDir()
	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "go", ".scip", "index.scip"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	indexPath := filepath.Join(dir, "index.scip")
	if err := os.WriteFile(indexPath, fixture, 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	engine := newTestEngineWithGit(t, dir)
	if !engine.GetScipBackend().IsAvailable() {
		t.Fatal("SCIP not available at startup")
	}

	if err := os.WriteFile(indexPath, []byte("not a scip index"), 0644); err != nil {
		t.Fatalf("corrupt index: %v", err)
	}
	if err := engine.ReloadSCIP(context.Background()); err == nil {
		t.Fatal("ReloadSCIP succeeded on a corrupt index")
	}
	if engine.GetScipBackend().GetIndex() == nil {
		t.Fatal("previous index dropped after failed reload")
	}
}
