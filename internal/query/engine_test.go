package query

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

// testEngine creates a test engine with minimal configuration
func testEngine(t *testing.T) (*Engine, func()) {
	t.Helper()

	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "ckb-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// Create .ckb directory
	ckbDir := filepath.Join(tmpDir, ".ckb")
	if mkdirErr := os.MkdirAll(ckbDir, 0755); mkdirErr != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("failed to create .ckb dir: %v", mkdirErr)
	}

	// Create test logger (silent)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Create test database
	db, err := storage.Open(tmpDir, logger)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("failed to create test db: %v", err)
	}

	// Create test config
	cfg := config.DefaultConfig()
	cfg.RepoRoot = tmpDir

	// Create engine
	engine, err := NewEngine(tmpDir, db, logger, cfg)
	if err != nil {
		_ = db.Close()
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("failed to create engine: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
		_ = os.RemoveAll(tmpDir)
	}

	return engine, cleanup
}

func TestNewEngine(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	if engine == nil {
		t.Fatal("engine should not be nil")
	}
}

func TestGetStatus(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()
	status, err := engine.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}

	// Verify required fields
	if status.CkbVersion == "" {
		t.Error("CkbVersion should not be empty")
	}

	if status.RepoState == nil {
		t.Error("RepoState should not be nil")
	}

	if status.Cache == nil {
		t.Error("Cache should not be nil")
	}

	// Verify backends are reported
	if len(status.Backends) == 0 {
		t.Error("Backends should not be empty")
	}
}

func TestDoctor(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("all checks", func(t *testing.T) {
		result, err := engine.Doctor(ctx, "")
		if err != nil {
			t.Fatalf("Doctor failed: %v", err)
		}

		if len(result.Checks) == 0 {
			t.Error("Checks should not be empty")
		}

		// Verify each check has required fields
		for _, check := range result.Checks {
			if check.Name == "" {
				t.Error("Check name should not be empty")
			}
			if check.Status == "" {
				t.Error("Check status should not be empty")
			}
			if check.Status != "pass" && check.Status != "warn" && check.Status != "fail" {
				t.Errorf("Invalid check status: %s", check.Status)
			}
		}
	})

	t.Run("specific check", func(t *testing.T) {
		result, err := engine.Doctor(ctx, "config")
		if err != nil {
			t.Fatalf("Doctor failed: %v", err)
		}

		// Should have filtered to just the config check
		found := false
		for _, check := range result.Checks {
			if check.Name == "config" {
				found = true
				break
			}
		}
		if !found {
			t.Error("Config check should be present")
		}
	})
}

func TestSearchSymbols(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("empty query", func(t *testing.T) {
		opts := SearchSymbolsOptions{
			Query: "",
			Limit: 10,
		}
		result, err := engine.SearchSymbols(ctx, opts)
		if err != nil {
			t.Fatalf("SearchSymbols failed: %v", err)
		}

		// Empty query should return empty results
		if result.TotalCount != 0 {
			t.Errorf("Expected 0 results for empty query, got %d", result.TotalCount)
		}
	})

	t.Run("with query", func(t *testing.T) {
		opts := SearchSymbolsOptions{
			Query: "nonexistent",
			Limit: 10,
		}
		result, err := engine.SearchSymbols(ctx, opts)
		if err != nil {
			t.Fatalf("SearchSymbols failed: %v", err)
		}

		// Should have provenance
		if result.Provenance == nil {
			t.Error("Provenance should not be nil")
		}
	})

	t.Run("with limit", func(t *testing.T) {
		opts := SearchSymbolsOptions{
			Query: "test",
			Limit: 5,
		}
		result, err := engine.SearchSymbols(ctx, opts)
		if err != nil {
			t.Fatalf("SearchSymbols failed: %v", err)
		}

		if len(result.Symbols) > 5 {
			t.Errorf("Expected at most 5 results, got %d", len(result.Symbols))
		}
	})
}

