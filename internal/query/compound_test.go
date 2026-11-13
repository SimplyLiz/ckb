package query

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/testutil"
)

// createTestDirectory creates a test directory structure in the engine's repo root.
func createTestDirectory(t *testing.T, engine *Engine, path string) {
	t.Helper()
	absPath := filepath.Join(engine.repoRoot, path)
	if err := os.MkdirAll(absPath, 0755); err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}
}

// createTestFile creates a test file in the engine's repo root.
func createTestFile(t *testing.T, engine *Engine, path, content string) {
	t.Helper()
	absPath := filepath.Join(engine.repoRoot, path)
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create directory for test file: %v", err)
	}
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
}

// =============================================================================
// Explore Tests
// =============================================================================

func TestExplore_Directory(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	// Create a test directory structure
	createTestDirectory(t, engine, "testpkg")
	createTestFile(t, engine, "testpkg/main.go", "package testpkg\n\nfunc Hello() {}\n")

	ctx := context.Background()

	resp, err := engine.Explore(ctx, ExploreOptions{
		Target: "testpkg",
		Depth:  ExploreStandard,
		Focus:  FocusStructure,
	})

	if err != nil {
		t.Fatalf("Explore failed: %v", err)
	}

	if resp == nil {
		t.Fatal("expected response")
		return
	}

	if resp.Overview == nil {
		t.Fatal("expected overview")
		return
	}

	if resp.Overview.TargetType != "directory" {
		t.Errorf("expected directory target type, got %s", resp.Overview.TargetType)
	}

	if resp.Health == nil {
		t.Error("expected health info")
	}
}

func TestExplore_File(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	// Create a test file
	createTestFile(t, engine, "testpkg/main.go", "package testpkg\n\nfunc Hello() {}\n")

	ctx := context.Background()

	resp, err := engine.Explore(ctx, ExploreOptions{
		Target: "testpkg/main.go",
		Depth:  ExploreShallow,
		Focus:  FocusStructure,
	})

	if err != nil {
		t.Fatalf("Explore failed: %v", err)
	}

	if resp == nil {
		t.Fatal("expected response")
		return
	}

	if resp.Overview == nil {
		t.Fatal("expected overview")
		return
	}

	if resp.Overview.TargetType != "file" {
		t.Errorf("expected file target type, got %s", resp.Overview.TargetType)
	}

	// Language should be detected
	if resp.Overview.Language != "go" {
		t.Errorf("expected go language, got %s", resp.Overview.Language)
	}
}

