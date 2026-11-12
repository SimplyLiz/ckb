package scip

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	indexmeta "github.com/SimplyLiz/CodeMCP/internal/index"
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

	// loadedStamp is the size/mtime of the index file the loaded index came
	// from, so a long-running server can notice when 'ckb index' rewrote it
	// (IndexFileChanged). failedStamp is the stamp of the last file that
	// failed to load — a half-written or corrupt file is not retried until it
	// changes again.
	loadedStamp fileStamp
	failedStamp fileStamp
}

// fileStamp identifies one version of a file on disk cheaply (os.Stat, no
// hashing). exists=false is the stamp of a missing file.
type fileStamp struct {
	exists  bool
	size    int64
	modTime time.Time
}

func statFile(path string) fileStamp {
	fi, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{exists: true, size: fi.Size(), modTime: fi.ModTime()}
}

func (a fileStamp) equal(b fileStamp) bool {
	return a.exists == b.exists && a.size == b.size && a.modTime.Equal(b.modTime)
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

// IndexFileChanged reports whether the index file on disk is a different
// version from the one this adapter loaded (or no index is loaded but a file
// now exists): the situation after 'ckb index' ran in another process while
// this one kept serving. One os.Stat, no I/O on the index itself. A file that
// already failed to load is not reported again until it changes, and a
// deleted file is not a change — IsAvailable covers that.
func (s *SCIPAdapter) IndexFileChanged() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cur := statFile(s.indexPath)
	if !cur.exists {
		return false
	}
	if cur.equal(s.loadedStamp) || cur.equal(s.failedStamp) {
		return false
	}
	return true
}

// IndexStamp identifies the exact index file version the loaded index was
// read from, together with the derivation-logic version. Anything derived from
// the index and persisted elsewhere (the FTS content) records it and is
// rebuilt when it no longer matches. Empty when no index is loaded.
func (s *SCIPAdapter) IndexStamp() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil || !s.loadedStamp.exists {
		return ""
	}
	return fmt.Sprintf("d%d:%d:%d", DerivedCacheVersion, s.loadedStamp.size, s.loadedStamp.modTime.UnixNano())
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

	// Stat before parsing: if the indexer rewrites the file while we parse it,
	// the post-write stamp differs from this one and the next
	// IndexFileChanged check triggers another load.
	stamp := statFile(s.indexPath)

	index, err := loadSCIPIndexInternal(s.indexPath, s.derivedCachePath())
	if err != nil {
		s.failedStamp = stamp
		return err
	}

	// Indexers like scip-typescript record no commit in the SCIP metadata.
	// 'ckb index' (full, incremental, watch, MCP reindex) writes the HEAD it
	// built from to .ckb/index-meta.json; use that so freshness checks can
	// work for every language, not only those whose indexer stamps a commit.
	if index.IndexedCommit == "" {
		if meta, err := indexmeta.LoadMeta(filepath.Join(s.repoRoot, ".ckb")); err == nil && meta != nil && meta.CommitHash != "" {
			index.IndexedCommit = meta.CommitHash
			s.logger.Debug("SCIP metadata has no commit; using .ckb/index-meta.json", "commit", meta.CommitHash)
		}
	}

	s.index = index
	s.loadedStamp = stamp
	s.failedStamp = fileStamp{}

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

	// Convert results. A local means nothing to whoever is searching a repo.
	symbols := make([]backends.SymbolResult, 0, len(scipSymbols))
	for _, scipSym := range scipSymbols {
		if IsLocalSymbol(scipSym.StableId) {
			continue
		}
		symbols = append(symbols, *s.convertToSymbolResult(scipSym))
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
// TypeSymbolIn returns the symbol of the type a file defines under a name, or "".
func (s *SCIPAdapter) TypeSymbolIn(path, name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return ""
	}
	doc := s.index.DocumentsByPath[path]
	if doc == nil {
		return ""
	}
	for _, occ := range doc.Occurrences {
		if occ.SymbolRoles&SymbolRoleDefinition != 0 && endsWithDescriptor(occ.Symbol, name+"#") {
			return occ.Symbol
		}
	}
	return ""
}

// endsWithDescriptor says whether a symbol's last descriptor is the given one. It
// follows a module, `m/Foo#`, or for a name at a crate's root the version, as in
// `rust-analyzer cargo app 1.0.0 Foo#`.
func endsWithDescriptor(symbol, descriptor string) bool {
	rest, ok := strings.CutSuffix(symbol, descriptor)
	return ok && (strings.HasSuffix(rest, "/") || strings.HasSuffix(rest, " "))
}

