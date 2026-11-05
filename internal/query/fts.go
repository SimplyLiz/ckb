package query

import (
	"context"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

// PopulateFTSFromSCIP populates the FTS5 symbol index from the loaded SCIP index.
// This should be called after the SCIP adapter loads its index.
func (e *Engine) PopulateFTSFromSCIP(ctx context.Context) error {
	e.ftsPopMu.Lock()
	defer e.ftsPopMu.Unlock()
	return e.populateFTSLocked(ctx)
}

// EnsureFTSCurrent rebuilds the FTS content when it was not built from the
// SCIP index and derivation logic currently in use, and does nothing
// otherwise. This is what makes an in-place upgrade work: content written by
// an older release (no stamp, or an older one) is rebuilt on first use,
// synchronously, so the very first query already sees correct kinds. Without
// it the CLI served stale rows until the .ckb directory was deleted, because
// 'ckb index' reports "nothing to do" for an unchanged commit and the
// background rebuild is cut off when a one-shot command exits.
func (e *Engine) EnsureFTSCurrent(ctx context.Context) error {
	if e.db == nil || e.scipAdapter == nil || !e.scipAdapter.IsAvailable() {
		return nil
	}
	e.ftsPopMu.Lock()
	defer e.ftsPopMu.Unlock()
	fts := storage.NewFTSManager(e.db.Conn(), storage.DefaultFTSConfig())
	if !e.ftsStaleLocked(ctx, fts) {
		return nil
	}
	// Cached answers were built from the old content (the query cache is
	// persisted in the database and keyed by query text only), so they go too.
	if err := e.ClearAllCache(); err != nil {
		e.logger.Warn("Failed to clear caches after FTS rebuild", "error", err.Error())
	}
	return e.populateFTSLocked(ctx)
}

func (e *Engine) ftsStaleLocked(ctx context.Context, fts *storage.FTSManager) bool {
	return fts.GetStamp(ctx) != fts.Stamp(e.scipAdapter.IndexStamp())
}

// invalidateStaleCaches is the cheap, synchronous half of EnsureFTSCurrent
// (one SELECT): when the FTS content is from another release or index it
// drops the persisted query/view caches immediately, so nothing computed by
// the older release is served while the rebuild is still running.
func (e *Engine) invalidateStaleCaches(ctx context.Context) {
	if e.db == nil || e.scipAdapter == nil || !e.scipAdapter.IsAvailable() {
		return
	}
	e.ftsPopMu.Lock()
	stale := e.ftsStaleLocked(ctx, storage.NewFTSManager(e.db.Conn(), storage.DefaultFTSConfig()))
	e.ftsPopMu.Unlock()
	if stale {
		if err := e.ClearAllCache(); err != nil {
			e.logger.Warn("Failed to clear stale caches", "error", err.Error())
		}
	}
}

func (e *Engine) populateFTSLocked(ctx context.Context) error {
	if e.scipAdapter == nil || !e.scipAdapter.IsAvailable() {
		e.logger.Debug("Skipping FTS population - SCIP adapter not available")
		return nil
	}

	start := time.Now()

	// Get the SCIP index
	index := e.scipAdapter.GetIndex()
	if index == nil {
		e.logger.Debug("Skipping FTS population - no SCIP index loaded")
		return nil
	}

	if len(index.Symbols) == 0 {
		e.logger.Debug("No symbols to index for FTS")
		return nil
	}

	// Get FTS manager from DB
	ftsManager := storage.NewFTSManager(e.db.Conn(), storage.DefaultFTSConfig())

	// Initialize schema if needed
	if err := ftsManager.InitSchema(); err != nil {
		e.logger.Warn("Failed to initialize FTS schema",
			"error", err.Error(),
		)
		return err
	}

	// Stream symbols in 10k chunks so we never materialise the full ~400MB
	// []SymbolFTSRecord slice for a 50k-file repo.
	const ftsChunkSize = 10_000
	symbolCount := 0
	if err := ftsManager.BulkInsertFunc(ctx, func(flush func([]storage.SymbolFTSRecord) error) error {
		chunk := make([]storage.SymbolFTSRecord, 0, ftsChunkSize)
		for _, sym := range index.Symbols {
			if scip.IsLocalSymbol(sym.Symbol) {
				continue
			}
			chunk = append(chunk, convertSymbolToFTSRecord(sym, index))
			if len(chunk) >= ftsChunkSize {
				if err := flush(chunk); err != nil {
					return err
				}
				symbolCount += len(chunk)
				chunk = chunk[:0]
			}
		}
		if len(chunk) > 0 {
			symbolCount += len(chunk)
			return flush(chunk)
		}
		return nil
	}); err != nil {
		e.logger.Warn("Failed to populate FTS index",
			"error", err.Error(),
			"symbol_count", symbolCount,
		)
		return err
	}

	if err := ftsManager.SetStamp(ctx, ftsManager.Stamp(e.scipAdapter.IndexStamp())); err != nil {
		e.logger.Warn("Failed to record FTS stamp", "error", err.Error())
	}

	e.logger.Info("FTS index populated from SCIP",
		"symbol_count", symbolCount,
		"duration_ms", time.Since(start).Milliseconds(),
	)

	return nil
}

// convertSymbolToFTSRecord converts a SCIP SymbolInformation to an FTS record
func convertSymbolToFTSRecord(symInfo *scip.SymbolInformation, index *scip.SCIPIndex) storage.SymbolFTSRecord {
	// Parse the SCIP identifier to extract useful info
	scipId, _ := scip.ParseSCIPIdentifier(symInfo.Symbol)

	// Get display name
	name := symInfo.DisplayName
	if name == "" && scipId != nil {
		name = scipId.GetSimpleName()
	}

	// Get kind string
	kind := inferKindString(symInfo.Kind, scipId)

	// Get documentation
	documentation := strings.Join(symInfo.Documentation, "\n")

	// Get file path from definition location using RefIndex for O(1) lookup.
	filePath := ""
	language := ""
	if index.RefIndex != nil {
		for _, ref := range index.RefIndex[symInfo.Symbol] {
			if ref.Occ.SymbolRoles&scip.SymbolRoleDefinition != 0 {
				filePath = ref.Doc.RelativePath
				language = ref.Doc.Language
				break
			}
		}
	} else {
		for _, doc := range index.Documents {
			for _, occ := range doc.Occurrences {
				if occ.Symbol == symInfo.Symbol && occ.SymbolRoles&scip.SymbolRoleDefinition != 0 {
					filePath = doc.RelativePath
					language = doc.Language
					break
				}
			}
			if filePath != "" {
				break
			}
		}
	}

	// Build a container-qualified signature (e.g. "Handler.handle") so a
	// member stays findable by searching its container's name — FTS5
	// matches across name/documentation/signature, so "Handler" still
	// finds "handle" even though its own display name doesn't contain it.
	//
	// Prefer the symbol's own descriptor chain (GetContainerName walks
	// back to the nearest enclosing type descriptor) over EnclosingSymbol:
	// scip-go/scip-typescript frequently leave EnclosingSymbol unset, but
	// the container is still recoverable from the symbol's own ID.
	signature := name
	if scipId != nil {
		if container := scipId.GetContainerName(); container != "" {
			signature = container + "." + name
		}
	}
	if signature == name && symInfo.EnclosingSymbol != "" {
		if enclosingId, err := scip.ParseSCIPIdentifier(symInfo.EnclosingSymbol); err == nil {
			if enclosingName := enclosingId.GetSimpleName(); enclosingName != "" {
				signature = enclosingName + "." + name
			}
		}
	}

	return storage.SymbolFTSRecord{
		ID:            symInfo.Symbol,
		Name:          name,
		Kind:          kind,
		Documentation: documentation,
		Signature:     signature,
		FilePath:      filePath,
		Language:      language,
	}
}

// inferKindString converts the SCIP kind int32 to a string
func inferKindString(kind int32, scipId *scip.SCIPIdentifier) string {
	switch kind {
	case 1:
		return "class"
	case 2:
		return "interface"
	case 3:
		return "enum"
	case 6:
		return "function"
	case 7:
		return "variable"
	case 8:
		return "constant"
	case 9:
		return "method"
	case 10:
		return "property"
	case 11:
		return "field"
	case 12:
		return "parameter"
	case 19:
		return "namespace"
	case 20:
		return "package"
	case 21:
		return "type"
	default:
		// Fall back to descriptor-based inference
		if scipId != nil {
			return string(scipId.ExtractSymbolKind())
		}
		return "unknown"
	}
}

// SearchSymbolsFTS performs FTS5-accelerated symbol search.
// Returns results from FTS if available, falls back to nil if not.
func (e *Engine) SearchSymbolsFTS(ctx context.Context, query string, limit int) ([]storage.FTSSearchResult, error) {
	if err := e.EnsureFTSCurrent(ctx); err != nil {
		e.logger.Warn("Failed to refresh stale FTS content", "error", err.Error())
	}

	// Get FTS manager
	ftsManager := storage.NewFTSManager(e.db.Conn(), storage.DefaultFTSConfig())

	// Check if FTS has data (error means FTS not available, not a failure)
	stats, statsErr := ftsManager.GetStats(ctx)
	if statsErr != nil {
		return nil, nil //nolint:nilerr // intentional: FTS unavailable = use fallback
	}

	indexedSymbols, ok := stats["indexed_symbols"].(int)
	if !ok || indexedSymbols == 0 {
		// No FTS data available
		return nil, nil
	}

	// Perform FTS search
	return ftsManager.Search(ctx, query, limit)
}

// RefreshFTS rebuilds the FTS index from current SCIP data.
func (e *Engine) RefreshFTS(ctx context.Context) error {
	return e.PopulateFTSFromSCIP(ctx)
}

// GetFTSStats returns statistics about the FTS index
func (e *Engine) GetFTSStats(ctx context.Context) (map[string]interface{}, error) {
	ftsManager := storage.NewFTSManager(e.db.Conn(), storage.DefaultFTSConfig())
	return ftsManager.GetStats(ctx)
}

// symbolsForFiles returns symbols defined in any of the given file paths, grouped
// by file path. A single WHERE file_path IN (…) query replaces N individual round-
// trips, which matters when SemanticSearchWithLIP returns up to 20 file URIs.
// limitPerFile is enforced per file in Go after the batch query returns.
func (e *Engine) symbolsForFiles(_ context.Context, filePaths []string, limitPerFile int) map[string][]storage.FTSSearchResult {
	if e.db == nil || len(filePaths) == 0 {
		return nil
	}

	// Build IN clause placeholders.
	placeholders := strings.Repeat("?,", len(filePaths))
	placeholders = placeholders[:len(placeholders)-1] // trim trailing comma
	args := make([]interface{}, len(filePaths))
	for i, p := range filePaths {
		args[i] = p
	}

	rows, err := e.db.Query(
		`SELECT id, name, kind, COALESCE(documentation,''), COALESCE(signature,''), file_path, COALESCE(language,'')
		 FROM symbols_fts_content WHERE file_path IN (`+placeholders+`) AND id NOT LIKE 'local %'`,
		args...)
	if err != nil {
		return nil
	}
	defer rows.Close() //nolint:errcheck

	out := make(map[string][]storage.FTSSearchResult, len(filePaths))
	for rows.Next() {
		var r storage.FTSSearchResult
		if err := rows.Scan(&r.ID, &r.Name, &r.Kind, &r.Documentation, &r.Signature, &r.FilePath, &r.Language); err != nil {
			continue
		}
		existing := out[r.FilePath]
		if limitPerFile <= 0 || len(existing) < limitPerFile {
			out[r.FilePath] = append(existing, r)
		}
	}
	return out
}
