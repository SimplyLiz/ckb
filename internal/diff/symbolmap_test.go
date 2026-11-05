package diff

import (
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// mockSymbolIndex implements SymbolIndex for testing
type mockSymbolIndex struct {
	documents map[string]*DocumentInfo
	symbols   map[string]*SymbolInfo
}

func newMockIndex() *mockSymbolIndex {
	return &mockSymbolIndex{
		documents: make(map[string]*DocumentInfo),
		symbols:   make(map[string]*SymbolInfo),
	}
}

func (m *mockSymbolIndex) GetDocument(filePath string) *DocumentInfo {
	return m.documents[filePath]
}

func (m *mockSymbolIndex) GetSymbolInfo(symbolID string) *SymbolInfo {
	return m.symbols[symbolID]
}

func TestDiffSymbolMapper_NilDiff(t *testing.T) {
	mapper := NewDiffSymbolMapper(newMockIndex())
	result, err := mapper.MapToSymbols(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}
}

func TestDiffSymbolMapper_EmptyDiff(t *testing.T) {
	mapper := NewDiffSymbolMapper(newMockIndex())
	result, err := mapper.MapToSymbols(&impact.ParsedDiff{Files: []impact.ChangedFile{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d symbols", len(result))
	}
}

func TestDiffSymbolMapper_FileNotInIndex(t *testing.T) {
	mapper := NewDiffSymbolMapper(newMockIndex())
	diff := &impact.ParsedDiff{
		Files: []impact.ChangedFile{
			{
				NewPath: "unknown.go",
				Hunks: []impact.ChangedHunk{
					{
						NewStart: 1,
						NewLines: 3,
						Added:    []int{1, 2, 3},
					},
				},
			},
		},
	}

	result, err := mapper.MapToSymbols(diff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should return low-confidence file-level entry
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	if result[0].Confidence != 0.3 {
		t.Errorf("expected confidence 0.3, got %f", result[0].Confidence)
	}
	if result[0].SymbolID != "file:unknown.go" {
		t.Errorf("expected file symbol ID, got %s", result[0].SymbolID)
	}
}

func TestDiffSymbolMapper_WithSymbolDefinition(t *testing.T) {
	index := newMockIndex()
	index.documents["main.go"] = &DocumentInfo{
		RelativePath: "main.go",
		Language:     "go",
		Symbols: []SymbolDefInfo{
			{
				Symbol:    "scip-go gomod example.com/test main.doSomething().",
				Name:      "doSomething",
				Kind:      "Function",
				StartLine: 10,
				EndLine:   20,
			},
		},
		Occurrences: []OccurrenceInfo{},
	}
	index.symbols["scip-go gomod example.com/test main.doSomething()."] = &SymbolInfo{
		Symbol: "scip-go gomod example.com/test main.doSomething().",
		Name:   "doSomething",
		Kind:   "Function",
	}

	mapper := NewDiffSymbolMapper(index)
	diff := &impact.ParsedDiff{
		Files: []impact.ChangedFile{
			{
				NewPath: "main.go",
				Hunks: []impact.ChangedHunk{
					{
						NewStart: 10,
						NewLines: 5,
						Added:    []int{10, 12, 15}, // Lines inside doSomething
					},
				},
			},
		},
	}

	result, err := mapper.MapToSymbols(diff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	sym := result[0]
	if sym.Name != "doSomething" {
		t.Errorf("expected name 'doSomething', got '%s'", sym.Name)
	}
	if sym.Confidence != 1.0 {
		t.Errorf("expected confidence 1.0 (exact definition line), got %f", sym.Confidence)
	}
	if sym.ChangeType != impact.ChangeModified {
		t.Errorf("expected ChangeModified, got %s", sym.ChangeType)
	}
}

func TestDiffSymbolMapper_WithOccurrence(t *testing.T) {
	index := newMockIndex()
	index.documents["main.go"] = &DocumentInfo{
		RelativePath: "main.go",
		Language:     "go",
		Symbols:      []SymbolDefInfo{},
		Occurrences: []OccurrenceInfo{
			{
				StartLine:    5,
				EndLine:      5,
				Symbol:       "scip-go gomod example.com/pkg helper().",
				IsDefinition: false,
			},
		},
	}

	mapper := NewDiffSymbolMapper(index)
	diff := &impact.ParsedDiff{
		Files: []impact.ChangedFile{
			{
				NewPath: "main.go",
				Hunks: []impact.ChangedHunk{
					{
						NewStart: 5,
						NewLines: 1,
						Added:    []int{5}, // Line with reference
					},
				},
			},
		},
	}

	result, err := mapper.MapToSymbols(diff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// A referenced symbol isn't changed by a change on the line referencing it,
	// and no definition contains the line: that's a file-level entry, not a guess.
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	sym := result[0]
	if sym.SymbolID != "file:main.go" {
		t.Errorf("expected a file-level entry, got %s", sym.SymbolID)
	}
	if sym.Confidence != confidenceUnresolved || sym.Note == "" {
		t.Errorf("expected low confidence with a note, got %f %q", sym.Confidence, sym.Note)
	}
}

func TestDiffSymbolMapper_NewFile(t *testing.T) {
	index := newMockIndex()
	index.documents["new.go"] = &DocumentInfo{
		RelativePath: "new.go",
		Language:     "go",
		Symbols: []SymbolDefInfo{
			{
				Symbol:    "scip-go gomod example.com/test newFunc().",
				Name:      "newFunc",
				Kind:      "Function",
				StartLine: 1,
				EndLine:   5,
			},
		},
	}

	mapper := NewDiffSymbolMapper(index)
	diff := &impact.ParsedDiff{
		Files: []impact.ChangedFile{
			{
				NewPath: "new.go",
				IsNew:   true,
				Hunks: []impact.ChangedHunk{
					{
						NewStart: 1,
						NewLines: 5,
						Added:    []int{1, 2, 3, 4, 5},
					},
				},
			},
		},
	}

	result, err := mapper.MapToSymbols(diff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	if result[0].ChangeType != impact.ChangeAdded {
		t.Errorf("expected ChangeAdded, got %s", result[0].ChangeType)
	}
}

func TestDiffSymbolMapper_DeletedFile(t *testing.T) {
	mapper := NewDiffSymbolMapper(newMockIndex())
	diff := &impact.ParsedDiff{
		Files: []impact.ChangedFile{
			{
				OldPath: "deleted.go",
				Deleted: true,
				Hunks: []impact.ChangedHunk{
					{
						OldStart: 1,
						OldLines: 10,
						Removed:  []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
					},
				},
			},
		},
	}

	result, err := mapper.MapToSymbols(diff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// For deleted file not in index, we get a file-level entry
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	if result[0].ChangeType != impact.ChangeDeleted {
		t.Errorf("expected ChangeDeleted, got %s", result[0].ChangeType)
	}
}

func TestDiffSymbolMapper_Deduplication(t *testing.T) {
	index := newMockIndex()
	index.documents["main.go"] = &DocumentInfo{
		RelativePath: "main.go",
		Language:     "go",
		Symbols: []SymbolDefInfo{
			{
				Symbol:    "scip-go gomod example.com/test main.foo().",
				Name:      "foo",
				Kind:      "Function",
				StartLine: 1,
				EndLine:   20,
			},
		},
	}

	mapper := NewDiffSymbolMapper(index)
	diff := &impact.ParsedDiff{
		Files: []impact.ChangedFile{
			{
				NewPath: "main.go",
				Hunks: []impact.ChangedHunk{
					{
						NewStart: 5,
						NewLines: 2,
						Added:    []int{5, 6},
					},
					{
						NewStart: 15,
						NewLines: 2,
						Added:    []int{15, 16},
					},
				},
			},
		},
	}

	result, err := mapper.MapToSymbols(diff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both hunks touch the same symbol - should be deduplicated
	if len(result) != 1 {
		t.Fatalf("expected 1 deduplicated result, got %d", len(result))
	}

	// Lines should be merged
	if len(result[0].Lines) != 4 {
		t.Errorf("expected 4 merged lines, got %d", len(result[0].Lines))
	}
}

func TestExtractSymbolName(t *testing.T) {
	tests := []struct {
		symbolID string
		expected string
	}{
		{"scip-go gomod example.com/pkg Foo().", "Foo"},
		{"scip-go gomod example.com/pkg Bar.", "Bar"},
		{"scip-go gomod example.com/pkg helper", "helper"},
		{"simple", "simple"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.symbolID, func(t *testing.T) {
			result := extractSymbolName(tt.symbolID)
			if result != tt.expected {
				t.Errorf("extractSymbolName(%q) = %q, want %q", tt.symbolID, result, tt.expected)
			}
		})
	}
}

func TestMergeLines(t *testing.T) {
	tests := []struct {
		a, b     []int
		expected []int
	}{
		{[]int{1, 2, 3}, []int{4, 5, 6}, []int{1, 2, 3, 4, 5, 6}},
		{[]int{1, 3, 5}, []int{2, 3, 4}, []int{1, 2, 3, 4, 5}},
		{[]int{}, []int{1, 2}, []int{1, 2}},
		{[]int{1, 2}, []int{}, []int{1, 2}},
		{[]int{}, []int{}, []int{}},
	}

	for _, tt := range tests {
		result := mergeLines(tt.a, tt.b)
		if len(result) != len(tt.expected) {
			t.Errorf("mergeLines(%v, %v) = %v, want %v", tt.a, tt.b, result, tt.expected)
			continue
		}
		for i, v := range result {
			if v != tt.expected[i] {
				t.Errorf("mergeLines(%v, %v) = %v, want %v", tt.a, tt.b, result, tt.expected)
				break
			}
		}
	}
}

func TestUniqueLines(t *testing.T) {
	tests := []struct {
		input    []int
		expected []int
	}{
		{[]int{3, 1, 2, 1, 3}, []int{1, 2, 3}},
		{[]int{5, 5, 5}, []int{5}},
		{[]int{}, []int{}},
		{nil, nil},
	}

	for _, tt := range tests {
		result := uniqueLines(tt.input)
		if len(result) != len(tt.expected) {
			t.Errorf("uniqueLines(%v) = %v, want %v", tt.input, result, tt.expected)
			continue
		}
		for i, v := range result {
			if v != tt.expected[i] {
				t.Errorf("uniqueLines(%v) = %v, want %v", tt.input, result, tt.expected)
				break
			}
		}
	}
}
