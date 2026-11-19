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
	for _, table := range []map[string]bool{largeResultTools, alwaysLoadTools} {
		for name := range table {
			if !defined[name] {
				t.Errorf("_meta table names %q, which is not a tool", name)
			}
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

func TestToolMeta(t *testing.T) {
	server := NewMCPServer("test", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	byName := map[string]Tool{}
	for _, tool := range server.GetToolDefinitions() {
		byName[tool.Name] = tool
	}
	if got := byName["assessChange"].Meta[metaMaxResultSizeChars]; got != largeResultChars {
		t.Errorf("assessChange maxResultSizeChars = %v", got)
	}
	if got := byName["assessChange"].Meta[metaAlwaysLoad]; got != true {
		t.Errorf("assessChange alwaysLoad = %v", got)
	}
	if byName["searchSymbols"].Meta != nil {
		t.Errorf("searchSymbols has _meta %v, want none", byName["searchSymbols"].Meta)
	}
	if largeResultChars > 500000 {
		t.Error("Claude Code caps maxResultSizeChars at 500,000")
	}
}

// Claude Code cuts a description at 4,096 characters for a tool loaded
// upfront (16,384 through tool search). With tool search off every tool is
// upfront, so every description has to fit the smaller limit.
func TestToolDescriptionsFitClaudeCodeLimits(t *testing.T) {
	server := NewMCPServer("test", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tool := range server.GetToolDefinitions() {
		if n := len(tool.Description); n > 4096 {
			t.Errorf("%s description is %d chars, Claude Code keeps 4096", tool.Name, n)
		}
	}
	if n := len(instructionsText()); n > 4096 {
		t.Errorf("server instructions are %d chars, Claude Code keeps 4096", n)
	}
}
