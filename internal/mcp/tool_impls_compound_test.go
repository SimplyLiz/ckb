package mcp

import (
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/query"
)

// =============================================================================
// Explore Tool Tests
// =============================================================================

func TestToolExplore_MissingTarget(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "explore", map[string]interface{}{})

	if !hasToolError(t, resp) {
		t.Error("expected error for missing target")
	}
	if errMsg := getToolErrorMessage(t, resp); !strings.Contains(errMsg, "target") {
		t.Errorf("expected error about target, got: %s", errMsg)
	}
}

func TestToolExplore_EmptyTarget(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "explore", map[string]interface{}{
		"target": "",
	})

	if !hasToolError(t, resp) {
		t.Error("expected error for empty target")
	}
}

func TestToolExplore_ValidTarget(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	// Use internal/mcp as target since we know it exists
	resp := callTool(t, server, "explore", map[string]interface{}{
		"target": "internal/mcp",
	})

	// Should succeed (may have limited data without SCIP index)
	if resp.Error != nil {
		t.Errorf("unexpected JSON-RPC error: %v", resp.Error)
	}
}

func TestToolExplore_DepthOptions(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	depths := []string{"shallow", "standard", "deep"}
	for _, depth := range depths {
		t.Run(depth, func(t *testing.T) {
			resp := callTool(t, server, "explore", map[string]interface{}{
				"target": "internal/mcp",
				"depth":  depth,
			})

			if resp.Error != nil {
				t.Errorf("unexpected error for depth=%s: %v", depth, resp.Error)
			}
		})
	}
}

func TestToolExplore_FocusOptions(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	focuses := []string{"structure", "dependencies", "changes"}
	for _, focus := range focuses {
		t.Run(focus, func(t *testing.T) {
			resp := callTool(t, server, "explore", map[string]interface{}{
				"target": "internal/mcp",
				"focus":  focus,
			})

			if resp.Error != nil {
				t.Errorf("unexpected error for focus=%s: %v", focus, resp.Error)
			}
		})
	}
}

// =============================================================================
// Understand Tool Tests
// =============================================================================

func TestToolUnderstand_MissingQuery(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "understand", map[string]interface{}{})

	if !hasToolError(t, resp) {
		t.Error("expected error for missing query")
	}
	if errMsg := getToolErrorMessage(t, resp); !strings.Contains(errMsg, "query") {
		t.Errorf("expected error about query, got: %s", errMsg)
	}
}

func TestToolUnderstand_EmptyQuery(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "understand", map[string]interface{}{
		"query": "",
	})

	if !hasToolError(t, resp) {
		t.Error("expected error for empty query")
	}
}

func TestToolUnderstand_ValidQuery(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "understand", map[string]interface{}{
		"query": "MCPServer",
	})

	// May fail if symbol not found, but shouldn't have JSON-RPC error
	if resp.Error != nil {
		t.Errorf("unexpected JSON-RPC error: %v", resp.Error)
	}
}

func TestToolUnderstand_WithOptions(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "understand", map[string]interface{}{
		"query":             "MCPServer",
		"includeReferences": false,
		"includeCallGraph":  false,
		"maxReferences":     float64(10),
	})

	if resp.Error != nil {
		t.Errorf("unexpected JSON-RPC error: %v", resp.Error)
	}
}

// =============================================================================
// PrepareChange Tool Tests
// =============================================================================

func TestToolPrepareChange_MissingTarget(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "prepareChange", map[string]interface{}{})

	if !hasToolError(t, resp) {
		t.Error("expected error for missing target")
	}
	if errMsg := getToolErrorMessage(t, resp); !strings.Contains(errMsg, "target") {
		t.Errorf("expected error about target, got: %s", errMsg)
	}
}

func TestToolPrepareChange_EmptyTarget(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "prepareChange", map[string]interface{}{
		"target": "",
	})

	if !hasToolError(t, resp) {
		t.Error("expected error for empty target")
	}
}

func TestToolPrepareChange_ValidTarget(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "prepareChange", map[string]interface{}{
		"target": "internal/mcp/handler.go",
	})

	// Should work even without SCIP (just won't have symbol-level impact)
	if resp.Error != nil {
		t.Errorf("unexpected JSON-RPC error: %v", resp.Error)
	}
}

func TestToolPrepareChange_ChangeTypeOptions(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	changeTypes := []string{"modify", "rename", "delete", "extract"}
	for _, ct := range changeTypes {
		t.Run(ct, func(t *testing.T) {
			resp := callTool(t, server, "prepareChange", map[string]interface{}{
				"target":     "internal/mcp/handler.go",
				"changeType": ct,
			})

			if resp.Error != nil {
				t.Errorf("unexpected error for changeType=%s: %v", ct, resp.Error)
			}
		})
	}
}

// =============================================================================
// BatchGet Tool Tests
// =============================================================================

