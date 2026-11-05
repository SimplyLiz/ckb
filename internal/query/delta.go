package query

import (
	"context"
	"fmt"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/diff"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

// ApplyDelta applies a delta artifact to the storage layer.
// Returns warnings (non-fatal issues) and error (fatal issues).
func (e *Engine) ApplyDelta(ctx context.Context, delta *diff.Delta) ([]string, error) {
	start := time.Now()
	var warnings []string

	if delta == nil {
		return nil, fmt.Errorf("delta is nil")
	}

	e.logger.Info("Applying delta",
		"commit", delta.Commit,
		"symbols_added", delta.Stats.SymbolsAdded,
		"symbols_modified", delta.Stats.SymbolsModified,
		"symbols_deleted", delta.Stats.SymbolsDeleted,
	)

	// Get FTS manager for symbol updates
	ftsManager := storage.NewFTSManager(e.db.Conn(), storage.DefaultFTSConfig())

	// Ensure FTS schema exists
	if err := ftsManager.InitSchema(); err != nil {
		warnings = append(warnings, fmt.Sprintf("FTS schema init failed: %v", err))
	}

	// Collect FTS records for bulk update
	var ftsRecords []storage.SymbolFTSRecord

	// Apply symbol additions
	for _, sym := range delta.Deltas.Symbols.Added {
		if !scip.IsLocalSymbol(sym.ID) {
			ftsRecords = append(ftsRecords, symbolRecordToFTS(&sym))
		}
	}

	// Apply symbol modifications (add to FTS, it will replace existing)
	for _, sym := range delta.Deltas.Symbols.Modified {
		if !scip.IsLocalSymbol(sym.ID) {
			ftsRecords = append(ftsRecords, symbolRecordToFTS(&sym))
		}
	}

	// If we have FTS records, bulk insert them
	if len(ftsRecords) > 0 {
		if err := ftsManager.BulkInsert(ctx, ftsRecords); err != nil {
			warnings = append(warnings, fmt.Sprintf("FTS bulk insert failed: %v", err))
		}
	}

	// Apply symbol deletions (from FTS content table)
	for _, symID := range delta.Deltas.Symbols.Deleted {
		if _, err := e.db.Exec("DELETE FROM symbols_fts_content WHERE id = ?", symID); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to delete symbol %s from FTS: %v", symID, err))
		}
	}

	// Rebuild FTS after deletions
	if len(delta.Deltas.Symbols.Deleted) > 0 {
		if err := ftsManager.Rebuild(ctx); err != nil {
			warnings = append(warnings, fmt.Sprintf("FTS rebuild after deletions failed: %v", err))
		}
	}

	// Apply reference additions to refs table (create if not exists)
	if _, err := e.db.Exec(`
		CREATE TABLE IF NOT EXISTS refs (
			from_file_id TEXT NOT NULL,
			line INTEGER NOT NULL,
			column INTEGER NOT NULL,
			to_symbol_id TEXT NOT NULL,
			kind TEXT,
			language TEXT,
			PRIMARY KEY (from_file_id, line, column, to_symbol_id)
		)
	`); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to create refs table: %v", err))
	}

	for _, ref := range delta.Deltas.Refs.Added {
		if _, err := e.db.Exec(`
			INSERT OR REPLACE INTO refs (from_file_id, line, column, to_symbol_id, kind, language)
			VALUES (?, ?, ?, ?, ?, ?)
		`, ref.FromFileID, ref.Line, ref.Column, ref.ToSymbolID, ref.Kind, ref.Language); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to add ref: %v", err))
		}
	}

	// Apply reference deletions
	for _, refKey := range delta.Deltas.Refs.Deleted {
		// Parse composite key and delete
		if _, err := e.db.Exec(`
			DELETE FROM refs WHERE from_file_id || ':' || line || ':' || column || ':' || to_symbol_id = ?
		`, refKey); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to delete ref %s: %v", refKey, err))
		}
	}

	// Apply call edge additions
	if _, err := e.db.Exec(`
		CREATE TABLE IF NOT EXISTS call_edges (
			caller_file_id TEXT NOT NULL,
			call_line INTEGER NOT NULL,
			call_column INTEGER NOT NULL,
			callee_id TEXT NOT NULL,
			caller_id TEXT,
			PRIMARY KEY (caller_file_id, call_line, call_column, callee_id)
		)
	`); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to create call_edges table: %v", err))
	}

	for _, call := range delta.Deltas.CallGraph.Added {
		if _, err := e.db.Exec(`
			INSERT OR REPLACE INTO call_edges (caller_file_id, call_line, call_column, callee_id, caller_id)
			VALUES (?, ?, ?, ?, ?)
		`, call.CallerFileID, call.CallLine, call.CallColumn, call.CalleeID, call.CallerID); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to add call edge: %v", err))
		}
	}

	// Apply call edge deletions
	for _, callKey := range delta.Deltas.CallGraph.Deleted {
		if _, err := e.db.Exec(`
			DELETE FROM call_edges WHERE caller_file_id || ':' || call_line || ':' || call_column || ':' || callee_id = ?
		`, callKey); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to delete call edge %s: %v", callKey, err))
		}
	}

	// Apply file records
	if _, err := e.db.Exec(`
		CREATE TABLE IF NOT EXISTS files (
			path TEXT PRIMARY KEY,
			language TEXT,
			hash TEXT
		)
	`); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to create files table: %v", err))
	}

	for _, file := range delta.Deltas.Files.Added {
		if _, err := e.db.Exec(`
			INSERT OR REPLACE INTO files (path, language, hash)
			VALUES (?, ?, ?)
		`, file.Path, file.Language, file.Hash); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to add file %s: %v", file.Path, err))
		}
	}

	for _, file := range delta.Deltas.Files.Modified {
		if _, err := e.db.Exec(`
			INSERT OR REPLACE INTO files (path, language, hash)
			VALUES (?, ?, ?)
		`, file.Path, file.Language, file.Hash); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to modify file %s: %v", file.Path, err))
		}
	}

	for _, filePath := range delta.Deltas.Files.Deleted {
		if _, err := e.db.Exec(`DELETE FROM files WHERE path = ?`, filePath); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to delete file %s: %v", filePath, err))
		}
	}

	// Update metadata
	if _, err := e.db.Exec(`
		INSERT OR REPLACE INTO metadata (key, value)
		VALUES ('last_delta_commit', ?)
	`, delta.Commit); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to update metadata: %v", err))
	}

	if _, err := e.db.Exec(`
		INSERT OR REPLACE INTO metadata (key, value)
		VALUES ('last_delta_timestamp', ?)
	`, fmt.Sprintf("%d", delta.Timestamp)); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to update timestamp metadata: %v", err))
	}

	if delta.NewSnapshotID != "" {
		if _, err := e.db.Exec(`
			INSERT OR REPLACE INTO metadata (key, value)
			VALUES ('snapshot_id', ?)
		`, delta.NewSnapshotID); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to update snapshot_id metadata: %v", err))
		}
	}

	e.logger.Info("Delta applied",
		"duration_ms", time.Since(start).Milliseconds(),
		"warnings", len(warnings),
	)

	return warnings, nil
}

// symbolRecordToFTS converts a diff.SymbolRecord to storage.SymbolFTSRecord
func symbolRecordToFTS(sym *diff.SymbolRecord) storage.SymbolFTSRecord {
	return storage.SymbolFTSRecord{
		ID:            sym.ID,
		Name:          sym.Name,
		Kind:          sym.Kind,
		Documentation: sym.Documentation,
		Signature:     sym.Signature,
		FilePath:      sym.FileID,
		Language:      sym.Language,
	}
}
