package mcp

import (
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/envelope"
	"github.com/SimplyLiz/CodeMCP/internal/query"
)

func callToolRaw(t *testing.T, s *MCPServer, name string) string {
	t.Helper()
	out, err := s.handleCallTool(map[string]interface{}{"name": name})
	if err != nil {
		t.Fatalf("handleCallTool: %v", err)
	}
	return out.(map[string]interface{})["content"].([]map[string]interface{})[0]["text"].(string)
}

// Code goes to an agent as it is written: `<`, `>` and `&` are not HTML here.
func TestCallTool_DoesNotEscapeCode(t *testing.T) {
	s := newTestMCPServer(t)
	s.tools["fakeTool"] = func(map[string]interface{}) (*envelope.Response, error) {
		return envelope.New().Data(map[string]string{"code": "return <K extends Kind>(a: K) => a && b"}).Build(), nil
	}
	text := callToolRaw(t, s, "fakeTool")
	if strings.Contains(text, `\u003c`) || strings.Contains(text, `\u0026`) || !strings.Contains(text, "<K extends Kind>(a: K) => a && b") {
		t.Errorf("code is escaped: %s", text)
	}
}

type textData struct{ body string }

func (d textData) Text() string { return d.body }

// A result that has a text form is sent as that text, with the envelope's warnings
// after it, not as a JSON string holding it.
func TestCallTool_TextResultIsSentAsText(t *testing.T) {
	s := newTestMCPServer(t)
	s.tools["fakeTool"] = func(map[string]interface{}) (*envelope.Response, error) {
		resp := envelope.New().Data(textData{body: "Kind (changed)\n  a.ts\n    3: const x: Kind = \"a\";\n"}).Build()
		resp.Warnings = append(resp.Warnings, envelope.Warning{Message: "the index is 2 commits behind"})
		return resp, nil
	}
	text := callToolRaw(t, s, "fakeTool")
	if !strings.HasPrefix(text, "Kind (changed)\n  a.ts\n    3: const x: Kind = \"a\";\n") {
		t.Errorf("text result is not sent as text: %q", text)
	}
	if !strings.Contains(text, "warning: the index is 2 commits behind") {
		t.Errorf("the warning is lost: %q", text)
	}
}

// findReferences answers as text by default and still marshals where JSON is asked of it.
func TestReferenceText(t *testing.T) {
	r := referenceText{lines: &query.ReferenceLines{Symbol: "show", Total: 1, Lines: []query.ReferenceLine{{File: "a.ts", Line: 4, Use: "call", Code: `show("a")`}}}}
	if got := r.Text(); got != "a.ts:4: [call] show(\"a\")\nshow: 1 reference\n" {
		t.Errorf("text = %q", got)
	}
	out, err := marshalForAgent(r)
	if err != nil || !strings.Contains(string(out), `"file":"a.ts"`) {
		t.Errorf("json = %s, %v", out, err)
	}
}
