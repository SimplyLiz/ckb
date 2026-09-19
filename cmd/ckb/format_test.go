package main

import (
	"strings"
	"testing"
)

func TestFormatResponse_JSON(t *testing.T) {
	resp := map[string]interface{}{
		"key": "value",
		"num": 42,
	}

	result, err := FormatResponse(resp, FormatJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, `"key": "value"`) {
		t.Error("JSON output missing expected key")
	}
	if !strings.Contains(result, `"num": 42`) {
		t.Error("JSON output missing expected number")
	}
}

func TestFormatResponse_UnsupportedFormat(t *testing.T) {
	resp := map[string]string{"key": "value"}

	_, err := FormatResponse(resp, "xml")
	if err == nil {
		t.Error("expected error for unsupported format")
	}
	if !strings.Contains(err.Error(), "unsupported format") {
		t.Errorf("error should mention unsupported format, got: %v", err)
	}
}

func TestFormatJSON(t *testing.T) {
	resp := struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}{
		Name:  "test",
		Value: 123,
	}

	result, err := formatJSON(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, `"name": "test"`) {
		t.Error("missing name field")
	}
	if !strings.Contains(result, `"value": 123`) {
		t.Error("missing value field")
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048576, "1.0 MiB"},
		{1073741824, "1.0 GiB"},
		{1099511627776, "1.0 TiB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatBytes(tt.bytes)
			if result != tt.expected {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, result, tt.expected)
			}
		})
	}
}

func TestFormatHuman_UnknownType(t *testing.T) {
	// For unknown types, should fall back to JSON with a note
	resp := struct {
		Foo string `json:"foo"`
	}{Foo: "bar"}

	result, err := formatHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Human format not available") {
		t.Error("missing fallback message")
	}
	if !strings.Contains(result, `"foo": "bar"`) {
		t.Error("missing JSON content")
	}
}

func TestFormatStatusHuman(t *testing.T) {
	resp := &StatusResponseCLI{
		CkbVersion: "7.5.0",
		Healthy:    true,
		Backends: []BackendStatusCLI{
			{ID: "scip", Available: true, Details: "100 symbols"},
		},
		Cache: CacheStatusCLI{
			QueryCount: 50,
			HitRate:    0.85,
			SizeBytes:  2048,
		},
	}

	result, err := formatStatusHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "CKB v7.5.0") {
		t.Error("missing version")
	}
	if !strings.Contains(result, "✓ System: Healthy") {
		t.Error("missing health status")
	}
	if !strings.Contains(result, "✓ scip") {
		t.Error("missing backend")
	}
	if !strings.Contains(result, "50 queries") {
		t.Error("missing cache stats")
	}
}

func TestFormatStatusHuman_Unhealthy(t *testing.T) {
	resp := &StatusResponseCLI{
		CkbVersion: "7.5.0",
		Healthy:    false,
		Backends: []BackendStatusCLI{
			{ID: "scip", Available: false},
		},
	}

	result, err := formatStatusHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "✗ System: Issues detected") {
		t.Error("missing unhealthy status")
	}
	if !strings.Contains(result, "✗ scip") {
		t.Error("missing unavailable backend")
	}
}

func TestFormatDoctorHuman(t *testing.T) {
	resp := &DoctorResponseCLI{
		Healthy: true,
		Checks: []DoctorCheckCLI{
			{Name: "scip-index", Status: "pass", Message: "Index found"},
			{Name: "git-repo", Status: "warn", Message: "Dirty", SuggestedFixes: []FixActionCLI{{Command: "git stash"}}},
			{Name: "config", Status: "fail", Message: "Missing config"},
		},
	}

	result, err := formatDoctorHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "CKB Doctor") {
		t.Error("missing header")
	}
	if !strings.Contains(result, "✓ scip-index") {
		t.Error("missing pass check")
	}
	if !strings.Contains(result, "⚠ git-repo") {
		t.Error("missing warn check")
	}
	if !strings.Contains(result, "✗ config") {
		t.Error("missing fail check")
	}
	if !strings.Contains(result, "git stash") {
		t.Error("missing suggested fix")
	}
	if !strings.Contains(result, "All checks passed") {
		t.Error("missing healthy message")
	}
}

func TestFormatSearchHuman(t *testing.T) {
	resp := &SearchResponseCLI{
		Query:        "Engine",
		TotalMatches: 2,
		Symbols: []SearchSymbolCLI{
			{
				Name:           "Engine",
				Kind:           "struct",
				StableID:       "sym1",
				ModuleID:       "query",
				RelevanceScore: 0.95,
				Location:       &LocationCLI{FileID: "engine.go", StartLine: 10},
			},
		},
	}

	result, err := formatSearchHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Search Results for: Engine") {
		t.Error("missing query")
	}
	if !strings.Contains(result, "Found 2 matches") {
		t.Error("missing match count")
	}
	if !strings.Contains(result, "Engine (struct)") {
		t.Error("missing symbol")
	}
}

