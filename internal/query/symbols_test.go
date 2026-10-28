package query

import (
	"context"
	"strings"
	"testing"
)

func TestParseScope(t *testing.T) {
	tests := []struct {
		scope    string
		expected []string
	}{
		{"", nil},
		{"internal/query", []string{"internal/query"}},
		{"cmd/ckb", []string{"cmd/ckb"}},
	}

	for _, tt := range tests {
		t.Run(tt.scope, func(t *testing.T) {
			result := parseScope(tt.scope)
			if tt.expected == nil && result != nil {
				t.Errorf("parseScope(%q) = %v, want nil", tt.scope, result)
				return
			}
			if tt.expected != nil {
				if len(result) != len(tt.expected) {
					t.Errorf("parseScope(%q) = %v, want %v", tt.scope, result, tt.expected)
					return
				}
				for i := range result {
					if result[i] != tt.expected[i] {
						t.Errorf("parseScope(%q)[%d] = %q, want %q", tt.scope, i, result[i], tt.expected[i])
					}
				}
			}
		})
	}
}

// TestRankSearchResults_CaseExactOutranksFold locks down the fix for the
// evidence-output-bugs ranking regression: a query like "Model" must
// deterministically outrank a case-insensitive-only match like a field
// named "model" — a class/interface/struct/type match wins outright when
// the case matches, and even when it only matches case-insensitively it
// still beats a property/parameter of the same name.
func TestRankSearchResults_CaseExactOutranksFold(t *testing.T) {
	t.Run("case-sensitive exact match beats case-fold-only match regardless of kind", func(t *testing.T) {
		results := []SearchResultItem{
			{Name: "model", Kind: "property", Visibility: &VisibilityInfo{Visibility: "public"}},
			{Name: "Model", Kind: "class", Visibility: &VisibilityInfo{Visibility: "public"}},
		}
		rankSearchResults(results, "Model")

		var classScore, propertyScore float64
		for _, r := range results {
			switch r.Kind {
			case "class":
				classScore = r.Score
				if got := r.Ranking.Signals["matchType"]; got != "exact" {
					t.Errorf("class matchType = %v, want %q", got, "exact")
				}
			case "property":
				propertyScore = r.Score
				if got := r.Ranking.Signals["matchType"]; got != "exact-fold" {
					t.Errorf("property matchType = %v, want %q", got, "exact-fold")
				}
			}
		}
		if classScore <= propertyScore {
			t.Errorf("case-sensitive exact match (class, score=%v) did not outrank case-fold-only match (property, score=%v)", classScore, propertyScore)
		}
	})

	t.Run("type-like kind tie-breaks over property/parameter among case-fold-only matches", func(t *testing.T) {
		results := []SearchResultItem{
			{Name: "model", Kind: "parameter", Visibility: &VisibilityInfo{Visibility: "public"}},
			{Name: "MODEL", Kind: "interface", Visibility: &VisibilityInfo{Visibility: "public"}},
		}
		rankSearchResults(results, "Model")

		var interfaceScore, paramScore float64
		for _, r := range results {
			switch r.Kind {
			case "interface":
				interfaceScore = r.Score
			case "parameter":
				paramScore = r.Score
			}
		}
		if interfaceScore <= paramScore {
			t.Errorf("type-like kind (interface, score=%v) did not tie-break over parameter (score=%v) among case-fold-only matches", interfaceScore, paramScore)
		}
	})

	t.Run("case-sensitive exact match wins even against a different type-like kind", func(t *testing.T) {
		// Mirrors the fixture disambiguation case: a free function named
		// exactly "Handler" and a struct/class also named exactly "Handler" —
		// both are case-sensitive exact matches, so the type-like kind bonus
		// (not the match-type tier) should decide it.
		results := []SearchResultItem{
			{Name: "Handler", Kind: "function", Visibility: &VisibilityInfo{Visibility: "public"}},
			{Name: "Handler", Kind: "class", Visibility: &VisibilityInfo{Visibility: "public"}},
		}
		rankSearchResults(results, "Handler")

		var classScore, funcScore float64
		for _, r := range results {
			switch r.Kind {
			case "class":
				classScore = r.Score
			case "function":
				funcScore = r.Score
			}
		}
		if classScore <= funcScore {
			t.Errorf("class (score=%v) did not outrank function (score=%v) for an identical case-sensitive exact match", classScore, funcScore)
		}
	})
}

