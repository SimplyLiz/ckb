package mcp

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"
)

func newQuietServer() *MCPServer {
	return NewMCPServer("test", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestNegotiateProtocolVersion(t *testing.T) {
	cases := map[string]string{
		"2025-11-25": "2025-11-25",
		"2025-06-18": "2025-06-18",
		"2024-11-05": "2024-11-05",
		// Batching is required in 2025-03-26 and not implemented here.
		"2025-03-26": "2025-11-25",
		"2026-07-28": "2025-11-25",
		"":           "2025-11-25",
	}
	for requested, want := range cases {
		if got := negotiateProtocolVersion(requested); got != want {
			t.Errorf("requested %q: got %q, want %q", requested, got, want)
		}
	}
}

func TestInitializeAnswersRequestedVersion(t *testing.T) {
	server := newQuietServer()
	result, err := server.handleInitialize(map[string]interface{}{"protocolVersion": "2025-06-18"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProtocolVersion != "2025-06-18" {
		t.Errorf("got %q", result.ProtocolVersion)
	}
	if server.protocolVersion != "2025-06-18" {
		t.Errorf("server kept %q", server.protocolVersion)
	}
}

func TestPingAnswersEmptyResult(t *testing.T) {
	resp := newQuietServer().handleMessage(&MCPMessage{Jsonrpc: "2.0", Id: float64(7), Method: "ping"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("ping failed: %+v", resp)
	}
	raw, _ := json.Marshal(resp.Result)
	if string(raw) != "{}" {
		t.Errorf("ping result %s, want {}", raw)
	}
}

func TestUnknownToolIsInvalidParams(t *testing.T) {
	resp := newQuietServer().handleMessage(&MCPMessage{
		Jsonrpc: "2.0", Id: float64(1), Method: "tools/call",
		Params: map[string]interface{}{"name": "noSuchTool"},
	})
	if resp.Error == nil || resp.Error.Code != InvalidParams {
		t.Fatalf("want InvalidParams, got %+v", resp.Error)
	}
}

func TestToolErrorSetsIsError(t *testing.T) {
	// annotationSet without symbol_uri fails in the handler before touching storage.
	resp := newQuietServer().handleMessage(&MCPMessage{
		Jsonrpc: "2.0", Id: float64(1), Method: "tools/call",
		Params: map[string]interface{}{"name": "annotationSet", "arguments": map[string]interface{}{}},
	})
	if resp.Error != nil {
		t.Fatalf("tool error must be a result, got protocol error %+v", resp.Error)
	}
	result := as[map[string]interface{}](t, resp.Result)
	if result["isError"] != true {
		t.Errorf("isError = %v, want true", result["isError"])
	}
}