func TestFormatImpactHuman(t *testing.T) {
	resp := &ImpactResponseCLI{
		SymbolID: "sym123",
		RiskScore: &RiskScoreCLI{
			Level:       "medium",
			Score:       0.5,
			Explanation: "Moderate impact",
		},
		DirectImpact: []ImpactItemCLI{
			{Name: "Caller1", Kind: "direct-caller"},
			{Name: "Caller2", Kind: "direct-caller"},
		},
		TransitiveImpact: []ImpactItemCLI{
			{Name: "Trans1", Kind: "transitive-caller"},
		},
		ModulesAffected: []ModuleImpactCLI{
			{ModuleID: "mod1", ImpactCount: 3},
		},
	}

	result, err := formatImpactHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Impact Analysis: sym123") {
		t.Error("missing header")
	}
	if !strings.Contains(result, "Risk Level: medium") {
		t.Error("missing risk level")
	}
	if !strings.Contains(result, "Direct Impact: 2 symbols") {
		t.Error("missing direct impact count")
	}
	if !strings.Contains(result, "Transitive Impact: 1 symbols") {
		t.Error("missing transitive impact count")
	}
	if !strings.Contains(result, "mod1: 3 symbols") {
		t.Error("missing module impact")
	}
}

func TestMin(t *testing.T) {
	tests := []struct {
		a, b, want int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{5, 5, 5},
		{0, 10, 0},
		{-5, 5, -5},
	}

	for _, tt := range tests {
		result := min(tt.a, tt.b)
		if result != tt.want {
			t.Errorf("min(%d, %d) = %d, want %d", tt.a, tt.b, result, tt.want)
		}
	}
}

func TestFormatSymbolHuman(t *testing.T) {
	resp := &SymbolResponseCLI{
		Symbol: SymbolInfoCLI{
			StableID:             "sym123",
			Name:                 "Engine",
			Kind:                 "struct",
			Visibility:           "public",
			VisibilityConfidence: 0.95,
			ContainerName:        "query",
		},
		Location: &LocationCLI{
			FileID:      "engine.go",
			StartLine:   10,
			StartColumn: 5,
		},
		Module: &ModuleInfoCLI{
			ModuleID: "internal/query",
		},
	}

	result, err := formatSymbolHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Symbol Details") {
		t.Error("missing header")
	}
	if !strings.Contains(result, "Name: Engine") {
		t.Error("missing name")
	}
	if !strings.Contains(result, "Kind: struct") {
		t.Error("missing kind")
	}
	if !strings.Contains(result, "Container: query") {
		t.Error("missing container")
	}
	if !strings.Contains(result, "engine.go:10:5") {
		t.Error("missing location")
	}
	if !strings.Contains(result, "Module: internal/query") {
		t.Error("missing module")
	}
}

func TestFormatSymbolHuman_MinimalFields(t *testing.T) {
	resp := &SymbolResponseCLI{
		Symbol: SymbolInfoCLI{
			StableID:   "sym456",
			Name:       "Foo",
			Kind:       "function",
			Visibility: "private",
		},
	}

	result, err := formatSymbolHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Name: Foo") {
		t.Error("missing name")
	}
	// Should not contain optional fields
	if strings.Contains(result, "Container:") {
		t.Error("should not have container when empty")
	}
	if strings.Contains(result, "Location:") {
		t.Error("should not have location when nil")
	}
}

func TestFormatRefsHuman(t *testing.T) {
	resp := &ReferencesResponseCLI{
		SymbolID:        "sym123",
		TotalReferences: 3,
		ByModule: []ModuleReferencesCLI{
			{ModuleID: "module1", Count: 2},
			{ModuleID: "module2", Count: 1},
		},
		References: []ReferenceCLI{
			{Location: &LocationCLI{FileID: "foo.go", StartLine: 10}, Kind: "call", IsTest: false},
			{Location: &LocationCLI{FileID: "bar.go", StartLine: 20}, Kind: "call", IsTest: true},
			{Location: &LocationCLI{FileID: "baz.go", StartLine: 30}, Kind: "import", IsTest: false},
		},
	}

	result, err := formatRefsHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "References to: sym123") {
		t.Error("missing header")
	}
	if !strings.Contains(result, "Total references: 3") {
		t.Error("missing total count")
	}
	if !strings.Contains(result, "module1: 2 references") {
		t.Error("missing module1 count")
	}
	if !strings.Contains(result, "foo.go:10 (call)") {
		t.Error("missing first reference")
	}
	if !strings.Contains(result, "[test]") {
		t.Error("missing test marker")
	}
}