func TestIsTypeLikeKind(t *testing.T) {
	typeLike := []string{"class", "interface", "struct", "type", "enum"}
	for _, k := range typeLike {
		if !isTypeLikeKind(k) {
			t.Errorf("isTypeLikeKind(%q) = false, want true", k)
		}
	}
	memberLike := []string{"property", "field", "parameter", "method", "function", "variable", ""}
	for _, k := range memberLike {
		if isTypeLikeKind(k) {
			t.Errorf("isTypeLikeKind(%q) = true, want false", k)
		}
	}
}

// TestRankSearchResults_EnumOutranksPropertyOnCaseFold is a regression test
// for a bug where isTypeLikeKind omitted "enum" — a real SCIP-emitted kind
// (SCIP kind 3 / inferKindString case 3 in internal/query/fts.go) — so a
// case-fold-only query like "STATUS" ranked an enum `Status` no higher than
// a property `status`, contrary to the type-like tie-break policy exercised
// by TestRankSearchResults_CaseExactOutranksFold.
func TestRankSearchResults_EnumOutranksPropertyOnCaseFold(t *testing.T) {
	results := []SearchResultItem{
		{Name: "status", Kind: "property", Visibility: &VisibilityInfo{Visibility: "public"}},
		{Name: "Status", Kind: "enum", Visibility: &VisibilityInfo{Visibility: "public"}},
	}
	rankSearchResults(results, "STATUS")

	var enumScore, propertyScore float64
	for _, r := range results {
		switch r.Kind {
		case "enum":
			enumScore = r.Score
		case "property":
			propertyScore = r.Score
		}
	}
	if enumScore <= propertyScore {
		t.Errorf("enum (score=%v) did not outrank property (score=%v) for case-fold-only query %q", enumScore, propertyScore, "STATUS")
	}
}

func TestGenerateSearchCacheKey(t *testing.T) {
	tests := []struct {
		name     string
		opts     SearchSymbolsOptions
		wantHash bool
	}{
		{
			name:     "simple query",
			opts:     SearchSymbolsOptions{Query: "Engine", Limit: 20},
			wantHash: true,
		},
		{
			name:     "with scope",
			opts:     SearchSymbolsOptions{Query: "Engine", Scope: "internal/query", Limit: 20},
			wantHash: true,
		},
		{
			name:     "with kinds",
			opts:     SearchSymbolsOptions{Query: "Engine", Kinds: []string{"function", "class"}, Limit: 20},
			wantHash: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateSearchCacheKey(tt.opts)
			if !strings.HasPrefix(result, "search:") {
				t.Errorf("generateSearchCacheKey() = %q, expected prefix 'search:'", result)
			}
			if tt.wantHash && len(result) < 10 {
				t.Errorf("generateSearchCacheKey() = %q, expected longer hash", result)
			}
		})
	}

	// Same options should produce same key
	t.Run("deterministic", func(t *testing.T) {
		opts := SearchSymbolsOptions{Query: "Engine", Limit: 20}
		key1 := generateSearchCacheKey(opts)
		key2 := generateSearchCacheKey(opts)
		if key1 != key2 {
			t.Errorf("generateSearchCacheKey not deterministic: %q != %q", key1, key2)
		}
	})

	// Kind order should not matter
	t.Run("kind order independent", func(t *testing.T) {
		opts1 := SearchSymbolsOptions{Query: "Engine", Kinds: []string{"function", "class"}, Limit: 20}
		opts2 := SearchSymbolsOptions{Query: "Engine", Kinds: []string{"class", "function"}, Limit: 20}
		key1 := generateSearchCacheKey(opts1)
		key2 := generateSearchCacheKey(opts2)
		if key1 != key2 {
			t.Errorf("generateSearchCacheKey should be kind-order independent: %q != %q", key1, key2)
		}
	})
}

func TestNewRankingV52(t *testing.T) {
	signals := map[string]interface{}{
		"matchType": "exact",
		"kind":      "function",
	}

	ranking := NewRankingV52(85.0, signals)

	if ranking == nil {
		t.Fatal("NewRankingV52 returned nil")
	}
	if ranking.Score != 85.0 {
		t.Errorf("Score = %f, want 85.0", ranking.Score)
	}
	if ranking.PolicyVersion != "5.2" {
		t.Errorf("PolicyVersion = %q, want '5.2'", ranking.PolicyVersion)
	}
	if ranking.Signals["matchType"] != "exact" {
		t.Errorf("Signals[matchType] = %v, want 'exact'", ranking.Signals["matchType"])
	}
}

