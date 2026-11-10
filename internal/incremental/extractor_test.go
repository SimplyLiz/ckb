package incremental

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/project"
)

func TestIsLocalSymbol(t *testing.T) {
	tests := []struct {
		symbolID string
		expected bool
	}{
		{"local 0", true},
		{"local 123", true},
		{"scip-go gomod example.com/foo 1.0 pkg.Func().", false},
		{"", false},
		{"loca", false},  // Too short to be "local "
		{"local", false}, // Missing space
	}

	for _, tc := range tests {
		t.Run(tc.symbolID, func(t *testing.T) {
			result := isLocalSymbol(tc.symbolID)
			if result != tc.expected {
				t.Errorf("isLocalSymbol(%q) = %v, want %v", tc.symbolID, result, tc.expected)
			}
		})
	}
}

func TestExtractSymbolName(t *testing.T) {
	tests := []struct {
		name        string
		symbolID    string
		displayName string
		expected    string
	}{
		{
			name:        "uses displayName if provided",
			symbolID:    "scip-go gomod example.com/foo 1.0 pkg.Func().",
			displayName: "Func",
			expected:    "Func",
		},
		{
			name:        "extracts from symbolID without displayName",
			symbolID:    "scip-go gomod example.com/foo 1.0 pkg.Func().",
			displayName: "",
			expected:    "Func",
		},
		{
			name:        "handles method names",
			symbolID:    "scip-go gomod example.com/foo 1.0 pkg.Type.Method().",
			displayName: "",
			expected:    "Method",
		},
		{
			name:        "handles type names",
			symbolID:    "scip-go gomod example.com/foo 1.0 pkg.Type.",
			displayName: "",
			expected:    "Type",
		},
		{
			name:        "handles short symbolID",
			symbolID:    "short",
			displayName: "",
			expected:    "short",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := extractSymbolName(tc.symbolID, tc.displayName)
			if result != tc.expected {
				t.Errorf("extractSymbolName(%q, %q) = %q, want %q",
					tc.symbolID, tc.displayName, result, tc.expected)
			}
		})
	}
}

// The numbers are SCIP's SymbolInformation.Kind, not LSP's SymbolKind: 6 is
// Boolean and 12 EnumMember, Function is 17 and Method 26.
func TestMapSymbolKind(t *testing.T) {
	tests := []struct {
		kind     int32
		expected string
	}{
		{0, "unknown"},
		{6, "boolean"},
		{7, "class"},
		{9, "constructor"},
		{12, "enummember"},
		{17, "function"},
		{26, "method"},
		{49, "struct"},
		{80, "staticmethod"},
		{999, "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			result := mapSymbolKind(tc.kind)
			if result != tc.expected {
				t.Errorf("mapSymbolKind(%d) = %q, want %q", tc.kind, result, tc.expected)
			}
		})
	}
}

func TestNewSCIPExtractor(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Test with relative path
	ext := NewSCIPExtractor("/repo", ".scip/index.scip", logger)
	if ext == nil {
		t.Fatal("expected non-nil extractor")
	}
	if ext.repoRoot != "/repo" {
		t.Errorf("expected repoRoot '/repo', got %q", ext.repoRoot)
	}
	expectedPath := filepath.Join("/repo", ".scip/index.scip")
	if ext.indexPath != expectedPath {
		t.Errorf("expected indexPath %q, got %q", expectedPath, ext.indexPath)
	}
}

func TestNewSCIPExtractor_AbsolutePath(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Test with absolute path
	ext := NewSCIPExtractor("/repo", "/custom/path/index.scip", logger)
	if ext.indexPath != "/custom/path/index.scip" {
		t.Errorf("expected absolute path to be preserved, got %q", ext.indexPath)
	}
}

