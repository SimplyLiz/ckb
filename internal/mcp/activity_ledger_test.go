package mcp

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/query"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// newActivityTestServer builds a minimal MCP server (all heavyweight
// backends disabled, same as newTestMCPServer) but with the activity
// ledger explicitly enabled, so handleCallTool actually records rows.
func newActivityTestServer(t *testing.T) (*MCPServer, *storage.DB) {
	t.Helper()
	resetActivityRecorders()
	t.Cleanup(resetActivityRecorders)

	tempDir := t.TempDir()

	cfg := &config.Config{
		Version:  5,
		RepoRoot: tempDir,
		Backends: config.BackendsConfig{
			Git:  config.GitConfig{Enabled: false},
			Scip: config.ScipConfig{Enabled: false},
			Lsp:  config.LspConfig{Enabled: false},
		},
		Activity: config.ActivityConfig{
			Enabled:       true,
			RetentionDays: 30,
			StoreParams:   true,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := storage.Open(tempDir, logger)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	engine, err := query.NewEngine(tempDir, db, logger, cfg)
	if err != nil {
		t.Fatalf("failed to create query engine: %v", err)
	}

	server := NewMCPServer(version.Version, engine, logger)
	return server, db
}

func initializeServer(t *testing.T, server *MCPServer, clientName, clientVersion string) {
	t.Helper()
	params := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    clientName,
			"version": clientVersion,
		},
	}
	if resp := sendRequest(t, server, "initialize", 1, params); resp == nil {
		t.Fatalf("expected a response to initialize")
	}
}

func TestHandleCallTool_RecordsActivityOnSuccess(t *testing.T) {
	server, db := newActivityTestServer(t)
	initializeServer(t, server, "test-client", "9.9.9")

	callTool(t, server, "getStatus", map[string]interface{}{})
	closeAllActivityRecorders() // force-flush the buffered write for this test

	calls, err := db.ListToolCalls(storage.ToolCallFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListToolCalls failed: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 recorded call, got %d: %+v", len(calls), calls)
	}
	c := calls[0]
	if c.Tool != "getStatus" {
		t.Errorf("expected tool=getStatus, got %q", c.Tool)
	}
	if c.Consumer != "test-client/9.9.9" {
		t.Errorf("expected consumer from clientInfo, got %q", c.Consumer)
	}
	if c.Error != "" {
		t.Errorf("expected no error, got %q", c.Error)
	}
	if c.ResponseBytes <= 0 {
		t.Errorf("expected positive response bytes, got %d", c.ResponseBytes)
	}
	if c.SessionID == "" {
		t.Errorf("expected a non-empty session id")
	}
}

func TestHandleCallTool_RecordsActivityOnError(t *testing.T) {
	server, db := newActivityTestServer(t)
	initializeServer(t, server, "test-client", "1.0")

	// searchSymbols requires "query" — omitting it triggers a handler error
	// before the engine is touched.
	callTool(t, server, "searchSymbols", map[string]interface{}{})
	closeAllActivityRecorders()

	calls, err := db.ListToolCalls(storage.ToolCallFilter{Tool: "searchSymbols", Limit: 10})
	if err != nil {
		t.Fatalf("ListToolCalls failed: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 recorded call, got %d", len(calls))
	}
	if calls[0].Error == "" {
		t.Errorf("expected error to be recorded, got empty string")
	}
}

func TestHandleCallTool_TargetExtractedFromParams(t *testing.T) {
	server, db := newActivityTestServer(t)
	initializeServer(t, server, "test-client", "1.0")

	callTool(t, server, "searchSymbols", map[string]interface{}{"query": "Engine"})
	closeAllActivityRecorders()

	calls, err := db.ListToolCalls(storage.ToolCallFilter{Tool: "searchSymbols", Limit: 10})
	if err != nil {
		t.Fatalf("ListToolCalls failed: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 recorded call, got %d", len(calls))
	}
	if calls[0].Target != "Engine" {
		t.Errorf("expected target=Engine, got %q", calls[0].Target)
	}
	if calls[0].ParamsHash == "" {
		t.Errorf("expected a params hash")
	}
}

func TestHandleCallTool_ConsumerFallsBackWithoutClientInfo(t *testing.T) {
	server, db := newActivityTestServer(t)
	// Skip initialize entirely: no clientInfo captured.

	oldClaudeCode := os.Getenv("CLAUDECODE")
	oldEntrypoint := os.Getenv("CLAUDE_CODE_ENTRYPOINT")
	_ = os.Unsetenv("CLAUDECODE")
	_ = os.Unsetenv("CLAUDE_CODE_ENTRYPOINT")
	t.Cleanup(func() {
		_ = os.Setenv("CLAUDECODE", oldClaudeCode)
		_ = os.Setenv("CLAUDE_CODE_ENTRYPOINT", oldEntrypoint)
	})

	callTool(t, server, "getStatus", map[string]interface{}{})
	closeAllActivityRecorders()

	calls, err := db.ListToolCalls(storage.ToolCallFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListToolCalls failed: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 recorded call, got %d", len(calls))
	}
	if calls[0].Consumer != "unknown" {
		t.Errorf("expected consumer=unknown without clientInfo or Claude Code env, got %q", calls[0].Consumer)
	}
}

func TestHandleCallTool_MultipleCallsRecordSeparateRows(t *testing.T) {
	server, db := newActivityTestServer(t)
	initializeServer(t, server, "test-client", "1.0")

	callTool(t, server, "getStatus", map[string]interface{}{})
	callTool(t, server, "getStatus", map[string]interface{}{})
	callTool(t, server, "searchSymbols", map[string]interface{}{"query": "Foo"})
	closeAllActivityRecorders()

	calls, err := db.ListToolCalls(storage.ToolCallFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListToolCalls failed: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("expected 3 recorded calls, got %d", len(calls))
	}
}

func TestConsumerIDPrefersClaudeCodeEnv(t *testing.T) {
	server := &MCPServer{}

	oldEntrypoint := os.Getenv("CLAUDE_CODE_ENTRYPOINT")
	_ = os.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Cleanup(func() { _ = os.Setenv("CLAUDE_CODE_ENTRYPOINT", oldEntrypoint) })

	if got := server.consumerID(); got != "claude-code" {
		t.Errorf("expected claude-code fallback, got %q", got)
	}
}

func TestCurrentSessionIDUsesEnvWhenSet(t *testing.T) {
	// currentSessionID caches via sync.Once at the package level, so this
	// test only verifies the env-var-present branch would be selected by a
	// fresh process; it can't force a re-read mid-test-run. Just assert the
	// function returns something non-empty and stable across calls.
	first := currentSessionID()
	second := currentSessionID()
	if first == "" {
		t.Fatalf("expected non-empty session id")
	}
	if first != second {
		t.Errorf("expected stable session id across calls, got %q then %q", first, second)
	}
	if !strings.Contains(first, ":") && os.Getenv("CLAUDE_CODE_SESSION_ID") == "" {
		t.Errorf("expected fallback session id to contain host:pid:start, got %q", first)
	}
}
