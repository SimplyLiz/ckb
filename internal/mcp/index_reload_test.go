package mcp

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/query"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// newLazyTestServer is the server 'ckb mcp' builds: lazy engine, created on
// the first tool call. scipEnabled false is a config with the backend off.
func newLazyTestServer(t *testing.T, scipEnabled bool) (*MCPServer, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ckb"), 0755); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewMCPServerLazy(version.Version, func() (*query.Engine, error) {
		cfg := config.DefaultConfig()
		cfg.RepoRoot = dir
		cfg.Backends.Git.Enabled = false
		cfg.Backends.Lsp.Enabled = false
		cfg.Backends.Scip.Enabled = scipEnabled
		db, err := storage.Open(dir, logger)
		if err != nil {
			return nil, err
		}
		t.Cleanup(func() { db.Close() })
		return query.NewEngine(dir, db, logger, cfg)
	}, logger)
	return s, dir
}

// callStatus runs getStatus through the tools/call path an MCP client uses
// and returns its data.
func callStatus(t *testing.T, s *MCPServer) map[string]interface{} {
	t.Helper()
	res, err := s.handleCallTool(map[string]interface{}{"name": "getStatus", "arguments": map[string]interface{}{}})
	if err != nil {
		t.Fatalf("getStatus: %v", err)
	}
	content := res.(map[string]interface{})["content"].([]map[string]interface{})
	var env struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal([]byte(content[0]["text"].(string)), &env); err != nil {
		t.Fatalf("decode getStatus: %v", err)
	}
	return env.Data
}

func scipBackendStatus(t *testing.T, data map[string]interface{}) map[string]interface{} {
	t.Helper()
	for _, b := range data["backends"].([]interface{}) {
		if m := b.(map[string]interface{}); m["id"] == "scip" {
			return m
		}
	}
	t.Fatal("no scip backend in getStatus")
	return nil
}

// 'ckb mcp' started in a repo without an index, then 'ckb index' ran in a
// terminal: the next call serves the new index, and getStatus agrees with the
// backend about it. 9.3.0 reported index.fresh true next to a SCIP backend
// saying "Index not found" until the server restarted.
func TestIndexBuiltAfterLazyStart_IsServedOnNextCall(t *testing.T) {
	s, dir := newLazyTestServer(t, true)

	before := callStatus(t, s)
	if idx := before["index"].(map[string]interface{}); idx["exists"] != false || idx["fresh"] != false {
		t.Fatalf("index before build = %v, want exists/fresh false", idx)
	}

	writeFixtureIndex(t, dir) // what 'ckb index' leaves behind

	after := callStatus(t, s)
	idx := after["index"].(map[string]interface{})
	if idx["exists"] != true || idx["fresh"] != true || idx["loaded"] != true {
		t.Fatalf("index after build = %v, want exists, fresh and loaded", idx)
	}
	if b := scipBackendStatus(t, after); b["available"] != true {
		t.Fatalf("scip backend after build = %v, want available", b)
	}
	if ws := s.missingIndexWarnings("findReferences"); len(ws) != 0 {
		t.Fatalf("index_missing still raised after the index was loaded: %+v", ws)
	}
}

// fresh means the running server serves the index, so a server whose SCIP
// backend is off never reports it, whatever the metadata says.
func TestIndexStaleness_NoSCIPBackendIsNeverFresh(t *testing.T) {
	s, dir := newLazyTestServer(t, false)
	writeFixtureIndex(t, dir)

	idx := callStatus(t, s)["index"].(map[string]interface{})
	if idx["fresh"] != false || idx["loaded"] != false {
		t.Fatalf("index = %v, want fresh and loaded false", idx)
	}
	if r, _ := idx["reason"].(string); !strings.Contains(r, "disabled") {
		t.Fatalf("reason = %q, want it to say the backend is disabled", r)
	}
}