func TestFormatRefsHuman_EmptyRefs(t *testing.T) {
	resp := &ReferencesResponseCLI{
		SymbolID:        "sym456",
		TotalReferences: 0,
		References:      []ReferenceCLI{},
	}

	result, err := formatRefsHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Total references: 0") {
		t.Error("missing zero count")
	}
}

func TestFormatArchHuman(t *testing.T) {
	resp := &ArchitectureResponseCLI{
		Modules: []ModuleSummaryCLI{
			{
				ModuleID:      "mod1",
				Name:          "Query Engine",
				RootPath:      "internal/query",
				Language:      "go",
				FileCount:     10,
				SymbolCount:   50,
				IncomingEdges: 5,
				OutgoingEdges: 3,
			},
		},
		DependencyGraph: []DependencyEdgeCLI{
			{From: "mod1", To: "mod2", Kind: "import"},
		},
		Entrypoints: []EntrypointCLI{
			{Name: "main", Kind: "binary", FileID: "cmd/ckb/main.go"},
		},
	}

	result, err := formatArchHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Architecture Overview") {
		t.Error("missing header")
	}
	if !strings.Contains(result, "Modules: 1") {
		t.Error("missing module count")
	}
	if !strings.Contains(result, "Dependencies: 1") {
		t.Error("missing dependency count")
	}
	if !strings.Contains(result, "Entrypoints: 1") {
		t.Error("missing entrypoint count")
	}
	if !strings.Contains(result, "Query Engine (go)") {
		t.Error("missing module name with language")
	}
	if !strings.Contains(result, "Path: internal/query") {
		t.Error("missing module path")
	}
	if !strings.Contains(result, "Files: 10, Symbols: 50") {
		t.Error("missing file/symbol counts")
	}
	if !strings.Contains(result, "Deps: 5 incoming, 3 outgoing") {
		t.Error("missing edge counts")
	}
	if !strings.Contains(result, "main (binary)") {
		t.Error("missing entrypoint")
	}
}

func TestFormatArchHuman_Empty(t *testing.T) {
	resp := &ArchitectureResponseCLI{
		Modules:         []ModuleSummaryCLI{},
		DependencyGraph: []DependencyEdgeCLI{},
		Entrypoints:     []EntrypointCLI{},
	}

	result, err := formatArchHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Modules: 0") {
		t.Error("missing zero module count")
	}
}

func TestFormatCallgraphHuman(t *testing.T) {
	resp := &CallgraphResponseCLI{
		Root: "Engine.Search",
		Nodes: []CallgraphNodeCLI{
			{ID: "1", Name: "Caller1", Depth: -1, Role: "caller"},
			{ID: "2", Name: "Caller2", Depth: -2, Role: "caller"},
			{ID: "3", Name: "Callee1", Depth: 1, Role: "callee"},
		},
		Edges: []CallgraphEdgeCLI{
			{From: "1", To: "root"},
			{From: "root", To: "3"},
		},
	}

	result, err := formatCallgraphHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Call Graph for: Engine.Search") {
		t.Error("missing header")
	}
	if !strings.Contains(result, "Nodes: 3, Edges: 2") {
		t.Error("missing counts")
	}
	if !strings.Contains(result, "Callers (who calls this)") {
		t.Error("missing callers section")
	}
	if !strings.Contains(result, "Callees (what this calls)") {
		t.Error("missing callees section")
	}
	if !strings.Contains(result, "Caller1 (caller)") {
		t.Error("missing caller node")
	}
	if !strings.Contains(result, "Callee1 (callee)") {
		t.Error("missing callee node")
	}
}

// TestFormatCallgraphHuman_CallersOnlyDirection is a regression test for a
// bug where `ckb callgraph <sym> --direction=callers` printed the header
// "Callees (what this calls)" even though every node had role "caller".
// internal/query/navigation.go's GetCallGraph always sets Depth to a
// non-negative distance (1 for direct callers/callees, 2 for transitive)
// regardless of direction — it never emits a negative Depth for callers.
// The old grouping logic (`n.Depth < 0` => caller, `n.Depth > 0` => callee)
// therefore always classified real caller nodes as callees. This fixture
// mirrors what --direction=callers actually produces: caller nodes with
// Depth: 1, not the unrealistic Depth: -1 the older test used.
func TestFormatCallgraphHuman_CallersOnlyDirection(t *testing.T) {
	resp := &CallgraphResponseCLI{
		Root: "Engine.buildProvenance",
		Nodes: []CallgraphNodeCLI{
			{ID: "root", Name: "buildProvenance", Depth: 0, Role: "root"},
			{ID: "1", Name: "Engine#AnalyzeImpact", Depth: 1, Role: "caller"},
			{ID: "2", Name: "Engine#PrepareChange", Depth: 1, Role: "caller"},
		},
		Edges: []CallgraphEdgeCLI{
			{From: "1", To: "root"},
			{From: "2", To: "root"},
		},
	}

	result, err := formatCallgraphHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Callers (who calls this)") {
		t.Error("expected callers section header for role=caller nodes")
	}
	if strings.Contains(result, "Callees (what this calls)") {
		t.Error("callers-only response should not print the callees header")
	}
	if !strings.Contains(result, "Engine#AnalyzeImpact (caller)") {
		t.Error("missing caller node under the callers section")
	}
}

