package incremental

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/project"
)

// SCIPExtractor extracts per-file data from SCIP index
type SCIPExtractor struct {
	repoRoot  string
	indexPath string
	logger    *slog.Logger
}

// NewSCIPExtractor creates a new SCIP extractor
// indexPath should be the configured SCIP index path (default: index.scip)
func NewSCIPExtractor(repoRoot string, indexPath string, logger *slog.Logger) *SCIPExtractor {
	// If indexPath is relative, make it absolute from repoRoot
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(repoRoot, indexPath)
	}
	return &SCIPExtractor{
		repoRoot:  repoRoot,
		indexPath: indexPath,
		logger:    logger,
	}
}

// ExtractDeltas runs scip-go and extracts data only for changed files
func (e *SCIPExtractor) ExtractDeltas(changedFiles []ChangedFile) (*SymbolDelta, error) {
	// Build set of paths we care about (for fast lookup during iteration)
	changedPaths := make(map[string]ChangedFile)
	for _, cf := range changedFiles {
		changedPaths[cf.Path] = cf
		// For renames, we need to extract from the new path
		if cf.OldPath != "" {
			changedPaths[cf.Path] = cf // New path maps to the change
		}
	}

	// Load full SCIP index (required - protobuf doesn't stream)
	// This is unavoidable, but we only process changed documents
	index, err := scip.LoadSCIPIndex(e.indexPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load SCIP index: %w", err)
	}

	delta := &SymbolDelta{}

	// Iterate documents, only process those in changedPaths set
	for _, doc := range index.Documents {
		cf, isChanged := changedPaths[doc.RelativePath]
		if !isChanged {
			// Skip this document - not in our changed set
			continue
		}

		// This is a changed file - extract its data
		fileDelta := e.extractFileDelta(doc, cf)
		delta.FileDeltas = append(delta.FileDeltas, fileDelta)

		// Update stats
		switch cf.ChangeType {
		case ChangeAdded:
			delta.Stats.FilesAdded++
		case ChangeModified, ChangeRenamed:
			delta.Stats.FilesChanged++
		}
		delta.Stats.SymbolsAdded += len(fileDelta.Symbols)
		delta.Stats.RefsAdded += len(fileDelta.Refs)
		delta.Stats.CallsAdded += len(fileDelta.CallEdges) // v1.1
	}

	// Handle deleted files (they won't be in the new SCIP index)
	for _, cf := range changedFiles {
		if cf.ChangeType == ChangeDeleted {
			delta.FileDeltas = append(delta.FileDeltas, FileDelta{
				Path:       cf.Path,
				OldPath:    cf.Path, // Same as path for deletes
				ChangeType: ChangeDeleted,
				// Symbols and Refs will be empty - updater handles deletion
			})
			delta.Stats.FilesDeleted++
		}
	}

	return delta, nil
}

// RunIndexer executes the SCIP indexer for the given language configuration.
// It handles output path configuration and fixed-output indexers (like scip-php).
func (e *SCIPExtractor) RunIndexer(config *project.IndexerConfig) error {
	// Ensure output directory exists
	outputDir := filepath.Dir(e.indexPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create index directory %s: %w", outputDir, err)
	}

	// Build command with output path
	cmd := config.BuildCommand(e.indexPath)
	cmd.Dir = e.repoRoot
	cmd.Stdout = os.Stderr // Show indexer output
	cmd.Stderr = os.Stderr

	defer project.UndoInferredTSConfig(cmd.Dir, strings.Join(cmd.Args, " "))()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("indexer failed: %w", err)
	}

	// Handle indexers with fixed output paths (e.g., scip-php)
	if config.HasFixedOutput() {
		fixedPath := filepath.Join(e.repoRoot, config.FixedOutput)
		if fixedPath != e.indexPath {
			if err := os.Rename(fixedPath, e.indexPath); err != nil {
				return fmt.Errorf("failed to move index from %s to %s: %w",
					fixedPath, e.indexPath, err)
			}
		}
	}

	// scip-python leaves imports through sys.path unresolved; the full index path
	// repairs them in indexplan.Finalize, and this one has to as well, or the deltas
	// read below lose those references again.
	if config.Cmd == "scip-python" {
		if _, err := scip.RepairPythonImports(e.repoRoot, e.indexPath); err != nil {
			slog.Warn("Could not repair the Python imports in the index", "error", err.Error())
		}
	}

	return nil
}

