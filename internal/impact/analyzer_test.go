package impact

import (
	"testing"
)

func TestNewImpactAnalyzer(t *testing.T) {
	tests := []struct {
		name     string
		maxDepth int
		expected int
	}{
		{"default depth", 0, 2},
		{"custom depth", 5, 5},
		{"negative depth", -1, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := NewImpactAnalyzer(tt.maxDepth)
			if analyzer.maxDepth != tt.expected {
				t.Errorf("expected maxDepth %d, got %d", tt.expected, analyzer.maxDepth)
			}
		})
	}
}

func TestAnalyze(t *testing.T) {
	analyzer := NewImpactAnalyzer(2)

	// Create a test symbol
	symbol := &Symbol{
		StableId:            "test.Symbol.myFunction",
		Name:                "myFunction",
		Kind:                KindFunction,
		Signature:           "func myFunction() string",
		SignatureNormalized: "func myFunction() string",
		ModuleId:            "module1",
		ModuleName:          "TestModule",
		ContainerName:       "Symbol",
		Location: &Location{
			FileId:      "test.go",
			StartLine:   10,
			StartColumn: 1,
			EndLine:     15,
			EndColumn:   2,
		},
		Modifiers: []string{"public"},
	}

	// Create test references
	refs := []Reference{
		{
			Location: &Location{
				FileId:      "caller1.go",
				StartLine:   20,
				StartColumn: 5,
				EndLine:     20,
				EndColumn:   15,
			},
			Kind:       RefCall,
			FromSymbol: "test.Caller1.callFunction",
			FromModule: "module2",
			IsTest:     false,
		},
		{
			Location: &Location{
				FileId:      "caller2.go",
				StartLine:   30,
				StartColumn: 10,
				EndLine:     30,
				EndColumn:   20,
			},
			Kind:       RefCall,
			FromSymbol: "test.Caller2.anotherCall",
			FromModule: "module2",
			IsTest:     false,
		},
		{
			Location: &Location{
				FileId:      "test_file.go",
				StartLine:   50,
				StartColumn: 8,
				EndLine:     50,
				EndColumn:   18,
			},
			Kind:       RefCall,
			FromSymbol: "test.TestMyFunction",
			FromModule: "module1",
			IsTest:     true,
		},
	}

	// Perform analysis
	result, err := analyzer.Analyze(symbol, refs)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	// Verify results
	if result.Symbol != symbol {
		t.Error("Symbol not set correctly")
	}

	if result.Visibility == nil {
		t.Error("Visibility not derived")
	}

	if result.RiskScore == nil {
		t.Error("Risk score not calculated")
	}

	if len(result.DirectImpact) != 3 {
		t.Errorf("expected 3 direct impacts, got %d", len(result.DirectImpact))
	}

	if result.AnalysisLimits == nil {
		t.Error("Analysis limits not set")
	}
}

func TestAnalyzeWithOptions(t *testing.T) {
	analyzer := NewImpactAnalyzer(2)

	symbol := &Symbol{
		StableId:   "test.Symbol.myFunction",
		Name:       "myFunction",
		Kind:       KindFunction,
		ModuleId:   "module1",
		ModuleName: "TestModule",
		Modifiers:  []string{"public"},
	}

	refs := []Reference{
		{
			Location:   &Location{FileId: "file1.go", StartLine: 10, StartColumn: 1, EndLine: 10, EndColumn: 10},
			Kind:       RefCall,
			FromSymbol: "caller1",
			FromModule: "module2",
			IsTest:     false,
		},
		{
			Location:   &Location{FileId: "test_file.go", StartLine: 20, StartColumn: 1, EndLine: 20, EndColumn: 10},
			Kind:       RefCall,
			FromSymbol: "testFunc",
			FromModule: "module1",
			IsTest:     true,
		},
	}

	// Test excluding tests
	opts := AnalyzeOptions{
		IncludeTests: false,
	}

	result, err := analyzer.AnalyzeWithOptions(symbol, refs, opts)
	if err != nil {
		t.Fatalf("AnalyzeWithOptions failed: %v", err)
	}

	// Should only have 1 direct impact (non-test)
	if len(result.DirectImpact) != 1 {
		t.Errorf("expected 1 direct impact (excluding tests), got %d", len(result.DirectImpact))
	}

	// Test including tests
	opts.IncludeTests = true
	result, err = analyzer.AnalyzeWithOptions(symbol, refs, opts)
	if err != nil {
		t.Fatalf("AnalyzeWithOptions failed: %v", err)
	}

	// Should have 2 direct impacts
	if len(result.DirectImpact) != 2 {
		t.Errorf("expected 2 direct impacts (including tests), got %d", len(result.DirectImpact))
	}
}