func TestFormatCallgraphHuman_Empty(t *testing.T) {
	resp := &CallgraphResponseCLI{
		Root:  "Orphan",
		Nodes: []CallgraphNodeCLI{},
		Edges: []CallgraphEdgeCLI{},
	}

	result, err := formatCallgraphHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Nodes: 0, Edges: 0") {
		t.Error("missing zero counts")
	}
}

func TestFormatHotspotsHuman(t *testing.T) {
	resp := &HotspotsResponseCLI{
		TimeWindow: "90 days",
		TotalCount: 2,
		Hotspots: []HotspotCLI{
			{
				FilePath:  "internal/query/engine.go",
				Score:     0.95,
				RiskLevel: "high",
				Churn:     HotspotChurnCLI{ChangeCount: 50, AuthorCount: 5},
			},
			{
				FilePath:  "internal/mcp/server.go",
				Score:     0.75,
				RiskLevel: "medium",
				Churn:     HotspotChurnCLI{ChangeCount: 25, AuthorCount: 3},
			},
		},
	}

	result, err := formatHotspotsHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Code Hotspots") {
		t.Error("missing header")
	}
	if !strings.Contains(result, "Total Hotspots: 2 (period: 90 days)") {
		t.Error("missing total count and period")
	}
	if !strings.Contains(result, "1. internal/query/engine.go") {
		t.Error("missing first hotspot")
	}
	if !strings.Contains(result, "Risk: high") {
		t.Error("missing risk level")
	}
	if !strings.Contains(result, "Changes: 50, Authors: 5") {
		t.Error("missing churn stats")
	}
}

func TestFormatJustifyHuman(t *testing.T) {
	tests := []struct {
		name     string
		verdict  string
		wantIcon string
	}{
		{"keep verdict", "keep", "✓"},
		{"investigate verdict", "investigate", "⚠"},
		{"remove verdict", "remove", "✗"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &JustifyResponseCLI{
				SymbolId:   "sym123",
				Verdict:    tt.verdict,
				Confidence: 0.85,
				Reasoning:  "Test reasoning",
			}

			result, err := formatJustifyHuman(resp)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !strings.Contains(result, "Symbol Justification: sym123") {
				t.Error("missing header")
			}
			if !strings.Contains(result, tt.wantIcon) {
				t.Errorf("missing verdict icon %q", tt.wantIcon)
			}
			if !strings.Contains(result, tt.verdict) {
				t.Errorf("missing verdict %q", tt.verdict)
			}
			if !strings.Contains(result, "85%") {
				t.Error("missing confidence percentage")
			}
			if !strings.Contains(result, "Reasoning: Test reasoning") {
				t.Error("missing reasoning")
			}
		})
	}
}

