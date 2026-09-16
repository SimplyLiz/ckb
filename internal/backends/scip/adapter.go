package scip

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	"github.com/SimplyLiz/CodeMCP/internal/repostate"
)

// SCIPAdapter implements the Backend and SymbolBackend interfaces for SCIP
type SCIPAdapter struct {
	indexPath    string
	index        *SCIPIndex
	logger       *slog.Logger
	queryTimeout time.Duration
	repoRoot     string
	cacheRoot    string // optional override for the derived-cache directory
	cfg          *config.Config

	// Mutex for thread-safe access to index
	mu sync.RWMutex

	// freshness tracks index freshness
	freshness *IndexFreshness
}

// NewSCIPAdapter creates a new SCIP adapter
func NewSCIPAdapter(cfg *config.Config, logger *slog.Logger) (*SCIPAdapter, error) {
	if !cfg.Backends.Scip.Enabled {
		return nil, errors.NewCkbError(
			errors.BackendUnavailable,
			"SCIP backend is disabled in configuration",
			nil,
			nil,
			nil,
		)
	}

	indexPath := GetIndexPath(cfg.RepoRoot, cfg.Backends.Scip.IndexPath)

	// Get default query timeout from config
	queryTimeout := time.Duration(cfg.QueryPolicy.TimeoutMs["scip"]) * time.Millisecond
	if queryTimeout == 0 {
		queryTimeout = 5 * time.Second
	}

	adapter := &SCIPAdapter{
		indexPath:    indexPath,
		logger:       logger,
		queryTimeout: queryTimeout,
		repoRoot:     cfg.RepoRoot,
		cfg:          cfg,
	}

	// Try to load the index immediately
	if err := adapter.LoadIndex(); err != nil {
		// Log warning but don't fail - adapter can still report unavailable
		logger.Warn("Failed to load SCIP index",
			"error", err.Error(),
			"path", indexPath)
	}

	return adapter, nil
}

// ID returns the backend identifier
func (s *SCIPAdapter) ID() backends.BackendID {
	return backends.BackendSCIP
}

// IsAvailable checks if the SCIP backend is available
func (s *SCIPAdapter) IsAvailable() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Check if index is loaded
	if s.index == nil {
		return false
	}

	// Check if index file still exists
	if _, err := os.Stat(s.indexPath); os.IsNotExist(err) {
		return false
	}

	return true
}

// Capabilities returns the capabilities supported by SCIP
func (s *SCIPAdapter) Capabilities() []string {
	return []string{
		"symbol-search",
		"find-references",
		"goto-definition",
		"find-implementations",
		"type-hierarchy",
	}
}

// Priority returns the priority of the SCIP backend (highest priority)
func (s *SCIPAdapter) Priority() int {
	return 1 // SCIP has highest priority
}

// derivedCachePath returns the path for the derived-index cache file.
// It lives alongside the .ckb database in <repoRoot>/.ckb/.
func (s *SCIPAdapter) derivedCachePath() string {
	root := s.repoRoot
	if s.cacheRoot != "" {
		root = s.cacheRoot
	}
	return filepath.Join(root, ".ckb", "scip_derived.gob")
}

// SetCacheRoot overrides the directory used for the derived-index cache.
// Useful in tests to isolate cache state per test instead of sharing the fixture dir.
func (s *SCIPAdapter) SetCacheRoot(dir string) {
	s.cacheRoot = dir
}

// LoadIndex loads or reloads the SCIP index
func (s *SCIPAdapter) LoadIndex() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("Loading SCIP index",
		"path", s.indexPath,
	)

	index, err := loadSCIPIndexInternal(s.indexPath, s.derivedCachePath())
	if err != nil {
		return err
	}

	s.index = index

	// Pre-warm CallerIndex in background so the first FindCallers / getCallGraph
	// call is instant instead of blocking for several seconds on a large repo.
	// callerIndexOnce guarantees no duplicate work if FindCallers is called
	// before this goroutine finishes.
	idx := index
	go func() {
		idx.callerIndexOnce.Do(func() {
			idx.CallerIndex = buildCallerIndex(idx.Documents)
		})
	}()

	s.logger.Info("SCIP index loaded successfully",
		"documents", len(index.Documents),
		"symbols", len(index.Symbols),
		"commit", index.IndexedCommit,
	)

	// Compute freshness
	if repoState, err := repostate.ComputeRepoState(s.repoRoot); err == nil {
		s.freshness = ComputeIndexFreshness(index.IndexedCommit, repoState, s.repoRoot)
		if s.freshness.IsStale() {
			s.logger.Warn("SCIP index is stale",
				"warning", s.freshness.Warning,
			)
		}
	}

	return nil
}