func TestGetSymbol(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("invalid symbol id", func(t *testing.T) {
		opts := GetSymbolOptions{
			SymbolId: "invalid-symbol-id",
		}
		result, err := engine.GetSymbol(ctx, opts)

		// Either returns error or empty result for invalid symbol
		if err != nil {
			// Error is acceptable for invalid symbol
			return
		}

		// If no error, should return empty result
		if result.Symbol != nil && result.Symbol.StableId != "" {
			t.Error("Expected nil or empty symbol for invalid ID")
		}
	})
}

func TestFindReferences(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("invalid symbol id", func(t *testing.T) {
		opts := FindReferencesOptions{
			SymbolId: "invalid-symbol-id",
			Limit:    10,
		}
		result, err := engine.FindReferences(ctx, opts)

		// Either returns error or empty result for invalid symbol
		if err != nil {
			// Error is acceptable for invalid symbol
			return
		}

		// Should return empty references for invalid symbol
		if result.TotalCount != 0 {
			t.Errorf("Expected 0 references for invalid symbol, got %d", result.TotalCount)
		}
	})

	t.Run("with valid options structure", func(t *testing.T) {
		opts := FindReferencesOptions{
			SymbolId:     "test-symbol",
			IncludeTests: true,
			Limit:        50,
		}
		// Just test that the function doesn't panic with valid options structure
		_, _ = engine.FindReferences(ctx, opts)
	})
}

func TestGetArchitecture(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("basic architecture", func(t *testing.T) {
		opts := GetArchitectureOptions{
			Depth: 2,
		}
		result, err := engine.GetArchitecture(ctx, opts)
		if err != nil {
			t.Fatalf("GetArchitecture failed: %v", err)
		}

		// Should have provenance
		if result.Provenance == nil {
			t.Error("Provenance should not be nil")
		}

		// Modules might be empty for test directory, that's OK
	})

	t.Run("with external deps", func(t *testing.T) {
		opts := GetArchitectureOptions{
			Depth:               2,
			IncludeExternalDeps: true,
		}
		result, err := engine.GetArchitecture(ctx, opts)
		if err != nil {
			t.Fatalf("GetArchitecture failed: %v", err)
		}

		// Should not panic or error
		_ = result
	})
}

func TestAnalyzeImpact(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("invalid symbol", func(t *testing.T) {
		opts := AnalyzeImpactOptions{
			SymbolId: "invalid-symbol-id",
			Depth:    2,
		}
		result, err := engine.AnalyzeImpact(ctx, opts)

		// Either returns error or empty result for invalid symbol
		if err != nil {
			// Error is acceptable for invalid symbol
			return
		}

		// Should return empty impact for invalid symbol
		if len(result.DirectImpact) != 0 {
			t.Errorf("Expected 0 direct impact for invalid symbol, got %d", len(result.DirectImpact))
		}
	})

	t.Run("with valid options structure", func(t *testing.T) {
		opts := AnalyzeImpactOptions{
			SymbolId: "test-symbol",
			Depth:    3,
		}
		// Just test that the function doesn't panic with valid options structure
		_, _ = engine.AnalyzeImpact(ctx, opts)
	})
}

func TestGenerateFixScript(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	// Run doctor to get a response
	doctorResp, err := engine.Doctor(ctx, "")
	if err != nil {
		t.Fatalf("Doctor failed: %v", err)
	}

	// Generate fix script
	script := engine.GenerateFixScript(doctorResp)

	// Script should be non-empty string
	if script == "" {
		t.Error("Fix script should not be empty")
	}

	// Script should contain shebang
	if len(script) > 0 && script[0:2] != "#!" {
		t.Error("Fix script should start with shebang")
	}
}

// =============================================================================
// v5.2 Navigation Tools Integration Tests
// =============================================================================