func TestFormatDiffSummaryHuman(t *testing.T) {
	resp := &DiffSummaryResponseCLI{
		Selector: DiffSelectorCLI{
			Type:  "commit",
			Value: "abc123",
		},
		Confidence: 0.85,
		Summary: DiffSummaryTextCLI{
			OneLiner:     "Refactored query engine for better performance",
			KeyChanges:   []string{"Added caching layer", "Optimized search algorithm"},
			RiskOverview: "Medium risk due to core changes",
		},
		ChangedFiles: []DiffFileChangeCLI{
			{FilePath: "internal/query/engine.go", ChangeType: "modified", Additions: 50, Deletions: 20, RiskLevel: "high"},
			{FilePath: "internal/query/cache.go", ChangeType: "added", Additions: 100, Deletions: 0, RiskLevel: "low"},
		},
		SymbolsAffected: []DiffSymbolAffectedCLI{
			{Name: "Engine.Search", Kind: "method", ChangeType: "modified", IsPublicAPI: true, IsEntrypoint: false},
			{Name: "NewCache", Kind: "function", ChangeType: "added", IsPublicAPI: false, IsEntrypoint: false},
		},
		RiskSignals: []DiffRiskSignalCLI{
			{Type: "api_change", Severity: "high", Description: "Public API modified"},
		},
		Limitations: []string{"Analysis limited to Go files"},
	}

	result := formatDiffSummaryHuman(resp)

	// Check header
	if !strings.Contains(result, "Diff Summary") {
		t.Error("missing header")
	}

	// Check selector
	if !strings.Contains(result, "Scope: commit (abc123)") {
		t.Error("missing selector info")
	}

	// Check confidence
	if !strings.Contains(result, "Confidence: 85%") {
		t.Error("missing confidence")
	}

	// Check summary
	if !strings.Contains(result, "Summary: Refactored query engine") {
		t.Error("missing one-liner summary")
	}

	// Check key changes
	if !strings.Contains(result, "Key Changes:") {
		t.Error("missing key changes section")
	}
	if !strings.Contains(result, "• Added caching layer") {
		t.Error("missing key change item")
	}

	// Check risk overview
	if !strings.Contains(result, "Risk: Medium risk due to core changes") {
		t.Error("missing risk overview")
	}

	// Check changed files
	if !strings.Contains(result, "Changed Files (2):") {
		t.Error("missing changed files section")
	}
	if !strings.Contains(result, "modified internal/query/engine.go (+50/-20) ⚠") {
		t.Error("missing high-risk file with marker")
	}

	// Check symbols affected
	if !strings.Contains(result, "Symbols Affected (2):") {
		t.Error("missing symbols section")
	}
	if !strings.Contains(result, "Engine.Search (method) [public]") {
		t.Error("missing public API marker")
	}

	// Check risk signals
	if !strings.Contains(result, "Risk Signals:") {
		t.Error("missing risk signals section")
	}
	if !strings.Contains(result, "⚠ [api_change] Public API modified") {
		t.Error("missing risk signal with icon")
	}

	// Check limitations
	if !strings.Contains(result, "Limitations:") {
		t.Error("missing limitations section")
	}
}

func TestFormatDiffSummaryHuman_Truncation(t *testing.T) {
	// Create 20 files to test truncation
	files := make([]DiffFileChangeCLI, 20)
	for i := range files {
		files[i] = DiffFileChangeCLI{
			FilePath:   "file.go",
			ChangeType: "modified",
			RiskLevel:  "low",
		}
	}

	resp := &DiffSummaryResponseCLI{
		Selector:     DiffSelectorCLI{Type: "branch", Value: "main"},
		Confidence:   0.9,
		Summary:      DiffSummaryTextCLI{},
		ChangedFiles: files,
	}

	result := formatDiffSummaryHuman(resp)

	// Should truncate at 15 and show "... and 5 more"
	if !strings.Contains(result, "... and 5 more") {
		t.Error("expected truncation message")
	}
}

func TestFormatDiffSummaryHuman_CriticalRisk(t *testing.T) {
	resp := &DiffSummaryResponseCLI{
		Selector:   DiffSelectorCLI{Type: "commit", Value: "xyz"},
		Confidence: 0.9,
		Summary:    DiffSummaryTextCLI{},
		ChangedFiles: []DiffFileChangeCLI{
			{FilePath: "security.go", ChangeType: "modified", RiskLevel: "critical"},
		},
		RiskSignals: []DiffRiskSignalCLI{
			{Type: "security", Severity: "critical", Description: "Security-sensitive file"},
		},
	}

	result := formatDiffSummaryHuman(resp)

	// Critical risk should use ✗ marker
	if !strings.Contains(result, "security.go") && !strings.Contains(result, "✗") {
		t.Error("expected critical risk marker for file")
	}
	if !strings.Contains(result, "✗ [security] Security-sensitive file") {
		t.Error("expected critical risk signal with ✗ icon")
	}
}

func TestFormatConceptsHuman(t *testing.T) {
	resp := &ConceptsResponseCLI{
		TotalFound: 3,
		Confidence: 0.92,
		Concepts: []ConceptCLI{
			{
				Name:        "Query Engine",
				Category:    "core",
				Description: "Central query processing system",
				Occurrences: 45,
				Score:       0.95,
				Files:       []string{"engine.go", "search.go", "filter.go", "cache.go"},
			},
			{
				Name:        "Symbol Resolution",
				Category:    "feature",
				Occurrences: 20,
				Score:       0.80,
				Files:       []string{"resolver.go"},
			},
		},
		Limitations: []string{"Limited to indexed files"},
	}

	result := formatConceptsHuman(resp)

	// Check header
	if !strings.Contains(result, "Key Concepts") {
		t.Error("missing header")
	}

	// Check count and confidence
	if !strings.Contains(result, "Found 3 concepts (confidence: 92%)") {
		t.Error("missing concept count and confidence")
	}

	// Check first concept
	if !strings.Contains(result, "1. Query Engine [core]") {
		t.Error("missing first concept with category")
	}
	if !strings.Contains(result, "Central query processing system") {
		t.Error("missing description")
	}
	if !strings.Contains(result, "Occurrences: 45, Score: 0.95") {
		t.Error("missing occurrences and score")
	}

	// Check files with truncation
	if !strings.Contains(result, "Files: engine.go, search.go, filter.go (+1 more)") {
		t.Error("missing files with truncation")
	}

	// Check second concept without category
	if !strings.Contains(result, "2. Symbol Resolution [feature]") {
		t.Error("missing second concept")
	}

	// Check limitations
	if !strings.Contains(result, "Limitations:") {
		t.Error("missing limitations section")
	}
}