// GetSymbol retrieves detailed information about a specific symbol
func (s *SCIPAdapter) GetSymbol(ctx context.Context, id string) (*backends.SymbolResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return nil, errors.NewCkbError(
			errors.IndexMissing,
			"SCIP index not loaded",
			nil,
			errors.GetSuggestedFixes(errors.IndexMissing),
			nil,
		)
	}

	// Apply timeout
	_, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	// Get symbol from index
	scipSym, err := s.index.GetSymbolByID(id)
	if err != nil {
		return nil, errors.NewCkbError(
			errors.SymbolNotFound,
			fmt.Sprintf("Symbol not found: %s", id),
			err,
			nil,
			nil,
		)
	}

	// Convert to SymbolResult
	result := s.convertToSymbolResult(scipSym)

	return result, nil
}

// SearchSymbols searches for symbols matching the query
func (s *SCIPAdapter) SearchSymbols(ctx context.Context, query string, opts backends.SearchOptions) (*backends.SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return nil, errors.NewCkbError(
			errors.IndexMissing,
			"SCIP index not loaded",
			nil,
			errors.GetSuggestedFixes(errors.IndexMissing),
			nil,
		)
	}

	// Apply timeout
	_, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	// Convert options
	scipOpts := SearchOptions{
		MaxResults:   opts.MaxResults,
		IncludeTests: opts.IncludeTests,
		Scope:        opts.Scope,
		Kind:         convertKindsToSCIP(opts.Kind),
	}

	// Search symbols
	scipSymbols, err := s.index.SearchSymbols(query, scipOpts)
	if err != nil {
		return nil, errors.NewCkbError(
			errors.InternalError,
			"Failed to search symbols",
			err,
			nil,
			nil,
		)
	}

	// Convert results
	symbols := make([]backends.SymbolResult, len(scipSymbols))
	for i, scipSym := range scipSymbols {
		symbols[i] = *s.convertToSymbolResult(scipSym)
	}

	// Compute completeness
	completeness := s.computeCompleteness()

	return &backends.SearchResult{
		Symbols:      symbols,
		Completeness: completeness,
		TotalMatches: len(symbols),
	}, nil
}

// FindReferences finds all references to a symbol
func (s *SCIPAdapter) FindReferences(ctx context.Context, symbolID string, opts backends.RefOptions) (*backends.ReferencesResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return nil, errors.NewCkbError(
			errors.IndexMissing,
			"SCIP index not loaded",
			nil,
			errors.GetSuggestedFixes(errors.IndexMissing),
			nil,
		)
	}

	// Apply timeout
	_, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	// Convert options
	scipOpts := ReferenceOptions{
		MaxResults:        opts.MaxResults,
		IncludeDefinition: opts.IncludeDeclaration,
		IncludeTests:      opts.IncludeTests,
		IncludeContext:    true,
		Scope:             opts.Scope,
	}

	// Find references
	scipRefs, err := s.index.FindReferences(symbolID, scipOpts)
	if err != nil {
		return nil, errors.NewCkbError(
			errors.InternalError,
			"Failed to find references",
			err,
			nil,
			nil,
		)
	}

	// Convert results
	references := make([]backends.Reference, len(scipRefs))
	for i, scipRef := range scipRefs {
		references[i] = s.convertToReference(scipRef)
	}

	// Compute completeness
	completeness := s.computeCompleteness()

	return &backends.ReferencesResult{
		References:      references,
		Completeness:    completeness,
		TotalReferences: len(references),
	}, nil
}

// convertToSymbolResult converts a SCIPSymbol to a SymbolResult
func (s *SCIPAdapter) convertToSymbolResult(scipSym *SCIPSymbol) *backends.SymbolResult {
	var location backends.Location
	if scipSym.Location != nil {
		location = backends.Location{
			Path:      scipSym.Location.FileId,
			Line:      scipSym.Location.StartLine + 1, // Convert to 1-indexed
			Column:    scipSym.Location.StartColumn + 1,
			EndLine:   scipSym.Location.EndLine + 1,
			EndColumn: scipSym.Location.EndColumn + 1,
		}
	}

	// Compute visibility confidence
	visibilityConfidence := 0.9 // SCIP has good visibility inference
	if scipSym.Visibility == "" {
		visibilityConfidence = 0.5
	}

	return &backends.SymbolResult{
		StableID:             scipSym.StableId,
		Name:                 scipSym.Name,
		Kind:                 string(scipSym.Kind),
		Location:             location,
		SignatureNormalized:  scipSym.SignatureNormalized,
		SignatureFull:        "", // SCIP indexes don't include unnormalized signatures
		Visibility:           scipSym.Visibility,
		VisibilityConfidence: visibilityConfidence,
		ContainerName:        scipSym.ContainerName,
		ModuleID:             "", // Module ID is resolved later by the query engine
		Documentation:        scipSym.Documentation,
		Completeness:         s.computeCompleteness(),
	}
}

