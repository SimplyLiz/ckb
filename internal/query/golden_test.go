package query

import (
	"context"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
	"github.com/SimplyLiz/CodeMCP/internal/testutil"
)

// setupGoldenEngine creates a query engine using a fixture's SCIP index.
func setupGoldenEngine(t *testing.T, fixture *testutil.FixtureContext) (*Engine, func()) {
	t.Helper()

	// Create temp directory for CKB storage
	tmpDir, err := os.MkdirTemp("", "ckb-golden-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Create .ckb directory
	ckbDir := filepath.Join(tmpDir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0o755); err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("Failed to create .ckb dir: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Create storage in temp dir
	db, err := storage.Open(tmpDir, logger)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("Failed to open storage: %v", err)
	}

	// Create config pointing to fixture
	cfg := config.DefaultConfig()
	cfg.RepoRoot = fixture.Root
	cfg.Backends.Scip.Enabled = true
	cfg.Backends.Scip.IndexPath = fixture.SCIPPath // Use absolute path to fixture's index

	// Create engine
	engine, err := NewEngine(fixture.Root, db, logger, cfg)
	if err != nil {
		_ = db.Close()
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("Failed to create engine: %v", err)
	}

	// Suppress the background FTS goroutine so the synchronous PopulateFTSFromSCIP
	// below is the only writer — this eliminates the race that produces non-
	// deterministic symbol counts (SCIP vs FTS return different result sets).
	engine.DisableBgFTS()

	// Ensure SCIP backend is loaded with fixture index.
	// Redirect the derived-index cache to tmpDir so concurrent tests don't
	// race on the shared fixture/.ckb/scip_derived.gob file.
	scipBackend := engine.GetScipBackend()
	if scipBackend != nil {
		scipBackend.SetCacheRoot(tmpDir)
		if loadErr := scipBackend.LoadIndex(); loadErr != nil {
			t.Logf("Warning: Failed to load SCIP index: %v", loadErr)
		}
	}

	// Populate FTS synchronously so every golden test run takes the FTS code path.
	if popErr := engine.PopulateFTSFromSCIP(context.Background()); popErr != nil {
		t.Logf("Warning: Failed to populate FTS: %v", popErr)
	}

	cleanup := func() {
		_ = db.Close()
		_ = os.RemoveAll(tmpDir)
	}

	return engine, cleanup
}

// TestGolden_SearchSymbols tests SearchSymbols against golden files.
func TestGolden_SearchSymbols(t *testing.T) {
	testutil.ForEachLanguage(t, func(t *testing.T, fixture *testutil.FixtureContext) {
		engine, cleanup := setupGoldenEngine(t, fixture)
		defer cleanup()

		ctx := context.Background()

		testCases := []struct {
			name  string
			query string
			limit int
		}{
			{"search_handler", "Handler", 50},
			{"search_service", "Service", 50},
			{"search_model", "Model", 50},
			{"search_main", "main", 50},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				resp, err := engine.SearchSymbols(ctx, SearchSymbolsOptions{
					Query: tc.query,
					Limit: tc.limit,
				})
				if err != nil {
					t.Fatalf("SearchSymbols failed: %v", err)
				}

				// Normalize the response for golden comparison
				result := normalizeSearchResults(resp)
				testutil.CompareGolden(t, fixture, tc.name, result)
			})
		}
	})
}

// TestGolden_GetCallGraph tests GetCallGraph against golden files.
func TestGolden_GetCallGraph(t *testing.T) {
	testutil.ForEachLanguage(t, func(t *testing.T, fixture *testutil.FixtureContext) {
		engine, cleanup := setupGoldenEngine(t, fixture)
		defer cleanup()

		ctx := context.Background()

		// First, find a symbol to get call graph for
		searchResp, err := engine.SearchSymbols(ctx, SearchSymbolsOptions{
			Query: "main",
			Limit: 5,
		})
		if err != nil {
			t.Fatalf("SearchSymbols failed: %v", err)
		}

		if len(searchResp.Symbols) == 0 {
			t.Skip("No symbols found for call graph test")
		}

		// Find the main function specifically
		var mainSymbolID string
		for _, sym := range searchResp.Symbols {
			if sym.Name == "main" && sym.Kind == "function" {
				mainSymbolID = sym.StableId
				break
			}
		}

		if mainSymbolID == "" {
			t.Skip("main function not found")
		}

		testCases := []struct {
			name      string
			symbolID  string
			depth     int
			direction string
		}{
			{"callgraph_main_depth1", mainSymbolID, 1, "both"},
			{"callgraph_main_depth2", mainSymbolID, 2, "both"},
			{"callgraph_main_callees", mainSymbolID, 2, "callees"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				resp, err := engine.GetCallGraph(ctx, CallGraphOptions{
					SymbolId:  tc.symbolID,
					Depth:     tc.depth,
					Direction: tc.direction,
				})
				if err != nil {
					t.Fatalf("GetCallGraph failed: %v", err)
				}

				// Normalize for golden comparison
				result := normalizeCallGraph(resp)
				testutil.CompareGolden(t, fixture, tc.name, result)
			})
		}
	})
}