func TestExplainFile(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("non-existent file", func(t *testing.T) {
		opts := ExplainFileOptions{
			FilePath: "non/existent/file.go",
		}
		result, err := engine.ExplainFile(ctx, opts)

		// Should handle non-existent files gracefully
		if err != nil {
			// Error is acceptable for non-existent file
			return
		}

		// Should have metadata even if file doesn't exist
		if result.Tool != "explainFile" {
			t.Errorf("expected tool 'explainFile', got %q", result.Tool)
		}
	})

	t.Run("creates file and explains it", func(t *testing.T) {
		// Create a test file in the temp directory
		testFile := filepath.Join(engine.repoRoot, "test_source.go")
		content := `package main

func main() {
	println("hello")
}
`
		if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		opts := ExplainFileOptions{
			FilePath: "test_source.go",
		}
		result, err := engine.ExplainFile(ctx, opts)
		if err != nil {
			t.Fatalf("ExplainFile failed: %v", err)
		}

		// Verify response structure
		if result.Tool != "explainFile" {
			t.Errorf("expected tool 'explainFile', got %q", result.Tool)
		}
		if result.Facts.Language != "go" {
			t.Errorf("expected language 'go', got %q", result.Facts.Language)
		}
	})
}

func TestTraceUsage(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("invalid symbol", func(t *testing.T) {
		opts := TraceUsageOptions{
			SymbolId: "invalid-symbol-id",
			MaxPaths: 5,
		}
		result, err := engine.TraceUsage(ctx, opts)

		// Should handle gracefully - either error or empty result
		if err != nil {
			return
		}

		// Should have metadata
		if result.Tool != "traceUsage" {
			t.Errorf("expected tool 'traceUsage', got %q", result.Tool)
		}
	})

	t.Run("with max paths", func(t *testing.T) {
		opts := TraceUsageOptions{
			SymbolId: "test-symbol",
			MaxPaths: 10,
			MaxDepth: 3,
		}
		// Should not panic
		_, _ = engine.TraceUsage(ctx, opts)
	})
}

func TestListEntrypoints(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("basic entrypoints", func(t *testing.T) {
		opts := ListEntrypointsOptions{
			Limit: 30,
		}
		result, err := engine.ListEntrypoints(ctx, opts)
		if err != nil {
			t.Fatalf("ListEntrypoints failed: %v", err)
		}

		// Verify response structure
		if result.Tool != "listEntrypoints" {
			t.Errorf("expected tool 'listEntrypoints', got %q", result.Tool)
		}
		// Entrypoints might be empty for test directory
	})

	t.Run("with module filter", func(t *testing.T) {
		opts := ListEntrypointsOptions{
			ModuleFilter: "internal",
			Limit:        10,
		}
		result, err := engine.ListEntrypoints(ctx, opts)
		if err != nil {
			t.Fatalf("ListEntrypoints failed: %v", err)
		}

		// Should not panic and return result
		_ = result
	})
}

func TestSummarizeDiff(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("with time window", func(t *testing.T) {
		opts := SummarizeDiffOptions{
			TimeWindow: &TimeWindowSelector{
				Start: "2024-01-01T00:00:00Z",
			},
		}
		result, err := engine.SummarizeDiff(ctx, opts)

		// Git backend may not be available in test, that's OK
		if err != nil {
			return
		}

		// Verify response structure
		if result.Tool != "summarizeDiff" {
			t.Errorf("expected tool 'summarizeDiff', got %q", result.Tool)
		}
	})

	t.Run("with commit", func(t *testing.T) {
		opts := SummarizeDiffOptions{
			Commit: "HEAD",
		}
		// Should not panic even if git not available
		_, _ = engine.SummarizeDiff(ctx, opts)
	})
}