func TestFormatConceptsHuman_Empty(t *testing.T) {
	resp := &ConceptsResponseCLI{
		TotalFound: 0,
		Confidence: 0.5,
		Concepts:   []ConceptCLI{},
	}

	result := formatConceptsHuman(resp)

	if !strings.Contains(result, "Found 0 concepts") {
		t.Error("should show zero concepts")
	}
}

func TestFormatChangeSetHuman(t *testing.T) {
	resp := &ChangeSetResponseCLI{
		Summary: &ChangeSummaryCLI{
			FilesChanged:         5,
			SymbolsChanged:       10,
			DirectlyAffected:     15,
			TransitivelyAffected: 25,
		},
		RiskScore: &RiskScoreCLI{
			Level:       "high",
			Score:       0.75,
			Explanation: "Core system changes",
		},
		BlastRadius: &BlastRadiusCLI{
			ModuleCount:       3,
			FileCount:         12,
			UniqueCallerCount: 20,
		},
		ChangedSymbols: []ChangedSymbolCLI{
			{Name: "Engine.Search", File: "engine.go", ChangeType: "modified"},
			{Name: "Engine.Filter", File: "engine.go", ChangeType: "modified"},
		},
		AffectedSymbols: []ImpactItemCLI{
			{Name: "Handler.Query", Kind: "direct-caller", Distance: 1},
			{Name: "API.Search", Kind: "transitive-caller", Distance: 2},
		},
		Recommendations: []RecommendationCLI{
			{Type: "review", Severity: "warn", Message: "Needs senior review", Action: "Request review"},
		},
		IndexStaleness: &IndexStalenessCLI{
			IsStale:          true,
			StalenessMessage: "Index is 3 commits behind",
		},
	}

	result := formatChangeSetHuman(resp)

	// Check header
	if !strings.Contains(result, "Change Impact Analysis") {
		t.Error("missing header")
	}

	// Check summary
	if !strings.Contains(result, "Files Changed:    5") {
		t.Error("missing files changed")
	}
	if !strings.Contains(result, "Direct Impact:    15 symbols") {
		t.Error("missing direct impact")
	}

	// Check risk score
	if !strings.Contains(result, "⚠ Risk Level: high (score: 0.75)") {
		t.Error("missing risk level with warning icon")
	}
	if !strings.Contains(result, "Core system changes") {
		t.Error("missing risk explanation")
	}

	// Check blast radius
	if !strings.Contains(result, "Modules: 3, Files: 12, Callers: 20") {
		t.Error("missing blast radius")
	}

	// Check index staleness
	if !strings.Contains(result, "⚠ Index Warning: Index is 3 commits behind") {
		t.Error("missing staleness warning")
	}

	// Check changed symbols
	if !strings.Contains(result, "Changed Symbols (2):") {
		t.Error("missing changed symbols section")
	}
	if !strings.Contains(result, "modified Engine.Search") {
		t.Error("missing changed symbol")
	}

	// Check affected symbols
	if !strings.Contains(result, "Affected Downstream (2):") {
		t.Error("missing affected symbols section")
	}
	if !strings.Contains(result, "Handler.Query (direct-caller, distance: 1)") {
		t.Error("missing affected symbol")
	}

	// Check recommendations
	if !strings.Contains(result, "Recommendations:") {
		t.Error("missing recommendations section")
	}
	if !strings.Contains(result, "⚠ Needs senior review") {
		t.Error("missing recommendation with warn icon")
	}
	if !strings.Contains(result, "Action: Request review") {
		t.Error("missing recommendation action")
	}
}

func TestFormatChangeSetHuman_CriticalRisk(t *testing.T) {
	resp := &ChangeSetResponseCLI{
		Summary: &ChangeSummaryCLI{},
		RiskScore: &RiskScoreCLI{
			Level: "critical",
			Score: 0.95,
		},
	}

	result := formatChangeSetHuman(resp)

	if !strings.Contains(result, "✗ Risk Level: critical") {
		t.Error("expected critical risk with ✗ icon")
	}
}