func TestHashFile_Extractor(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "extractor-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.go")
	content := []byte("package main\n\nfunc main() {}\n")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	hash1, err := hashFile(testFile)
	if err != nil {
		t.Fatalf("hashFile failed: %v", err)
	}
	if hash1 == "" {
		t.Error("expected non-empty hash")
	}

	// Same content, same hash
	hash2, err := hashFile(testFile)
	if err != nil {
		t.Fatalf("hashFile failed: %v", err)
	}
	if hash1 != hash2 {
		t.Error("expected same hash for same file")
	}

	// Different content, different hash
	if err := os.WriteFile(testFile, []byte("different content"), 0644); err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}
	hash3, err := hashFile(testFile)
	if err != nil {
		t.Fatalf("hashFile failed: %v", err)
	}
	if hash1 == hash3 {
		t.Error("expected different hash for different content")
	}
}

func TestHashFile_Extractor_NonExistent(t *testing.T) {
	_, err := hashFile("/nonexistent/file.go")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestComputeDocHash(t *testing.T) {
	doc1 := &scip.Document{
		RelativePath: "main.go",
		Occurrences: []*scip.Occurrence{
			{Symbol: "pkg.Foo", Range: []int32{10, 0, 10, 3}, SymbolRoles: 1},
		},
		Symbols: []*scip.SymbolInformation{
			{Symbol: "pkg.Foo", Kind: 12},
		},
	}

	hash1 := computeDocHash(doc1)
	if hash1 == "" {
		t.Error("expected non-empty hash")
	}
	if len(hash1) != 16 {
		t.Errorf("expected 16-char hash, got %d chars", len(hash1))
	}

	// Same document, same hash
	hash2 := computeDocHash(doc1)
	if hash1 != hash2 {
		t.Error("expected same hash for same document")
	}

	// Different document, different hash
	doc2 := &scip.Document{
		RelativePath: "main.go",
		Occurrences: []*scip.Occurrence{
			{Symbol: "pkg.Bar", Range: []int32{10, 0, 10, 3}, SymbolRoles: 1},
		},
	}
	hash3 := computeDocHash(doc2)
	if hash1 == hash3 {
		t.Error("expected different hash for different document")
	}

	// Different path, different hash
	doc3 := &scip.Document{
		RelativePath: "other.go",
		Occurrences: []*scip.Occurrence{
			{Symbol: "pkg.Foo", Range: []int32{10, 0, 10, 3}, SymbolRoles: 1},
		},
	}
	hash4 := computeDocHash(doc3)
	if hash1 == hash4 {
		t.Error("expected different hash for different path")
	}
}

func TestExtractFileDelta(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "extractor-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test file
	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ext := NewSCIPExtractor(tmpDir, ".scip/index.scip", logger)

	// Create a mock SCIP document
	doc := &scip.Document{
		RelativePath: "main.go",
		Language:     "go",
		Occurrences: []*scip.Occurrence{
			// Definition
			{Symbol: "scip-go gomod example 1.0 main.main().", Range: []int32{5, 5, 5, 9}, SymbolRoles: 1},
			// Reference
			{Symbol: "scip-go gomod fmt 1.0 fmt.Println().", Range: []int32{6, 1, 6, 8}, SymbolRoles: 0},
			// Local symbol (should be skipped)
			{Symbol: "local 0", Range: []int32{7, 1, 7, 5}, SymbolRoles: 1},
		},
		Symbols: []*scip.SymbolInformation{
			{Symbol: "scip-go gomod example 1.0 main.main().", DisplayName: "main", Kind: 17},
		},
	}

	change := ChangedFile{
		Path:       "main.go",
		ChangeType: ChangeAdded,
	}

	delta := ext.extractFileDelta(doc, change)

	if delta.Path != "main.go" {
		t.Errorf("expected path 'main.go', got %q", delta.Path)
	}
	if delta.ChangeType != ChangeAdded {
		t.Errorf("expected change type 'added', got %q", delta.ChangeType)
	}

	// Should have 1 symbol (local symbol filtered out)
	if len(delta.Symbols) != 1 {
		t.Errorf("expected 1 symbol, got %d", len(delta.Symbols))
	}
	if len(delta.Symbols) > 0 {
		sym := delta.Symbols[0]
		if sym.Name != "main" {
			t.Errorf("expected symbol name 'main', got %q", sym.Name)
		}
		if sym.Kind != "function" {
			t.Errorf("expected kind 'function', got %q", sym.Kind)
		}
		if sym.StartLine != 6 { // SCIP is 0-indexed, we use 1-indexed
			t.Errorf("expected StartLine 6, got %d", sym.StartLine)
		}
	}

	// Should have 1 reference (local symbols filtered out)
	if len(delta.Refs) != 1 {
		t.Errorf("expected 1 reference, got %d", len(delta.Refs))
	}
}

func TestExtractFileDelta_Rename(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "extractor-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test file at the new path
	testFile := filepath.Join(tmpDir, "new.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ext := NewSCIPExtractor(tmpDir, ".scip/index.scip", logger)

	doc := &scip.Document{
		RelativePath: "new.go",
		Language:     "go",
		Occurrences:  []*scip.Occurrence{},
	}

	change := ChangedFile{
		Path:       "new.go",
		OldPath:    "old.go",
		ChangeType: ChangeRenamed,
	}

	delta := ext.extractFileDelta(doc, change)

	if delta.Path != "new.go" {
		t.Errorf("expected path 'new.go', got %q", delta.Path)
	}
	if delta.OldPath != "old.go" {
		t.Errorf("expected oldPath 'old.go', got %q", delta.OldPath)
	}
	if delta.ChangeType != ChangeRenamed {
		t.Errorf("expected change type 'renamed', got %q", delta.ChangeType)
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config == nil {
		t.Fatal("expected non-nil config")
	}
	if config.IndexPath != "index.scip" {
		t.Errorf("expected IndexPath 'index.scip', got %q", config.IndexPath)
	}
	if config.IncrementalThreshold != 50 {
		t.Errorf("expected IncrementalThreshold 50, got %d", config.IncrementalThreshold)
	}
	if config.IndexTests {
		t.Error("expected IndexTests=false by default")
	}
}

// v1.1 Callgraph Tests

func TestIsCallableReference(t *testing.T) {
	symbolInfo := map[string]*scip.SymbolInformation{
		"rust-analyzer cargo demo 0.1.0 run.":      {Kind: 17}, // Function, ID without "()"
		"rust-analyzer cargo demo 0.1.0 S#go.":     {Kind: 26}, // Method
		"rust-analyzer cargo demo 0.1.0 Mode#Off.": {Kind: 12}, // EnumMember, LSP's Function
		"rust-analyzer cargo demo 0.1.0 FLAG.":     {Kind: 6},  // Boolean, LSP's Method
		"scip-go gomod example 1.0 pkg.NoKind().":  {Kind: 0},
	}

	tests := []struct {
		name     string
		symbolID string
		expected bool
	}{
		{"function by Kind", "rust-analyzer cargo demo 0.1.0 run.", true},
		{"method by Kind", "rust-analyzer cargo demo 0.1.0 S#go.", true},
		{"enum member is not called", "rust-analyzer cargo demo 0.1.0 Mode#Off.", false},
		{"boolean is not called", "rust-analyzer cargo demo 0.1.0 FLAG.", false},
		{"no Kind falls back to the ID", "scip-go gomod example 1.0 pkg.NoKind().", true},
		{"unknown target, function ID", "scip-go gomod example 1.0 other.Func().", true},
		{"unknown target, value ID", "scip-go gomod example 1.0 other.Variable.", false},
		{"parameter of a function", "scip-typescript npm demo 1.0.0 src/`a.ts`/run().(x)", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCallableReference(tc.symbolID, symbolInfo); got != tc.expected {
				t.Errorf("isCallableReference(%q) = %v, want %v", tc.symbolID, got, tc.expected)
			}
		})
	}
}

func TestResolveCallerSymbol(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ext := NewSCIPExtractor("/repo", ".scip/index.scip", logger)

	symbols := []Symbol{
		{ID: "scip-go gomod example 1.0 pkg.init().", Name: "init", Kind: "function", Callable: true, StartLine: 5, EndLine: 10},
		{ID: "scip-go gomod example 1.0 pkg.main().", Name: "main", Kind: "function", Callable: true, StartLine: 12, EndLine: 30},
		{ID: "scip-go gomod example 1.0 pkg.helper().", Name: "helper", Kind: "function", Callable: true, StartLine: 32, EndLine: 40},
		{ID: "scip-go gomod example 1.0 pkg.Type.", Name: "Type", Kind: "struct", StartLine: 1, EndLine: 3}, // Not a callable
	}

	tests := []struct {
		name     string
		callLine int
		expected string
	}{
		{"call in init", 7, "scip-go gomod example 1.0 pkg.init()."},
		{"call in main start", 12, "scip-go gomod example 1.0 pkg.main()."},
		{"call in main middle", 20, "scip-go gomod example 1.0 pkg.main()."},
		{"call in main end", 30, "scip-go gomod example 1.0 pkg.main()."},
		{"call in helper", 35, "scip-go gomod example 1.0 pkg.helper()."},
		{"call before any function", 1, ""},  // Should return empty (top-level or struct def line)
		{"call after all functions", 50, ""}, // Should return empty
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ext.resolveCallerSymbol(symbols, tc.callLine)
			if result != tc.expected {
				t.Errorf("resolveCallerSymbol(..., %d) = %q, want %q", tc.callLine, result, tc.expected)
			}
		})
	}
}