// VariantSymbolsIn returns the symbols of the variants a file defines for an enum, by
// name. Rust-analyzer names them under the enum, `mod/E#A#`, and a pattern refers to
// one however it is spelled: `E::A`, `Self::A`, or a bare `A` through `use E::*`, the
// last two without a reference to the enum itself.
func (s *SCIPAdapter) VariantSymbolsIn(path, typeSymbol string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil || !strings.HasSuffix(typeSymbol, "#") {
		return nil
	}
	doc := s.index.DocumentsByPath[path]
	if doc == nil {
		return nil
	}
	variants := make(map[string]string)
	for _, occ := range doc.Occurrences {
		if occ.SymbolRoles&SymbolRoleDefinition == 0 || !strings.HasPrefix(occ.Symbol, typeSymbol) {
			continue
		}
		name, ok := strings.CutSuffix(occ.Symbol[len(typeSymbol):], "#")
		if ok && name != "" && !strings.ContainsAny(name, "#/.()[]`") {
			variants[name] = occ.Symbol
		}
	}
	return variants
}

// ValueSymbolIn returns the symbol of the variable, constant or function a file
// defines under a name at its top level, or "".
func (s *SCIPAdapter) ValueSymbolIn(path, name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return ""
	}
	doc := s.index.DocumentsByPath[path]
	if doc == nil {
		return ""
	}
	for _, occ := range doc.Occurrences {
		if occ.SymbolRoles&SymbolRoleDefinition == 0 {
			continue
		}
		if endsWithDescriptor(occ.Symbol, name+".") || endsWithDescriptor(occ.Symbol, name+"().") {
			return occ.Symbol
		}
	}
	return ""
}

// KeySymbolAt returns the symbol of the object key defined under a name on a line
// (1-based) of a file, or "". scip-typescript names such a key `name0:`, and the
// places that read or set it through the object's type refer to it.
func (s *SCIPAdapter) KeySymbolAt(path string, line int, name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return ""
	}
	doc := s.index.DocumentsByPath[path]
	if doc == nil {
		return ""
	}
	for _, occ := range doc.Occurrences {
		if occ.SymbolRoles&SymbolRoleDefinition == 0 || len(occ.Range) == 0 || int(occ.Range[0]) != line-1 {
			continue
		}
		at := strings.LastIndexByte(occ.Symbol, '/')
		descriptor := occ.Symbol[at+1:]
		if !strings.HasSuffix(descriptor, ":") || !strings.HasPrefix(descriptor, name) {
			continue
		}
		if strings.Trim(descriptor[len(name):len(descriptor)-1], "0123456789") == "" {
			return occ.Symbol
		}
	}
	return ""
}

