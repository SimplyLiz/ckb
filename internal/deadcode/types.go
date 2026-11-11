// Package deadcode provides static dead code detection using SCIP index reference analysis.
package deadcode

// DeadCodeCategory classifies why code is considered dead.
type DeadCodeCategory string

const (
	// CategoryZeroRefs means no references found at all.
	CategoryZeroRefs DeadCodeCategory = "zero_refs"

	// CategorySelfOnly means only referenced by itself (recursive but never called).
	CategorySelfOnly DeadCodeCategory = "self_only"

	// CategoryTestOnly means only referenced from test files.
	CategoryTestOnly DeadCodeCategory = "test_only"

	// CategoryInternalExport means exported but only used within same package.
	CategoryInternalExport DeadCodeCategory = "internal_export"
)

// DeadCodeItem represents a single piece of dead code found.
type DeadCodeItem struct {
	// SymbolID is the stable CKB symbol identifier.
	SymbolID string `json:"symbolId"`

	// SymbolName is the human-readable name.
	SymbolName string `json:"symbolName"`

	// Kind is the symbol kind (function, type, method, constant, variable).
	Kind string `json:"kind"`

	// FilePath is relative to repo root.
	FilePath string `json:"filePath"`

	// LineNumber is where the symbol is defined.
	LineNumber int `json:"lineNumber"`

	// LineEnd is the end line of the symbol definition.
	LineEnd int `json:"lineEnd,omitempty"`

	// Confidence is how certain we are this is dead (0.0 - 1.0).
	Confidence float64 `json:"confidence"`

	// Reason explains why this is considered dead.
	Reason string `json:"reason"`

	// Category classifies the type of dead code.
	Category DeadCodeCategory `json:"category"`

	// ReferenceCount is total references found.
	ReferenceCount int `json:"referenceCount"`

	// TestReferences is count of references from test files.
	TestReferences int `json:"testReferences,omitempty"`

	// SelfReferences is count of self-references.
	SelfReferences int `json:"selfReferences,omitempty"`

	// SourceSnippet is optional source code preview.
	SourceSnippet string `json:"sourceSnippet,omitempty"`

	// Exported indicates if the symbol is exported/public.
	Exported bool `json:"exported"`
}

// DeadCodeSummary provides aggregate statistics.
type DeadCodeSummary struct {
	// TotalSymbols is all symbols analyzed.
	TotalSymbols int `json:"totalSymbols"`

	// DeadCount is definitely dead symbols.
	DeadCount int `json:"deadCount"`

	// SuspiciousCount is possibly dead symbols.
	SuspiciousCount int `json:"suspiciousCount"`

	// ByKind breaks down dead code by symbol kind.
	ByKind map[string]int `json:"byKind"`

	// ByCategory breaks down dead code by category.
	ByCategory map[string]int `json:"byCategory"`

	// EstimatedLines is approximate LOC that could be removed.
	EstimatedLines int `json:"estimatedLines"`
}

// ReferenceStats categorizes references to a symbol.
type ReferenceStats struct {
	// Total is all references found.
	Total int

	// FromTests is references from test files.
	FromTests int

	// FromSelf is self-references (same symbol).
	FromSelf int

	// External is references from other packages.
	External int

	// Internal is references from same package.
	Internal int
}

// AnalyzerOptions configures the dead code analyzer.
type AnalyzerOptions struct {
	// Scope limits analysis to specific packages/paths.
	Scope []string

	// IncludeExported analyzes exported symbols (default: true).
	IncludeExported bool

	// IncludeUnexported analyzes unexported symbols (default: false).
	IncludeUnexported bool

	// MinConfidence filters results below this threshold (default: 0.7).
	MinConfidence float64

	// ExcludePatterns are glob patterns to skip.
	ExcludePatterns []string

	// ExcludeTestOnly doesn't report test-only refs as dead (default: true).
	ExcludeTestOnly bool

	// Limit is max results to return (default: 100).
	Limit int

	// IncludeSource includes source code snippets.
	IncludeSource bool

	// ExcludeSymbols are symbol IDs a framework calls without a reference in
	// the index (a Next.js page, a route handler); they are never dead.
	ExcludeSymbols map[string]bool
}

// DefaultOptions returns sensible default options.
func DefaultOptions() AnalyzerOptions {
	return AnalyzerOptions{
		IncludeExported:   true,
		IncludeUnexported: false,
		MinConfidence:     0.7,
		ExcludeTestOnly:   true,
		Limit:             100,
		IncludeSource:     false,
	}
}

// Result is the output of dead code analysis.
type Result struct {
	// DeadCode is the list of dead code items found.
	DeadCode []DeadCodeItem `json:"deadCode"`

	// Summary provides aggregate statistics.
	Summary DeadCodeSummary `json:"summary"`

	// Scope that was analyzed.
	Scope []string `json:"scope,omitempty"`
}