// TestGolden_FindReferences tests FindReferences against golden files.
func TestGolden_FindReferences(t *testing.T) {
	testutil.ForEachLanguage(t, func(t *testing.T, fixture *testutil.FixtureContext) {
		engine, cleanup := setupGoldenEngine(t, fixture)
		defer cleanup()

		ctx := context.Background()

		// Find a symbol with references
		searchResp, err := engine.SearchSymbols(ctx, SearchSymbolsOptions{
			Query: "FormatOutput",
			Limit: 5,
		})
		if err != nil {
			t.Fatalf("SearchSymbols failed: %v", err)
		}

		if len(searchResp.Symbols) == 0 {
			t.Skip("No FormatOutput symbol found")
		}

		symbolID := searchResp.Symbols[0].StableId

		t.Run("refs_FormatOutput", func(t *testing.T) {
			resp, err := engine.FindReferences(ctx, FindReferencesOptions{
				SymbolId: symbolID,
				Limit:    100,
			})
			if err != nil {
				t.Fatalf("FindReferences failed: %v", err)
			}

			result := normalizeReferences(resp)
			testutil.CompareGolden(t, fixture, "refs_FormatOutput", result)
		})
	})
}

// TestGolden_GetSymbol tests GetSymbol against golden files.
func TestGolden_GetSymbol(t *testing.T) {
	testutil.ForEachLanguage(t, func(t *testing.T, fixture *testutil.FixtureContext) {
		engine, cleanup := setupGoldenEngine(t, fixture)
		defer cleanup()

		ctx := context.Background()

		// Test cases for different symbol types
		// Use specific names to avoid non-deterministic results
		testCases := []struct {
			searchQuery string
			goldenName  string
			kinds       []string
		}{
			{"NewHandler", "symbol_NewHandler", nil},         // Factory function
			{"DefaultService", "symbol_DefaultService", nil}, // Implementation class
			{"FormatOutput", "symbol_FormatOutput", nil},     // Internal function
			// Kind-filtered to "class": now that field/property members are
			// correctly indexed (see fix/evidence-output-bugs), the fixtures
			// also contain a lowercase "model" field which is an equally
			// valid, unfiltered name match for the query "Model" — ranking
			// alone is no longer enough to deterministically land on the
			// "Data structure" class this case is meant to exercise.
			{"Model", "symbol_Model", []string{"class"}}, // Data structure
		}

		for _, tc := range testCases {
			t.Run(tc.goldenName, func(t *testing.T) {
				// First find the symbol
				searchResp, err := engine.SearchSymbols(ctx, SearchSymbolsOptions{
					Query: tc.searchQuery,
					Limit: 1,
					Kinds: tc.kinds,
				})
				if err != nil {
					t.Fatalf("SearchSymbols failed: %v", err)
				}
				if len(searchResp.Symbols) == 0 {
					t.Skipf("No %s symbol found", tc.searchQuery)
				}

				symbolID := searchResp.Symbols[0].StableId

				resp, err := engine.GetSymbol(ctx, GetSymbolOptions{
					SymbolId: symbolID,
				})
				if err != nil {
					t.Fatalf("GetSymbol failed: %v", err)
				}

				result := normalizeGetSymbol(resp)
				testutil.CompareGolden(t, fixture, tc.goldenName, result)
			})
		}
	})
}