func TestFormatChangeSetHuman_LowRisk(t *testing.T) {
	resp := &ChangeSetResponseCLI{
		Summary: &ChangeSummaryCLI{},
		RiskScore: &RiskScoreCLI{
			Level: "low",
			Score: 0.2,
		},
	}

	result := formatChangeSetHuman(resp)

	if !strings.Contains(result, "✓ Risk Level: low") {
		t.Error("expected low risk with ✓ icon")
	}
}

func TestFormatChangeSetHuman_Truncation(t *testing.T) {
	// Create 15 changed symbols to test truncation
	symbols := make([]ChangedSymbolCLI, 15)
	for i := range symbols {
		symbols[i] = ChangedSymbolCLI{Name: "Sym", File: "file.go", ChangeType: "modified"}
	}

	resp := &ChangeSetResponseCLI{
		Summary:        &ChangeSummaryCLI{},
		ChangedSymbols: symbols,
	}

	result := formatChangeSetHuman(resp)

	if !strings.Contains(result, "... and 5 more") {
		t.Error("expected truncation message for changed symbols")
	}
}

func TestFormatChangeSetHuman_FreshIndex(t *testing.T) {
	resp := &ChangeSetResponseCLI{
		Summary: &ChangeSummaryCLI{},
		IndexStaleness: &IndexStalenessCLI{
			IsStale: false,
		},
	}

	result := formatChangeSetHuman(resp)

	if strings.Contains(result, "Index Warning") {
		t.Error("should not show staleness warning when fresh")
	}
}