func TestResolveCallerSymbol_InferredEndLine(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ext := NewSCIPExtractor("/repo", ".scip/index.scip", logger)

	// Symbols without explicit EndLine (EndLine == StartLine or 0)
	symbols := []Symbol{
		{ID: "scip-go gomod example 1.0 pkg.first().", Name: "first", Kind: "function", Callable: true, StartLine: 10, EndLine: 10},
		{ID: "scip-go gomod example 1.0 pkg.second().", Name: "second", Kind: "function", Callable: true, StartLine: 50, EndLine: 50},
	}

	tests := []struct {
		name     string
		callLine int
		expected string
	}{
		// First function's inferred end is second's start - 1 = 49
		{"call in first function", 25, "scip-go gomod example 1.0 pkg.first()."},
		{"call at inferred boundary", 49, "scip-go gomod example 1.0 pkg.first()."},
		// Second function uses default 500 lines
		{"call in second function", 100, "scip-go gomod example 1.0 pkg.second()."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ext.resolveCallerSymbol(symbols, tc.callLine)
			if result != tc.expected {
				t.Errorf("resolveCallerSymbol(..., %d) = %q, want %q", tc.callLine, result, tc.expected)
			}
		})
	}
}

func TestExtractFileDelta_CallEdges(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "extractor-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ext := NewSCIPExtractor(tmpDir, ".scip/index.scip", logger)

	// Create a document with a function definition and a call
	doc := &scip.Document{
		RelativePath: "main.go",
		Language:     "go",
		Occurrences: []*scip.Occurrence{
			// Definition of main.main
			{Symbol: "scip-go gomod example 1.0 main.main().", Range: []int32{5, 5, 5, 9}, SymbolRoles: 1},
			// Call to fmt.Println from within main
			{Symbol: "scip-go gomod fmt 1.0 fmt.Println().", Range: []int32{7, 1, 7, 12}, SymbolRoles: 0},
			// Reference to a non-callable (should not create call edge)
			{Symbol: "scip-go gomod example 1.0 pkg.Variable.", Range: []int32{8, 1, 8, 8}, SymbolRoles: 0},
		},
		Symbols: []*scip.SymbolInformation{
			{Symbol: "scip-go gomod example 1.0 main.main().", DisplayName: "main", Kind: 17},
			{Symbol: "scip-go gomod fmt 1.0 fmt.Println().", DisplayName: "Println", Kind: 17},
		},
	}

	change := ChangedFile{
		Path:       "main.go",
		ChangeType: ChangeAdded,
	}

	delta := ext.extractFileDelta(doc, change)

	// Should have 1 call edge (to fmt.Println)
	if len(delta.CallEdges) != 1 {
		t.Fatalf("expected 1 call edge, got %d", len(delta.CallEdges))
	}

	edge := delta.CallEdges[0]
	if edge.CalleeID != "scip-go gomod fmt 1.0 fmt.Println()." {
		t.Errorf("expected callee 'fmt.Println', got %q", edge.CalleeID)
	}
	if edge.CallerFile != "main.go" {
		t.Errorf("expected caller file 'main.go', got %q", edge.CallerFile)
	}
	if edge.Line != 8 { // SCIP 0-indexed, we use 1-indexed
		t.Errorf("expected line 8, got %d", edge.Line)
	}
	// CallerID should be resolved to main.main since the call is on line 8, within main (lines 6-end)
	if edge.CallerID != "scip-go gomod example 1.0 main.main()." {
		t.Errorf("expected caller 'main.main', got %q", edge.CallerID)
	}
}