// TestGolden_ExplainSymbol tests ExplainSymbol against golden files.
func TestGolden_ExplainSymbol(t *testing.T) {
	testutil.ForEachLanguage(t, func(t *testing.T, fixture *testutil.FixtureContext) {
		engine, cleanup := setupGoldenEngine(t, fixture)
		defer cleanup()

		ctx := context.Background()

		// Find symbols to explain
		// Use specific names to avoid non-deterministic results
		testCases := []struct {
			searchQuery string
			goldenName  string
		}{
			{"NewHandler", "explain_NewHandler"},
			{"DefaultService", "explain_DefaultService"},
			{"FormatOutput", "explain_FormatOutput"},
		}

		for _, tc := range testCases {
			t.Run(tc.goldenName, func(t *testing.T) {
				// Find the symbol first
				searchResp, err := engine.SearchSymbols(ctx, SearchSymbolsOptions{
					Query: tc.searchQuery,
					Limit: 1,
				})
				if err != nil {
					t.Fatalf("SearchSymbols failed: %v", err)
				}
				if len(searchResp.Symbols) == 0 {
					t.Skipf("No %s symbol found", tc.searchQuery)
				}

				symbolID := searchResp.Symbols[0].StableId

				resp, err := engine.ExplainSymbol(ctx, ExplainSymbolOptions{
					SymbolId: symbolID,
				})
				if err != nil {
					t.Fatalf("ExplainSymbol failed: %v", err)
				}

				result := normalizeExplainSymbol(resp)
				testutil.CompareGolden(t, fixture, tc.goldenName, result)
			})
		}
	})
}

// TestGolden_GetArchitecture tests GetArchitecture against golden files.
func TestGolden_GetArchitecture(t *testing.T) {
	testutil.ForEachLanguage(t, func(t *testing.T, fixture *testutil.FixtureContext) {
		engine, cleanup := setupGoldenEngine(t, fixture)
		defer cleanup()

		ctx := context.Background()

		t.Run("arch_default", func(t *testing.T) {
			resp, err := engine.GetArchitecture(ctx, GetArchitectureOptions{
				Depth:               2,
				IncludeExternalDeps: false,
			})
			if err != nil {
				t.Fatalf("GetArchitecture failed: %v", err)
			}

			result := normalizeArchitecture(resp)
			testutil.CompareGolden(t, fixture, "arch_default", result)
		})
	})
}

// TestGolden_TraceUsage tests TraceUsage against golden files.
func TestGolden_TraceUsage(t *testing.T) {
	testutil.ForEachLanguage(t, func(t *testing.T, fixture *testutil.FixtureContext) {
		engine, cleanup := setupGoldenEngine(t, fixture)
		defer cleanup()

		ctx := context.Background()

		// Find an internal symbol to trace
		searchResp, err := engine.SearchSymbols(ctx, SearchSymbolsOptions{
			Query: "FormatOutput",
			Limit: 1,
		})
		if err != nil {
			t.Fatalf("SearchSymbols failed: %v", err)
		}
		if len(searchResp.Symbols) == 0 {
			t.Skip("No FormatOutput symbol found")
		}

		symbolID := searchResp.Symbols[0].StableId

		t.Run("trace_FormatOutput", func(t *testing.T) {
			resp, err := engine.TraceUsage(ctx, TraceUsageOptions{
				SymbolId: symbolID,
				MaxPaths: 10,
				MaxDepth: 5,
			})
			if err != nil {
				t.Fatalf("TraceUsage failed: %v", err)
			}

			result := normalizeTraceUsage(resp)
			testutil.CompareGolden(t, fixture, "trace_FormatOutput", result)
		})
	})
}

// TestGolden_AnalyzeImpact tests AnalyzeImpact against golden files.
func TestGolden_AnalyzeImpact(t *testing.T) {
	testutil.ForEachLanguage(t, func(t *testing.T, fixture *testutil.FixtureContext) {
		engine, cleanup := setupGoldenEngine(t, fixture)
		defer cleanup()

		ctx := context.Background()

		// Test impact analysis on different symbol types
		testCases := []struct {
			searchQuery string
			goldenName  string
			depth       int
		}{
			{"FormatOutput", "impact_FormatOutput", 2}, // Internal utility
			{"Handler", "impact_Handler", 2},           // High-level type
		}

		for _, tc := range testCases {
			t.Run(tc.goldenName, func(t *testing.T) {
				// First find the symbol
				searchResp, err := engine.SearchSymbols(ctx, SearchSymbolsOptions{
					Query: tc.searchQuery,
					Limit: 1,
				})
				if err != nil {
					t.Fatalf("SearchSymbols failed: %v", err)
				}
				if len(searchResp.Symbols) == 0 {
					t.Skipf("No %s symbol found", tc.searchQuery)
				}

				symbolID := searchResp.Symbols[0].StableId

				resp, err := engine.AnalyzeImpact(ctx, AnalyzeImpactOptions{
					SymbolId: symbolID,
					Depth:    tc.depth,
				})
				if err != nil {
					t.Fatalf("AnalyzeImpact failed: %v", err)
				}

				result := normalizeAnalyzeImpact(resp)
				testutil.CompareGolden(t, fixture, tc.goldenName, result)
			})
		}
	})
}

