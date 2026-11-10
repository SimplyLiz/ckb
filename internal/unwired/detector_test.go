package unwired

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/architecture"
	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/config"
)

// testdata/ts is a real scip-typescript index (regenerate with
// `scip-typescript index --output index.scip` in that directory; *.scip is
// gitignored, so `git add -f` it).
// src/index.ts calls wired() from src/lib.ts; nothing uses the rest.
func TestAnalyzeTypeScriptFixture(t *testing.T) {
	root, err := filepath.Abs("testdata/ts")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.DefaultConfig()
	cfg.RepoRoot = root
	cfg.Backends.Scip.Enabled = true
	cfg.Backends.Scip.IndexPath = filepath.Join(root, "index.scip")
	adapter, err := scip.NewSCIPAdapter(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}

	d := NewDetector(adapter, root, logger)
	reachable, _ := d.BuildReachableSet(context.Background(), []architecture.Entrypoint{
		{FileId: "src/index.ts", Name: "index.ts", Kind: architecture.EntrypointServer},
	})
	result, err := d.Analyze(context.Background(), DetectorOptions{}, reachable)
	if err != nil {
		t.Fatal(err)
	}

	got := make(map[string]UnwiredItem)
	for _, m := range result.UnwiredModules {
		for _, item := range m.Items {
			got[item.SymbolName] = item
		}
	}
	// Exported and unused: reported, with the file and line of the definition,
	// even though lib.ts is reachable through wired().
	for name, line := range map[string]int{"unwiredFn": 3, "arrowUnwired": 4, "Box#open": 6} {
		item, ok := got[name]
		if !ok {
			t.Errorf("%s not reported; got %v", name, got)
			continue
		}
		if item.FilePath != "src/lib.ts" || item.LineNumber != line || item.Module != "src" {
			t.Errorf("%s at %s:%d in %q, want src/lib.ts:%d in src", name, item.FilePath, item.LineNumber, item.Module, line)
		}
	}
	// wired is used; helper and privateUnused are not exported, so the
	// compiler's unused-code checks own them.
	for _, name := range []string{"wired", "helper", "privateUnused"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s reported as unwired", name)
		}
	}
	if result.Summary.TotalExported != 4 {
		t.Errorf("TotalExported = %d, want 4 (wired, unwiredFn, arrowUnwired, Box#open)", result.Summary.TotalExported)
	}
}