func TestFormatStatusHuman_NoIndex(t *testing.T) {
	resp := &StatusResponseCLI{
		CkbVersion: "8.0.0",
		Healthy:    true,
		IndexStatus: &IndexStatusCLI{
			Exists: false,
		},
	}

	result, err := formatStatusHuman(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check index not found message
	if !strings.Contains(result, "✗ No index found") {
		t.Error("missing no index message")
	}
	if !strings.Contains(result, "Run 'ckb index' to create one") {
		t.Error("missing index creation hint")
	}

	// Check command guidance
	if !strings.Contains(result, "Commands that work without index (git-based):") {
		t.Error("missing git-based commands guidance")
	}
	if !strings.Contains(result, "hotspots, ownership, reviewers, diff-summary, pr-summary") {
		t.Error("missing list of git-based commands")
	}
	if !strings.Contains(result, "Commands that need SCIP index:") {
		t.Error("missing SCIP commands guidance")
	}
	if !strings.Contains(result, "search, refs, callgraph, impact, dead-code, trace, explain") {
		t.Error("missing list of SCIP commands")
	}
}

func TestFormatDiffSummaryHuman_EntrypointMarker(t *testing.T) {
	resp := &DiffSummaryResponseCLI{
		Selector:   DiffSelectorCLI{Type: "commit", Value: "abc"},
		Confidence: 0.9,
		Summary:    DiffSummaryTextCLI{},
		SymbolsAffected: []DiffSymbolAffectedCLI{
			{Name: "main", Kind: "function", ChangeType: "modified", IsEntrypoint: true},
		},
	}

	result := formatDiffSummaryHuman(resp)

	if !strings.Contains(result, "main (function) [entrypoint]") {
		t.Error("missing entrypoint marker")
	}
}

func TestFormatDiffSummaryHuman_DefaultRiskSignalIcon(t *testing.T) {
	resp := &DiffSummaryResponseCLI{
		Selector:   DiffSelectorCLI{Type: "commit", Value: "abc"},
		Confidence: 0.9,
		Summary:    DiffSummaryTextCLI{},
		RiskSignals: []DiffRiskSignalCLI{
			{Type: "complexity", Severity: "low", Description: "Minor complexity increase"},
		},
	}

	result := formatDiffSummaryHuman(resp)

	// Low severity should use ○ icon (default)
	if !strings.Contains(result, "○ [complexity] Minor complexity increase") {
		t.Error("expected default circle icon for low severity")
	}
}

func TestFormatDiffSummaryHuman_SymbolTruncation(t *testing.T) {
	// Create 15 symbols to test truncation at 10
	symbols := make([]DiffSymbolAffectedCLI, 15)
	for i := range symbols {
		symbols[i] = DiffSymbolAffectedCLI{Name: "Sym", Kind: "func", ChangeType: "modified"}
	}

	resp := &DiffSummaryResponseCLI{
		Selector:        DiffSelectorCLI{Type: "branch", Value: "main"},
		Confidence:      0.9,
		Summary:         DiffSummaryTextCLI{},
		SymbolsAffected: symbols,
	}

	result := formatDiffSummaryHuman(resp)

	if !strings.Contains(result, "... and 5 more") {
		t.Error("expected truncation message for symbols")
	}
}

func TestFormatConceptsHuman_NoCategory(t *testing.T) {
	resp := &ConceptsResponseCLI{
		TotalFound: 1,
		Confidence: 0.8,
		Concepts: []ConceptCLI{
			{
				Name:        "Uncategorized",
				Category:    "", // No category
				Occurrences: 5,
				Score:       0.6,
			},
		},
	}

	result := formatConceptsHuman(resp)

	// Should show name without category brackets
	if !strings.Contains(result, "1. Uncategorized\n") {
		t.Error("should show concept without category brackets")
	}
	if strings.Contains(result, "1. Uncategorized []") {
		t.Error("should not show empty category brackets")
	}
}

func TestFormatConceptsHuman_NoDescription(t *testing.T) {
	resp := &ConceptsResponseCLI{
		TotalFound: 1,
		Confidence: 0.8,
		Concepts: []ConceptCLI{
			{
				Name:        "Simple",
				Category:    "test",
				Description: "", // No description
				Occurrences: 3,
				Score:       0.5,
			},
		},
	}

	result := formatConceptsHuman(resp)

	// Should have occurrences line directly after name
	if !strings.Contains(result, "1. Simple [test]\n   Occurrences:") {
		t.Error("should show occurrences directly after name when no description")
	}
}

func TestFormatConceptsHuman_NoFiles(t *testing.T) {
	resp := &ConceptsResponseCLI{
		TotalFound: 1,
		Confidence: 0.8,
		Concepts: []ConceptCLI{
			{
				Name:        "NoFiles",
				Category:    "test",
				Occurrences: 2,
				Score:       0.4,
				Files:       []string{}, // Empty files
			},
		},
	}

	result := formatConceptsHuman(resp)

	// Should not have Files: line
	if strings.Contains(result, "Files:") {
		t.Error("should not show Files section when empty")
	}
}

func TestFormatChangeSetHuman_ErrorSeverityRecommendation(t *testing.T) {
	resp := &ChangeSetResponseCLI{
		Summary: &ChangeSummaryCLI{},
		Recommendations: []RecommendationCLI{
			{Type: "breaking", Severity: "error", Message: "Breaking change detected"},
		},
	}

	result := formatChangeSetHuman(resp)

	if !strings.Contains(result, "✗ Breaking change detected") {
		t.Error("expected error severity with ✗ icon")
	}
}

func TestFormatChangeSetHuman_DefaultRecommendationIcon(t *testing.T) {
	resp := &ChangeSetResponseCLI{
		Summary: &ChangeSummaryCLI{},
		Recommendations: []RecommendationCLI{
			{Type: "info", Severity: "info", Message: "Consider adding tests"},
		},
	}

	result := formatChangeSetHuman(resp)

	// Info severity should use → icon (default)
	if !strings.Contains(result, "→ Consider adding tests") {
		t.Error("expected default arrow icon for info severity")
	}
}

func TestFormatChangeSetHuman_RecommendationWithoutAction(t *testing.T) {
	resp := &ChangeSetResponseCLI{
		Summary: &ChangeSummaryCLI{},
		Recommendations: []RecommendationCLI{
			{Type: "note", Severity: "info", Message: "Just a note", Action: ""},
		},
	}

	result := formatChangeSetHuman(resp)

	if !strings.Contains(result, "→ Just a note") {
		t.Error("expected recommendation message")
	}
	if strings.Contains(result, "Action:") {
		t.Error("should not show Action when empty")
	}
}

func TestFormatChangeSetHuman_AffectedSymbolsTruncation(t *testing.T) {
	// Create 15 affected symbols to test truncation at 10
	affected := make([]ImpactItemCLI, 15)
	for i := range affected {
		affected[i] = ImpactItemCLI{Name: "Affected", Kind: "caller", Distance: 1}
	}

	resp := &ChangeSetResponseCLI{
		Summary:         &ChangeSummaryCLI{},
		AffectedSymbols: affected,
	}

	result := formatChangeSetHuman(resp)

	if !strings.Contains(result, "... and 5 more") {
		t.Error("expected truncation message for affected symbols")
	}
}

func TestFormatChangeSetHuman_MediumRisk(t *testing.T) {
	resp := &ChangeSetResponseCLI{
		Summary: &ChangeSummaryCLI{},
		RiskScore: &RiskScoreCLI{
			Level: "medium",
			Score: 0.5,
		},
	}

	result := formatChangeSetHuman(resp)

	// Medium risk should use ✓ icon (not warn/critical)
	if !strings.Contains(result, "✓ Risk Level: medium") {
		t.Error("expected medium risk with ✓ icon")
	}
}

func TestFormatChangeSetHuman_NilSummary(t *testing.T) {
	resp := &ChangeSetResponseCLI{
		Summary: nil,
	}

	result := formatChangeSetHuman(resp)

	// Should not panic, should not have Summary section
	if strings.Contains(result, "Files Changed:") {
		t.Error("should not show summary when nil")
	}
}
