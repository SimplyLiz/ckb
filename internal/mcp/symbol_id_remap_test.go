package mcp

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/query"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// scip-go writes the HEAD commit into the version field of every ID of the
// repo's own symbols, so an ID kept from before a commit + reindex must still
// resolve.
func TestCallTool_SymbolIDFromAnotherCommitResolves(t *testing.T) {
	dir := t.TempDir()
	fixturePath := filepath.Join("..", "..", "testdata", "fixtures", "go", ".scip", "index.scip")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), fixture, 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	idx, err := scip.LoadSCIPIndex(fixturePath)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	// A defined function of the fixture module.
	var current string
	for id := range idx.DefinitionIndex {
		if strings.HasPrefix(id, "scip-go gomod ") && strings.HasSuffix(id, "().") && len(strings.Fields(id)) >= 5 {
			if current == "" || id < current {
				current = id
			}
		}
	}
	if current == "" {
		t.Fatal("fixture has no scip-go function definition")
	}
	f := strings.SplitN(current, " ", 5)
	f[3] = "0000000aaaaa"
	if f[3] == strings.SplitN(current, " ", 5)[3] {
		t.Fatal("fixture already uses the foreign version")
	}
	foreign := strings.Join(f, " ")

	cfg := config.DefaultConfig()
	cfg.RepoRoot = dir
	cfg.Backends.Git.Enabled = false
	cfg.Backends.Lsp.Enabled = false
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := storage.Open(dir, logger)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	engine, err := query.NewEngine(dir, db, logger, cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	server := NewMCPServer(version.Version, engine, logger)

	resp, err := server.CallTool("findReferences", map[string]interface{}{"symbolId": foreign})
	if err != nil {
		t.Fatalf("findReferences with %q: %v", foreign, err)
	}
	var remapped bool
	for _, w := range resp.Warnings {
		if w.Code == WarnSymbolIDVersionRemapped && strings.Contains(w.Message, current) {
			remapped = true
		}
	}
	if !remapped {
		t.Errorf("no %s warning naming %q: %+v", WarnSymbolIDVersionRemapped, current, resp.Warnings)
	}

	// The exact ID gets no warning.
	resp, err = server.CallTool("findReferences", map[string]interface{}{"symbolId": current})
	if err != nil {
		t.Fatalf("findReferences with %q: %v", current, err)
	}
	for _, w := range resp.Warnings {
		if w.Code == WarnSymbolIDVersionRemapped {
			t.Errorf("exact ID was remapped: %+v", w)
		}
	}
}
