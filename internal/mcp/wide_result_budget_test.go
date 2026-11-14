package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/mcp/testdata"
	"github.com/SimplyLiz/CodeMCP/internal/query"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// NFR Token Baselines (bytes) - CI gates.
// These define maximum response sizes per tool per tier.
// 10% tolerance applied in tests. Latency tracked in benchmarks only.
//
// Baselines are derived from synthetic fixtures (deterministic).
// Update these when fixture generators change.
var nfrTokenBaselines = map[string]map[string]int{
	"searchSymbols": {
		"small":  3600,  // 20 symbols
		"medium": 18000, // 100 symbols
		"large":  91000, // 500 symbols
	},
	"findReferences": {
		"small":  4500,   // 50 refs
		"medium": 45000,  // 500 refs
		"large":  450000, // 5000 refs
	},
	"getCallGraph": {
		"shallow": 900,   // depth=2, branching=3
		"deep":    16000, // depth=4, branching=5
	},
	"getHotspots": {
		"small": 900,   // 10 hotspots
		"large": 17000, // 200 hotspots
	},
	"analyzeImpact": {
		"small": 2000,  // 10 impact nodes
		"large": 18000, // 100 impact nodes
	},
	"getArchitecture": {
		"small": 1500, // 5 modules
		"large": 8000, // 30 modules
	},
	"traceUsage": {
		"small": 800,  // 5 paths
		"large": 7800, // 50 paths
	},
	"summarizeDiff": {
		"small": 2200,  // 10 files
		"large": 20000, // 100 files
	},
	"listEntrypoints": {
		"small": 4800,  // 20 entrypoints
		"large": 24000, // 100 entrypoints
	},
	// v8.0 Compound Tools
	"explore": {
		"small": 4300,  // explainFile + 20 symbols + shallow callgraph + 10 hotspots
		"large": 95000, // explainFile + 500 symbols + deep callgraph + 200 hotspots
	},
	"understand": {
		"small": 5600,   // symbol detail + 50 refs + shallow callgraph
		"large": 461000, // symbol detail + 5000 refs + deep callgraph
	},
	"prepareChange": {
		"small": 2500,  // 10 impact + 5 tests + coupling
		"large": 16200, // 100 impact + 50 tests + coupling
	},
	"batchGet": {
		"small": 4800,  // 20 symbols with full detail
		"large": 11800, // 50 symbols with full detail
	},
	"batchSearch": {
		"small":  3400,  // 3 queries × ~7 results each
		"medium": 18100, // 5 queries × 20 results
		"large":  91000, // 10 queries × 50 results
	},
	// v8.1 Change Impact Analysis
	"analyzeChange": {
		"small":  6000,   // 10 changed, 20 affected
		"medium": 55000,  // 100 changed, 200 affected
		"large":  270000, // 500 changed, 1000 affected
		"xlarge": 540000, // 1000 changed, 2000 affected
	},
	"getAffectedTests": {
		"small":  1500,  // 5 tests
		"medium": 6000,  // 20 tests
		"large":  15000, // 50 tests
		"xlarge": 30000, // 100 tests
	},
}

// Wide-result token budgets for integration tests (legacy).
// These are per-response limits used when testing with real SCIP index.
const (
	maxCallGraphBytes      = 20000 // ~5000 tokens
	maxFindReferencesBytes = 30000 // ~7500 tokens: the test asks for 100 references at ~250 bytes each
	maxAnalyzeImpactBytes  = 16000 // ~4000 tokens
	maxGetHotspotsBytes    = 13000 // ~3250 tokens — accommodates cartographer-tagged builds where each hotspot is enriched with up to 3 co-change partners
)

// testResponseMetrics captures token-related metrics for a tool response (test-local).
type testResponseMetrics struct {
	ToolName        string
	JSONBytes       int
	EstimatedTokens int
}

// measureToolResponse measures the size of a tool response.
func measureToolResponse(toolName string, response interface{}) testResponseMetrics {
	data, _ := json.Marshal(response)
	return testResponseMetrics{
		ToolName:        toolName,
		JSONBytes:       len(data),
		EstimatedTokens: len(data) / 4,
	}
}