func TestGetHotspots(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("basic hotspots", func(t *testing.T) {
		opts := GetHotspotsOptions{
			Limit: 20,
		}
		result, err := engine.GetHotspots(ctx, opts)

		// Git backend may not be available in test, that's OK
		if err != nil {
			return
		}

		// Verify response structure
		if result.Tool != "getHotspots" {
			t.Errorf("expected tool 'getHotspots', got %q", result.Tool)
		}
	})

	t.Run("with scope filter", func(t *testing.T) {
		opts := GetHotspotsOptions{
			Scope: "internal/query",
			Limit: 10,
		}
		// Should not panic
		_, _ = engine.GetHotspots(ctx, opts)
	})

	t.Run("respects limit", func(t *testing.T) {
		opts := GetHotspotsOptions{
			Limit: 5,
		}
		result, err := engine.GetHotspots(ctx, opts)
		if err != nil {
			return
		}

		if len(result.Hotspots) > 5 {
			t.Errorf("expected at most 5 hotspots, got %d", len(result.Hotspots))
		}
	})
}

func TestExplainPath(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("test file path", func(t *testing.T) {
		opts := ExplainPathOptions{
			FilePath: "internal/query/engine_test.go",
		}
		result, err := engine.ExplainPath(ctx, opts)
		if err != nil {
			t.Fatalf("ExplainPath failed: %v", err)
		}

		// Verify response structure
		if result.Tool != "explainPath" {
			t.Errorf("expected tool 'explainPath', got %q", result.Tool)
		}
		if result.Role != "test-only" {
			t.Errorf("expected role 'test-only' for test file, got %q", result.Role)
		}
	})

	t.Run("config file path", func(t *testing.T) {
		opts := ExplainPathOptions{
			FilePath: "config.json",
		}
		result, err := engine.ExplainPath(ctx, opts)
		if err != nil {
			t.Fatalf("ExplainPath failed: %v", err)
		}

		if result.Role != "config" {
			t.Errorf("expected role 'config' for config file, got %q", result.Role)
		}
	})

	t.Run("with context hint", func(t *testing.T) {
		opts := ExplainPathOptions{
			FilePath:    "internal/api/handler.go",
			ContextHint: "from traceUsage",
		}
		result, err := engine.ExplainPath(ctx, opts)
		if err != nil {
			t.Fatalf("ExplainPath failed: %v", err)
		}

		if result.Role != "glue" {
			t.Errorf("expected role 'glue' for handler file, got %q", result.Role)
		}
	})

	t.Run("core file path", func(t *testing.T) {
		opts := ExplainPathOptions{
			FilePath: "app/internal/query/engine.go",
		}
		result, err := engine.ExplainPath(ctx, opts)
		if err != nil {
			t.Fatalf("ExplainPath failed: %v", err)
		}

		if result.Role != "core" {
			t.Errorf("expected role 'core' for internal file, got %q", result.Role)
		}
	})
}

func TestListKeyConcepts(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("basic concepts", func(t *testing.T) {
		opts := ListKeyConceptsOptions{
			Limit: 12,
		}
		result, err := engine.ListKeyConcepts(ctx, opts)
		if err != nil {
			t.Fatalf("ListKeyConcepts failed: %v", err)
		}

		// Verify response structure
		if result.Tool != "listKeyConcepts" {
			t.Errorf("expected tool 'listKeyConcepts', got %q", result.Tool)
		}
		// Concepts might be empty for test directory
	})

	t.Run("respects limit", func(t *testing.T) {
		opts := ListKeyConceptsOptions{
			Limit: 5,
		}
		result, err := engine.ListKeyConcepts(ctx, opts)
		if err != nil {
			t.Fatalf("ListKeyConcepts failed: %v", err)
		}

		if len(result.Concepts) > 5 {
			t.Errorf("expected at most 5 concepts, got %d", len(result.Concepts))
		}
	})
}