// RunSCIPGo executes the scip-go indexer with configured output path.
// Deprecated: Use RunIndexer with the appropriate IndexerConfig instead.
func (e *SCIPExtractor) RunSCIPGo() error {
	config := project.GetIndexerConfig(project.LangGo)
	if config == nil {
		return fmt.Errorf("no indexer configuration for Go")
	}
	return e.RunIndexer(config)
}

// IsIndexerInstalled checks if the indexer for the given config is available.
func (e *SCIPExtractor) IsIndexerInstalled(config *project.IndexerConfig) bool {
	return config.IsInstalled()
}

// LoadIndex loads the SCIP index from disk
func (e *SCIPExtractor) LoadIndex() (*scip.SCIPIndex, error) {
	return scip.LoadSCIPIndex(e.indexPath)
}

// extractFileDelta extracts symbols and refs from a SCIP document
func (e *SCIPExtractor) extractFileDelta(doc *scip.Document, change ChangedFile) FileDelta {
	delta := FileDelta{
		Path:       change.Path,
		OldPath:    change.OldPath, // Preserve for rename handling
		ChangeType: change.ChangeType,
	}

	// If this is a rename, OldPath is the path to delete from
	// If OldPath is empty, use Path (for add/modify)
	if delta.OldPath == "" {
		delta.OldPath = delta.Path
	}

	// Use file hash from change detection if available
	// Otherwise compute it now
	if change.Hash != "" {
		delta.Hash = change.Hash
	} else {
		// Compute hash from actual file (not SCIP, for true content hash)
		fullPath := filepath.Join(e.repoRoot, change.Path)
		if h, err := hashFile(fullPath); err == nil {
			delta.Hash = h
		}
	}

	// Build symbol info map for enrichment
	symbolInfo := make(map[string]*scip.SymbolInformation)
	for _, sym := range doc.Symbols {
		symbolInfo[sym.Symbol] = sym
	}

	// Extract definitions (symbols)
	// IMPORTANT: Filter out local symbols and ensure consistent FilePath
	for _, occ := range doc.Occurrences {
		if occ.SymbolRoles&scip.SymbolRoleDefinition == 0 {
			continue // Not a definition
		}

		// Skip local symbols (they start with "local ")
		if isLocalSymbol(occ.Symbol) {
			continue
		}

		sym := Symbol{
			ID:       occ.Symbol,
			FilePath: change.Path, // Use canonical path from change, not doc
		}

		// Parse range (SCIP is 0-indexed, we use 1-indexed)
		if len(occ.Range) >= 1 {
			sym.StartLine = int(occ.Range[0]) + 1 // #nosec G115 -- SCIP int32 coordinates fit in int
		}
		sym.EndLine = definitionEndLine(sym.StartLine, occ.EnclosingRange)

		// Enrich with symbol information if available
		var kind int32
		if info, ok := symbolInfo[occ.Symbol]; ok {
			kind = info.Kind
			sym.Name = extractSymbolName(occ.Symbol, info.DisplayName)
			sym.Kind = mapSymbolKind(info.Kind)
			if len(info.Documentation) > 0 {
				sym.Documentation = info.Documentation[0]
			}
		} else {
			// Minimal info from symbol ID alone
			sym.Name = extractSymbolName(occ.Symbol, "")
			sym.Kind = "unknown"
		}
		sym.Callable = scip.IsCallableDefinition(occ.Symbol, kind, occ.Range, occ.EnclosingRange)

		delta.Symbols = append(delta.Symbols, sym)
	}

	// Extract references (non-definitions)
	for _, occ := range doc.Occurrences {
		if occ.SymbolRoles&scip.SymbolRoleDefinition != 0 {
			continue // Skip definitions
		}

		// Skip local symbols
		if isLocalSymbol(occ.Symbol) {
			continue
		}

		ref := Reference{
			FromFile:   change.Path,
			ToSymbolID: occ.Symbol,
			Kind:       "reference",
		}

		if len(occ.Range) >= 1 {
			ref.FromLine = int(occ.Range[0]) + 1 // #nosec G115 -- SCIP int32 coordinates fit in int
		}

		delta.Refs = append(delta.Refs, ref)
	}

	// v1.1: Extract call edges
	// Call edges are references to callable symbols (functions/methods)
	callable := callableDefinitions(delta.Symbols)
	for _, occ := range doc.Occurrences {
		// Skip definitions
		if occ.SymbolRoles&scip.SymbolRoleDefinition != 0 {
			continue
		}

		// Skip local symbols
		if isLocalSymbol(occ.Symbol) {
			continue
		}

		// Check if callee is callable (function/method)
		if !callable[occ.Symbol] && !isCallableReference(occ.Symbol, symbolInfo) {
			continue
		}

		edge := CallEdge{
			CallerFile: change.Path,
			CalleeID:   occ.Symbol,
		}

		// Parse location (SCIP is 0-indexed, we use 1-indexed)
		if len(occ.Range) >= 1 {
			edge.Line = int(occ.Range[0]) + 1 // #nosec G115 -- SCIP int32 coordinates fit in int
		}
		if len(occ.Range) >= 2 {
			edge.Column = int(occ.Range[1]) + 1 // #nosec G115 -- SCIP int32 coordinates fit in int
		}
		if len(occ.Range) >= 4 {
			edge.EndColumn = int(occ.Range[3]) + 1 // #nosec G115 -- SCIP int32 coordinates fit in int
		}

		// Resolve caller symbol (may be empty for top-level calls)
		edge.CallerID = e.resolveCallerSymbol(delta.Symbols, edge.Line)

		delta.CallEdges = append(delta.CallEdges, edge)
	}

	// Compute document hash for change detection optimization
	delta.SCIPDocumentHash = computeDocHash(doc)
	delta.SymbolCount = len(delta.Symbols)

	return delta
}