// rust-analyzer states a Kind for every symbol, and its function IDs carry no
// "()": `fn run` is "run.". Calls to it are edges, a reference to an enum
// member is not, and the caller is the function whose EnclosingRange holds the
// call, not whatever definition happens to precede it.
func TestExtractFileDelta_RustKinds(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "lib.rs"), []byte("fn main() {}"), 0644); err != nil {
		t.Fatal(err)
	}
	ext := NewSCIPExtractor(tmpDir, ".scip/index.scip", slog.New(slog.NewTextHandler(io.Discard, nil)))

	pkg := "rust-analyzer cargo demo 0.1.0 "
	outer, run, helper, off := pkg+"outer.", pkg+"run.", pkg+"helper.", pkg+"Mode#Off."
	doc := &scip.Document{
		RelativePath: "lib.rs",
		Occurrences: []*scip.Occurrence{
			{Symbol: outer, Range: []int32{0, 3, 8}, EnclosingRange: []int32{0, 0, 3, 1}, SymbolRoles: 1},
			{Symbol: run, Range: []int32{1, 4}, SymbolRoles: 0},
			{Symbol: off, Range: []int32{2, 4, 13}, SymbolRoles: 0},
			{Symbol: helper, Range: []int32{5, 3, 9}, EnclosingRange: []int32{5, 0, 7, 1}, SymbolRoles: 1},
			{Symbol: run, Range: []int32{6, 4, 7}, SymbolRoles: 0},
			// A top-level item after helper: no function encloses it.
			{Symbol: run, Range: []int32{20, 4, 7}, SymbolRoles: 0},
		},
		Symbols: []*scip.SymbolInformation{
			{Symbol: outer, Kind: 17},
			{Symbol: helper, Kind: 17},
			{Symbol: run, Kind: 17},
			{Symbol: off, Kind: 12},
		},
	}

	delta := ext.extractFileDelta(doc, ChangedFile{Path: "lib.rs", ChangeType: ChangeAdded})

	type edge struct {
		caller string
		line   int
	}
	var got []edge
	for _, e := range delta.CallEdges {
		if e.CalleeID != run {
			t.Errorf("edge to %q, only calls to run are calls", e.CalleeID)
		}
		got = append(got, edge{e.CallerID, e.Line})
	}
	want := []edge{{outer, 2}, {helper, 7}, {"", 21}}
	if len(got) != len(want) {
		t.Fatalf("edges = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("edge %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// `export const save = () => persist()` is a term ("save.") to SCIP, without a
// Kind; scip-typescript marks it as a function with an EnclosingRange that
// starts after the name. It is the caller of persist, and a call to it from
// the same file is an edge.
func TestExtractFileDelta_FunctionValuedConstant(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "a.ts"), []byte("export const save = () => persist()"), 0644); err != nil {
		t.Fatal(err)
	}
	ext := NewSCIPExtractor(tmpDir, ".scip/index.scip", slog.New(slog.NewTextHandler(io.Discard, nil)))

	pkg := "scip-typescript npm demo 1.0.0 src/`a.ts`/"
	save, persist, limit, run := pkg+"save.", pkg+"persist().", pkg+"LIMIT.", pkg+"run()."
	doc := &scip.Document{
		RelativePath: "a.ts",
		Occurrences: []*scip.Occurrence{
			{Symbol: save, Range: []int32{0, 13, 17}, EnclosingRange: []int32{0, 20, 2, 1}, SymbolRoles: 1},
			{Symbol: persist, Range: []int32{1, 2, 9}, SymbolRoles: 0},
			{Symbol: limit, Range: []int32{3, 13, 18}, SymbolRoles: 1},
			{Symbol: run, Range: []int32{4, 9, 12}, EnclosingRange: []int32{4, 0, 7, 1}, SymbolRoles: 1},
			{Symbol: save, Range: []int32{5, 2, 6}, SymbolRoles: 0},
			{Symbol: limit, Range: []int32{6, 2, 7}, SymbolRoles: 0},
		},
	}

	delta := ext.extractFileDelta(doc, ChangedFile{Path: "a.ts", ChangeType: ChangeAdded})

	got := map[string]string{}
	for _, e := range delta.CallEdges {
		got[e.CalleeID] = e.CallerID
	}
	if got[persist] != save {
		t.Errorf("caller of persist = %q, want save", got[persist])
	}
	if c, ok := got[save]; !ok || c != run {
		t.Errorf("call to save: caller %q (edge %v), want run", c, ok)
	}
	if _, ok := got[limit]; ok {
		t.Errorf("LIMIT is a value, got a call edge to it")
	}
}