// convertToReference converts a SCIPReference to a Reference
func (s *SCIPAdapter) convertToReference(scipRef *SCIPReference) backends.Reference {
	var location backends.Location
	if scipRef.Location != nil {
		location = backends.Location{
			Path:      scipRef.Location.FileId,
			Line:      scipRef.Location.StartLine + 1, // Convert to 1-indexed
			Column:    scipRef.Location.StartColumn + 1,
			EndLine:   scipRef.Location.EndLine + 1,
			EndColumn: scipRef.Location.EndColumn + 1,
		}
	}

	// fromSymbol/fromSymbolName resolve the enclosing symbol (e.g. the
	// caller function containing this reference). Some scip-go/scip-ts
	// "enclosing symbols" are module/namespace-level (descriptor ends in
	// just "/" with nothing after it, e.g. a bare file-scope symbol) and
	// GetSimpleName legitimately has no short name to give back. Treat
	// that the same as "no enclosing symbol resolved" rather than
	// surfacing the raw, space-and-backtick-laden SCIP ID as a "name" —
	// callers should omit the field, not display a wall of text.
	fromSymbol := ""
	fromSymbolName := ""
	if scipRef.FromSymbol != "" {
		if fromId, err := ParseSCIPIdentifier(scipRef.FromSymbol); err == nil {
			if name := fromId.GetSimpleName(); name != "" {
				fromSymbol = scipRef.FromSymbol
				fromSymbolName = name
			}
		}
	}

	return backends.Reference{
		Location:       location,
		Kind:           string(scipRef.Kind),
		SymbolID:       scipRef.SymbolId,
		Context:        scipRef.Context,
		FromSymbol:     fromSymbol,
		FromSymbolName: fromSymbolName,
	}
}

// computeCompleteness computes the completeness of results based on index freshness
func (s *SCIPAdapter) computeCompleteness() backends.CompletenessInfo {
	if s.freshness == nil {
		return backends.NewCompletenessInfo(1.0, backends.FullBackend, "SCIP index loaded")
	}

	score := s.freshness.GetCompletenessScore()
	reason := backends.FullBackend
	details := "SCIP index available"

	if s.freshness.IsStale() {
		reason = backends.IndexStale
		details = s.freshness.Warning
	}

	return backends.NewCompletenessInfo(score, reason, details)
}

// convertKindsToSCIP converts backend kind strings to SCIP SymbolKind
func convertKindsToSCIP(kinds []string) []SymbolKind {
	scipKinds := make([]SymbolKind, len(kinds))
	for i, k := range kinds {
		scipKinds[i] = SymbolKind(k)
	}
	return scipKinds
}

// GetIndexInfo returns information about the loaded index
func (s *SCIPAdapter) GetIndexInfo() *IndexInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return &IndexInfo{
			Available: false,
			Path:      s.indexPath,
		}
	}

	return &IndexInfo{
		Available:     true,
		Path:          s.indexPath,
		DocumentCount: len(s.index.Documents),
		SymbolCount:   len(s.index.Symbols),
		IndexedCommit: s.index.IndexedCommit,
		LoadedAt:      s.index.LoadedAt,
		Freshness:     s.freshness,
	}
}

// IndexInfo contains information about the SCIP index
type IndexInfo struct {
	Available     bool
	Path          string
	DocumentCount int
	SymbolCount   int
	IndexedCommit string
	LoadedAt      time.Time
	Freshness     *IndexFreshness
}

// Reload reloads the SCIP index
func (s *SCIPAdapter) Reload() error {
	return s.LoadIndex()
}

// Close closes the adapter and releases resources
func (s *SCIPAdapter) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.index = nil
	s.freshness = nil

	s.logger.Info("SCIP adapter closed")
	return nil
}

// BuildCallGraph builds a call graph for a symbol using the SCIP index
func (s *SCIPAdapter) BuildCallGraph(symbolId string, opts CallGraphOptions) (*CallGraph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return nil, errors.NewCkbError(
			errors.IndexMissing,
			"SCIP index not loaded",
			nil,
			errors.GetSuggestedFixes(errors.IndexMissing),
			nil,
		)
	}

	return s.index.BuildCallGraph(symbolId, opts)
}

// GetCallerCount returns the number of callers for a symbol
func (s *SCIPAdapter) GetCallerCount(symbolId string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return 0
	}

	return s.index.GetCallerCount(symbolId)
}

// GetCalleeCount returns the number of callees for a symbol
func (s *SCIPAdapter) GetCalleeCount(symbolId string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return 0
	}

	return s.index.GetCalleeCount(symbolId)
}

// CountSymbolsByPath counts the number of symbols in documents matching a path prefix
func (s *SCIPAdapter) CountSymbolsByPath(pathPrefix string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return 0
	}

	return s.index.CountSymbolsByPath(pathPrefix)
}

// AllSymbols returns all symbols in the index
func (s *SCIPAdapter) AllSymbols() []*SymbolInformation {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return nil
	}

	return s.index.AllSymbols()
}

// GetReferenceCount returns the count of references to a symbol
func (s *SCIPAdapter) GetReferenceCount(symbolId string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return 0
	}

	return s.index.GetReferenceCount(symbolId)
}

// GetIndex returns the underlying SCIP index for direct access.
// This is used by FTS population and other systems that need raw symbol data.
func (s *SCIPAdapter) GetIndex() *SCIPIndex {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.index
}