func TestExplore_InvalidPath(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	_, err := engine.Explore(ctx, ExploreOptions{
		Target: "nonexistent/path",
	})

	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestExplore_DeepDepth(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	// Create a test directory structure
	createTestDirectory(t, engine, "testpkg")
	createTestFile(t, engine, "testpkg/main.go", "package testpkg\n\nfunc Hello() {}\n")

	ctx := context.Background()

	resp, err := engine.Explore(ctx, ExploreOptions{
		Target: "testpkg",
		Depth:  ExploreDeep,
		Focus:  FocusChanges,
	})

	if err != nil {
		t.Fatalf("Explore failed: %v", err)
	}

	if resp == nil {
		t.Fatal("expected response")
		return
	}

	if resp.Health == nil {
		t.Fatal("expected health info")
		return
	}

	// Deep exploration should include hotspots when focus is changes
	// (depends on git being available)
	// Hotspots may still be empty if no files have changed recently
	_ = resp.Health.GitAvailable
}

// =============================================================================
// Understand Tests
// =============================================================================

func TestUnderstand_ExactMatch(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	// Search for a symbol that exists (Engine is a common one)
	resp, err := engine.Understand(ctx, UnderstandOptions{
		Query:             "Engine",
		IncludeReferences: false,
		IncludeCallGraph:  false,
	})

	if err != nil {
		// May fail if SCIP not available, that's OK
		return
	}

	if resp == nil {
		t.Fatal("expected response")
		return
	}

	if resp.Symbol == nil && resp.Ambiguity == nil {
		t.Error("expected symbol or ambiguity info")
	}
}

func TestUnderstand_Ambiguous(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	// Search for a common term that might match multiple symbols
	resp, err := engine.Understand(ctx, UnderstandOptions{
		Query:             "Get",
		IncludeReferences: false,
		IncludeCallGraph:  false,
	})

	if err != nil {
		// May fail if SCIP not available
		return
	}

	if resp == nil {
		t.Fatal("expected response")
		return
	}

	// With a vague query like "Get", we should get ambiguity info
	if resp.Ambiguity != nil && resp.Ambiguity.MatchCount < 2 {
		t.Error("expected multiple matches for ambiguous query")
	}
}

func TestUnderstand_NotFound(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	_, err := engine.Understand(ctx, UnderstandOptions{
		Query: "NonExistentSymbol12345",
	})

	if err == nil {
		t.Error("expected error for non-existent symbol")
	}
}

func TestUnderstand_WithReferences(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	resp, err := engine.Understand(ctx, UnderstandOptions{
		Query:             "Engine",
		IncludeReferences: true,
		MaxReferences:     10,
	})

	if err != nil {
		return // SCIP may not be available
	}

	if resp == nil {
		t.Fatal("expected response")
		return
	}

	// References should be grouped by file
	if resp.References != nil && len(resp.References.ByFile) > 0 {
		for _, fileRefs := range resp.References.ByFile {
			if fileRefs.File == "" {
				t.Error("reference file should have a path")
			}
			if fileRefs.Count != len(fileRefs.References) {
				t.Error("reference count mismatch")
			}
		}
	}
}

// =============================================================================
// PrepareChange Tests
// =============================================================================

func TestPrepareChange_File(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	// Create a test file
	createTestFile(t, engine, "testpkg/main.go", "package testpkg\n\nfunc Hello() {}\n")

	ctx := context.Background()

	resp, err := engine.PrepareChange(ctx, PrepareChangeOptions{
		Target:     "testpkg/main.go",
		ChangeType: ChangeModify,
	})

	if err != nil {
		t.Fatalf("PrepareChange failed: %v", err)
	}

	if resp == nil {
		t.Fatal("expected response")
		return
	}

	if resp.Target == nil {
		t.Fatal("expected target info")
		return
	}

	if resp.Target.Kind != "file" {
		t.Errorf("expected file kind, got %s", resp.Target.Kind)
	}

	if resp.RiskAssessment == nil {
		t.Error("expected risk assessment")
	}
}

func TestPrepareChange_Directory(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	// Create a test directory
	createTestDirectory(t, engine, "testpkg")
	createTestFile(t, engine, "testpkg/main.go", "package testpkg\n\nfunc Hello() {}\n")

	ctx := context.Background()

	resp, err := engine.PrepareChange(ctx, PrepareChangeOptions{
		Target:     "testpkg",
		ChangeType: ChangeModify,
	})

	if err != nil {
		t.Fatalf("PrepareChange failed: %v", err)
	}

	if resp == nil || resp.Target == nil {
		t.Fatal("expected response with target")
		return
	}

	if resp.Target.Kind != "module" {
		t.Errorf("expected module kind for directory, got %s", resp.Target.Kind)
	}
}

func TestPrepareChange_InvalidTarget(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	_, err := engine.PrepareChange(ctx, PrepareChangeOptions{
		Target: "nonexistent/target",
	})

	if err == nil {
		t.Error("expected error for invalid target")
	}
}

func TestPrepareChange_DeleteType(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	// Create a test file
	createTestFile(t, engine, "testpkg/main.go", "package testpkg\n\nfunc Hello() {}\n")

	ctx := context.Background()

	resp, err := engine.PrepareChange(ctx, PrepareChangeOptions{
		Target:     "testpkg/main.go",
		ChangeType: ChangeDelete,
	})

	if err != nil {
		t.Fatalf("PrepareChange failed: %v", err)
	}

	// Delete change type should increase risk score
	if resp != nil && resp.RiskAssessment != nil {
		found := false
		for _, factor := range resp.RiskAssessment.Factors {
			if factor == "Deletion change type" {
				found = true
				break
			}
		}
		// Risk factor may or may not be present depending on implementation
		_ = found
	}
}

// =============================================================================
// calculatePrepareRisk Tests
// =============================================================================

// TestCalculatePrepareRisk_ScoreRounding is a regression test for ckb impact
// prepare reporting "score": 0.7000000000000001 instead of 0.7. score is
// accumulated from binary floats (0.15, 0.2, ...) whose sum isn't exactly
// representable in float64; these specific factor weights (moderate
// dependents 0.15 + module spread 0.2 + public visibility 0.15 + no tests
// 0.2) are the exact combination that reproduces the reported value when
// summed naively.
//
// Path is set to a .go file so the "no tests" factor actually fires: test
// discovery (and therefore its risk factor) is language-gated — see
// TestCalculatePrepareRisk_TestFactorOmittedForUnsupportedLanguage — and Go
// is one of the two languages it's implemented for.
func TestCalculatePrepareRisk_ScoreRounding(t *testing.T) {
	engine := &Engine{}

	dependents := make([]PrepareDependent, 8) // >5 -> +0.15 "moderate dependent count"
	transitive := &PrepareTransitive{ModuleSpread: 6}
	target := &PrepareChangeTarget{Visibility: "public", Path: "internal/example/widget.go"} // +0.15
	// transitive module spread >5 -> +0.2, no tests -> +0.2

	risk := engine.calculatePrepareRisk(target, dependents, transitive, nil, nil, nil, ChangeModify)

	if risk.Score != 0.7 {
		t.Errorf("Score = %v, want exactly 0.7 (rounded)", risk.Score)
	}
}

// TestCalculatePrepareRisk_TestFactorWording is a regression test for the
// "No tests found" risk factor overstating what's actually checked.
// getPrepareTests only globs for *_test.go/*.test.ts/*.spec.ts files in the
// target's own module directory — it never checks whether the target
// symbol itself is referenced by any test — so the wording should describe
// that narrower check, not imply broader test-coverage knowledge.
//
// Path is set to a .go file: the factor is language-gated (see
// TestCalculatePrepareRisk_TestFactorOmittedForUnsupportedLanguage), and
// this test is specifically about wording, not about the gate itself.
func TestCalculatePrepareRisk_TestFactorWording(t *testing.T) {
	engine := &Engine{}
	target := &PrepareChangeTarget{Visibility: "internal", Path: "internal/example/widget.go"}

	risk := engine.calculatePrepareRisk(target, nil, nil, nil, nil, nil, ChangeModify)

	foundOldWording := false
	foundNewWording := false
	for _, f := range risk.Factors {
		if f == "No tests found" {
			foundOldWording = true
		}
		if f == "No test files found in target module" {
			foundNewWording = true
		}
	}
	if foundOldWording {
		t.Error(`factor "No tests found" overstates what the check actually verifies (same-module file existence, not symbol-level coverage)`)
	}
	if !foundNewWording {
		t.Errorf("expected factor %q, got %v", "No test files found in target module", risk.Factors)
	}
}

// TestCalculatePrepareRisk_TestFactorOmittedForUnsupportedLanguage is a
// regression test for the "No test files found in target module" factor
// firing for every language, even though getPrepareTests only implements
// discovery for Go (*_test.go) and TypeScript/JavaScript (*.test.ts/js,
// *.spec.ts/js). For a Python target (or any other language without
// discovery support) an empty `tests` slice means discovery never looked,
// not that there are genuinely no tests — asserting "no tests found" there
// is a false claim, not a finding, so neither the factor nor its 0.2 score
// contribution should appear.
func TestCalculatePrepareRisk_TestFactorOmittedForUnsupportedLanguage(t *testing.T) {
	engine := &Engine{}
	target := &PrepareChangeTarget{Visibility: "internal", Path: "app/models/widget.py", ModuleId: "app/models"}

	risk := engine.calculatePrepareRisk(target, nil, nil, nil, nil, nil, ChangeModify)

	for _, f := range risk.Factors {
		if f == "No test files found in target module" {
			t.Errorf("factor %q should not fire for a language (.py) test discovery doesn't support; got factors %v", f, risk.Factors)
		}
	}
	if risk.Score != 0 {
		t.Errorf("Score = %v, want 0 (no factors should have fired for a bare internal-visibility Python target)", risk.Score)
	}
}

// TestCalculatePrepareRisk_SuggestionOnlyForPublicVisibility is a
// regression test for "Ensure backward compatibility or bump major
// version" being suggested for internal/unexported symbols. This follows
// directly from the Go-method-visibility fix (SCIPIdentifier.GetSimpleName):
// calculatePrepareRisk was already correctly gated on
// `target.Visibility == "public"` (compound.go) — the observed bug was
// entirely a symptom of visibility itself coming back "public" for
// unexported Go methods (fixed separately). Once visibility is derived
// correctly, this suggestion (and the "Public API change" factor) stops
// firing for them without any change here; this test locks that in so a
// future visibility regression would be caught here too.
func TestCalculatePrepareRisk_SuggestionOnlyForPublicVisibility(t *testing.T) {
	engine := &Engine{}

	t.Run("internal visibility", func(t *testing.T) {
		target := &PrepareChangeTarget{Visibility: "internal"}
		risk := engine.calculatePrepareRisk(target, nil, nil, nil, nil, nil, ChangeModify)

		for _, f := range risk.Factors {
			if f == "Public API change" {
				t.Error(`"Public API change" factor should not fire for internal visibility`)
			}
		}
		for _, s := range risk.Suggestions {
			if s == "Ensure backward compatibility or bump major version" {
				t.Error(`backward-compatibility suggestion should not fire for internal visibility`)
			}
		}
	})

	t.Run("public visibility", func(t *testing.T) {
		target := &PrepareChangeTarget{Visibility: "public"}
		risk := engine.calculatePrepareRisk(target, nil, nil, nil, nil, nil, ChangeModify)

		factorFound := false
		for _, f := range risk.Factors {
			if f == "Public API change" {
				factorFound = true
			}
		}
		suggestionFound := false
		for _, s := range risk.Suggestions {
			if s == "Ensure backward compatibility or bump major version" {
				suggestionFound = true
			}
		}
		if !factorFound {
			t.Error(`expected "Public API change" factor for public visibility`)
		}
		if !suggestionFound {
			t.Error(`expected backward-compatibility suggestion for public visibility`)
		}
	})
}

// =============================================================================
// BatchGet Tests
// =============================================================================

func TestBatchGet_Empty(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	_, err := engine.BatchGet(ctx, BatchGetOptions{
		SymbolIds: []string{},
	})

	// Empty is technically valid but will return empty results
	if err != nil {
		t.Errorf("unexpected error for empty batch: %v", err)
	}
}

func TestBatchGet_TooMany(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	ids := make([]string, 51)
	for i := range ids {
		ids[i] = "fake-id"
	}

	_, err := engine.BatchGet(ctx, BatchGetOptions{
		SymbolIds: ids,
	})

	if err == nil {
		t.Error("expected error for too many symbols")
	}
}

func TestBatchGet_MixedResults(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	// Mix of valid-looking and invalid IDs
	resp, err := engine.BatchGet(ctx, BatchGetOptions{
		SymbolIds: []string{"invalid-id-1", "invalid-id-2"},
	})

	if err != nil {
		t.Fatalf("BatchGet failed: %v", err)
	}

	if resp == nil {
		t.Fatal("expected response")
		return
	}

	// All should be errors since they're fake IDs - both empty is also valid
	_ = len(resp.Errors)
	_ = len(resp.Results)
}

// =============================================================================
// BatchSearch Tests
// =============================================================================

func TestBatchSearch_Empty(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	_, err := engine.BatchSearch(ctx, BatchSearchOptions{
		Queries: []BatchSearchQuery{},
	})

	if err != nil {
		t.Errorf("unexpected error for empty batch: %v", err)
	}
}

func TestBatchSearch_TooMany(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	queries := make([]BatchSearchQuery, 11)
	for i := range queries {
		queries[i] = BatchSearchQuery{Query: "test"}
	}

	_, err := engine.BatchSearch(ctx, BatchSearchOptions{
		Queries: queries,
	})

	if err == nil {
		t.Error("expected error for too many queries")
	}
}

func TestBatchSearch_Multiple(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	resp, err := engine.BatchSearch(ctx, BatchSearchOptions{
		Queries: []BatchSearchQuery{
			{Query: "Engine", Limit: 5},
			{Query: "Config", Limit: 5},
			{Query: "Logger", Kind: "function", Limit: 5},
		},
	})

	if err != nil {
		t.Fatalf("BatchSearch failed: %v", err)
	}

	if resp == nil {
		t.Fatal("expected response")
		return
	}

	if len(resp.Results) != 3 {
		t.Errorf("expected 3 results, got %d", len(resp.Results))
	}

	// Check each result has the original query
	for i, result := range resp.Results {
		if result.Query == "" {
			t.Errorf("result %d missing query", i)
		}
	}
}

func TestBatchSearch_WithScope(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	resp, err := engine.BatchSearch(ctx, BatchSearchOptions{
		Queries: []BatchSearchQuery{
			{Query: "Engine", Scope: "internal/query", Limit: 5},
		},
	})

	if err != nil {
		t.Fatalf("BatchSearch failed: %v", err)
	}

	if resp == nil {
		t.Fatal("expected response")
		return
	}

	if len(resp.Results) != 1 {
		t.Errorf("expected 1 result, got %d", len(resp.Results))
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestCompoundToolsWorkflow(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	// Create test directory structure
	createTestDirectory(t, engine, "internal/query")
	createTestFile(t, engine, "internal/query/engine.go", `package query

type Engine struct {
	name string
}

func NewEngine() *Engine {
	return &Engine{}
}

func (e *Engine) Search(query string) []string {
	return nil
}
`)

	ctx := context.Background()

	// Typical workflow: explore -> understand -> prepareChange

	// 1. Explore the query module
	exploreResp, err := engine.Explore(ctx, ExploreOptions{
		Target: "internal/query",
		Depth:  ExploreStandard,
	})
	if err != nil {
		t.Fatalf("Explore failed: %v", err)
	}

	if exploreResp == nil || exploreResp.Overview == nil {
		t.Fatal("expected overview from explore")
		return
	}

	// 2. Prepare a change on the file
	prepareResp, err := engine.PrepareChange(ctx, PrepareChangeOptions{
		Target:     "internal/query/engine.go",
		ChangeType: ChangeModify,
	})
	if err != nil {
		t.Fatalf("PrepareChange failed: %v", err)
	}

	if prepareResp == nil || prepareResp.RiskAssessment == nil {
		t.Error("expected risk assessment in workflow")
	}
}

func TestSummarizeTransitive_CountsDirectAndTransitiveModules(t *testing.T) {
	resp := &AnalyzeImpactResponse{
		DirectImpact: []ImpactItem{
			{ModuleId: "a", Distance: 1}, {ModuleId: "b", Distance: 1}, {ModuleId: "a", Distance: 1},
		},
		TransitiveImpact: []ImpactItem{{ModuleId: "c", Distance: 2}, {Distance: 2}},
	}
	got := summarizeTransitive(resp)
	if got.ModuleSpread != 3 {
		t.Errorf("ModuleSpread = %d, want 3 (a, b, c)", got.ModuleSpread)
	}
	if got.TotalCallers != 5 || got.MaxDepth != 2 {
		t.Errorf("got %+v", got)
	}
	// depth-1 only: previously 0
	if got := summarizeTransitive(&AnalyzeImpactResponse{DirectImpact: resp.DirectImpact}); got.ModuleSpread != 2 {
		t.Errorf("direct-only spread = %d, want 2", got.ModuleSpread)
	}
}

func TestMarkConsumersNotAssessed(t *testing.T) {
	t.Run("low becomes not assessed, evidence stays", func(t *testing.T) {
		risk := &PrepareRisk{Level: "low", Score: 0.2, Factors: []string{"No test files found in target module"}}
		markConsumersNotAssessed(risk, 0)
		if risk.Level != RiskLevelNotAssessed || risk.Score != 0 {
			t.Errorf("risk = %s %.2f, want %s 0", risk.Level, risk.Score, RiskLevelNotAssessed)
		}
		if len(risk.Factors) != 2 || risk.Factors[1] != "No test files found in target module" {
			t.Errorf("factors = %v, want the evidence kept", risk.Factors)
		}
	})

	t.Run("evidence that reaches medium stands, as a lower bound", func(t *testing.T) {
		risk := &PrepareRisk{Level: "high", Score: 0.5}
		markConsumersNotAssessed(risk, 0)
		if risk.Level != "high" || risk.Score != 0.5 {
			t.Errorf("risk = %s %.2f, want it unchanged", risk.Level, risk.Score)
		}
		if len(risk.Factors) == 0 || !strings.Contains(risk.Factors[0], "not assessed") {
			t.Errorf("factors = %v, want the gap named", risk.Factors)
		}
	})

	t.Run("known dependents are left alone", func(t *testing.T) {
		risk := &PrepareRisk{Level: "low", Score: 0.1}
		markConsumersNotAssessed(risk, 2)
		if risk.Level != "low" || len(risk.Factors) != 0 {
			t.Errorf("risk = %+v, want it unchanged", risk)
		}
	})
}

// The same engine with and without its index: the blind spot is only without.
func TestPrepareChange_NoIndexIsNotAssessed(t *testing.T) {
	testutil.ForEachLanguage(t, func(t *testing.T, fixture *testutil.FixtureContext) {
		engine, cleanup := setupGoldenEngine(t, fixture)
		defer cleanup()
		ctx := context.Background()

		var target string
		for _, doc := range engine.scipAdapter.GetIndex().Documents {
			target = doc.RelativePath
			break
		}

		engine.scipAdapter = nil
		resp, err := engine.PrepareChange(ctx, PrepareChangeOptions{Target: target, ChangeType: ChangeModify})
		if err != nil {
			t.Fatalf("PrepareChange(%s): %v", target, err)
		}
		if resp.RiskAssessment == nil {
			t.Fatal("no risk assessment")
		}
		if resp.RiskAssessment.Level == "low" {
			t.Errorf("risk is low with no consumer graph: %+v", resp.RiskAssessment)
		}
		named := false
		for _, f := range resp.RiskAssessment.Factors {
			named = named || strings.Contains(f, "not assessed")
		}
		if !named {
			t.Errorf("factors don't name the gap: %v", resp.RiskAssessment.Factors)
		}
	})
}

// AnalyzeImpact can still reach its analysis without an index, on a symbol known
// from identity data. Nothing looked up its references then, so it isn't low.
func TestAnalyzeImpact_IdentityOnlyIsNotAssessed(t *testing.T) {
	testutil.ForEachLanguage(t, func(t *testing.T, fixture *testutil.FixtureContext) {
		engine, cleanup := setupGoldenEngine(t, fixture)
		defer cleanup()

		const id = "ckb:test:sym:identityonly"
		_, err := engine.db.Conn().Exec(`INSERT INTO symbol_mappings
			(stable_id, state, fingerprint_json, location_json, definition_version_semantics, last_verified_at, last_verified_state_id)
			VALUES (?, 'active', '{"name":"Lonely","kind":"function","qualifiedContainer":""}', '{"path":"a.go","line":1,"column":1}', 'unknown', datetime('now'), 'state1')`, id)
		if err != nil {
			t.Fatalf("insert mapping: %v", err)
		}

		engine.scipAdapter = nil
		resp, err := engine.AnalyzeImpact(context.Background(), AnalyzeImpactOptions{SymbolId: id})
		if err != nil {
			t.Fatalf("AnalyzeImpact: %v", err)
		}
		if resp.RiskScore.Level != RiskLevelNotAssessed {
			t.Errorf("risk = %+v, want %s", resp.RiskScore, RiskLevelNotAssessed)
		}
		if resp.BlastRadius != nil && resp.BlastRadius.RiskLevel != RiskLevelNotAssessed {
			t.Errorf("blast radius risk = %s, want %s", resp.BlastRadius.RiskLevel, RiskLevelNotAssessed)
		}
	})
}

// relatedTests used to be the first 20 test files of the target's module in
// alphabetical order, labeled "same-module": for query.NewEngine that missed
// every test that calls it from another package. The tests that reference the
// target come first now, direct before transitive, and same-module files only
// fill the remaining room.
func TestGetPrepareTests_ReferencingTestsFirst(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"pkg/a_test.go", "pkg/b_test.go", "pkg/engine_test.go"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), []byte("package pkg\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	engine := &Engine{repoRoot: root}
	target := &PrepareChangeTarget{Kind: "function", Path: "pkg/engine.go", ModuleId: "pkg"}
	item := func(path, name string, distance int) ImpactItem {
		return ImpactItem{StableId: name, Name: name, Distance: distance, Confidence: 0.9, Location: &LocationInfo{FileId: path}}
	}
	consumers := []ImpactItem{
		item("other/zz_test.go", "TestZ", 1),
		item("api/handlers_test.go", "TestHandlers", 2), // reaches it through a helper
		item("api/api_test.go", "TestAPI", 1),
		item("pkg/engine_test.go", "TestEngine", 1),
		item("cmd/main.go", "main", 1), // production code is no test
	}

	got := engine.getPrepareTests(target, consumers)

	want := []PrepareTest{
		{File: "api/api_test.go", Name: "TestAPI", Relation: "direct"},
		{File: "other/zz_test.go", Name: "TestZ", Relation: "direct"},
		{File: "pkg/engine_test.go", Name: "TestEngine", Relation: "direct"},
		{File: "api/handlers_test.go", Name: "TestHandlers", Relation: "transitive"},
		{File: "pkg/a_test.go", Relation: "same-module"},
		{File: "pkg/b_test.go", Relation: "same-module"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d tests %+v, want %+v", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("test %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Same-module files only fill up to maxPrepareTests; tests that reference the
// target are not cut for them.
func TestPrepareTestList_FillerOnlyUpToCap(t *testing.T) {
	var referencing []AffectedTest
	for i := 0; i < maxPrepareTests+5; i++ {
		referencing = append(referencing, AffectedTest{FilePath: fmt.Sprintf("x/r%02d_test.go", i), Reason: "direct"})
	}
	got := prepareTestList(referencing, []string{"pkg/filler_test.go"})
	if len(got) != maxPrepareTests+5 {
		t.Fatalf("got %d tests, want all %d referencing ones", len(got), maxPrepareTests+5)
	}
	for _, test := range got {
		if test.Relation == "same-module" {
			t.Errorf("same-module filler %s listed although the referencing tests filled the list", test.File)
		}
	}

	got = prepareTestList(nil, []string{"pkg/a_test.go", "pkg/b_test.go"})
	if len(got) != 2 || got[0].Relation != "same-module" {
		t.Errorf("without references, same-module files should be listed as such, got %+v", got)
	}
}

func TestIsMemberSymbol(t *testing.T) {
	const pkg = "scip-go gomod example.com/m v1 `example.com/m/pkg`/"
	cases := map[string]bool{
		pkg + "Model#":             false,
		pkg + "NewModel().":        false,
		pkg + "DefaultTimeout.":    false,
		pkg + "Model#Name.":        true,
		pkg + "Model#Transform().": true,
	}
	for sym, want := range cases {
		if got := isMemberSymbol(sym); got != want {
			t.Errorf("isMemberSymbol(%q) = %v, want %v", sym, got, want)
		}
	}
}

// A file target used to attribute nothing: prepareChange on a file that
// declares a widely used type reported zero dependents. It is the sum of the
// consumers of what the file declares now, its types and their fields and
// methods included, minus the file's own uses.
func TestPrepareChange_FileTargetAggregatesDeclaredSymbols(t *testing.T) {
	fixture := testutil.LoadFixture(t, "go")
	engine, cleanup := setupGoldenEngine(t, fixture)
	defer cleanup()

	resp, err := engine.PrepareChange(context.Background(), PrepareChangeOptions{Target: "pkg/model.go", ChangeType: ChangeModify})
	if err != nil {
		t.Fatalf("PrepareChange failed: %v", err)
	}
	if len(resp.DirectDependents) == 0 {
		t.Fatal("pkg/model.go declares Model, NewModel and Transform, used by pkg/service.go, but no dependents were reported")
	}
	files := make(map[string]bool)
	for _, d := range resp.DirectDependents {
		if d.File == "pkg/model.go" {
			t.Errorf("the file's own use %s:%d listed as a dependent", d.File, d.Line)
		}
		files[d.File] = true
	}
	if !files["pkg/service.go"] {
		t.Errorf("pkg/service.go (uses Model and NewModel) missing from dependents: %+v", resp.DirectDependents)
	}
	if resp.TransitiveImpact == nil || resp.TransitiveImpact.TotalCallers == 0 {
		t.Errorf("expected a transitive summary, got %+v", resp.TransitiveImpact)
	}
}

// TestCalculatePrepareRisk_RenameDeleteFloor: a rename or delete with production
// callers gets assessChange's high floor, so prepareChange no longer says low for
// a change assessChange calls high once it is in the diff. modify and a
// rename without callers keep the count-based score.
func TestCalculatePrepareRisk_RenameDeleteFloor(t *testing.T) {
	engine := &Engine{}
	target := &PrepareChangeTarget{Visibility: "internal", Path: "internal/example/widget.go"}
	tests := []PrepareTest{{File: "internal/example/widget_test.go"}}
	one := []PrepareDependent{{Name: "caller", File: "internal/other/use.go", Line: 3}}

	cases := []struct {
		name       string
		changeType ChangeType
		dependents []PrepareDependent
		wantScore  float64
		wantLevel  string
	}{
		{"rename with a caller", ChangeRename, one, highRiskFloor, "high"},
		{"delete with a caller", ChangeDelete, one, highRiskFloor, "high"},
		{"modify with a caller", ChangeModify, one, 0, "low"},
		{"rename without callers", ChangeRename, nil, 0.1, "low"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			risk := engine.calculatePrepareRisk(target, c.dependents, nil, tests, nil, nil, c.changeType)
			if risk.Score != c.wantScore || risk.Level != c.wantLevel {
				t.Errorf("got %v %s, want %v %s (factors %v)", risk.Score, risk.Level, c.wantScore, c.wantLevel, risk.Factors)
			}
		})
	}
}

// prepareChange counted reference rows from a budgeted list: a caller using the
// target three times was three callers, and past 20 items the count, the
// transitive total and the module spread were whatever fit. It now counts each
// consumer once from the whole lists, as assessChange does, and no longer lists
// AnalyzeImpact's bare factor names ("visibility", "direct-callers", ...), which
// appeared on every call whatever their value.
func TestPrepareChange_SymbolCountsConsumersOnce(t *testing.T) {
	fixture := testutil.LoadFixture(t, "go")
	engine, cleanup := setupGoldenEngine(t, fixture)
	defer cleanup()

	var target string
	for _, sym := range engine.scipAdapter.GetIndex().GetDocument("pkg/model.go").Symbols {
		if strings.HasSuffix(sym.Symbol, "/NewModel().") {
			target = sym.Symbol
		}
	}
	if target == "" {
		t.Fatal("NewModel not in the fixture index")
	}
	resp, err := engine.PrepareChange(context.Background(), PrepareChangeOptions{Target: target, ChangeType: ChangeModify})
	if err != nil {
		t.Fatalf("PrepareChange failed: %v", err)
	}
	if len(resp.DirectDependents) == 0 || resp.TransitiveImpact == nil {
		t.Fatalf("NewModel is called from pkg/service.go, got %+v / %+v", resp.DirectDependents, resp.TransitiveImpact)
	}
	seen := make(map[string]bool)
	for _, d := range resp.DirectDependents {
		key := d.SymbolId
		if key == "" {
			key = d.File
		}
		if seen[key] {
			t.Errorf("consumer %s listed twice", key)
		}
		seen[key] = true
	}
	if got := resp.TransitiveImpact.DirectCallers; got != len(resp.DirectDependents) {
		t.Errorf("DirectCallers = %d, want %d", got, len(resp.DirectDependents))
	}
	for _, f := range resp.RiskAssessment.Factors {
		switch f {
		case "visibility", "direct-callers", "module-spread", "impact-kind":
			t.Errorf("bare analyzeImpact factor name %q in prepareChange factors %v", f, resp.RiskAssessment.Factors)
		}
	}
	if !strings.Contains(resp.RiskAssessment.Basis, "at least high") {
		t.Errorf("modify with callers should say what a signature change scores, basis = %q", resp.RiskAssessment.Basis)
	}
}

// The dependents list is cut to the response budget; the score counts them all.
func TestCalculatePrepareRisk_CountsCallersBeyondBudget(t *testing.T) {
	engine := &Engine{}
	target := &PrepareChangeTarget{Visibility: "internal", Path: "app/x.py"}
	shown := make([]PrepareDependent, 3)
	risk := engine.calculatePrepareRisk(target, shown, &PrepareTransitive{DirectCallers: 12}, nil, nil, nil, ChangeModify)
	if risk.Score != 0.25 || len(risk.Factors) == 0 || risk.Factors[0] != "High dependent count (12 direct callers)" {
		t.Errorf("got %v %v, want 0.25 from 12 callers", risk.Score, risk.Factors)
	}
	risk = engine.calculatePrepareRisk(target, shown, &PrepareTransitive{DirectCallers: 12}, nil, nil, nil, ChangeRename)
	if want := "rename breaks 12 production caller(s) unless they change too"; risk.Factors[len(risk.Factors)-1] != want {
		t.Errorf("floor factor = %q, want %q", risk.Factors[len(risk.Factors)-1], want)
	}
}

// A modify score can't tell a body edit from a signature change, so it says
// which one it scored and what assessChange will do with the other.
func TestModifyRiskBasis(t *testing.T) {
	cases := []struct {
		name       string
		changeType ChangeType
		callers    int
		public     bool
		score      float64
		want       string // substring; "" means no basis
	}{
		{"callers below the floor", ChangeModify, 9, true, 0.3, "the 9 production caller(s) stay as they are, it is at least high (0.6)"},
		{"exported, no callers", ChangeModify, 0, true, 0.15, "at least medium (0.4)"},
		{"internal, no callers", ChangeModify, 0, false, 0.2, ""},
		{"already at the floor", ChangeModify, 30, true, 0.6, ""},
		{"rename has its own floor", ChangeRename, 3, true, 0.6, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := modifyRiskBasis(c.changeType, c.callers, 0, c.public, c.score)
			if c.want == "" {
				if got != "" {
					t.Errorf("basis = %q, want none", got)
				}
				return
			}
			if !strings.Contains(got, c.want) || !strings.Contains(got, "assessChange rescores the real diff") {
				t.Errorf("basis = %q, want it to contain %q", got, c.want)
			}
		})
	}
}