// Multi-language support tests (v7.6)

func TestIsIndexerInstalled(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ext := NewSCIPExtractor("/repo", ".scip/index.scip", logger)

	tests := []struct {
		name   string
		lang   project.Language
		exists bool // Whether the test expects the indexer to exist (may vary by environment)
	}{
		// These tests check that IsIndexerInstalled doesn't panic
		// Actual availability depends on the test environment
		{"Go", project.LangGo, false}, // scip-go typically not in test env
		{"TypeScript", project.LangTypeScript, false},
		{"Python", project.LangPython, false},
		{"Dart", project.LangDart, false},
		{"Rust", project.LangRust, false},
		{"Java", project.LangJava, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := project.GetIndexerConfig(tt.lang)
			if config == nil {
				t.Skipf("no indexer config for %s", tt.lang)
			}

			// Should not panic
			_ = ext.IsIndexerInstalled(config)
		})
	}
}

func TestRunIndexer_CreatesOutputDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "extractor-runindexer-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use a nested output path that doesn't exist
	outputPath := filepath.Join(tmpDir, "nested", "deep", "index.scip")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ext := NewSCIPExtractor(tmpDir, outputPath, logger)

	config := project.GetIndexerConfig(project.LangGo)
	if config == nil {
		t.Skip("no Go indexer config")
	}

	// RunIndexer should create the output directory
	// (It will fail because scip-go isn't installed, but the directory should be created first)
	_ = ext.RunIndexer(config)

	// Check that the directory was created
	outputDir := filepath.Dir(outputPath)
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		t.Errorf("expected output directory %s to be created", outputDir)
	}
}

