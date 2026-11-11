package query

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

// nextFixtureEngine copies testdata/next, a real scip-typescript index of a
// two-route Next.js app (regenerate with `scip-typescript index --infer-tsconfig`
// in that directory; *.scip is gitignored, so `git add -f` it), into a temp dir
// and opens an engine on it. src/app/page.tsx and src/app/api/hello/route.ts
// call greet() from src/lib/used.ts; src/lib/unused.ts is never imported.
func nextFixtureEngine(t *testing.T) *Engine {
	t.Helper()
	src, err := filepath.Abs("testdata/next")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := os.MkdirAll(filepath.Join(dir, ".ckb"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(dir, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := config.DefaultConfig()
	cfg.RepoRoot = dir
	cfg.Backends.Scip.Enabled = true
	cfg.Backends.Scip.IndexPath = filepath.Join(dir, "index.scip")
	e, err := NewEngine(dir, db, logger, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if e.scipAdapter == nil || !e.scipAdapter.IsAvailable() {
		t.Fatal("fixture index not loaded")
	}
	return e
}

// A Next.js app has no main and no server file; its entrypoints are the files
// the framework calls. Without them the walk had nothing to start from and
// reported 0 exported, 0 unwired, which read as a clean bill.
func TestFindUnwiredModulesNextApp(t *testing.T) {
	e := nextFixtureEngine(t)
	resp, err := e.FindUnwiredModules(context.Background(), FindUnwiredModulesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Entrypoints) < 2 {
		t.Fatalf("entrypoints = %v, want page.tsx and route.ts", resp.Entrypoints)
	}
	if resp.ReachableCount == 0 || resp.Summary.TotalExported == 0 {
		t.Fatalf("reachable %d, exported %d: nothing analyzed", resp.ReachableCount, resp.Summary.TotalExported)
	}
	got := map[string]string{}
	for _, m := range resp.UnwiredModules {
		for _, item := range m.Items {
			got[item.SymbolName] = item.FilePath
		}
	}
	if got["neverCalled"] != "src/lib/unused.ts" {
		t.Errorf("neverCalled not reported from src/lib/unused.ts; got %v", got)
	}
	for _, name := range []string{"greet", "shout", "Home", "GET"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s is reached from a Next.js entrypoint but reported unwired", name)
		}
	}
}

// Pages, route handlers and the metadata Next.js reads have no reference in
// the index. They are not dead code, and the file a finding names is the
// file the symbol is defined in, not a guess from the symbol ID.
func TestFindDeadCodeNextApp(t *testing.T) {
	e := nextFixtureEngine(t)
	resp, err := e.FindDeadCode(context.Background(), FindDeadCodeOptions{IncludeExported: true})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]DeadCodeItem{}
	for _, item := range resp.DeadCode {
		got[item.SymbolName] = item
	}
	// shout is exported but used only inside its file: a 0.60 note under the
	// default confidence, not a finding.
	for _, name := range []string{"Home", "GET", "metadata", "dynamic", "greet", "shout", "privateHelper"} {
		if item, ok := got[name]; ok {
			t.Errorf("%s reported dead: %+v", name, item)
		}
	}
	for name, kind := range map[string]string{"neverCalled": "function", "UNUSED_MAX": "constant", "LIMITS": "constant"} {
		item, ok := got[name]
		if !ok {
			t.Errorf("%s not reported; got %v", name, resp.DeadCode)
			continue
		}
		if !strings.HasPrefix(item.FilePath, "src/lib/") || item.LineNumber == 0 || item.Kind != kind || item.SymbolID == "" {
			t.Errorf("%s = %+v, want a src/lib file with a line, kind %s and an ID", name, item, kind)
		}
	}
}
