package mcp

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestToolHintsNameRealTools(t *testing.T) {
	server := NewMCPServer("test", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defined := map[string]bool{}
	for _, tool := range server.GetToolDefinitions() {
		defined[tool.Name] = true
	}
	for name := range writingTools {
		if !defined[name] {
			t.Errorf("writingTools names %q, which is not a tool", name)
		}
	}
	for name := range readOnlyOpenWorld {
		if !defined[name] {
			t.Errorf("readOnlyOpenWorld names %q, which is not a tool", name)
		}
		if _, ok := writingTools[name]; ok {
			t.Errorf("%q is in both writingTools and readOnlyOpenWorld", name)
		}
	}
}

func TestEveryToolIsAnnotated(t *testing.T) {
	server := NewMCPServer("test", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tool := range server.GetToolDefinitions() {
		a := tool.Annotations
		if a == nil || a.ReadOnlyHint == nil {
			t.Fatalf("%s has no readOnlyHint", tool.Name)
		}
		_, writes := writingTools[tool.Name]
		if *a.ReadOnlyHint == writes {
			t.Errorf("%s: readOnlyHint=%v, but writingTools says writes=%v", tool.Name, *a.ReadOnlyHint, writes)
		}
	}
}

func TestToolAnnotationsJSON(t *testing.T) {
	cases := map[string]string{
		"searchSymbols":          `{"readOnlyHint":true}`,
		"federationStatusRemote": `{"readOnlyHint":true,"openWorldHint":true}`,
		"recordDecision":         `{"readOnlyHint":false,"destructiveHint":false,"idempotentHint":false,"openWorldHint":false}`,
		"cancelJob":              `{"readOnlyHint":false,"destructiveHint":true,"idempotentHint":true,"openWorldHint":false}`,
	}
	for name, want := range cases {
		got, err := json.Marshal(annotationsFor(name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s: got %s, want %s", name, got, want)
		}
	}
}

func TestToolsListCarriesAnnotations(t *testing.T) {
	server := NewMCPServer("test", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	raw, err := json.Marshal(server.GetFilteredTools())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"annotations":{"readOnlyHint":true}`) {
		t.Error("tools/list payload has no readOnlyHint annotation")
	}
}