func TestRunSCIPGo_Deprecated(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "extractor-runscipgo-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ext := NewSCIPExtractor(tmpDir, ".scip/index.scip", logger)

	// RunSCIPGo should call RunIndexer internally
	// It will fail because scip-go isn't installed, but it shouldn't panic
	err = ext.RunSCIPGo()

	// We expect an error (indexer not found or similar)
	if err == nil {
		t.Skip("scip-go is installed, test passes")
	}

	// Error should be about the indexer failing, not a nil pointer or similar
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

func TestGetIndexerConfigForAllLanguages(t *testing.T) {
	// Verify that GetIndexerConfig returns correct configs for supported languages
	supportedLangs := []project.Language{
		project.LangGo,
		project.LangTypeScript,
		project.LangJavaScript,
		project.LangPython,
		project.LangDart,
		project.LangRust,
	}

	for _, lang := range supportedLangs {
		t.Run(string(lang), func(t *testing.T) {
			config := project.GetIndexerConfig(lang)
			if config == nil {
				t.Errorf("expected non-nil config for %s", lang)
				return
			}

			if config.Cmd == "" {
				t.Errorf("expected non-empty Cmd for %s", lang)
			}

			if !config.SupportsIncremental {
				t.Errorf("expected SupportsIncremental=true for %s", lang)
			}
		})
	}

	// Verify unsupported languages
	unsupportedLangs := []project.Language{
		project.LangJava,
		project.LangKotlin,
		project.LangCpp,
		project.LangRuby,
		project.LangCSharp,
		project.LangPHP,
	}

	for _, lang := range unsupportedLangs {
		t.Run(string(lang)+"_no_incremental", func(t *testing.T) {
			config := project.GetIndexerConfig(lang)
			if config == nil {
				t.Skipf("no config for %s", lang)
			}

			if config.SupportsIncremental {
				t.Errorf("expected SupportsIncremental=false for %s", lang)
			}
		})
	}
}

func TestIndexerConfigBuildCommand(t *testing.T) {
	tests := []struct {
		name       string
		lang       project.Language
		outputPath string
		wantCmd    string
	}{
		{
			name:       "Go with output",
			lang:       project.LangGo,
			outputPath: "/tmp/index.scip",
			wantCmd:    "scip-go",
		},
		{
			name:       "TypeScript with output",
			lang:       project.LangTypeScript,
			outputPath: "/tmp/index.scip",
			wantCmd:    "scip-typescript",
		},
		{
			name:       "Rust (fixed output)",
			lang:       project.LangRust,
			outputPath: "/tmp/index.scip",
			wantCmd:    "rust-analyzer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := project.GetIndexerConfig(tt.lang)
			if config == nil {
				t.Skipf("no config for %s", tt.lang)
			}

			cmd := config.BuildCommand(tt.outputPath)
			if cmd == nil {
				t.Fatal("expected non-nil command")
			}

			// Check command name - it may be the full path or just the command name
			if len(cmd.Args) == 0 {
				t.Fatal("expected non-empty Args")
			}
			cmdName := cmd.Args[0]
			// The command might be the full path (e.g., /Users/.../scip-go) or just the name
			if !strings.HasSuffix(cmdName, tt.wantCmd) && cmdName != tt.wantCmd {
				t.Errorf("expected command ending with %s, got %s", tt.wantCmd, cmdName)
			}
		})
	}
}