func TestAnalyzeNilSymbol(t *testing.T) {
	analyzer := NewImpactAnalyzer(2)
	_, err := analyzer.Analyze(nil, []Reference{})
	if err == nil {
		t.Error("expected error for nil symbol, got nil")
	}
}

func TestGenerateModuleSummaries(t *testing.T) {
	analyzer := NewImpactAnalyzer(2)

	impacts := []ImpactItem{
		{
			ModuleId:   "module1",
			ModuleName: "Module1",
			Kind:       DirectCaller,
		},
		{
			ModuleId:   "module1",
			ModuleName: "Module1",
			Kind:       DirectCaller,
		},
		{
			ModuleId:   "module2",
			ModuleName: "Module2",
			Kind:       TypeDependency,
		},
	}

	summaries := analyzer.generateModuleSummaries(impacts)

	if len(summaries) != 2 {
		t.Errorf("expected 2 module summaries, got %d", len(summaries))
	}

	// Find module1 summary
	var module1Summary *ModuleSummary
	for i := range summaries {
		if summaries[i].ModuleId == "module1" {
			module1Summary = &summaries[i]
			break
		}
	}

	if module1Summary == nil {
		t.Fatal("module1 summary not found")
	}

	if module1Summary.ImpactCount != 2 {
		t.Errorf("expected module1 impact count 2, got %d", module1Summary.ImpactCount)
	}
}

// TestProcessDirectReferences_UsesResolvedFromName is a regression test for
// a bug where ckb impact prepare's directDependents entries all showed
// symbolId: "" and name: "unknown", with only file/line set. The backend
// now resolves the enclosing (caller) symbol via Reference.FromSymbol and
// Reference.FromName (see internal/backends/scip's convertToReference);
// this verifies the analyzer prefers a resolved FromName over echoing the
// raw stable ID.
func TestProcessDirectReferences_UsesResolvedFromName(t *testing.T) {
	analyzer := NewImpactAnalyzer(2)
	symbol := &Symbol{StableId: "sym.Target", Name: "Target", Kind: KindFunction}

	refs := []Reference{
		{
			Location:   &Location{FileId: "caller.go", StartLine: 5},
			Kind:       RefCall,
			FromSymbol: "scip-go gomod mod v0 `pkg`/Engine#buildProvenance().",
			FromName:   "buildProvenance",
			FromModule: "internal/query",
		},
	}

	items := analyzer.processDirectReferences(symbol, refs, nil)
	if len(items) != 1 {
		t.Fatalf("expected 1 impact item, got %d", len(items))
	}

	item := items[0]
	if item.StableId != refs[0].FromSymbol {
		t.Errorf("StableId = %q, want %q", item.StableId, refs[0].FromSymbol)
	}
	if item.Name != "buildProvenance" {
		t.Errorf("Name = %q, want %q (resolved FromName should win over the raw stable ID)", item.Name, "buildProvenance")
	}
}

// TestProcessDirectReferences_OmitsUnresolvedEnclosingSymbol verifies that
// when the backend genuinely can't resolve an enclosing symbol, the
// analyzer reports empty StableId/Name rather than fabricating "unknown" —
// callers (JSON encoders using `omitempty`) are expected to drop the
// field, not display a placeholder.
func TestProcessDirectReferences_OmitsUnresolvedEnclosingSymbol(t *testing.T) {
	analyzer := NewImpactAnalyzer(2)
	symbol := &Symbol{StableId: "sym.Target", Name: "Target", Kind: KindFunction}

	refs := []Reference{
		{
			Location: &Location{FileId: "pkg-level.go", StartLine: 1},
			Kind:     RefCall,
			// FromSymbol/FromName intentionally left empty: no enclosing
			// function could be resolved for this reference.
		},
	}

	items := analyzer.processDirectReferences(symbol, refs, nil)
	if len(items) != 1 {
		t.Fatalf("expected 1 impact item, got %d", len(items))
	}

	item := items[0]
	if item.StableId != "" {
		t.Errorf("StableId = %q, want empty", item.StableId)
	}
	if item.Name != "" {
		t.Errorf("Name = %q, want empty (not \"unknown\")", item.Name)
	}
}