// normalizeSearchResults normalizes SearchSymbolsResponse for golden comparison.
func normalizeSearchResults(resp *SearchSymbolsResponse) map[string]any {
	results := make([]map[string]any, 0, len(resp.Symbols))
	for _, r := range resp.Symbols {
		file := ""
		line := 0
		if r.Location != nil {
			file = r.Location.FileId
			line = r.Location.StartLine
		}
		results = append(results, map[string]any{
			"name":     r.Name,
			"kind":     r.Kind,
			"moduleId": r.ModuleId,
			"file":     normalizeFilePath(file),
			"line":     line,
		})
	}

	// Sort for stable golden comparison: engine ranks by score, but equal-score
	// results can appear in different order between runs.
	sort.Slice(results, func(i, j int) bool {
		ai, aj := results[i], results[j]
		ni, _ := ai["name"].(string)
		nj, _ := aj["name"].(string)
		if ni != nj {
			return ni < nj
		}
		ki, _ := ai["kind"].(string)
		kj, _ := aj["kind"].(string)
		if ki != kj {
			return ki < kj
		}
		fi, _ := ai["file"].(string)
		fj, _ := aj["file"].(string)
		return fi < fj
	})

	return map[string]any{
		"symbols": results,
		"total":   resp.TotalCount,
	}
}

// normalizeCallGraph normalizes CallGraphResponse for golden comparison.
func normalizeCallGraph(resp *CallGraphResponse) map[string]any {
	nodes := make([]map[string]any, 0, len(resp.Nodes))
	for _, n := range resp.Nodes {
		file := ""
		if n.Location != nil {
			file = n.Location.FileId
		}
		nodes = append(nodes, map[string]any{
			"id":   n.ID,
			"name": n.Name,
			"file": normalizeFilePath(file),
			"role": n.Role,
		})
	}

	edges := make([]map[string]any, 0, len(resp.Edges))
	for _, e := range resp.Edges {
		edges = append(edges, map[string]any{
			"from": e.From,
			"to":   e.To,
		})
	}

	return map[string]any{
		"root":  resp.Root,
		"nodes": nodes,
		"edges": edges,
	}
}

// normalizeReferences normalizes FindReferencesResponse for golden comparison.
func normalizeReferences(resp *FindReferencesResponse) map[string]any {
	refs := make([]map[string]any, 0, len(resp.References))
	for _, r := range resp.References {
		file := ""
		line := 0
		column := 0
		if r.Location != nil {
			file = r.Location.FileId
			line = r.Location.StartLine
			column = r.Location.StartColumn
		}
		refs = append(refs, map[string]any{
			"file":    normalizeFilePath(file),
			"line":    line,
			"column":  column,
			"context": r.Context,
			"kind":    r.Kind,
		})
	}

	return map[string]any{
		"references": refs,
		"total":      resp.TotalCount,
	}
}

// normalizeGetSymbol normalizes GetSymbolResponse for golden comparison.
func normalizeGetSymbol(resp *GetSymbolResponse) map[string]any {
	result := map[string]any{
		"redirected": resp.Redirected,
		"deleted":    resp.Deleted,
	}

	if resp.Symbol != nil {
		file := ""
		line := 0
		if resp.Symbol.Location != nil {
			file = resp.Symbol.Location.FileId
			line = resp.Symbol.Location.StartLine
		}
		result["symbol"] = map[string]any{
			"name":     resp.Symbol.Name,
			"kind":     resp.Symbol.Kind,
			"moduleId": resp.Symbol.ModuleId,
			"file":     normalizeFilePath(file),
			"line":     line,
		}
	}

	return result
}

// normalizeFilePath strips absolute path prefixes for stable comparison.
func normalizeFilePath(path string) string {
	// Get just the relative part after the fixture root
	// This handles paths like /tmp/.../testdata/fixtures/go/pkg/handler.go
	// and /tmp/.../testdata/fixtures/typescript/src/pkg/handler.ts
	parts := []string{
		"src/pkg/", "src/internal/", "src/main.ts", // TypeScript (has src/ prefix)
		"pkg/", "internal/", "main.go", // Go (no src/ prefix)
	}
	for _, p := range parts {
		if idx := indexLast(path, p); idx != -1 {
			return path[idx:]
		}
	}
	// Fallback: return just the filename
	return filepath.Base(path)
}

