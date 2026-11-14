// Package incremental provides incremental SCIP index updates for Go codebases.
//
// V1.0 Scope: Go only, file-level granularity, in-place updates.
// V1.0 Limitation: Reverse references (callers of symbols in changed files) may be stale.
//
// V1.1 Scope: Incremental callgraph updates.
// V1.1 Contract: Outgoing calls (callees) from changed files are always accurate.
// V1.1 Limitation: Incoming calls (callers) to changed symbols may be stale.
//
// V2.0 Scope: Transitive invalidation.
// V2.0 Contract: In eager mode with queue drained, all queries return accurate results.
// V2.0 Limitation: In lazy mode, reverse refs may be stale until full reindex.
package incremental

import "time"

// ChangeType represents how a file changed
type ChangeType string

const (
	ChangeAdded    ChangeType = "added"
	ChangeModified ChangeType = "modified"
	ChangeDeleted  ChangeType = "deleted"
	ChangeRenamed  ChangeType = "renamed"
)

// ChangedFile represents a file that needs reindexing
type ChangedFile struct {
	Path       string     // New path (or current path if not renamed)
	OldPath    string     // Original path for renames - CRITICAL: must be threaded through pipeline
	ChangeType ChangeType // Type of change
	Hash       string     // New content hash (empty if deleted)
}

// FileDelta represents changes to apply for a single file
type FileDelta struct {
	Path       string     // New path (or current path if not renamed)
	OldPath    string     // Original path (for renames) - used for deletions
	ChangeType ChangeType // Type of change

	// Data to insert (extracted from SCIP)
	Symbols   []Symbol
	Refs      []Reference
	CallEdges []CallEdge // v1.1: Call edges from this file

	// Metadata
	Hash             string // SHA256 of file content
	SCIPDocumentHash string // Hash of SCIP document (skip update if unchanged)
	SymbolCount      int    // Number of symbols in this file
}

// CallEdge represents a function call from caller to callee (v1.1)
// Edges are owned by the caller file (caller-owned edges invariant).
// When a file is reindexed, its outgoing call edges are deleted and rebuilt.
type CallEdge struct {
	CallerID   string // SCIP symbol ID of calling function (may be empty if unresolved)
	CallerFile string // File containing the call (always set)
	CalleeID   string // SCIP symbol ID of called function
	Line       int    // 1-indexed line number of call site
	Column     int    // 1-indexed column of call site
	EndColumn  int    // Optional: end column for nested call disambiguation
}

// IndexedFile represents a file in our tracking table
type IndexedFile struct {
	Path             string
	Hash             string
	Mtime            int64
	IndexedAt        time.Time
	IndexedCommit    string
	SCIPDocumentHash string
	SymbolCount      int
}

// SymbolDelta represents all changes to apply to the index
type SymbolDelta struct {
	FileDeltas []FileDelta
	Stats      DeltaStats
}

// DeltaStats tracks what changed during an incremental update
type DeltaStats struct {
	FilesChanged   int
	FilesAdded     int
	FilesDeleted   int
	SymbolsAdded   int
	SymbolsRemoved int
	RefsAdded      int
	RefsRemoved    int
	CallsAdded     int // v1.1: Call edges added
	Duration       time.Duration

	// For UI display
	IndexState string // "full", "partial", or "unchanged"
}

// Symbol is a simplified symbol for delta tracking
type Symbol struct {
	ID            string // SCIP symbol ID
	Name          string // Short display name
	Kind          string // Symbol kind (function, type, etc.)
	FilePath      string // File where symbol is defined
	StartLine     int    // 1-indexed start line
	EndLine       int    // 1-indexed end line
	Documentation string // Doc comment if available
	Callable      bool   // Function, method or function-valued constant (scip.IsCallableDefinition)
}

// Reference is a simplified reference for delta tracking
// NOTE: We only track refs FROM a file, not TO a file
// This is intentional - see Invariant 3 (Caller-Owned Edges)
type Reference struct {
	FromFile   string // File containing the reference
	FromLine   int    // Line number of reference
	ToSymbolID string // Symbol being referenced
	Kind       string // "reference", "implementation" (NOT "call" in v1)
}

// InvalidationMode controls how transitive dependencies are handled (v2)
type InvalidationMode string