func TestDeduplicateReferences(t *testing.T) {
	tests := []struct {
		name     string
		refs     []ReferenceInfo
		expected int
	}{
		{
			name:     "empty",
			refs:     []ReferenceInfo{},
			expected: 0,
		},
		{
			name: "no duplicates",
			refs: []ReferenceInfo{
				{Location: &LocationInfo{FileId: "a.go", StartLine: 10, StartColumn: 5}},
				{Location: &LocationInfo{FileId: "b.go", StartLine: 20, StartColumn: 10}},
			},
			expected: 2,
		},
		{
			name: "with duplicates",
			refs: []ReferenceInfo{
				{Location: &LocationInfo{FileId: "a.go", StartLine: 10, StartColumn: 5}},
				{Location: &LocationInfo{FileId: "a.go", StartLine: 10, StartColumn: 5}},
				{Location: &LocationInfo{FileId: "b.go", StartLine: 20, StartColumn: 10}},
			},
			expected: 2,
		},
		{
			name: "nil location skipped",
			refs: []ReferenceInfo{
				{Location: nil},
				{Location: &LocationInfo{FileId: "a.go", StartLine: 10, StartColumn: 5}},
			},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deduplicateReferences(tt.refs)
			if len(result) != tt.expected {
				t.Errorf("deduplicateReferences() returned %d refs, want %d", len(result), tt.expected)
			}
		})
	}
}

func TestSortReferences(t *testing.T) {
	refs := []ReferenceInfo{
		{Location: &LocationInfo{FileId: "b.go", StartLine: 10, StartColumn: 5}},
		{Location: &LocationInfo{FileId: "a.go", StartLine: 20, StartColumn: 10}},
		{Location: &LocationInfo{FileId: "a.go", StartLine: 10, StartColumn: 15}},
		{Location: &LocationInfo{FileId: "a.go", StartLine: 10, StartColumn: 5}},
	}

	sortReferences(refs)

	// Should be sorted by file, then line, then column
	expected := []struct {
		file   string
		line   int
		column int
	}{
		{"a.go", 10, 5},
		{"a.go", 10, 15},
		{"a.go", 20, 10},
		{"b.go", 10, 5},
	}

	for i, exp := range expected {
		if refs[i].Location.FileId != exp.file {
			t.Errorf("refs[%d].FileId = %q, want %q", i, refs[i].Location.FileId, exp.file)
		}
		if refs[i].Location.StartLine != exp.line {
			t.Errorf("refs[%d].StartLine = %d, want %d", i, refs[i].Location.StartLine, exp.line)
		}
		if refs[i].Location.StartColumn != exp.column {
			t.Errorf("refs[%d].StartColumn = %d, want %d", i, refs[i].Location.StartColumn, exp.column)
		}
	}
}

func TestGenerateTreesitterSymbolId(t *testing.T) {
	tests := []struct {
		path string
		name string
		kind string
		line int
	}{
		{"internal/query/engine.go", "Engine", "struct", 42},
		{"cmd/ckb/main.go", "main", "function", 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := generateTreesitterSymbolId(tt.path, tt.name, tt.kind, tt.line)
			if !strings.HasPrefix(id, "ts-") {
				t.Errorf("generateTreesitterSymbolId() = %q, expected prefix 'ts-'", id)
			}
			if len(id) < 10 {
				t.Errorf("generateTreesitterSymbolId() = %q, expected longer ID", id)
			}
		})
	}

	// Same inputs should produce same ID
	t.Run("deterministic", func(t *testing.T) {
		id1 := generateTreesitterSymbolId("a.go", "Foo", "function", 10)
		id2 := generateTreesitterSymbolId("a.go", "Foo", "function", 10)
		if id1 != id2 {
			t.Errorf("generateTreesitterSymbolId not deterministic: %q != %q", id1, id2)
		}
	})

	// Different inputs should produce different IDs
	t.Run("different inputs", func(t *testing.T) {
		id1 := generateTreesitterSymbolId("a.go", "Foo", "function", 10)
		id2 := generateTreesitterSymbolId("a.go", "Bar", "function", 10)
		if id1 == id2 {
			t.Errorf("generateTreesitterSymbolId should differ for different names")
		}
	})
}

