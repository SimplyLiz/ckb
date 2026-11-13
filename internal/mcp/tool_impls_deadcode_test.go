package mcp

import (
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/query"
)

// Without a SCIP index findDeadCode used to answer deadCode: [] next to a
// generic warning, which reads as "no dead code". It now says analyzed: false
// with the reason.
func TestFindDeadCodeWithoutIndexSaysNotAnalyzed(t *testing.T) {
	s := newTestMCPServer(t)
	resp, err := s.toolFindDeadCode(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	res, ok := resp.Data.(*query.FindDeadCodeResponse)
	if !ok {
		t.Fatalf("data is %T", resp.Data)
	}
	if res.Analyzed {
		t.Error("analyzed true without an index")
	}
	if res.NotAnalyzedReason == "" {
		t.Error("no reason given")
	}
	if res.Provenance == nil || res.Provenance.Completeness.Reason != "not-analyzed" {
		t.Errorf("provenance completeness: %+v", res.Provenance)
	}
	found := false
	for _, w := range resp.Warnings {
		if strings.Contains(w.Message, "not analyzed") {
			found = true
		}
	}
	if !found {
		t.Errorf("no not-analyzed warning: %+v", resp.Warnings)
	}
}