// TestNFRScenarios validates token budgets using synthetic fixtures.
// These tests are deterministic and always run (no SCIP index required).
func TestNFRScenarios(t *testing.T) {
	const tolerance = 1.10 // 10% tolerance

	scenarios := []struct {
		name     string
		tool     string
		tier     string
		fixtures *testdata.FixtureSet
		genJSON  func(*testdata.FixtureSet) string
	}{
		// searchSymbols scenarios
		{"searchSymbols_small", "searchSymbols", "small", testdata.SmallFixtures(), (*testdata.FixtureSet).ToSearchSymbolsJSON},
		{"searchSymbols_medium", "searchSymbols", "medium", testdata.MediumFixtures(), (*testdata.FixtureSet).ToSearchSymbolsJSON},
		{"searchSymbols_large", "searchSymbols", "large", testdata.LargeFixtures(), (*testdata.FixtureSet).ToSearchSymbolsJSON},

		// findReferences scenarios
		{"findReferences_small", "findReferences", "small", testdata.SmallFixtures(), (*testdata.FixtureSet).ToFindReferencesJSON},
		{"findReferences_medium", "findReferences", "medium", testdata.MediumFixtures(), (*testdata.FixtureSet).ToFindReferencesJSON},
		{"findReferences_large", "findReferences", "large", testdata.LargeFixtures(), (*testdata.FixtureSet).ToFindReferencesJSON},

		// getCallGraph scenarios
		{"getCallGraph_shallow", "getCallGraph", "shallow", testdata.SmallFixtures(), (*testdata.FixtureSet).ToGetCallGraphJSON},
		{"getCallGraph_deep", "getCallGraph", "deep", testdata.LargeFixtures(), (*testdata.FixtureSet).ToGetCallGraphJSON},

		// getHotspots scenarios
		{"getHotspots_small", "getHotspots", "small", testdata.SmallFixtures(), (*testdata.FixtureSet).ToGetHotspotsJSON},
		{"getHotspots_large", "getHotspots", "large", testdata.LargeFixtures(), (*testdata.FixtureSet).ToGetHotspotsJSON},

		// analyzeImpact scenarios
		{"analyzeImpact_small", "analyzeImpact", "small", testdata.SmallFixtures(), (*testdata.FixtureSet).ToAnalyzeImpactJSON},
		{"analyzeImpact_large", "analyzeImpact", "large", testdata.LargeFixtures(), (*testdata.FixtureSet).ToAnalyzeImpactJSON},

		// getArchitecture scenarios
		{"getArchitecture_small", "getArchitecture", "small", testdata.SmallFixtures(), (*testdata.FixtureSet).ToGetArchitectureJSON},
		{"getArchitecture_large", "getArchitecture", "large", testdata.LargeFixtures(), (*testdata.FixtureSet).ToGetArchitectureJSON},

		// traceUsage scenarios
		{"traceUsage_small", "traceUsage", "small", testdata.SmallFixtures(), (*testdata.FixtureSet).ToTraceUsageJSON},
		{"traceUsage_large", "traceUsage", "large", testdata.LargeFixtures(), (*testdata.FixtureSet).ToTraceUsageJSON},

		// summarizeDiff scenarios
		{"summarizeDiff_small", "summarizeDiff", "small", testdata.SmallFixtures(), (*testdata.FixtureSet).ToSummarizeDiffJSON},
		{"summarizeDiff_large", "summarizeDiff", "large", testdata.LargeFixtures(), (*testdata.FixtureSet).ToSummarizeDiffJSON},

		// listEntrypoints scenarios
		{"listEntrypoints_small", "listEntrypoints", "small", testdata.SmallFixtures(), (*testdata.FixtureSet).ToListEntrypointsJSON},
		{"listEntrypoints_large", "listEntrypoints", "large", testdata.LargeFixtures(), (*testdata.FixtureSet).ToListEntrypointsJSON},

		// v8.0 compound tool scenarios
		{"explore_small", "explore", "small", testdata.SmallFixtures(), (*testdata.FixtureSet).ToExploreJSON},
		{"explore_large", "explore", "large", testdata.LargeFixtures(), (*testdata.FixtureSet).ToExploreJSON},

		{"understand_small", "understand", "small", testdata.SmallFixtures(), (*testdata.FixtureSet).ToUnderstandJSON},
		{"understand_large", "understand", "large", testdata.LargeFixtures(), (*testdata.FixtureSet).ToUnderstandJSON},

		{"prepareChange_small", "prepareChange", "small", testdata.SmallFixtures(), (*testdata.FixtureSet).ToPrepareChangeJSON},
		{"prepareChange_large", "prepareChange", "large", testdata.LargeFixtures(), (*testdata.FixtureSet).ToPrepareChangeJSON},

		{"batchGet_small", "batchGet", "small", testdata.SmallFixtures(), (*testdata.FixtureSet).ToBatchGetJSON},
		{"batchGet_large", "batchGet", "large", testdata.LargeFixtures(), (*testdata.FixtureSet).ToBatchGetJSON},

		{"batchSearch_small", "batchSearch", "small", testdata.SmallFixtures(), (*testdata.FixtureSet).ToBatchSearchJSON},
		{"batchSearch_medium", "batchSearch", "medium", testdata.MediumFixtures(), (*testdata.FixtureSet).ToBatchSearchJSON},
		{"batchSearch_large", "batchSearch", "large", testdata.LargeFixtures(), (*testdata.FixtureSet).ToBatchSearchJSON},

		// v8.1 analyzeChange scenarios (Change Impact Analysis)
		{"analyzeChange_small", "analyzeChange", "small", testdata.SmallFixtures(), (*testdata.FixtureSet).ToAnalyzeChangeJSON},
		{"analyzeChange_medium", "analyzeChange", "medium", testdata.MediumFixtures(), (*testdata.FixtureSet).ToAnalyzeChangeJSON},
		{"analyzeChange_large", "analyzeChange", "large", testdata.LargeFixtures(), (*testdata.FixtureSet).ToAnalyzeChangeJSON},
		{"analyzeChange_xlarge", "analyzeChange", "xlarge", testdata.ExtraLargeFixtures(), (*testdata.FixtureSet).ToAnalyzeChangeJSON},

		// v8.1 getAffectedTests scenarios
		{"getAffectedTests_small", "getAffectedTests", "small", testdata.SmallFixtures(), (*testdata.FixtureSet).ToGetAffectedTestsJSON},
		{"getAffectedTests_medium", "getAffectedTests", "medium", testdata.MediumFixtures(), (*testdata.FixtureSet).ToGetAffectedTestsJSON},
		{"getAffectedTests_large", "getAffectedTests", "large", testdata.LargeFixtures(), (*testdata.FixtureSet).ToGetAffectedTestsJSON},
		{"getAffectedTests_xlarge", "getAffectedTests", "xlarge", testdata.ExtraLargeFixtures(), (*testdata.FixtureSet).ToGetAffectedTestsJSON},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			// Generate fixture JSON with timing
			start := time.Now()
			responseJSON := sc.genJSON(sc.fixtures)
			elapsed := time.Since(start)
			actualBytes := len(responseJSON)

			// Get baseline
			baselines, ok := nfrTokenBaselines[sc.tool]
			if !ok {
				t.Fatalf("No baselines defined for tool: %s", sc.tool)
			}
			baseline, ok := baselines[sc.tier]
			if !ok {
				t.Fatalf("No baseline defined for tier: %s/%s", sc.tool, sc.tier)
			}

			// Check against baseline with tolerance
			maxAllowed := int(float64(baseline) * tolerance)
			t.Logf("%s: %d bytes (baseline: %d, max: %d) [%v]", sc.name, actualBytes, baseline, maxAllowed, elapsed)

			if actualBytes > maxAllowed {
				t.Errorf("REGRESSION: %s exceeds baseline by >10%%: %d bytes (max: %d)",
					sc.name, actualBytes, maxAllowed)
			}

			// Also check if we're significantly under (potential baseline update)
			minExpected := int(float64(baseline) * 0.5)
			if actualBytes < minExpected {
				t.Logf("NOTE: %s is significantly under baseline (%d < %d*0.5=%d), consider updating baseline",
					sc.name, actualBytes, baseline, minExpected)
			}
		})
	}
}

