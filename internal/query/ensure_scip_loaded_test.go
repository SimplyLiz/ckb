package query

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func goFixtureIndex(t *testing.T) []byte {
	t.Helper()
	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "go", ".scip", "index.scip"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return fixture
}

// The #134 case: a server started before 'ckb index' ran must pick up the
// index on its next query without a restart.
func TestEnsureSCIPLoaded_PicksUpIndexWrittenAfterStartup(t *testing.T) {
	dir := t.TempDir()
	engine := newTestEngineWithGit(t, dir)
	ctx := context.Background()

	if reloaded, err := engine.EnsureSCIPLoaded(ctx); err != nil || reloaded {
		t.Fatalf("no index on disk: reloaded=%v err=%v, want false/nil", reloaded, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "index.scip"), goFixtureIndex(t), 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	reloaded, err := engine.EnsureSCIPLoaded(ctx)
	if err != nil {
		t.Fatalf("EnsureSCIPLoaded: %v", err)
	}
	if !reloaded {
		t.Fatal("index appeared on disk but EnsureSCIPLoaded did not reload")
	}
	if !engine.GetScipBackend().IsAvailable() {
		t.Fatal("SCIP still unavailable after reload")
	}

	// Unchanged file: no second reload.
	if reloaded, err := engine.EnsureSCIPLoaded(ctx); err != nil || reloaded {
		t.Fatalf("unchanged index: reloaded=%v err=%v, want false/nil", reloaded, err)
	}
}

// Many queries racing to notice the same change must share one reload.
func TestEnsureSCIPLoaded_ConcurrentCallersShareOneReload(t *testing.T) {
	dir := t.TempDir()
	engine := newTestEngineWithGit(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), goFixtureIndex(t), 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	const n = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	reloads := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reloaded, err := engine.EnsureSCIPLoaded(context.Background())
			if err != nil {
				t.Errorf("EnsureSCIPLoaded: %v", err)
			}
			if reloaded {
				mu.Lock()
				reloads++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if reloads != 1 {
		t.Fatalf("got %d reloads for one file change, want exactly 1", reloads)
	}
	if !engine.GetScipBackend().IsAvailable() {
		t.Fatal("SCIP unavailable after concurrent reload")
	}
}

// A file that fails to load (half-written by the indexer, or corrupt) is
// reported once and then left alone until it changes again; the previously
// loaded index keeps serving meanwhile.
func TestEnsureSCIPLoaded_FailedFileIsNotRetriedUntilItChanges(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.scip")
	if err := os.WriteFile(indexPath, goFixtureIndex(t), 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	engine := newTestEngineWithGit(t, dir)
	ctx := context.Background()
	if !engine.GetScipBackend().IsAvailable() {
		t.Fatal("SCIP not available at startup")
	}

	if err := os.WriteFile(indexPath, []byte("not a scip index"), 0644); err != nil {
		t.Fatalf("corrupt index: %v", err)
	}
	if _, err := engine.EnsureSCIPLoaded(ctx); err == nil {
		t.Fatal("corrupt index loaded without error")
	}
	if engine.GetScipBackend().GetIndex() == nil {
		t.Fatal("previous index dropped after failed reload")
	}

	// Same corrupt file again: no retry, no error.
	if reloaded, err := engine.EnsureSCIPLoaded(ctx); err != nil || reloaded {
		t.Fatalf("second check on unchanged corrupt file: reloaded=%v err=%v, want false/nil", reloaded, err)
	}

	// Indexer finishes writing a good file: picked up.
	if err := os.WriteFile(indexPath, goFixtureIndex(t), 0644); err != nil {
		t.Fatalf("rewrite index: %v", err)
	}
	if reloaded, err := engine.EnsureSCIPLoaded(ctx); err != nil || !reloaded {
		t.Fatalf("valid rewrite: reloaded=%v err=%v, want true/nil", reloaded, err)
	}
}

// getStatus must describe the index on disk, not the one from startup.
func TestGetStatus_ReportsIndexWrittenAfterStartup(t *testing.T) {
	dir := t.TempDir()
	engine := newTestEngineWithGit(t, dir)
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(dir, "index.scip"), goFixtureIndex(t), 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	status, err := engine.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	for _, b := range status.Backends {
		if b.Id == "scip" {
			if !b.Available {
				t.Fatalf("scip backend reported unavailable after index was written: %+v", b)
			}
			return
		}
	}
	t.Fatal("no scip backend in status")
}