func TestToolBatchGet_MissingSymbolIds(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "batchGet", map[string]interface{}{})

	if !hasToolError(t, resp) {
		t.Error("expected error for missing symbolIds")
	}
	if errMsg := getToolErrorMessage(t, resp); !strings.Contains(errMsg, "symbolIds") {
		t.Errorf("expected error about symbolIds, got: %s", errMsg)
	}
}

func TestToolBatchGet_EmptyArray(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "batchGet", map[string]interface{}{
		"symbolIds": []interface{}{},
	})

	if !hasToolError(t, resp) {
		t.Error("expected error for empty symbolIds array")
	}
}

func TestToolBatchGet_InvalidArrayContents(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "batchGet", map[string]interface{}{
		"symbolIds": []interface{}{123, 456}, // numbers, not strings
	})

	if !hasToolError(t, resp) {
		t.Error("expected error for non-string array contents")
	}
}

func TestToolBatchGet_ValidSymbolIds(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "batchGet", map[string]interface{}{
		"symbolIds": []interface{}{"ckb:test:sym:abc123", "ckb:test:sym:def456"},
	})

	// Should succeed (symbols may not be found, but no param error)
	if resp.Error != nil {
		t.Errorf("unexpected JSON-RPC error: %v", resp.Error)
	}
}

// =============================================================================
// BatchSearch Tool Tests
// =============================================================================

func TestToolBatchSearch_MissingQueries(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "batchSearch", map[string]interface{}{})

	if !hasToolError(t, resp) {
		t.Error("expected error for missing queries")
	}
	if errMsg := getToolErrorMessage(t, resp); !strings.Contains(errMsg, "queries") {
		t.Errorf("expected error about queries, got: %s", errMsg)
	}
}

func TestToolBatchSearch_EmptyArray(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "batchSearch", map[string]interface{}{
		"queries": []interface{}{},
	})

	if !hasToolError(t, resp) {
		t.Error("expected error for empty queries array")
	}
}

func TestToolBatchSearch_InvalidQueryObject(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "batchSearch", map[string]interface{}{
		"queries": []interface{}{
			map[string]interface{}{}, // missing query field
		},
	})

	if !hasToolError(t, resp) {
		t.Error("expected error for query object without query field")
	}
}

func TestToolBatchSearch_ValidQueries(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "batchSearch", map[string]interface{}{
		"queries": []interface{}{
			map[string]interface{}{"query": "Engine"},
			map[string]interface{}{"query": "Config", "limit": float64(5)},
		},
	})

	if resp.Error != nil {
		t.Errorf("unexpected JSON-RPC error: %v", resp.Error)
	}
}

func TestToolBatchSearch_WithAllOptions(t *testing.T) {
	t.Parallel()
	server := newTestMCPServer(t)

	resp := callTool(t, server, "batchSearch", map[string]interface{}{
		"queries": []interface{}{
			map[string]interface{}{
				"query": "Handler",
				"kind":  "function",
				"scope": "internal/mcp",
				"limit": float64(10),
			},
		},
	})

	if resp.Error != nil {
		t.Errorf("unexpected JSON-RPC error: %v", resp.Error)
	}
}

func TestCompactPrepareChange_TargetLabelAndHint(t *testing.T) {
	const id = "scip-go gomod example.com/m 1a2b `example.com/m/lib`/Save()."
	symbol := buildMCPCompactPrepareChange(id, &query.PrepareChangeResponse{
		Target: &query.PrepareChangeTarget{SymbolId: id, Name: "Save", Kind: "function"},
	}, "scip")
	if symbol.Hint != "" {
		t.Errorf("a symbol target got a hint: %q", symbol.Hint)
	}
	if strings.Contains(symbol.Summary, "scip-go") || !strings.Contains(symbol.Summary, "Save") {
		t.Errorf("summary should name the symbol, not repeat its ID: %q", symbol.Summary)
	}
	if symbol.Target != id {
		t.Errorf("target %q, want the ID as given", symbol.Target)
	}

	file := buildMCPCompactPrepareChange("lib/save.go", &query.PrepareChangeResponse{
		Target: &query.PrepareChangeTarget{Name: "save.go", Kind: "file", Path: "lib/save.go"},
	}, "scip")
	if !strings.Contains(file.Hint, "searchSymbols") {
		t.Errorf("a file target should point at a symbol target: %q", file.Hint)
	}
}

// The compact view is the MCP default, so what a modify score assumes has to
// reach it, not only the full response.
func TestCompactPrepareChange_CarriesRiskBasis(t *testing.T) {
	const basis = "Scored as an edit that keeps the declaration; assessChange rescores the real diff."
	c := buildMCPCompactPrepareChange("x", &query.PrepareChangeResponse{
		RiskAssessment: &query.PrepareRisk{Level: "low", Basis: basis},
	}, "scip")
	if c.RiskBasis != basis {
		t.Errorf("risk_basis = %q, want %q", c.RiskBasis, basis)
	}
}