// extractSymbolName extracts the short name from a SCIP symbol ID
// Example: "scip-go gomod github.com/foo/bar 1.0 pkg.Func()." -> "Func"
func extractSymbolName(symbolID string, displayName string) string {
	if displayName != "" {
		return displayName
	}

	// Parse the symbol ID to extract the name
	// SCIP format: scheme manager name version descriptor.
	parts := strings.Split(symbolID, " ")
	if len(parts) < 5 {
		return symbolID
	}

	descriptor := parts[len(parts)-1]
	// Remove trailing . if present
	descriptor = strings.TrimSuffix(descriptor, ".")

	// Get the last component (the actual name)
	// Handles: package.Type.Method() -> Method
	components := strings.Split(descriptor, ".")
	if len(components) > 0 {
		name := components[len(components)-1]
		// Remove parentheses for functions/methods
		name = strings.TrimSuffix(name, "()")
		return name
	}

	return descriptor
}

// isLocalSymbol checks if a symbol is file-local
func isLocalSymbol(symbolID string) bool {
	return len(symbolID) > 6 && symbolID[:6] == "local "
}

// mapSymbolKind names a SCIP SymbolInformation.Kind in lower case
// ("function", "staticmethod", "enummember"), or "unknown" for none.
func mapSymbolKind(kind int32) string {
	if kind == 0 {
		return "unknown"
	}
	name, ok := scippb.SymbolInformation_Kind_name[kind]
	if !ok {
		return "unknown"
	}
	return strings.ToLower(name)
}

// computeDocHash computes a hash of the SCIP document for caching
// This lets us skip updates if the SCIP doc is unchanged
// (e.g., formatting changes that don't affect symbols)
func computeDocHash(doc *scip.Document) string {
	h := sha256.New()

	// Include document path for safety
	h.Write([]byte(doc.RelativePath))

	var buf [4]byte

	// Hash occurrences (order-dependent for stability)
	for _, occ := range doc.Occurrences {
		h.Write([]byte(occ.Symbol))

		// Properly encode int32 range values
		for _, r := range occ.Range {
			binary.LittleEndian.PutUint32(buf[:], uint32(r)) // #nosec G115 //nolint:gosec // SCIP coordinates are always non-negative
			h.Write(buf[:])
		}

		// Include role bits for stability
		binary.LittleEndian.PutUint32(buf[:], uint32(occ.SymbolRoles)) // #nosec G115 //nolint:gosec // SCIP role bitmask is non-negative
		h.Write(buf[:])
	}

	// Include symbol information (definitions, docs, kinds)
	for _, sym := range doc.Symbols {
		h.Write([]byte(sym.Symbol))
		binary.LittleEndian.PutUint32(buf[:], uint32(sym.Kind)) // #nosec G115 //nolint:gosec // SCIP symbol kind enum is non-negative (0-26)
		h.Write(buf[:])
		for _, d := range sym.Documentation {
			h.Write([]byte(d))
		}
	}

	return fmt.Sprintf("%x", h.Sum(nil))[:16] // Truncate for storage
}