func indexLast(s, substr string) int {
	for i := len(s) - len(substr); i >= 0; i-- {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// roundFloat rounds a float to the specified number of decimal places.
// This helps avoid floating point precision issues in golden comparisons.
func roundFloat(val float64, precision int) float64 {
	ratio := math.Pow(10, float64(precision))
	return math.Round(val*ratio) / ratio
}

// normalizeExplainSymbol normalizes ExplainSymbolResponse for golden comparison.
func normalizeExplainSymbol(resp *ExplainSymbolResponse) map[string]any {
	facts := map[string]any{
		"module": resp.Facts.Module,
	}

	if resp.Facts.Symbol != nil {
		file := ""
		if resp.Facts.Symbol.Location != nil {
			file = resp.Facts.Symbol.Location.FileId
		}
		facts["symbol"] = map[string]any{
			"name": resp.Facts.Symbol.Name,
			"kind": resp.Facts.Symbol.Kind,
			"file": normalizeFilePath(file),
		}
	}

	if resp.Facts.Usage != nil {
		facts["usage"] = map[string]any{
			"callerCount":    resp.Facts.Usage.CallerCount,
			"calleeCount":    resp.Facts.Usage.CalleeCount,
			"referenceCount": resp.Facts.Usage.ReferenceCount,
		}
	}

	return map[string]any{
		"facts": facts,
		"summary": map[string]any{
			"tldr":     resp.Summary.Tldr,
			"identity": resp.Summary.Identity,
			"usage":    resp.Summary.Usage,
		},
	}
}

// normalizeArchitecture normalizes GetArchitectureResponse for golden comparison.
func normalizeArchitecture(resp *GetArchitectureResponse) map[string]any {
	modules := make([]map[string]any, 0, len(resp.Modules))
	for _, m := range resp.Modules {
		modules = append(modules, map[string]any{
			"moduleId":    m.ModuleId,
			"name":        m.Name,
			"path":        m.Path,
			"symbolCount": m.SymbolCount,
			"fileCount":   m.FileCount,
		})
	}

	deps := make([]map[string]any, 0, len(resp.DependencyGraph))
	for _, d := range resp.DependencyGraph {
		deps = append(deps, map[string]any{
			"from": d.From,
			"to":   d.To,
		})
	}

	return map[string]any{
		"modules":         modules,
		"dependencyGraph": deps,
	}
}

// normalizeTraceUsage normalizes TraceUsageResponse for golden comparison.
func normalizeTraceUsage(resp *TraceUsageResponse) map[string]any {
	paths := make([]map[string]any, 0, len(resp.Paths))
	for _, p := range resp.Paths {
		nodes := make([]map[string]any, 0, len(p.Nodes))
		for _, n := range p.Nodes {
			file := ""
			if n.Location != nil {
				file = n.Location.FileId
			}
			nodes = append(nodes, map[string]any{
				"name": n.Name,
				"kind": n.Kind,
				"role": n.Role,
				"file": normalizeFilePath(file),
			})
		}
		paths = append(paths, map[string]any{
			"pathType": p.PathType,
			"nodes":    nodes,
		})
	}

	return map[string]any{
		"targetSymbol":    resp.TargetSymbol,
		"paths":           paths,
		"totalPathsFound": resp.TotalPathsFound,
	}
}

// normalizeAnalyzeImpact normalizes AnalyzeImpactResponse for golden comparison.
func normalizeAnalyzeImpact(resp *AnalyzeImpactResponse) map[string]any {
	result := map[string]any{}

	// Normalize symbol info
	if resp.Symbol != nil {
		file := ""
		if resp.Symbol.Location != nil {
			file = resp.Symbol.Location.FileId
		}
		result["symbol"] = map[string]any{
			"name": resp.Symbol.Name,
			"kind": resp.Symbol.Kind,
			"file": normalizeFilePath(file),
		}
	}

	// Normalize visibility
	if resp.Visibility != nil {
		result["visibility"] = map[string]any{
			"visibility": resp.Visibility.Visibility,
			"confidence": resp.Visibility.Confidence,
			"source":     resp.Visibility.Source,
		}
	}

	// Normalize risk score
	if resp.RiskScore != nil {
		result["riskScore"] = map[string]any{
			"score":       roundFloat(resp.RiskScore.Score, 2),
			"level":       resp.RiskScore.Level,
			"explanation": resp.RiskScore.Explanation,
		}
	}

	// Normalize blast radius
	if resp.BlastRadius != nil {
		result["blastRadius"] = map[string]any{
			"moduleCount":       resp.BlastRadius.ModuleCount,
			"fileCount":         resp.BlastRadius.FileCount,
			"uniqueCallerCount": resp.BlastRadius.UniqueCallerCount,
			"riskLevel":         resp.BlastRadius.RiskLevel,
		}
	}

	// Normalize direct impact
	directImpact := make([]map[string]any, 0, len(resp.DirectImpact))
	for _, item := range resp.DirectImpact {
		file := ""
		if item.Location != nil {
			file = item.Location.FileId
		}
		directImpact = append(directImpact, map[string]any{
			"name": item.Name,
			"kind": item.Kind,
			"file": normalizeFilePath(file),
		})
	}
	result["directImpact"] = directImpact

	// Normalize modules affected
	modulesAffected := make([]map[string]any, 0, len(resp.ModulesAffected))
	for _, m := range resp.ModulesAffected {
		modulesAffected = append(modulesAffected, map[string]any{
			"moduleId":    m.ModuleId,
			"name":        m.Name,
			"impactCount": m.ImpactCount,
		})
	}
	result["modulesAffected"] = modulesAffected

	return result
}

// TestGolden_SCIPBackendDirect tests SCIP backend methods directly.
func TestGolden_SCIPBackendDirect(t *testing.T) {
	testutil.ForEachLanguage(t, func(t *testing.T, fixture *testutil.FixtureContext) {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))

		// Create config for SCIP adapter
		cfg := config.DefaultConfig()
		cfg.RepoRoot = fixture.Root
		cfg.Backends.Scip.Enabled = true
		cfg.Backends.Scip.IndexPath = fixture.SCIPPath

		// Load SCIP index directly
		adapter, err := scip.NewSCIPAdapter(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create SCIP adapter: %v", err)
		}

		t.Run("scip_all_symbols", func(t *testing.T) {
			symbols := adapter.AllSymbols()

			// Normalize for golden comparison
			result := make([]map[string]any, 0, len(symbols))
			for _, s := range symbols {
				result = append(result, map[string]any{
					"symbol": s.Symbol,
					"kind":   s.Kind,
				})
			}

			testutil.CompareGolden(t, fixture, "scip_all_symbols", result)
		})
	})
}

