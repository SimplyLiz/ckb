package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/query"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

// An ID from an index at another commit resolves over HTTP as over MCP, and the
// response says which ID was used.
func TestHandleFindReferences_SymbolIDFromAnotherCommit(t *testing.T) {
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
	s := &Server{engine: engine, logger: logger}

	get := func(id string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.handleFindReferences(w, httptest.NewRequest(http.MethodGet, "/refs/"+url.PathEscape(id), nil))
		return w
	}

	w := get(foreign)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d for %q: %s", w.Code, foreign, w.Body.String())
	}
	if got, want := w.Header().Get(symbolIDRemappedHeader), foreign+" -> "+current; got != want {
		t.Errorf("%s = %q, want %q", symbolIDRemappedHeader, got, want)
	}

	if h := get(current).Header().Get(symbolIDRemappedHeader); h != "" {
		t.Errorf("exact ID was remapped: %q", h)
	}
}
