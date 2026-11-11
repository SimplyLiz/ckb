package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/envelope"
)

// callToolText runs a tool through handleCallTool and decodes the envelope it returns.
func callToolText(t *testing.T, s *MCPServer, name string) *envelope.Response {
	t.Helper()
	out, err := s.handleCallTool(map[string]interface{}{"name": name})
	if err != nil {
		t.Fatalf("handleCallTool: %v", err)
	}
	content := out.(map[string]interface{})["content"].([]map[string]interface{})
	var resp envelope.Response
	if err := json.Unmarshal([]byte(content[0]["text"].(string)), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return &resp
}

// A server whose binary was replaced on disk says so in the meta of every tool
// response, not only in getStatus, and stats the binary at most once per interval.
func TestHandleCallTool_StaleServerInMeta(t *testing.T) {
	s := newTestMCPServer(t)
	s.tools["fakeTool"] = func(map[string]interface{}) (*envelope.Response, error) {
		return NewToolResponse().Data(map[string]string{"ok": "yes"}).Build(), nil
	}

	bin := filepath.Join(t.TempDir(), "ckb")
	if err := os.WriteFile(bin, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Hour)
	if err := os.Chtimes(bin, started, started); err != nil {
		t.Fatal(err)
	}
	s.binaryPath, s.binaryModTime = bin, started

	old := binaryStaleCheckInterval
	binaryStaleCheckInterval = time.Hour
	t.Cleanup(func() { binaryStaleCheckInterval = old })

	if resp := callToolText(t, s, "fakeTool"); resp.Meta != nil && resp.Meta.StaleServer != "" {
		t.Fatalf("fresh binary flagged stale: %q", resp.Meta.StaleServer)
	}

	// Replace the binary. Within the interval the cached answer stands.
	if err := os.Chtimes(bin, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if resp := callToolText(t, s, "fakeTool"); resp.Meta != nil && resp.Meta.StaleServer != "" {
		t.Fatalf("binary stat'ed again within the interval")
	}

	// Past the interval it is seen, and it stays seen.
	binaryStaleCheckInterval = 0
	resp := callToolText(t, s, "fakeTool")
	if resp.Meta == nil || !strings.Contains(resp.Meta.StaleServer, "changed after this server started") {
		t.Fatalf("meta = %+v, want staleServer", resp.Meta)
	}
	binaryStaleCheckInterval = time.Hour
	if resp := callToolText(t, s, "fakeTool"); resp.Meta == nil || resp.Meta.StaleServer == "" {
		t.Fatal("stale flag lost")
	}
}