// findProjectRoot walks up from current directory to find go.mod
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// newTestMCPServerWithIndex creates an MCP server with the project's SCIP index.
// Returns nil if no index is available (allows tests to skip gracefully).
func newTestMCPServerWithIndex(t testing.TB) *MCPServer {
	t.Helper()

	// Find project root
	projectRoot, err := findProjectRoot()
	if err != nil {
		return nil
	}

	// Check for SCIP index
	scipPath := filepath.Join(projectRoot, ".scip", "index.scip")
	if _, err := os.Stat(scipPath); os.IsNotExist(err) {
		return nil
	}

	cfg := &config.Config{
		Version:  5,
		RepoRoot: projectRoot,
		Backends: config.BackendsConfig{
			Git: config.GitConfig{
				Enabled: false,
			},
			Scip: config.ScipConfig{
				Enabled:   true,
				IndexPath: scipPath,
			},
			Lsp: config.LspConfig{
				Enabled: false,
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := storage.Open(":memory:", logger)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	engine, err := query.NewEngine(".", db, logger, cfg)
	if err != nil {
		t.Fatalf("Failed to create query engine: %v", err)
	}

	return NewMCPServer(version.Version, engine, logger)
}

// TestWideResultTokenBudgetsIntegration validates wide-result tools with real SCIP index.
// These are integration tests - skipped if no SCIP index available.
// For deterministic CI tests, see TestNFRScenarios.
func TestWideResultTokenBudgetsIntegration(t *testing.T) {
	server := newTestMCPServerWithIndex(t)
	if server == nil {
		t.Skip("Skipping: no SCIP index available (run 'ckb index' first)")
	}

	// Test getCallGraph
	t.Run("getCallGraph", func(t *testing.T) {
		// Find a symbol with callers
		resp := sendRequest(t, server, "tools/call", 1, map[string]interface{}{
			"name": "searchSymbols",
			"arguments": map[string]interface{}{
				"query": "Engine",
				"limit": 5,
			},
		})

		if resp.Error != nil {
			t.Skipf("searchSymbols failed: %v", resp.Error)
		}

		// Parse to get a symbol ID
		var result map[string]interface{}
		resultBytes, _ := json.Marshal(resp.Result)
		if err := json.Unmarshal(resultBytes, &result); err != nil {
			t.Skipf("Failed to unmarshal result: %v", err)
		}

		content, ok := result["content"].([]interface{})
		if !ok || len(content) == 0 {
			t.Skip("No symbols found for testing")
		}

		// Get first content item's text and parse it
		firstContent, ok := content[0].(map[string]interface{})
		if !ok {
			t.Skip("Invalid content format")
		}
		text, ok := firstContent["text"].(string)
		if !ok {
			t.Skip("No text in content")
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(text), &data); err != nil {
			t.Skipf("Failed to parse response: %v", err)
		}

		// The envelope has {"schemaVersion": "1.0", "data": {"symbols": [...]}}
		dataField, ok := data["data"].(map[string]interface{})
		if !ok {
			t.Skip("No data field in response")
		}

		symbols, ok := dataField["symbols"].([]interface{})
		if !ok || len(symbols) == 0 {
			t.Skip("No symbols in response")
		}

		firstSymbol, ok := symbols[0].(map[string]interface{})
		if !ok {
			t.Skip("Invalid symbol format")
		}
		symbolID, ok := firstSymbol["stableId"].(string)
		if !ok {
			t.Skip("Symbol has no stableId")
		}

		// Now test getCallGraph with this symbol
		callGraphResp := sendRequest(t, server, "tools/call", 2, map[string]interface{}{
			"name": "getCallGraph",
			"arguments": map[string]interface{}{
				"symbolId":  symbolID,
				"direction": "both",
				"depth":     2,
			},
		})

		if callGraphResp.Error != nil {
			t.Logf("getCallGraph returned error (may be expected): %v", callGraphResp.Error)
			return
		}

		metrics := measureToolResponse("getCallGraph", callGraphResp.Result)
		t.Logf("getCallGraph: %d bytes (~%d tokens)", metrics.JSONBytes, metrics.EstimatedTokens)

		if metrics.JSONBytes > maxCallGraphBytes {
			t.Errorf("getCallGraph exceeds token budget: %d bytes (max %d)",
				metrics.JSONBytes, maxCallGraphBytes)
		}
	})

	// Test getHotspots (doesn't need symbol lookup)
	t.Run("getHotspots", func(t *testing.T) {
		resp := sendRequest(t, server, "tools/call", 3, map[string]interface{}{
			"name": "getHotspots",
			"arguments": map[string]interface{}{
				"limit": 20,
			},
		})

		if resp.Error != nil {
			t.Logf("getHotspots returned error (may be expected): %v", resp.Error)
			return
		}

		metrics := measureToolResponse("getHotspots", resp.Result)
		t.Logf("getHotspots: %d bytes (~%d tokens)", metrics.JSONBytes, metrics.EstimatedTokens)

		if metrics.JSONBytes > maxGetHotspotsBytes {
			t.Errorf("getHotspots exceeds token budget: %d bytes (max %d)",
				metrics.JSONBytes, maxGetHotspotsBytes)
		}
	})

	// Test findReferences
	t.Run("findReferences", func(t *testing.T) {
		// Search for a symbol first
		resp := sendRequest(t, server, "tools/call", 4, map[string]interface{}{
			"name": "searchSymbols",
			"arguments": map[string]interface{}{
				"query": "Engine",
				"limit": 1,
			},
		})

		if resp.Error != nil {
			t.Skip("searchSymbols failed")
		}

		var result map[string]interface{}
		resultBytes, _ := json.Marshal(resp.Result)
		if err := json.Unmarshal(resultBytes, &result); err != nil {
			t.Skip("Failed to unmarshal result")
		}

		content, ok := result["content"].([]interface{})
		if !ok || len(content) == 0 {
			t.Skip("No symbols found")
		}

		firstContent, ok := content[0].(map[string]interface{})
		if !ok {
			t.Skip("Invalid content format")
		}
		text, ok := firstContent["text"].(string)
		if !ok {
			t.Skip("No text in content")
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(text), &data); err != nil {
			t.Skip("Failed to parse response")
		}

		dataField, ok := data["data"].(map[string]interface{})
		if !ok {
			t.Skip("No data field in response")
		}

		symbols, ok := dataField["symbols"].([]interface{})
		if !ok || len(symbols) == 0 {
			t.Skip("No symbols in response")
		}

		firstSymbol, ok := symbols[0].(map[string]interface{})
		if !ok {
			t.Skip("Invalid symbol format")
		}
		symbolID, ok := firstSymbol["stableId"].(string)
		if !ok {
			t.Skip("Symbol has no stableId")
		}

		// Test findReferences
		refsResp := sendRequest(t, server, "tools/call", 5, map[string]interface{}{
			"name": "findReferences",
			"arguments": map[string]interface{}{
				"symbolId": symbolID,
				"limit":    100,
			},
		})

		if refsResp.Error != nil {
			t.Logf("findReferences returned error (may be expected): %v", refsResp.Error)
			return
		}

		metrics := measureToolResponse("findReferences", refsResp.Result)
		t.Logf("findReferences: %d bytes (~%d tokens)", metrics.JSONBytes, metrics.EstimatedTokens)

		if metrics.JSONBytes > maxFindReferencesBytes {
			t.Errorf("findReferences exceeds token budget: %d bytes (max %d)",
				metrics.JSONBytes, maxFindReferencesBytes)
		}
	})

	// Test analyzeImpact
	t.Run("analyzeImpact", func(t *testing.T) {
		// Search for a symbol first
		resp := sendRequest(t, server, "tools/call", 6, map[string]interface{}{
			"name": "searchSymbols",
			"arguments": map[string]interface{}{
				"query": "Search",
				"limit": 1,
			},
		})

		if resp.Error != nil {
			t.Skip("searchSymbols failed")
		}

		var result map[string]interface{}
		resultBytes, _ := json.Marshal(resp.Result)
		if err := json.Unmarshal(resultBytes, &result); err != nil {
			t.Skip("Failed to unmarshal result")
		}

		content, ok := result["content"].([]interface{})
		if !ok || len(content) == 0 {
			t.Skip("No symbols found")
		}

		firstContent, ok := content[0].(map[string]interface{})
		if !ok {
			t.Skip("Invalid content format")
		}
		text, ok := firstContent["text"].(string)
		if !ok {
			t.Skip("No text in content")
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(text), &data); err != nil {
			t.Skip("Failed to parse response")
		}

		// The envelope has {"schemaVersion": "1.0", "data": {"symbols": [...]}}
		dataField, ok := data["data"].(map[string]interface{})
		if !ok {
			t.Skip("No data field in response")
		}

		symbols, ok := dataField["symbols"].([]interface{})
		if !ok || len(symbols) == 0 {
			t.Skip("No symbols in response")
		}

		firstSymbol, ok := symbols[0].(map[string]interface{})
		if !ok {
			t.Skip("Invalid symbol format")
		}
		symbolID, ok := firstSymbol["stableId"].(string)
		if !ok {
			t.Skip("Symbol has no stableId")
		}

		// Test analyzeImpact
		impactResp := sendRequest(t, server, "tools/call", 5, map[string]interface{}{
			"name": "analyzeImpact",
			"arguments": map[string]interface{}{
				"symbolId": symbolID,
				"depth":    2,
			},
		})

		if impactResp.Error != nil {
			t.Logf("analyzeImpact returned error (may be expected): %v", impactResp.Error)
			return
		}

		metrics := measureToolResponse("analyzeImpact", impactResp.Result)
		t.Logf("analyzeImpact: %d bytes (~%d tokens)", metrics.JSONBytes, metrics.EstimatedTokens)

		if metrics.JSONBytes > maxAnalyzeImpactBytes {
			t.Errorf("analyzeImpact exceeds token budget: %d bytes (max %d)",
				metrics.JSONBytes, maxAnalyzeImpactBytes)
		}
	})
}

// TestWideResultMetricsOutput outputs current wide-result metrics for manual review.
// Run with: go test -v -run TestWideResultMetricsOutput ./internal/mcp/...
func TestWideResultMetricsOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping metrics output in short mode")
	}

	server := newTestMCPServerWithIndex(t)
	if server == nil {
		t.Skip("Skipping: no SCIP index available")
	}

	t.Log("")
	t.Log("=== WIDE-RESULT TOKEN METRICS ===")

	// getStatus (baseline - should be small)
	resp := sendRequest(t, server, "tools/call", 1, map[string]interface{}{
		"name":      "getStatus",
		"arguments": map[string]interface{}{},
	})
	if resp.Error == nil {
		metrics := measureToolResponse("getStatus", resp.Result)
		t.Logf("%-20s: %6d bytes, ~%5d tokens", "getStatus", metrics.JSONBytes, metrics.EstimatedTokens)
	}

	// getHotspots
	resp = sendRequest(t, server, "tools/call", 2, map[string]interface{}{
		"name": "getHotspots",
		"arguments": map[string]interface{}{
			"limit": 20,
		},
	})
	if resp.Error == nil {
		metrics := measureToolResponse("getHotspots", resp.Result)
		t.Logf("%-20s: %6d bytes, ~%5d tokens", "getHotspots", metrics.JSONBytes, metrics.EstimatedTokens)
	}

	// searchSymbols
	resp = sendRequest(t, server, "tools/call", 3, map[string]interface{}{
		"name": "searchSymbols",
		"arguments": map[string]interface{}{
			"query": "Engine",
			"limit": 50,
		},
	})
	if resp.Error == nil {
		metrics := measureToolResponse("searchSymbols", resp.Result)
		t.Logf("%-20s: %6d bytes, ~%5d tokens", "searchSymbols", metrics.JSONBytes, metrics.EstimatedTokens)
	}

	// getArchitecture
	resp = sendRequest(t, server, "tools/call", 4, map[string]interface{}{
		"name": "getArchitecture",
		"arguments": map[string]interface{}{
			"depth": 2,
		},
	})
	if resp.Error == nil {
		metrics := measureToolResponse("getArchitecture", resp.Result)
		t.Logf("%-20s: %6d bytes, ~%5d tokens", "getArchitecture", metrics.JSONBytes, metrics.EstimatedTokens)
	}

	t.Log("==================================")
	t.Log("")
}

// consumerFiles is uncapped in spirit but has to stay affordable: at the bound, with
// longer paths than most repos have, it costs less than the budget of the analyzeImpact
// response it is part of.
func TestConsumerFilesStayWithinBudget(t *testing.T) {
	if query.MaxConsumerFiles != testdata.MaxConsumerFilesFixture() {
		t.Fatalf("fixtures bound consumerFiles at %d, the engine at %d", testdata.MaxConsumerFilesFixture(), query.MaxConsumerFiles)
	}
	items := make([]query.ImpactItem, 5000)
	for i := range items {
		items[i].Location = &query.LocationInfo{FileId: fmt.Sprintf("internal/some/module%d/consumer_file_%04d.go", i%40, i)}
	}
	files := query.BuildConsumerFiles(items, nil, nil, "")
	if files.Total != 5000 || !files.Truncated || files.Note == "" {
		t.Errorf("a cut list must say so: total=%d truncated=%v note=%q", files.Total, files.Truncated, files.Note)
	}
	data, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > maxAnalyzeImpactBytes {
		t.Errorf("consumerFiles is %d bytes at its bound, over the %d of a whole analyzeImpact response", len(data), maxAnalyzeImpactBytes)
	}
}