func TestRunIndexer_FixedOutputPath(t *testing.T) {
	// Test that RunIndexer handles fixed output indexers (like rust-analyzer)
	// by moving the file from the fixed location to the configured path
	tmpDir, err := os.MkdirTemp("", "extractor-fixedoutput-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a mock "fixed output" file at index.scip (where rust-analyzer outputs)
	fixedOutputPath := filepath.Join(tmpDir, "index.scip")
	if err := os.WriteFile(fixedOutputPath, []byte("mock scip index"), 0644); err != nil {
		t.Fatalf("failed to create mock fixed output: %v", err)
	}

	// Configure extractor to use a different output path
	configuredOutput := filepath.Join(tmpDir, ".scip", "index.scip")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ext := NewSCIPExtractor(tmpDir, configuredOutput, logger)

	// Create a mock IndexerConfig that simulates a fixed-output indexer
	// We can't actually run rust-analyzer, so we'll test the file move logic directly
	config := &project.IndexerConfig{
		Cmd:         "true", // Unix true command always succeeds
		FixedOutput: "index.scip",
	}

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(configuredOutput), 0755); err != nil {
		t.Fatalf("failed to create output dir: %v", err)
	}

	// Run the indexer - it will run "true" (which does nothing) and then
	// should move the fixed output file
	err = ext.RunIndexer(config)
	if err != nil {
		t.Fatalf("RunIndexer failed: %v", err)
	}

	// Verify the file was moved
	if _, err := os.Stat(configuredOutput); os.IsNotExist(err) {
		t.Errorf("expected file to be moved to %s", configuredOutput)
	}

	// Verify original location no longer has the file
	if _, err := os.Stat(fixedOutputPath); !os.IsNotExist(err) {
		t.Errorf("expected file to be removed from %s", fixedOutputPath)
	}
}