// hashFile computes SHA256 of a file's contents
func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:]), nil
}

// isCallableReference decides whether a reference targets a callable, from its
// ID and, when the target is defined in the same document, its Kind. A target
// defined in this document has already been decided by callableDefinitions,
// which also sees function-valued constants.
func isCallableReference(symbolID string, symbolInfo map[string]*scip.SymbolInformation) bool {
	var kind int32
	if info, ok := symbolInfo[symbolID]; ok {
		kind = info.Kind
	}
	return scip.IsCallableReference(symbolID, kind)
}

// callableDefinitions is the set of symbols a document defines that can be
// called, so a call to one of them counts even when only its definition can
// tell (a function-valued constant).
func callableDefinitions(symbols []Symbol) map[string]bool {
	set := make(map[string]bool)
	for _, sym := range symbols {
		if sym.Callable {
			set[sym.ID] = true
		}
	}
	return set
}

// definitionEndLine is the 1-indexed last line of a definition, from its
// EnclosingRange. The definition's own Range covers only the name, so without
// an EnclosingRange the end is the start, and resolveCallerSymbol infers it.
func definitionEndLine(startLine int, enclosing []int32) int {
	switch len(enclosing) {
	case 3:
		return int(enclosing[0]) + 1 // #nosec G115 -- SCIP int32 coordinates fit in int
	case 4:
		return int(enclosing[2]) + 1 // #nosec G115 -- SCIP int32 coordinates fit in int
	}
	return startLine
}

// extractFileDeltaFromProto is the proto-native equivalent of extractFileDelta.
// It operates directly on *scippb.Document, avoiding all intermediate
// *scip.Document / *scip.Occurrence / *scip.SymbolInformation allocations
// that convertDocument would create. Used by PopulateFromFullIndexStreaming.
func (e *SCIPExtractor) extractFileDeltaFromProto(pbDoc *scippb.Document, change ChangedFile) FileDelta {
	delta := FileDelta{
		Path:       change.Path,
		OldPath:    change.OldPath,
		ChangeType: change.ChangeType,
	}
	if delta.OldPath == "" {
		delta.OldPath = delta.Path
	}

	if change.Hash != "" {
		delta.Hash = change.Hash
	} else {
		fullPath := filepath.Join(e.repoRoot, change.Path)
		if h, err := hashFile(fullPath); err == nil {
			delta.Hash = h
		}
	}

	// Build symbol info map — values are pointers into the proto message; no new allocs.
	symbolInfo := make(map[string]*scippb.SymbolInformation, len(pbDoc.Symbols))
	for _, sym := range pbDoc.Symbols {
		symbolInfo[sym.Symbol] = sym
	}

	const defRole = int32(1) // scippb.SymbolRole_Definition == 1

	// Extract definitions
	for _, occ := range pbDoc.Occurrences {
		if occ.SymbolRoles&defRole == 0 {
			continue
		}
		if isLocalSymbol(occ.Symbol) {
			continue
		}
		sym := Symbol{
			ID:       occ.Symbol,
			FilePath: change.Path,
		}
		if len(occ.Range) >= 1 {
			sym.StartLine = int(occ.Range[0]) + 1 // #nosec G115
		}
		sym.EndLine = definitionEndLine(sym.StartLine, occ.EnclosingRange)
		var kind int32
		if info, ok := symbolInfo[occ.Symbol]; ok {
			kind = int32(info.Kind)
			sym.Name = extractSymbolName(occ.Symbol, info.DisplayName)
			sym.Kind = mapSymbolKind(kind)
			if len(info.Documentation) > 0 {
				sym.Documentation = info.Documentation[0]
			}
		} else {
			sym.Name = extractSymbolName(occ.Symbol, "")
			sym.Kind = "unknown"
		}
		sym.Callable = scip.IsCallableDefinition(occ.Symbol, kind, occ.Range, occ.EnclosingRange)
		delta.Symbols = append(delta.Symbols, sym)
	}

	// Extract references
	for _, occ := range pbDoc.Occurrences {
		if occ.SymbolRoles&defRole != 0 {
			continue
		}
		if isLocalSymbol(occ.Symbol) {
			continue
		}
		ref := Reference{
			FromFile:   change.Path,
			ToSymbolID: occ.Symbol,
			Kind:       "reference",
		}
		if len(occ.Range) >= 1 {
			ref.FromLine = int(occ.Range[0]) + 1 // #nosec G115
		}
		delta.Refs = append(delta.Refs, ref)
	}

	// Extract call edges
	callable := callableDefinitions(delta.Symbols)
	for _, occ := range pbDoc.Occurrences {
		if occ.SymbolRoles&defRole != 0 {
			continue
		}
		if isLocalSymbol(occ.Symbol) {
			continue
		}
		if !callable[occ.Symbol] && !isCallableReferenceFromProto(occ.Symbol, symbolInfo) {
			continue
		}
		edge := CallEdge{
			CallerFile: change.Path,
			CalleeID:   occ.Symbol,
		}
		if len(occ.Range) >= 1 {
			edge.Line = int(occ.Range[0]) + 1 // #nosec G115
		}
		if len(occ.Range) >= 2 {
			edge.Column = int(occ.Range[1]) + 1 // #nosec G115
		}
		if len(occ.Range) >= 4 {
			edge.EndColumn = int(occ.Range[3]) + 1 // #nosec G115
		}
		edge.CallerID = e.resolveCallerSymbol(delta.Symbols, edge.Line)
		delta.CallEdges = append(delta.CallEdges, edge)
	}

	delta.SCIPDocumentHash = computeDocHashProto(pbDoc)
	delta.SymbolCount = len(delta.Symbols)
	return delta
}