// TestPrepareChange_ResolvesModuleIdForSymbolTarget is a regression test
// for ckb impact prepare's "No tests found" risk factor firing regardless
// of whether test files actually exist next to the target. Root cause:
// the SCIP backend never resolves ModuleId on symbol lookups (see
// backends/scip/adapter.go's convertToSymbolResult), so
// resolvePrepareTarget's PrepareChangeTarget.ModuleId came back "" for
// every symbol target — which meant getPrepareTests' same-module
// *_test.go glob (gated on `target.ModuleId != ""`) never even ran. This
// verifies resolvePrepareTarget now derives ModuleId from the symbol's
// file path when the backend doesn't supply one, the same way the
// file/directory target branches already did.
func TestPrepareChange_ResolvesModuleIdForSymbolTarget(t *testing.T) {
	testutil.ForEachLanguage(t, func(t *testing.T, fixture *testutil.FixtureContext) {
		engine, cleanup := setupGoldenEngine(t, fixture)
		defer cleanup()

		ctx := context.Background()

		searchResp, err := engine.SearchSymbols(ctx, SearchSymbolsOptions{Query: "NewHandler", Limit: 1})
		if err != nil {
			t.Fatalf("SearchSymbols failed: %v", err)
		}
		if len(searchResp.Symbols) == 0 {
			t.Skip("No NewHandler symbol found")
		}

		resp, err := engine.PrepareChange(ctx, PrepareChangeOptions{
			Target:     searchResp.Symbols[0].StableId,
			ChangeType: ChangeModify,
		})
		if err != nil {
			t.Fatalf("PrepareChange failed: %v", err)
		}
		if resp.Target == nil {
			t.Fatal("expected target info")
		}

		if resp.Target.ModuleId == "" {
			t.Error("Target.ModuleId is empty — resolvePrepareTarget should derive it from the symbol's file path (pkg/handler.go -> \"pkg\") when the SCIP backend doesn't supply one")
		}
	})
}
