package impact

// ChangeType represents the type of change made to a symbol
type ChangeType string

const (
	ChangeAdded    ChangeType = "added"
	ChangeModified ChangeType = "modified"
	ChangeDeleted  ChangeType = "deleted"
)

// ChangedSymbol represents a symbol affected by a code change
type ChangedSymbol struct {
	SymbolID   string     // SCIP stable symbol ID
	Name       string     // Human-readable symbol name
	File       string     // File path
	ChangeType ChangeType // Type of change
	Lines      []int      // Changed line numbers
	Confidence float64    // Mapping confidence (0.0-1.0)
	HunkIndex  int        // Index of the hunk this change came from (for tracing)
	// Note qualifies the entry: on a file-level ("file:...") entry, which changed
	// lines could not be attributed to a definition; on a symbol, that only comments
	// or whitespace changed in it.
	Note string
	// Trivial means only comments or whitespace changed, which is why Confidence
	// is low: unlike other low-confidence entries, this isn't a mapping problem.
	Trivial bool
	// DeletionUnresolved marks a file-level entry for removed code whose
	// definition isn't in the index, so what used it is unknown.
	DeletionUnresolved bool
}

// ParsedDiff represents a parsed git diff with structured information
type ParsedDiff struct {
	Files []ChangedFile // Files changed in the diff
}

// ChangedFile represents a single file in a diff
type ChangedFile struct {
	OldPath string        // Original file path (for renames)
	NewPath string        // New file path
	IsNew   bool          // File was newly created
	Deleted bool          // File was deleted
	Renamed bool          // File was renamed
	Hunks   []ChangedHunk // Change hunks
}

// ChangedHunk represents a single hunk of changes within a file
type ChangedHunk struct {
	OldStart int   // Starting line in old file
	OldLines int   // Number of lines in old file
	NewStart int   // Starting line in new file
	NewLines int   // Number of lines in new file
	Added    []int // Line numbers of added lines (in new file)
	Removed  []int // Line numbers of removed lines (in old file)

	// The fields below are only set by the diff parser; a hunk built by hand
	// leaves them empty and is taken to be in new-file coordinates.
	OldText []string      // Text of the hunk's old-file lines (context and removed), from OldStart
	NewText []string      // Text of the hunk's new-file lines (context and added), from NewStart
	Blocks  []ChangeBlock // Runs of adjacent removed and added lines
}

// ChangeBlock is one run of adjacent removed and added lines in a hunk: a
// modification if it has both, a pure deletion or insertion if it has one. A
// side with no lines marks a position: the block sits between OldLine-1 and
// OldLine in the old file, or between NewLine-1 and NewLine in the new file.
type ChangeBlock struct {
	OldLine int      // First removed line, or the old-file line following an insertion
	NewLine int      // First added line, or the new-file line following a deletion
	Removed []string // Text of the removed lines
	Added   []string // Text of the added lines
}

// DiffParser parses git diffs into structured data
type DiffParser interface {
	// Parse parses a unified diff string into a ParsedDiff
	Parse(diffContent string) (*ParsedDiff, error)
}

// SymbolMapper maps diff changes to SCIP symbols
type SymbolMapper interface {
	// MapToSymbols maps changed lines in a ParsedDiff to symbols
	// Returns symbols with confidence scores based on mapping precision
	MapToSymbols(diff *ParsedDiff) ([]ChangedSymbol, error)
}

// TestMapper maps symbols/files to tests that cover them
type TestMapper interface {
	// GetTestsForSymbol returns tests that cover a specific symbol
	GetTestsForSymbol(symbolID string) ([]AffectedTest, error)

	// GetTestsForFile returns tests that cover a specific file
	GetTestsForFile(filePath string) ([]AffectedTest, error)

	// GetTestsForPackage returns all tests in a package
	GetTestsForPackage(pkgPath string) ([]AffectedTest, error)
}

// AffectedTest represents a test that may be affected by changes
type AffectedTest struct {
	Name       string  // Test function name (e.g., "TestAuthenticate")
	Path       string  // Test file path
	Package    string  // Package path
	Reason     string  // Why this test was selected
	Priority   int     // Run order priority (lower = run first)
	Confidence float64 // How confident we are this test is affected (0.0-1.0)
}

// ImpactAggregator aggregates impact analysis across multiple changed symbols
type ImpactAggregator interface {
	// AggregateImpact combines impact results from multiple symbols
	// Deduplicates affected symbols and aggregates risk scores
	AggregateImpact(results []*ImpactAnalysisResult) (*AggregatedImpactResult, error)
}

// AggregatedImpactResult is the result of analyzing multiple changed symbols
type AggregatedImpactResult struct {
	Summary         ChangeSummary    // High-level summary
	ChangedSymbols  []ChangedSymbol  // Symbols that were changed
	AffectedSymbols []ImpactItem     // Deduplicated affected symbols
	ModulesAffected []ModuleSummary  // Impact grouped by module
	BlastRadius     *BlastRadius     // Combined blast radius
	RiskScore       *RiskScore       // Aggregated risk score
	Recommendations []Recommendation // Suggested actions
}

// ChangeSummary provides a high-level overview of a change set
type ChangeSummary struct {
	FilesChanged         int    // Number of files changed
	SymbolsChanged       int    // Number of symbols changed
	DirectlyAffected     int    // Symbols directly affected
	TransitivelyAffected int    // Symbols transitively affected
	EstimatedRisk        string // "low", "medium", "high", "critical"
}

// Recommendation suggests an action based on impact analysis
type Recommendation struct {
	Type     string // "coverage", "review", "split", "test"
	Severity string // "info", "warning", "error"
	Message  string // Human-readable message
	Action   string // Suggested action to take
}

// IndexStalenessInfo provides information about SCIP index freshness
type IndexStalenessInfo struct {
	IndexTimestamp   int64  // Unix timestamp of index creation
	HeadTimestamp    int64  // Unix timestamp of HEAD commit
	CommitsBehind    int    // Number of commits since index was created
	IsStale          bool   // Whether the index is considered stale
	StalenessMessage string // Human-readable staleness description
}

// StalenessChecker checks if the SCIP index is stale relative to git state
type StalenessChecker interface {
	// CheckStaleness compares index timestamp to git HEAD
	CheckStaleness() (*IndexStalenessInfo, error)
}