func TestRunIndexer_FixedOutputSamePath(t *testing.T) {
	// Test that when fixed output path matches configured path, no move is needed
	tmpDir, err := os.MkdirTemp("", "extractor-samepath-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use the fixed output location as the configured output
	fixedOutputPath := filepath.Join(tmpDir, "index.scip")
	if err := os.WriteFile(fixedOutputPath, []byte("mock scip index"), 0644); err != nil {
		t.Fatalf("failed to create mock fixed output: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ext := NewSCIPExtractor(tmpDir, fixedOutputPath, logger)

	config := &project.IndexerConfig{
		Cmd:         "true",
		FixedOutput: "index.scip",
	}

	err = ext.RunIndexer(config)
	if err != nil {
		t.Fatalf("RunIndexer failed: %v", err)
	}

	// File should still exist at the same location
	if _, err := os.Stat(fixedOutputPath); os.IsNotExist(err) {
		t.Errorf("expected file to remain at %s", fixedOutputPath)
	}
}

func TestRunIndexer_CommandFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "extractor-cmdfail-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ext := NewSCIPExtractor(tmpDir, filepath.Join(tmpDir, ".scip", "index.scip"), logger)

	config := &project.IndexerConfig{
		Cmd:        "false", // Unix false command always fails
		OutputFlag: "--output",
	}

	err = ext.RunIndexer(config)
	if err == nil {
		t.Fatal("expected RunIndexer to fail for 'false' command")
	}

	if !strings.Contains(err.Error(), "indexer failed") {
		t.Errorf("expected 'indexer failed' in error, got: %v", err)
	}
}

func TestRunIndexer_OutputDirectoryCreationFailure(t *testing.T) {
	// Test error handling when output directory can't be created
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Use a path that can't be created (nested under a file)
	tmpDir, err := os.MkdirTemp("", "extractor-dirfail-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a file where we want to create a directory
	blockingFile := filepath.Join(tmpDir, "blocker")
	if err := os.WriteFile(blockingFile, []byte("I'm a file"), 0644); err != nil {
		t.Fatalf("failed to create blocking file: %v", err)
	}

	// Try to use a path nested under the file
	invalidOutput := filepath.Join(blockingFile, "nested", "index.scip")
	ext := NewSCIPExtractor(tmpDir, invalidOutput, logger)

	config := &project.IndexerConfig{
		Cmd:        "true",
		OutputFlag: "--output",
	}

	err = ext.RunIndexer(config)
	if err == nil {
		t.Fatal("expected RunIndexer to fail when directory can't be created")
	}

	if !strings.Contains(err.Error(), "failed to create index directory") {
		t.Errorf("expected 'failed to create index directory' in error, got: %v", err)
	}
}
