package query

import (
	"context"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/deadcode"
)

// FindDeadCodeOptions configures the dead code detection.
type FindDeadCodeOptions struct {
	// Scope limits analysis to specific packages/paths.
	Scope []string `json:"scope,omitempty"`

	// IncludeExported analyzes exported symbols (default: true).
	IncludeExported bool `json:"includeExported"`

	// IncludeUnexported analyzes unexported symbols (default: false).
	IncludeUnexported bool `json:"includeUnexported"`

	// MinConfidence filters results below this threshold (default: 0.7).
	MinConfidence float64 `json:"minConfidence"`

	// ExcludePatterns are glob patterns to skip.
	ExcludePatterns []string `json:"excludePatterns,omitempty"`

	// ExcludeTestOnly doesn't report test-only refs as dead (default: true).
	ExcludeTestOnly bool `json:"excludeTestOnly"`

	// Limit is max results to return (default: 100).
	Limit int `json:"limit"`

	// IncludeSource includes source code snippets.
	IncludeSource bool `json:"includeSource"`
}

// FindDeadCodeResponse is the response from dead code detection.
type FindDeadCodeResponse struct {
	// DeadCode is the list of dead code items found.
	DeadCode []DeadCodeItem `json:"deadCode"`

	// Summary provides aggregate statistics.
	Summary DeadCodeSummary `json:"summary"`

	// Scope that was analyzed.
	Scope []string `json:"scope,omitempty"`

	// Provenance metadata.
	Provenance *Provenance `json:"provenance,omitempty"`
}

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
	LineNumber int `json:"lineNumber,omitempty"`

	// Confidence is how certain we are this is dead (0.0 - 1.0).
	Confidence float64 `json:"confidence"`

	// Reason explains why this is considered dead.
	Reason string `json:"reason"`

	// Category classifies the type of dead code.
	Category string `json:"category"`

	// ReferenceCount is total references found.
	ReferenceCount int `json:"referenceCount"`

	// TestReferences is count of references from test files.
	TestReferences int `json:"testReferences,omitempty"`

	// SelfReferences is count of self-references.
	SelfReferences int `json:"selfReferences,omitempty"`

	// Exported indicates if the symbol is exported/public.
	Exported bool `json:"exported"`
}

// DeadCodeSummary provides aggregate statistics.
type DeadCodeSummary struct {
	// TotalSymbols is all symbols analyzed.
	TotalSymbols int `json:"totalSymbols"`

	// DeadCount is definitely dead symbols (confidence >= 0.9).
	DeadCount int `json:"deadCount"`

	// SuspiciousCount is possibly dead symbols (confidence < 0.9).
	SuspiciousCount int `json:"suspiciousCount"`

	// ByKind breaks down dead code by symbol kind.
	ByKind map[string]int `json:"byKind"`

	// ByCategory breaks down dead code by category.
	ByCategory map[string]int `json:"byCategory"`

	// EstimatedLines is approximate LOC that could be removed.
	EstimatedLines int `json:"estimatedLines"`
}

// FindDeadCode detects unused code using static analysis of the SCIP index.
func (e *Engine) FindDeadCode(ctx context.Context, opts FindDeadCodeOptions) (*FindDeadCodeResponse, error) {
	startTime := time.Now()

	// Apply defaults
	if opts.MinConfidence == 0 {
		opts.MinConfidence = 0.7
	}
	if opts.Limit == 0 {
		opts.Limit = 100
	}
	// Default to including exported if neither is set
	if !opts.IncludeExported && !opts.IncludeUnexported {
		opts.IncludeExported = true
	}

	// Create analyzer
	analyzer := deadcode.NewAnalyzer(
		e.scipAdapter,
		e.repoRoot,
		e.logger,
		opts.ExcludePatterns,
	)

	// What a framework calls by convention has no reference in the index and
	// is not dead: a Next.js page's default export, a route handler's GET.
	var entrypointSymbols map[string]bool
	if e.scipAdapter != nil {
		entrypointSymbols = e.frameworkUsedSymbols(e.scipAdapter.GetIndex())
	}

	// Run analysis
	analyzerOpts := deadcode.AnalyzerOptions{
		ExcludeSymbols:    entrypointSymbols,
		Scope:             opts.Scope,
		IncludeExported:   opts.IncludeExported,
		IncludeUnexported: opts.IncludeUnexported,
		MinConfidence:     opts.MinConfidence,
		ExcludePatterns:   opts.ExcludePatterns,
		ExcludeTestOnly:   opts.ExcludeTestOnly,
		Limit:             opts.Limit,
		IncludeSource:     opts.IncludeSource,
	}

	result, err := analyzer.Analyze(ctx, analyzerOpts)
	if err != nil {
		return nil, err
	}

	// Convert to response format
	response := &FindDeadCodeResponse{
		DeadCode: make([]DeadCodeItem, len(result.DeadCode)),
		Summary: DeadCodeSummary{
			TotalSymbols:    result.Summary.TotalSymbols,
			DeadCount:       result.Summary.DeadCount,
			SuspiciousCount: result.Summary.SuspiciousCount,
			ByKind:          result.Summary.ByKind,
			ByCategory:      result.Summary.ByCategory,
			EstimatedLines:  result.Summary.EstimatedLines,
		},
		Scope: result.Scope,
	}

	// Convert dead code items
	for i, item := range result.DeadCode {
		response.DeadCode[i] = DeadCodeItem{
			SymbolID:       item.SymbolID,
			SymbolName:     item.SymbolName,
			Kind:           item.Kind,
			FilePath:       item.FilePath,
			LineNumber:     item.LineNumber,
			Confidence:     item.Confidence,
			Reason:         item.Reason,
			Category:       string(item.Category),
			ReferenceCount: item.ReferenceCount,
			TestReferences: item.TestReferences,
			SelfReferences: item.SelfReferences,
			Exported:       item.Exported,
		}
	}

	// Build provenance
	repoState, _ := e.GetRepoState(ctx, "head")
	response.Provenance = e.buildProvenance(repoState, "head", startTime, nil, CompletenessInfo{})

	return response, nil
}
