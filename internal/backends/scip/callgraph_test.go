package scip

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/config"
)

// findRepoRoot finds the repository root by looking for go.mod
func findRepoRoot() (string, error) {
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
			return "", fmt.Errorf("could not find go.mod")
		}
		dir = parent
	}
}

func TestFindCallers(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Skipf("Could not find repo root: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.RepoRoot = repoRoot

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	adapter, err := NewSCIPAdapter(cfg, logger)
	if err != nil {
		t.Skipf("SCIP adapter not available: %v", err)
	}

	if !adapter.IsAvailable() {
		t.Skip("SCIP index not available")
	}

	// Check index info
	indexInfo := adapter.GetIndexInfo()
	t.Logf("Index available: %v, docs: %d, symbols: %d",
		indexInfo.Available, indexInfo.DocumentCount, indexInfo.SymbolCount)

	// Find NewEngine via internal search
	var symbolId string
	results, err := adapter.index.SearchSymbols("NewEngine", SearchOptions{MaxResults: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	t.Logf("Found %d results for 'NewEngine'", len(results))
	for _, s := range results {
		t.Logf("  - %s: %s (kind=%s)", s.Name, s.StableId, s.Kind)
		if s.Name == "NewEngine" && s.Kind == KindFunction {
			symbolId = s.StableId
		}
	}

	if symbolId == "" {
		t.Skip("NewEngine function not found")
	}

	t.Logf("Testing FindCallers for: %s", symbolId)

	// Test callers
	callers, err := adapter.index.FindCallers(symbolId)
	if err != nil {
		t.Errorf("FindCallers error: %v", err)
	} else {
		t.Logf("Found %d callers:", len(callers))
		for _, c := range callers {
			t.Logf("  - %s (%s)", c.Name, c.SymbolID)
		}
	}

	// Test callees
	callees, err := adapter.index.FindCallees(symbolId)
	if err != nil {
		t.Errorf("FindCallees error: %v", err)
	} else {
		t.Logf("Found %d callees:", len(callees))
		for _, c := range callees {
			t.Logf("  - %s (%s)", c.Name, c.SymbolID)
		}
	}

	// Debug: Check raw occurrences for this symbol
	t.Log("\n=== Debug: Checking raw occurrences ===")
	for _, doc := range adapter.index.Documents {
		for _, occ := range doc.Occurrences {
			if occ.Symbol == symbolId {
				t.Logf("Occurrence in %s at line %d, roles=%d, enclosing=%v",
					doc.RelativePath, occ.Range[0], occ.SymbolRoles, occ.EnclosingRange)
			}
		}
	}

	// Debug: Check what EnclosingRange looks like for function defs
	t.Log("\n=== Debug: Sample function definitions with enclosing ranges ===")
	count := 0
	for _, doc := range adapter.index.Documents {
		if count >= 5 {
			break
		}
		for _, sym := range doc.Symbols {
			if mapSCIPKind(sym.Kind) == KindFunction {
				for _, occ := range doc.Occurrences {
					if occ.Symbol == sym.Symbol && occ.SymbolRoles&SymbolRoleDefinition != 0 {
						t.Logf("Function %s: range=%v, enclosing=%v",
							extractSymbolName(sym.Symbol), occ.Range, occ.EnclosingRange)
						count++
						break
					}
				}
			}
		}
	}

	// Debug: Check function ranges in engine_helper.go where NewEngine is called
	t.Log("\n=== Debug: Functions in cmd/ckb/engine_helper.go ===")
	for _, doc := range adapter.index.Documents {
		if doc.RelativePath == "cmd/ckb/engine_helper.go" {
			t.Logf("Document: %s, symbols: %d", doc.RelativePath, len(doc.Symbols))
			funcRanges := buildFunctionRanges(doc)
			t.Logf("Found %d function ranges", len(funcRanges))
			for sym, r := range funcRanges {
				t.Logf("  Function %s: lines %d-%d", extractSymbolName(sym), r.start, r.end)
			}
			// Also show symbol kinds and raw data
			for _, sym := range doc.Symbols {
				kind := mapSCIPKind(sym.Kind)
				t.Logf("  Symbol %s: rawKind=%d mapped=%s displayName=%q",
					sym.Symbol, sym.Kind, kind, sym.DisplayName)
			}
		}
	}
}

func TestCallGraph(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Skipf("Could not find repo root: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.RepoRoot = repoRoot

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	adapter, err := NewSCIPAdapter(cfg, logger)
	if err != nil {
		t.Skipf("SCIP adapter not available: %v", err)
	}

	if !adapter.IsAvailable() {
		t.Skip("SCIP index not available")
	}

	// Find a good function to test with
	results, err := adapter.index.SearchSymbols("NewEngine", SearchOptions{MaxResults: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	var symbolId string
	for _, s := range results {
		if s.Name == "NewEngine" && s.Kind == KindFunction {
			symbolId = s.StableId
			break
		}
	}

	if symbolId == "" {
		t.Skip("NewEngine function not found")
	}

	t.Logf("Testing call graph for: %s", symbolId)

	graph, err := adapter.BuildCallGraph(symbolId, CallGraphOptions{
		Direction: DirectionBoth,
		MaxDepth:  2,
		MaxNodes:  50,
	})

	if err != nil {
		t.Fatalf("BuildCallGraph error: %v", err)
	}

	t.Logf("Call graph: %d nodes, %d edges", len(graph.Nodes), len(graph.Edges))
	t.Logf("Direct callers: %d", len(graph.Callers))
	t.Logf("Direct callees: %d", len(graph.Callees))

	for _, caller := range graph.Callers {
		t.Logf("  Caller: %s", caller.Name)
	}
	for _, callee := range graph.Callees {
		t.Logf("  Callee: %s", callee.Name)
	}
}

func TestCountSymbolsByPath(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Skipf("Could not find repo root: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.RepoRoot = repoRoot

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	adapter, err := NewSCIPAdapter(cfg, logger)
	if err != nil {
		t.Skipf("SCIP adapter not available: %v", err)
	}

	if !adapter.IsAvailable() {
		t.Skip("SCIP index not available")
	}

	// Test symbol counting
	paths := []string{
		"internal/query",
		"internal/backends",
		"internal/mcp",
		"cmd/ckb",
		".",
	}
	for _, p := range paths {
		count := adapter.CountSymbolsByPath(p)
		t.Logf("Path %q: %d symbols", p, count)
	}

	// Also log some sample document paths
	t.Log("\n=== Sample document paths ===")
	indexInfo := adapter.GetIndexInfo()
	t.Logf("Total documents: %d", indexInfo.DocumentCount)

	// List a few document paths from the index
	for i, doc := range adapter.index.Documents {
		if i >= 10 {
			break
		}
		t.Logf("  Doc %d: %s (symbols: %d)", i, doc.RelativePath, len(doc.Symbols))
	}
}

func TestMapSCIPKind(t *testing.T) {
	tests := []struct {
		kind     int32
		expected SymbolKind
	}{
		{0, KindUnknown},    // Default case
		{1, KindUnknown},    // UnspecifiedSymbol
		{2, KindUnknown},    // Comment
		{3, KindPackage},    // Package
		{4, KindModule},     // PackageObject
		{5, KindClass},      // Class
		{6, KindClass},      // Object
		{7, KindInterface},  // Trait
		{8, KindMethod},     // TraitMethod
		{9, KindMethod},     // Method
		{10, KindFunction},  // Macro
		{11, KindType},      // Type
		{12, KindParameter}, // Parameter
		{13, KindParameter}, // SelfParameter
		{14, KindType},      // TypeParameter
		{15, KindVariable},  // Local
		{16, KindField},     // Field
		{17, KindInterface}, // Interface
		{18, KindFunction},  // Function
		{19, KindVariable},  // Variable
		{20, KindConstant},  // Constant
		{21, KindConstant},  // String
		{22, KindConstant},  // Number
		{23, KindConstant},  // Boolean
		{24, KindVariable},  // Array
		{25, KindNamespace}, // Namespace
		{26, KindConstant},  // Null
		{27, KindProperty},  // Property
		{28, KindEnum},      // Enum
		{29, KindConstant},  // EnumMember
		{30, KindClass},     // Struct
		{31, KindFunction},  // Event
		{32, KindFunction},  // Operator
		{33, KindMethod},    // Constructor
		{34, KindMethod},    // Destructor
		{99, KindUnknown},   // Unknown kind
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("kind_%d", tt.kind), func(t *testing.T) {
			result := mapSCIPKind(tt.kind)
			if result != tt.expected {
				t.Errorf("mapSCIPKind(%d) = %s, want %s", tt.kind, result, tt.expected)
			}
		})
	}
}

func TestExtractSymbolName(t *testing.T) {
	tests := []struct {
		name     string
		symbolId string
		expected string
	}{
		{
			name:     "function with parens",
			symbolId: "scip-go go ckb/internal/query NewEngine().",
			expected: "NewEngine",
		},
		{
			name:     "method on receiver",
			symbolId: "scip-go go ckb/internal/query Engine#Close().",
			expected: "Engine#Close", // includes receiver name
		},
		{
			name:     "type/struct",
			symbolId: "scip-go go ckb/internal/query Engine#",
			expected: "Engine#",
		},
		{
			name:     "field",
			symbolId: "scip-go go ckb/internal/query Engine#logger.",
			expected: "Engine#logger", // includes type name
		},
		{
			name:     "simple function",
			symbolId: "scip-go go fmt Printf().",
			expected: "Printf",
		},
		{
			name:     "empty string",
			symbolId: "",
			expected: "",
		},
		{
			name:     "single word",
			symbolId: "test",
			expected: "test",
		},
		// Regression cases below: real scip-go symbol IDs quote the full
		// module-relative package path in backticks inside the descriptor
		// itself (`github.com/org/repo/pkg`/Type#Method()., not split out
		// as a separate space-delimited field the way these simplified
		// fixtures above are). The old implementation looked for the last
		// "." anywhere in the descriptor to find a name separator, which
		// found the dot in "github.com" instead — e.g. ckb callgraph's
		// human/JSON output showed the node name as
		// "com/SimplyLiz/CodeMCP/internal/query`/Engine#buildModuleLevelResponse"
		// (the "github." prefix silently sliced off).
		{
			name:     "real scip-go method with dotted module path",
			symbolId: "scip-go gomod github.com/SimplyLiz/CodeMCP v0.0.0 `github.com/SimplyLiz/CodeMCP/internal/query`/Engine#buildModuleLevelResponse().",
			expected: "Engine#buildModuleLevelResponse",
		},
		{
			name:     "real scip-go bare type with dotted module path",
			symbolId: "scip-go gomod github.com/SimplyLiz/CodeMCP v0.0.0 `github.com/SimplyLiz/CodeMCP/internal/query`/Engine#",
			expected: "Engine#",
		},
		{
			name:     "real scip-go field with dotted module path",
			symbolId: "scip-go gomod github.com/SimplyLiz/CodeMCP v0.0.0 `github.com/SimplyLiz/CodeMCP/internal/query`/Engine#logger.",
			expected: "Engine#logger",
		},
		{
			name:     "real scip-go package-level function with dotted module path",
			symbolId: "scip-go gomod github.com/SimplyLiz/CodeMCP v0.0.0 `github.com/SimplyLiz/CodeMCP/internal/api`/NewServer().",
			expected: "NewServer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractSymbolName(tt.symbolId)
			if result != tt.expected {
				t.Errorf("extractSymbolName(%q) = %q, want %q", tt.symbolId, result, tt.expected)
			}
		})
	}
}

func TestCallGraphConstants(t *testing.T) {
	// Test DefaultMaxFunctionLines
	if DefaultMaxFunctionLines != 500 {
		t.Errorf("DefaultMaxFunctionLines = %d, expected 500", DefaultMaxFunctionLines)
	}

	// Test direction constants
	if DirectionCallers != "callers" {
		t.Errorf("DirectionCallers = %q, expected 'callers'", DirectionCallers)
	}
	if DirectionCallees != "callees" {
		t.Errorf("DirectionCallees = %q, expected 'callees'", DirectionCallees)
	}
	if DirectionBoth != "both" {
		t.Errorf("DirectionBoth = %q, expected 'both'", DirectionBoth)
	}
}

func TestCallGraphStructs(t *testing.T) {
	// Test CallGraphNode
	node := &CallGraphNode{
		SymbolID: "test:sym:1",
		Name:     "TestFunc",
		Kind:     KindFunction,
		Location: &Location{
			FileId:    "test.go",
			StartLine: 10,
		},
	}

	if node.SymbolID != "test:sym:1" {
		t.Errorf("node.SymbolID = %q, expected 'test:sym:1'", node.SymbolID)
	}
	if node.Name != "TestFunc" {
		t.Errorf("node.Name = %q, expected 'TestFunc'", node.Name)
	}
	if node.Kind != KindFunction {
		t.Errorf("node.Kind = %s, expected %s", node.Kind, KindFunction)
	}
	if node.Location.StartLine != 10 {
		t.Errorf("node.Location.StartLine = %d, expected 10", node.Location.StartLine)
	}

	// Test CallGraphEdge
	edge := CallGraphEdge{
		From: "caller:sym",
		To:   "callee:sym",
		Kind: "call",
	}

	if edge.From != "caller:sym" {
		t.Errorf("edge.From = %q, expected 'caller:sym'", edge.From)
	}
	if edge.To != "callee:sym" {
		t.Errorf("edge.To = %q, expected 'callee:sym'", edge.To)
	}
	if edge.Kind != "call" {
		t.Errorf("edge.Kind = %q, expected 'call'", edge.Kind)
	}

	// Test CallGraph
	graph := &CallGraph{
		Root:    node,
		Nodes:   map[string]*CallGraphNode{"test:sym:1": node},
		Edges:   []CallGraphEdge{edge},
		Callers: []*CallGraphNode{},
		Callees: []*CallGraphNode{node},
	}

	if graph.Root != node {
		t.Error("graph.Root not set correctly")
	}
	if len(graph.Nodes) != 1 {
		t.Errorf("len(graph.Nodes) = %d, expected 1", len(graph.Nodes))
	}
	if len(graph.Edges) != 1 {
		t.Errorf("len(graph.Edges) = %d, expected 1", len(graph.Edges))
	}
	if len(graph.Callees) != 1 {
		t.Errorf("len(graph.Callees) = %d, expected 1", len(graph.Callees))
	}
	if len(graph.Callers) != 0 {
		t.Errorf("len(graph.Callers) = %d, expected 0", len(graph.Callers))
	}
}

func TestCallGraphOptionsDefaults(t *testing.T) {
	// Test that BuildCallGraph handles default options correctly
	// Create an empty index
	idx := &SCIPIndex{
		Documents: []*Document{},
	}

	// Test with zero/negative MaxDepth (should default to 1)
	graph, err := idx.BuildCallGraph("test:sym", CallGraphOptions{
		Direction: DirectionBoth,
		MaxDepth:  0,
		MaxNodes:  0,
	})
	if err != nil {
		t.Fatalf("BuildCallGraph error: %v", err)
	}

	// Should still create a graph with root node
	if graph == nil {
		t.Fatal("Expected non-nil graph")
	}
	if graph.Root == nil {
		t.Fatal("Expected non-nil root")
	}
	if graph.Root.SymbolID != "test:sym" {
		t.Errorf("Root SymbolID = %q, expected 'test:sym'", graph.Root.SymbolID)
	}

	// Test with MaxDepth > 4 (should be clamped to 4)
	graph2, err := idx.BuildCallGraph("test:sym", CallGraphOptions{
		Direction: DirectionBoth,
		MaxDepth:  10,
		MaxNodes:  100,
	})
	if err != nil {
		t.Fatalf("BuildCallGraph error: %v", err)
	}
	if graph2 == nil {
		t.Fatal("Expected non-nil graph for clamped depth")
	}
}

func TestLineRange(t *testing.T) {
	// Test lineRange struct
	lr := lineRange{
		start: 10,
		end:   50,
	}

	if lr.start != 10 {
		t.Errorf("lr.start = %d, expected 10", lr.start)
	}
	if lr.end != 50 {
		t.Errorf("lr.end = %d, expected 50", lr.end)
	}
}

func TestBuildFunctionRangesEmpty(t *testing.T) {
	// Test with empty document
	doc := &Document{
		RelativePath: "test.go",
		Symbols:      []*SymbolInformation{},
		Occurrences:  []*Occurrence{},
	}

	ranges := buildFunctionRanges(doc)
	if len(ranges) != 0 {
		t.Errorf("Expected 0 ranges for empty document, got %d", len(ranges))
	}
}

func TestBuildFunctionRangesWithFunctions(t *testing.T) {
	// Test with document containing functions
	doc := &Document{
		RelativePath: "test.go",
		Symbols: []*SymbolInformation{
			{
				Symbol: "scip-go go test Func1().",
				Kind:   18, // Function
			},
			{
				Symbol: "scip-go go test Func2().",
				Kind:   18, // Function
			},
		},
		Occurrences: []*Occurrence{
			{
				Symbol:      "scip-go go test Func1().",
				Range:       []int32{10, 0, 10, 10},
				SymbolRoles: SymbolRoleDefinition,
			},
			{
				Symbol:      "scip-go go test Func2().",
				Range:       []int32{30, 0, 30, 10},
				SymbolRoles: SymbolRoleDefinition,
			},
		},
	}

	ranges := buildFunctionRanges(doc)
	if len(ranges) != 2 {
		t.Errorf("Expected 2 ranges, got %d", len(ranges))
	}

	// Func1 should end at line 29 (Func2 starts at 30)
	if r, ok := ranges["scip-go go test Func1()."]; ok {
		if r.start != 10 {
			t.Errorf("Func1 start = %d, expected 10", r.start)
		}
		if r.end != 29 {
			t.Errorf("Func1 end = %d, expected 29", r.end)
		}
	} else {
		t.Error("Func1 not found in ranges")
	}

	// Func2 should use default max lines
	if r, ok := ranges["scip-go go test Func2()."]; ok {
		if r.start != 30 {
			t.Errorf("Func2 start = %d, expected 30", r.start)
		}
		if r.end != 30+DefaultMaxFunctionLines {
			t.Errorf("Func2 end = %d, expected %d", r.end, 30+DefaultMaxFunctionLines)
		}
	} else {
		t.Error("Func2 not found in ranges")
	}
}

func TestFindCalleesNoDefinition(t *testing.T) {
	// Test FindCallees with a symbol that has no definition
	idx := &SCIPIndex{
		Documents: []*Document{
			{
				RelativePath: "test.go",
				Symbols:      []*SymbolInformation{},
				Occurrences:  []*Occurrence{},
			},
		},
	}

	callees, err := idx.FindCallees("nonexistent:sym")
	if err != nil {
		t.Fatalf("FindCallees error: %v", err)
	}
	if len(callees) != 0 {
		t.Errorf("Expected 0 callees for nonexistent symbol, got %d", len(callees))
	}
}

func TestFindCallersNoReferences(t *testing.T) {
	// Test FindCallers with a symbol that has no references
	idx := &SCIPIndex{
		Documents: []*Document{
			{
				RelativePath: "test.go",
				Symbols:      []*SymbolInformation{},
				Occurrences:  []*Occurrence{},
			},
		},
	}

	callers, err := idx.FindCallers("nonexistent:sym")
	if err != nil {
		t.Fatalf("FindCallers error: %v", err)
	}
	if len(callers) != 0 {
		t.Errorf("Expected 0 callers for nonexistent symbol, got %d", len(callers))
	}
}

func TestGetCallerCount(t *testing.T) {
	// Test GetCallerCount with empty index
	idx := &SCIPIndex{
		Documents: []*Document{},
	}

	count := idx.GetCallerCount("test:sym")
	if count != 0 {
		t.Errorf("GetCallerCount = %d, expected 0 for empty index", count)
	}
}

func TestGetCalleeCount(t *testing.T) {
	// Test GetCalleeCount with empty index
	idx := &SCIPIndex{
		Documents: []*Document{},
	}

	count := idx.GetCalleeCount("test:sym")
	if count != 0 {
		t.Errorf("GetCalleeCount = %d, expected 0 for empty index", count)
	}
}

func TestIsFunctionSymbol(t *testing.T) {
	tests := []struct {
		name     string
		symbolId string
		want     bool
	}{
		// Functions should return true
		{
			name:     "function",
			symbolId: "scip-go go ckb/internal/query NewEngine().",
			want:     true,
		},
		{
			name:     "method",
			symbolId: "scip-go go ckb/internal/query Engine#Close().",
			want:     true,
		},
		{
			name:     "function with params",
			symbolId: "scip-go go fmt Printf().",
			want:     true,
		},
		// Non-functions should return false
		{
			name:     "type/struct",
			symbolId: "scip-go go ckb/internal/query Engine#",
			want:     false,
		},
		{
			name:     "field",
			symbolId: "scip-go go ckb/internal/query Engine#logger.",
			want:     false,
		},
		{
			name:     "package",
			symbolId: "scip-go go ckb/internal/query/",
			want:     false,
		},
		{
			name:     "variable",
			symbolId: "scip-go go ckb/internal/config DefaultConfig.",
			want:     false,
		},
		{
			name:     "empty string",
			symbolId: "",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isFunctionSymbol(tt.symbolId)
			if got != tt.want {
				t.Errorf("isFunctionSymbol(%q) = %v, want %v", tt.symbolId, got, tt.want)
			}
		})
	}
}