func TestRecentlyRelevant(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("basic recent items", func(t *testing.T) {
		opts := RecentlyRelevantOptions{}
		result, err := engine.RecentlyRelevant(ctx, opts)

		// Git backend may not be available in test, that's OK
		if err != nil {
			return
		}

		// Verify response structure
		if result.Tool != "recentlyRelevant" {
			t.Errorf("expected tool 'recentlyRelevant', got %q", result.Tool)
		}
	})

	t.Run("with module filter", func(t *testing.T) {
		opts := RecentlyRelevantOptions{
			ModuleFilter: "internal/query",
		}
		// Should not panic
		_, _ = engine.RecentlyRelevant(ctx, opts)
	})

	t.Run("with time window", func(t *testing.T) {
		opts := RecentlyRelevantOptions{
			TimeWindow: &TimeWindowSelector{
				Start: "2024-01-01T00:00:00Z",
			},
		}
		// Should not panic
		_, _ = engine.RecentlyRelevant(ctx, opts)
	})
}

// =============================================================================
// Additional v5.1/v5.2 Tool Tests
// =============================================================================

func TestExplainSymbol(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("invalid symbol", func(t *testing.T) {
		opts := ExplainSymbolOptions{
			SymbolId: "invalid-symbol-id",
		}
		result, err := engine.ExplainSymbol(ctx, opts)

		// Should handle gracefully
		if err != nil {
			return
		}

		// Should have metadata
		if result.CkbVersion == "" {
			t.Error("CkbVersion should not be empty")
		}
	})
}

func TestJustifySymbol(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("invalid symbol", func(t *testing.T) {
		opts := JustifySymbolOptions{
			SymbolId: "invalid-symbol-id",
		}
		result, err := engine.JustifySymbol(ctx, opts)

		// Should handle gracefully
		if err != nil {
			return
		}

		// Should have verdict even for unknown symbol
		if result.Verdict == "" {
			t.Error("Verdict should not be empty")
		}
	})
}

func TestGetCallGraph(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("invalid symbol", func(t *testing.T) {
		opts := CallGraphOptions{
			SymbolId:  "invalid-symbol-id",
			Direction: "both",
			Depth:     1,
		}
		result, err := engine.GetCallGraph(ctx, opts)

		// Should handle gracefully
		if err != nil {
			return
		}

		// Should have metadata - at least one of these should be set
		if result.Root == "" && result.CkbVersion == "" {
			t.Error("expected at least Root or CkbVersion to be set")
		}
	})

	t.Run("with callers direction", func(t *testing.T) {
		opts := CallGraphOptions{
			SymbolId:  "test-symbol",
			Direction: "callers",
			Depth:     2,
		}
		// Should not panic
		_, _ = engine.GetCallGraph(ctx, opts)
	})

	t.Run("with callees direction", func(t *testing.T) {
		opts := CallGraphOptions{
			SymbolId:  "test-symbol",
			Direction: "callees",
			Depth:     2,
		}
		// Should not panic
		_, _ = engine.GetCallGraph(ctx, opts)
	})
}

func TestGetModuleOverview(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("by path", func(t *testing.T) {
		opts := ModuleOverviewOptions{
			Path: "internal/query",
		}
		result, err := engine.GetModuleOverview(ctx, opts)

		// Module may not exist in test dir, that's OK
		if err != nil {
			return
		}

		if result.Provenance == nil {
			t.Error("Provenance should not be nil")
		}
	})

	t.Run("by name", func(t *testing.T) {
		opts := ModuleOverviewOptions{
			Name: "query",
		}
		// Should not panic
		_, _ = engine.GetModuleOverview(ctx, opts)
	})
}

func TestAnchorConfigToRepo(t *testing.T) {
	cfg := config.DefaultConfig()
	got := anchorConfigToRepo(cfg, "/repos/other")
	if got.RepoRoot != "/repos/other" {
		t.Errorf("RepoRoot = %q, want /repos/other", got.RepoRoot)
	}
	if cfg.RepoRoot != "." {
		t.Errorf("caller's config changed to %q", cfg.RepoRoot)
	}
	abs := config.DefaultConfig()
	abs.RepoRoot = "/fixed"
	if anchorConfigToRepo(abs, "/repos/other").RepoRoot != "/fixed" {
		t.Error("an absolute RepoRoot must stay")
	}
}