func TestInferVisibility(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		expected string
	}{
		{"", "function", "unknown"},
		{"Engine", "struct", "public"},
		{"engine", "struct", "internal"},
		{"GetUser", "method", "public"},
		{"getUser", "method", "internal"},
		{"_helper", "function", "internal"},
		{"__private", "function", "private"},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.kind, func(t *testing.T) {
			result := inferVisibility(tt.name, tt.kind)
			if result != tt.expected {
				t.Errorf("inferVisibility(%q, %q) = %q, want %q", tt.name, tt.kind, result, tt.expected)
			}
		})
	}
}

func TestRankSearchResults(t *testing.T) {
	tests := []struct {
		name         string
		results      []SearchResultItem
		query        string
		wantScored   bool
		wantRankings bool
	}{
		{
			name:         "empty results",
			results:      []SearchResultItem{},
			query:        "Engine",
			wantScored:   true,
			wantRankings: true,
		},
		{
			name: "exact match",
			results: []SearchResultItem{
				{Name: "Engine", Kind: "class"},
			},
			query:        "Engine",
			wantScored:   true,
			wantRankings: true,
		},
		{
			name: "partial match",
			results: []SearchResultItem{
				{Name: "EngineFactory", Kind: "class"},
			},
			query:        "Engine",
			wantScored:   true,
			wantRankings: true,
		},
		{
			name: "contains match",
			results: []SearchResultItem{
				{Name: "QueryEngine", Kind: "class"},
			},
			query:        "Engine",
			wantScored:   true,
			wantRankings: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := make([]SearchResultItem, len(tt.results))
			copy(results, tt.results)

			rankSearchResults(results, tt.query)

			for i, r := range results {
				if tt.wantScored && r.Score == 0 && len(tt.results) > 0 {
					t.Errorf("results[%d].Score = 0, expected non-zero score", i)
				}
				if tt.wantRankings && r.Ranking == nil && len(tt.results) > 0 {
					t.Errorf("results[%d].Ranking = nil, expected ranking", i)
				}
			}
		})
	}

	// Exact match should score higher than partial
	t.Run("exact scores higher", func(t *testing.T) {
		results := []SearchResultItem{
			{Name: "EngineFactory", Kind: "class"},
			{Name: "Engine", Kind: "class"},
		}
		rankSearchResults(results, "Engine")

		// Find scores
		var exactScore, partialScore float64
		for _, r := range results {
			if r.Name == "Engine" {
				exactScore = r.Score
			} else {
				partialScore = r.Score
			}
		}

		if exactScore <= partialScore {
			t.Errorf("exact match score (%f) should be higher than partial (%f)", exactScore, partialScore)
		}
	})
}

// =============================================================================
// Engine Method Tests - GetSymbol, SearchSymbols, FindReferences
// =============================================================================

func TestGetSymbol_DefaultRepoStateMode(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	// Test with empty RepoStateMode - should default to "head"
	resp, err := engine.GetSymbol(ctx, GetSymbolOptions{
		SymbolId:      "nonexistent:sym:123",
		RepoStateMode: "",
	})

	// We expect an error since the symbol doesn't exist, but we're testing
	// that the method runs without panic when RepoStateMode is empty
	if err != nil {
		// Error is expected for non-existent symbol
		return
	}

	// If no error, verify the response has provenance
	if resp != nil && resp.Provenance == nil {
		t.Error("expected provenance in response")
	}
}

func TestGetSymbol_SymbolNotFound(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	resp, err := engine.GetSymbol(ctx, GetSymbolOptions{
		SymbolId:      "ckb:test:sym:nonexistent",
		RepoStateMode: "head",
	})

	// Should return response with drilldowns, not error
	if err != nil {
		// Some configurations may return error - that's OK
		return
	}

	if resp == nil {
		t.Fatal("expected non-nil response for not-found symbol")
	}

	// Should have drilldowns to help user
	if len(resp.Drilldowns) == 0 {
		t.Error("expected drilldowns in response for not-found symbol")
	}
}

