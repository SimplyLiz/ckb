package mcp

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/query"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// #134: an MCP server started before 'ckb index' ran must answer getStatus
// (and every other tool) from the index that is on disk now, without a
// reconnect.
func TestToolCall_PicksUpIndexBuiltAfterStartup(t *testing.T) {
	dir := t.TempDir()
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

	scipStatus := func() map[string]interface{} {
		t.Helper()
		resp, err := server.CallTool("getStatus", map[string]interface{}{})
		if err != nil {
			t.Fatalf("getStatus: %v", err)
		}
		data, ok := resp.Data.(map[string]interface{})
		if !ok {
			t.Fatalf("getStatus data has type %T", resp.Data)
		}
		backends, ok := data["backends"].([]map[string]interface{})
		if !ok {
			t.Fatalf("getStatus backends has type %T", data["backends"])
		}
		for _, b := range backends {
			if b["id"] == "scip" {
				return b
			}
		}
		t.Fatal("no scip backend in getStatus")
		return nil
	}

	if b := scipStatus(); b["available"] == true {
		t.Fatalf("scip available before any index exists: %+v", b)
	}

	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "go", ".scip", "index.scip"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), fixture, 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	if b := scipStatus(); b["available"] != true {
		t.Fatalf("scip still unavailable after index was written: %+v", b)
	}
}
