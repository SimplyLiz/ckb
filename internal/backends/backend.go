package backends

import (
	"context"
)

// BackendID uniquely identifies a backend type
type BackendID string

const (
	// BackendSCIP represents the SCIP index backend
	BackendSCIP BackendID = "scip"
	// BackendGlean represents the Glean index backend
	BackendGlean BackendID = "glean"
	// BackendLSP represents the Language Server Protocol backend
	BackendLSP BackendID = "lsp"
	// BackendGit represents the Git backend
	BackendGit BackendID = "git"
)

// Backend is the base interface that all backends must implement
type Backend interface {
	// ID returns the unique identifier for this backend
	ID() BackendID

	// IsAvailable checks if this backend is currently available and ready to use
	IsAvailable() bool

	// Capabilities returns a list of capability identifiers this backend supports
	// Examples: "symbol-search", "find-references", "goto-definition", "blame"
	Capabilities() []string

	// Priority returns the priority of this backend (lower = higher priority)
	// SCIP=1, Glean=2, LSP=3, Git=4
	Priority() int
}

// SymbolBackend extends Backend with symbol-related operations
type SymbolBackend interface {
	Backend

	// GetSymbol retrieves detailed information about a specific symbol
	GetSymbol(ctx context.Context, id string) (*SymbolResult, error)

	// SearchSymbols searches for symbols matching the query
	SearchSymbols(ctx context.Context, query string, opts SearchOptions) (*SearchResult, error)

	// FindReferences finds all references to a symbol
	FindReferences(ctx context.Context, symbolID string, opts RefOptions) (*ReferencesResult, error)
}

// SearchOptions contains options for symbol search
type SearchOptions struct {
	// MaxResults limits the number of results returned
	MaxResults int

	// IncludeTests whether to include test files in search
	IncludeTests bool

	// Scope limits search to specific modules or paths
	Scope []string

	// Kind filters by symbol kind (function, class, etc.)
	Kind []string
}

// RefOptions contains options for finding references
type RefOptions struct {
	// MaxResults limits the number of references returned
	MaxResults int

	// IncludeTests whether to include references in test files
	IncludeTests bool

	// Scope limits search to specific modules or paths
	Scope []string

	// IncludeDeclaration whether to include the symbol declaration
	IncludeDeclaration bool
}

// SymbolResult represents detailed information about a symbol
type SymbolResult struct {
	// StableID is the unique, stable identifier for this symbol
	StableID string

	// Name is the human-readable name
	Name string

	// Kind is the symbol kind (function, class, variable, etc.)
	Kind string

	// Location is the primary definition location
	Location Location

	// Signature is the normalized signature
	SignatureNormalized string

	// SignatureFull is the full signature with all details
	SignatureFull string

	// Visibility indicates access level (public, private, internal, etc.)
	Visibility string

	// VisibilityConfidence indicates confidence in visibility determination (0.0-1.0)
	VisibilityConfidence float64

	// ContainerName is the name of the containing symbol (class, namespace, etc.)
	ContainerName string

	// ModuleID identifies the module this symbol belongs to
	ModuleID string

	// Documentation is the doc comment
	Documentation string

	// Completeness tracks result quality
	Completeness CompletenessInfo
}

// SearchResult represents the result of a symbol search
type SearchResult struct {
	// Symbols is the list of matching symbols
	Symbols []SymbolResult

	// Completeness tracks result quality
	Completeness CompletenessInfo

	// TotalMatches is the total number of matches (may be > len(Symbols) if limited)
	TotalMatches int
}

// ReferencesResult represents references to a symbol
type ReferencesResult struct {
	// References is the list of reference locations
	References []Reference

	// Completeness tracks result quality
	Completeness CompletenessInfo

	// TotalReferences is the total count (may be > len(References) if limited)
	TotalReferences int
}

// Reference represents a single reference to a symbol
type Reference struct {
	// Location is where the reference appears
	Location Location

	// Kind is the reference kind (read, write, call, etc.)
	Kind string

	// SymbolID is the stable ID of the referenced symbol
	SymbolID string

	// Context is surrounding code snippet
	Context string

	// FromSymbol is the stable ID of the symbol that encloses this
	// reference (e.g. the caller function containing the call site).
	// Empty when the backend can't resolve an enclosing symbol (e.g.
	// a package-level reference outside any function).
	FromSymbol string

	// FromSymbolName is a human-readable short name for FromSymbol,
	// resolved by the backend where possible. Empty when FromSymbol
	// is empty or a display name couldn't be derived.
	FromSymbolName string
}

// Location represents a position in source code
type Location struct {
	// Path is the file path relative to repo root
	Path string

	// Line is the line number (1-indexed)
	Line int

	// Column is the column number (1-indexed)
	Column int

	// EndLine is the end line (for ranges)
	EndLine int

	// EndColumn is the end column (for ranges)
	EndColumn int
}