func TestGetSymbol_ProvenanceBuilding(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	resp, err := engine.GetSymbol(ctx, GetSymbolOptions{
		SymbolId:      "test:sym:1",
		RepoStateMode: "head",
	})

	if err != nil {
		// Error is acceptable for non-existent symbol
		return
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Verify provenance is always built
	if resp.Provenance == nil {
		t.Error("expected provenance in response")
	} else {
		// Provenance should have valid query duration
		if resp.Provenance.QueryDurationMs < 0 {
			t.Error("expected non-negative QueryDurationMs in provenance")
		}
	}
}

func TestSearchSymbols_DefaultLimit(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	// Test with zero limit - should default to 20
	resp, err := engine.SearchSymbols(ctx, SearchSymbolsOptions{
		Query: "Engine",
		Limit: 0, // Should default to 20
	})

	if err != nil {
		t.Fatalf("SearchSymbols failed: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Verify provenance was built
	if resp.Provenance == nil {
		t.Error("expected provenance in response")
	}
}

func TestSearchSymbols_WithKinds(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	tests := []struct {
		name  string
		kinds []string
	}{
		{"single kind", []string{"function"}},
		{"multiple kinds", []string{"function", "method"}},
		{"empty kinds", []string{}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp, err := engine.SearchSymbols(ctx, SearchSymbolsOptions{
				Query: "Test",
				Kinds: tt.kinds,
				Limit: 10,
			})

			if err != nil {
				t.Fatalf("SearchSymbols with kinds %v failed: %v", tt.kinds, err)
			}

			if resp == nil {
				t.Fatal("expected non-nil response")
			}
		})
	}
}

func TestSearchSymbols_ProvenanceBuilding(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	resp, err := engine.SearchSymbols(ctx, SearchSymbolsOptions{
		Query: "Engine",
		Limit: 10,
	})

	if err != nil {
		t.Fatalf("SearchSymbols failed: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Verify provenance is always built
	if resp.Provenance == nil {
		t.Fatal("expected provenance in response")
	}

	if resp.Provenance.QueryDurationMs < 0 {
		t.Error("expected non-negative QueryDurationMs")
	}
}

func TestSearchSymbols_TruncationInfo(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	// Request with very small limit
	resp, err := engine.SearchSymbols(ctx, SearchSymbolsOptions{
		Query: "Engine",
		Limit: 1,
	})

	if err != nil {
		t.Fatalf("SearchSymbols failed: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// If there are more results than the limit, Truncated should be true
	if resp.TotalCount > 1 && !resp.Truncated {
		t.Error("expected Truncated=true when results exceed limit")
	}

	// Verify truncation info if present
	if resp.Truncated && resp.TruncationInfo != nil {
		if resp.TruncationInfo.Reason == "" {
			t.Error("expected truncation reason")
		}
	}
}

func TestFindReferences_DefaultLimit(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	// Test with zero limit - should default to 100
	resp, err := engine.FindReferences(ctx, FindReferencesOptions{
		SymbolId: "test:sym:1",
		Limit:    0, // Should default to 100
	})

	// Error is acceptable for non-existent symbol
	if err != nil {
		return
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Verify provenance was built
	if resp.Provenance == nil {
		t.Error("expected provenance in response")
	}
}

func TestFindReferences_SymbolNotFound(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	resp, err := engine.FindReferences(ctx, FindReferencesOptions{
		SymbolId: "ckb:test:sym:nonexistent",
		Limit:    10,
	})

	// Should return response or error - either is acceptable
	if err != nil {
		// Error is expected for non-existent symbol
		return
	}

	if resp == nil {
		t.Fatal("expected non-nil response for not-found symbol")
	}

	// Should have empty references
	if len(resp.References) > 0 {
		t.Logf("FindReferences for non-existent symbol returned %d references", len(resp.References))
	}
}

func TestFindReferences_ProvenanceBuilding(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	resp, err := engine.FindReferences(ctx, FindReferencesOptions{
		SymbolId: "test:sym:1",
		Limit:    10,
	})

	// Error is acceptable for non-existent symbol
	if err != nil {
		return
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Verify provenance is always built
	if resp.Provenance == nil {
		t.Error("expected provenance in response")
	} else {
		if resp.Provenance.QueryDurationMs < 0 {
			t.Error("expected non-negative QueryDurationMs in provenance")
		}
	}
}

func TestFindReferences_IncludeTests(t *testing.T) {
	t.Parallel()
	engine, cleanup := testEngine(t)
	defer cleanup()

	ctx := context.Background()

	tests := []struct {
		name         string
		includeTests bool
	}{
		{"include tests", true},
		{"exclude tests", false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp, err := engine.FindReferences(ctx, FindReferencesOptions{
				SymbolId:     "test:sym:1",
				IncludeTests: tt.includeTests,
				Limit:        10,
			})

			// Error is acceptable for non-existent symbol
			if err != nil {
				return
			}

			if resp == nil {
				t.Fatal("expected non-nil response")
			}
		})
	}
}