// GeneratedMethods returns the given method of a type where the index has calls but no
// definition: a macro wrote it, so nothing in the source declares it. Rust-analyzer's
// symbols name the impl, as in `mod/impl#[`Foo<'a>`]new().`.
func (s *SCIPAdapter) GeneratedMethods(typeSymbol, method string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	name := SymbolSimpleName(typeSymbol)
	module := strings.TrimSuffix(typeSymbol, name+"#")
	if name == "" || module == typeSymbol {
		return nil
	}
	exact := module + "impl#[" + name + "]" + method + "()."
	generic := module + "impl#[`" + name + "<"
	tail := "]" + method + "()."
	var ids []string
	for id := range s.index.RefIndex {
		if (id == exact || strings.HasPrefix(id, generic) && strings.HasSuffix(id, tail)) && s.index.DefinitionIndex[id] == nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// DefinitionSite is where the index defines a symbol, 1-based.
type DefinitionSite struct {
	Symbol string
	Path   string
	Line   int
	Column int
}

// GoTypeMethods is a concrete Go type and those of its methods a lookup asked for.
type GoTypeMethods struct {
	Type    DefinitionSite            // The type's own definition; Path is empty without one
	Methods map[string]DefinitionSite // By method name
}

// GoMethodsNamed returns, per concrete Go type, its methods with one of the given names.
// scip-go names a concrete type's method `T#M().` and an interface's `I#M.`, so
// interfaces drop out. The method sets are the index's: what the type declared when it
// was built.
func (s *SCIPAdapter) GoMethodsNamed(names []string) map[string]*GoTypeMethods {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil || len(names) == 0 {
		return nil
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	types := make(map[string]*GoTypeMethods)
	for id, ref := range s.index.DefinitionIndex {
		if !strings.HasPrefix(id, "scip-go ") || !strings.HasSuffix(id, "().") {
			continue
		}
		hash := strings.LastIndex(id, "#")
		if hash < 0 {
			continue
		}
		method := id[hash+1 : len(id)-3]
		if !wanted[method] || strings.ContainsAny(method, "#./`()") {
			continue
		}
		site, ok := definitionSite(id, ref)
		if !ok {
			continue
		}
		typeID := id[:hash+1]
		entry := types[typeID]
		if entry == nil {
			entry = &GoTypeMethods{Methods: make(map[string]DefinitionSite)}
			if typeRef := s.index.DefinitionIndex[typeID]; typeRef != nil {
				entry.Type, _ = definitionSite(typeID, typeRef)
			}
			entry.Type.Symbol = typeID
			types[typeID] = entry
		}
		entry.Methods[method] = site
	}
	return types
}

// Implementations returns the symbols the index records as implementing a symbol. Not
// every indexer writes these: scip-go only does for types in the interface's package.
func (s *SCIPAdapter) Implementations(symbolID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	refs, err := s.index.FindImplementations(symbolID)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool, len(refs))
	var ids []string
	for _, ref := range refs {
		if !seen[ref.SymbolId] {
			seen[ref.SymbolId] = true
			ids = append(ids, ref.SymbolId)
		}
	}
	sort.Strings(ids)
	return ids
}

func definitionSite(id string, ref *OccurrenceRef) (DefinitionSite, bool) {
	if ref == nil || ref.Doc == nil || ref.Occ == nil {
		return DefinitionSite{}, false
	}
	loc := parseOccurrenceRange(ref.Occ, ref.Doc.RelativePath)
	if loc == nil {
		return DefinitionSite{}, false
	}
	return DefinitionSite{Symbol: id, Path: loc.FileId, Line: loc.StartLine + 1, Column: loc.StartColumn + 1}, true
}

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
	contexts := newContextReader(s.repoRoot)
	names := SpelledNames(symbolID)
	for i, scipRef := range scipRefs {
		references[i] = s.convertToReference(scipRef)
		if scipRef.Location != nil {
			references[i].Context = contexts.line(scipRef.Location.FileId, scipRef.Location.StartLine, names)
		}
	}

	// Real total, uncapped by MaxResults, so callers can report truncation honestly.
	total := s.index.CountReferences(symbolID, scipOpts)
	if total < len(references) {
		total = len(references)
	}

	// Compute completeness
	completeness := s.computeCompleteness()

	return &backends.ReferencesResult{
		References:      references,
		Completeness:    completeness,
		TotalReferences: total,
	}, nil
}

// DefinitionLocation returns where a symbol is defined, 1-indexed like every
// location this adapter returns, or nil if the index has no definition for it.
func (s *SCIPAdapter) DefinitionLocation(symbolID string) *backends.Location {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return nil
	}
	loc := findSymbolLocationFast(symbolID, s.index)
	if loc == nil {
		return nil
	}
	return &backends.Location{
		Path:      loc.FileId,
		Line:      loc.StartLine + 1, // Convert to 1-indexed
		Column:    loc.StartColumn + 1,
		EndLine:   loc.EndLine + 1,
		EndColumn: loc.EndColumn + 1,
	}
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
	// "enclosing symbols" are module/namespace-level (e.g. scip-typescript
	// emits a whole-file SymbolInformation whose EnclosingRange covers the
	// entire document, so a top-level reference with no enclosing
	// function/type resolves its "container" to that file-scope symbol).
	// GetSimpleName now always returns the last descriptor's name — even
	// for a namespace descriptor, e.g. "handler.ts" — but that's a file
	// path segment, not a meaningful enclosing symbol. Treat a
	// namespace-kind resolution the same as "no enclosing symbol
	// resolved" rather than surfacing a bare file name as if it were a
	// caller.
	fromSymbol := ""
	fromSymbolName := ""
	if scipRef.FromSymbol != "" {
		if fromId, err := ParseSCIPIdentifier(scipRef.FromSymbol); err == nil {
			if name := fromId.GetSimpleName(); name != "" && fromId.ExtractSymbolKind() != KindNamespace {
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

// IsCallable reports whether a symbol can be called, including a
// function-valued constant (see SCIPIndex.IsCallable).
func (s *SCIPAdapter) IsCallable(symbolId string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return false
	}
	return s.index.IsCallable(symbolId)
}

// GetIndex returns the underlying SCIP index for direct access.
// This is used by FTS population and other systems that need raw symbol data.
func (s *SCIPAdapter) GetIndex() *SCIPIndex {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.index
}