// computeDocHashProto is the proto-native equivalent of computeDocHash.
func computeDocHashProto(pbDoc *scippb.Document) string {
	h := sha256.New()
	h.Write([]byte(pbDoc.RelativePath))
	var buf [4]byte
	for _, occ := range pbDoc.Occurrences {
		h.Write([]byte(occ.Symbol))
		for _, r := range occ.Range {
			binary.LittleEndian.PutUint32(buf[:], uint32(r)) // #nosec G115 //nolint:gosec
			h.Write(buf[:])
		}
		binary.LittleEndian.PutUint32(buf[:], uint32(occ.SymbolRoles)) // #nosec G115 //nolint:gosec
		h.Write(buf[:])
	}
	for _, sym := range pbDoc.Symbols {
		h.Write([]byte(sym.Symbol))
		binary.LittleEndian.PutUint32(buf[:], uint32(sym.Kind)) // #nosec G115 //nolint:gosec
		h.Write(buf[:])
		for _, d := range sym.Documentation {
			h.Write([]byte(d))
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// isCallableReferenceFromProto is the proto-native isCallableReference.
func isCallableReferenceFromProto(symbolID string, symbolInfo map[string]*scippb.SymbolInformation) bool {
	var kind int32
	if info, ok := symbolInfo[symbolID]; ok {
		kind = int32(info.Kind)
	}
	return scip.IsCallableReference(symbolID, kind)
}

// resolveCallerSymbol finds the enclosing callable symbol for a call site
// Returns the symbol ID of the innermost function/method containing the call,
// or empty string if unresolved (e.g., top-level var initializers)
func (e *SCIPExtractor) resolveCallerSymbol(symbols []Symbol, callLine int) string {
	// Filter to callables only
	var callables []Symbol
	for _, sym := range symbols {
		if sym.Callable {
			callables = append(callables, sym)
		}
	}

	if len(callables) == 0 {
		return ""
	}

	// Sort by start line ascending
	sort.Slice(callables, func(i, j int) bool {
		return callables[i].StartLine < callables[j].StartLine
	})

	// Find enclosing callable (innermost)
	var enclosing *Symbol
	for i := range callables {
		sym := &callables[i]
		endLine := sym.EndLine

		// Infer end line if not set or same as start
		if endLine == 0 || endLine == sym.StartLine {
			if i+1 < len(callables) {
				endLine = callables[i+1].StartLine - 1
			} else {
				endLine = sym.StartLine + 500 // DefaultMaxFunctionLines
			}
		}

		if sym.StartLine <= callLine && callLine <= endLine {
			enclosing = sym
			// Don't break - keep looking for innermost (nested functions)
		}
	}

	if enclosing != nil {
		return enclosing.ID
	}
	return "" // NULL caller - top-level call or unresolved
}