const (
	// InvalidationNone disables transitive invalidation
	InvalidationNone InvalidationMode = "none"
	// InvalidationLazy enqueues dependents but drains on next full index
	InvalidationLazy InvalidationMode = "lazy"
	// InvalidationEager enqueues and drains immediately (with budgets)
	InvalidationEager InvalidationMode = "eager"
	// InvalidationDeferred enqueues and drains periodically in background
	InvalidationDeferred InvalidationMode = "deferred"
)

// RescanReason describes why a file is in the rescan queue
type RescanReason string

const (
	// RescanDepChange indicates the file's dependencies changed
	RescanDepChange RescanReason = "dep_change"
	// RescanBudgetExceeded indicates we ran out of time/file budget
	RescanBudgetExceeded RescanReason = "budget_exceeded"
	// RescanManual indicates a user-triggered rescan
	RescanManual RescanReason = "manual"
)

// FileDependency represents a file-level dependency relationship
// A -> B means A references symbols defined in B
type FileDependency struct {
	DependentFile string // File that uses symbols
	DefiningFile  string // File that defines those symbols
}

// RescanQueueEntry represents a file queued for rescanning
type RescanQueueEntry struct {
	FilePath   string       // File that needs rescanning
	Reason     RescanReason // Why it was queued
	Depth      int          // BFS hop count from original change
	EnqueuedAt time.Time    // When it was queued
	Attempts   int          // Number of rescan attempts
}

// IndexState represents current index status for display
type IndexState struct {
	State           string // "full", "partial", "partial_dirty", "full_dirty", "pending", "unknown"
	LastFull        int64  // Unix timestamp of last full reindex
	LastIncremental int64  // Unix timestamp of last incremental update
	FilesSinceFull  int    // Count of files updated since last full reindex
	Commit          string // Git commit at last index
	IsDirty         bool   // Has uncommitted changes not yet indexed
	PendingRescans  int    // v2: Count of files in rescan_queue
}

// Config configures incremental indexing behavior
type Config struct {
	IndexPath            string   // Path to SCIP index file (default: index.scip)
	Excludes             []string // Glob patterns to exclude
	IncrementalThreshold int      // Percentage of files changed before falling back to full (default: 50)
	IndexTests           bool     // Whether to index _test.go files (default: false)

	// v2: Transitive invalidation settings
	Transitive TransitiveConfig
}

// TransitiveConfig configures transitive invalidation behavior (v2)
type TransitiveConfig struct {
	Enabled        bool             // Enable transitive invalidation (default: true)
	Mode           InvalidationMode // Invalidation mode (default: lazy)
	Depth          int              // BFS cascade depth limit (default: 1)
	MaxRescanFiles int              // Max files to rescan per drain run (default: 200)
	MaxRescanMs    int              // Max time in ms per drain run (default: 1500, 0 = unlimited)
}

// DefaultConfig returns the default incremental indexing configuration
func DefaultConfig() *Config {
	return &Config{
		IndexPath:            "index.scip",
		IncrementalThreshold: 50,
		IndexTests:           false,
		Transitive: TransitiveConfig{
			Enabled:        true,
			Mode:           InvalidationLazy,
			Depth:          1,
			MaxRescanFiles: 200,
			MaxRescanMs:    1500,
		},
	}
}

// Index metadata keys stored in index_meta table
const (
	MetaKeyIndexState       = "index_state"       // "full", "partial", or "pending"
	MetaKeyLastFull         = "last_full_index"   // Unix timestamp
	MetaKeyLastIncremental  = "last_incremental"  // Unix timestamp
	MetaKeyIndexCommit      = "index_commit"      // Git commit SHA
	MetaKeyFilesSinceFull   = "files_since_full"  // Count of files updated since last full
	MetaKeySchemaVersion    = "schema_version"    // Schema version (should match storage.currentSchemaVersion)
	MetaKeyCallgraphQuality = "callgraph_quality" // v1.1: "ok" or "degraded"
	MetaKeyLastDepsUpdate   = "last_deps_update"  // v2: Unix timestamp of last file_deps update
	MetaKeyInvalidationMode = "invalidation_mode" // v2: Current invalidation mode
)
