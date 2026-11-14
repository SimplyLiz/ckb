package incremental

import (
	"database/sql"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

// IndexUpdater applies incremental updates to the database
type IndexUpdater struct {
	db         *storage.DB
	store      *Store
	depTracker *DependencyTracker
	config     *Config
	logger     *slog.Logger
}

// NewIndexUpdater creates a new incremental updater
func NewIndexUpdater(db *storage.DB, store *Store, logger *slog.Logger) *IndexUpdater {
	config := DefaultConfig()
	return &IndexUpdater{
		db:         db,
		store:      store,
		depTracker: NewDependencyTracker(db, store, &config.Transitive, logger),
		config:     config,
		logger:     logger,
	}
}

// SetConfig updates the configuration
func (u *IndexUpdater) SetConfig(config *Config) {
	u.config = config
	u.depTracker = NewDependencyTracker(u.db, u.store, &config.Transitive, u.logger)
}

// applyStmts holds pre-prepared statements shared across files in a single transaction.
type applyStmts struct {
	symbol *sql.Stmt
	call   *sql.Stmt
	deps   *sql.Stmt
}

// ApplyDelta applies symbol changes to the database
// V1.1 updates: indexed_files, file_symbols, callgraph
// V2.0 updates: file_deps for transitive invalidation
func (u *IndexUpdater) ApplyDelta(delta *SymbolDelta) error {
	// Build symbol-to-file map for dependency tracking
	symbolToFile, err := u.depTracker.BuildSymbolToFileMap()
	if err != nil {
		u.logger.Warn("Failed to build symbol-to-file map", "error", err.Error())
		symbolToFile = make(map[string]string) // Continue with empty map
	}

	// Also add symbols from this delta (they may not be in DB yet)
	for _, fd := range delta.FileDeltas {
		for _, sym := range fd.Symbols {
			symbolToFile[sym.ID] = fd.Path
		}
	}

	return u.db.WithTx(func(tx *sql.Tx) error {
		symbolStmt, err := tx.Prepare(`INSERT OR IGNORE INTO file_symbols (file_path, symbol_id) VALUES (?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare file_symbols insert: %w", err)
		}
		defer symbolStmt.Close() //nolint:errcheck

		callStmt, err := tx.Prepare(`
			INSERT OR REPLACE INTO callgraph
			(caller_id, callee_id, caller_file, call_line, call_col, call_end_col)
			VALUES (?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return fmt.Errorf("prepare callgraph insert: %w", err)
		}
		defer callStmt.Close() //nolint:errcheck

		depsStmt, err := tx.Prepare(`INSERT OR IGNORE INTO file_deps (dependent_file, defining_file) VALUES (?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare file_deps insert: %w", err)
		}
		defer depsStmt.Close() //nolint:errcheck

		stmts := applyStmts{symbol: symbolStmt, call: callStmt, deps: depsStmt}

		for _, fileDelta := range delta.FileDeltas {
			if err := u.applyFileDelta(tx, stmts, fileDelta, symbolToFile); err != nil {
				return fmt.Errorf("failed to update %s: %w", fileDelta.Path, err)
			}
		}
		return nil
	})
}

// ApplyDeltaWithInvalidation applies delta and triggers transitive invalidation
// Returns the list of files that were enqueued for rescanning
func (u *IndexUpdater) ApplyDeltaWithInvalidation(delta *SymbolDelta) (int, error) {
	// Apply the delta first
	if err := u.ApplyDelta(delta); err != nil {
		return 0, err
	}

	// Collect changed files for invalidation
	var changedFiles []string
	for _, fd := range delta.FileDeltas {
		if fd.ChangeType != ChangeDeleted {
			changedFiles = append(changedFiles, fd.Path)
		}
	}

	// Trigger transitive invalidation
	enqueued, err := u.depTracker.InvalidateDependents(changedFiles)
	if err != nil {
		return enqueued, fmt.Errorf("invalidate dependents: %w", err)
	}

	return enqueued, nil
}

// applyFileDelta applies changes for a single file
// CRITICAL: Uses OldPath for deletions to handle renames correctly
// V2.0: symbolToFile maps symbols to their defining files for dependency tracking
func (u *IndexUpdater) applyFileDelta(tx *sql.Tx, stmts applyStmts, delta FileDelta, symbolToFile map[string]string) error {
	switch delta.ChangeType {
	case ChangeDeleted:
		return u.deleteFileData(tx, delta.Path)

	case ChangeAdded:
		return u.insertFileData(tx, stmts, delta, symbolToFile)

	case ChangeModified:
		if err := u.deleteFileData(tx, delta.Path); err != nil {
			return err
		}
		return u.insertFileData(tx, stmts, delta, symbolToFile)

	case ChangeRenamed:
		// CRITICAL: Delete using OldPath, insert using Path
		if delta.OldPath == "" {
			return fmt.Errorf("rename without OldPath for %s", delta.Path)
		}
		if err := u.deleteFileData(tx, delta.OldPath); err != nil {
			return err
		}
		return u.insertFileData(tx, stmts, delta, symbolToFile)
	}

	return nil
}

// deleteFileData removes all data owned by a file
// This includes: file_symbols mapping, indexed_files entry, callgraph edges, and file_deps
func (u *IndexUpdater) deleteFileData(tx *sql.Tx, path string) error {
	// 1. Delete file_symbols mapping for this file
	_, err := tx.Exec(`DELETE FROM file_symbols WHERE file_path = ?`, path)
	if err != nil {
		return fmt.Errorf("delete file_symbols: %w", err)
	}

	// 2. Delete file tracking entry
	_, err = tx.Exec(`DELETE FROM indexed_files WHERE path = ?`, path)
	if err != nil {
		return fmt.Errorf("delete indexed_files: %w", err)
	}

	// 3. Delete call edges owned by this file (v1.1: caller-owned edges invariant)
	_, err = tx.Exec(`DELETE FROM callgraph WHERE caller_file = ?`, path)
	if err != nil {
		return fmt.Errorf("delete callgraph: %w", err)
	}

	// 4. Delete file dependencies for this file (v2: transitive invalidation)
	_, err = tx.Exec(`DELETE FROM file_deps WHERE dependent_file = ?`, path)
	if err != nil {
		return fmt.Errorf("delete file_deps: %w", err)
	}

	u.logger.Debug("Deleted file data", "path", path)

	return nil
}

// insertFileData adds all data for a file from its FileDelta.
// Uses pre-prepared statements from stmts — no Prepare/Close inside.
// V2.0: symbolToFile is used to update file_deps for transitive invalidation.
// deleteFileData is always called before insertFileData for modified/renamed files,
// so skipDelete=true is correct: the per-file DELETE has already happened.
func (u *IndexUpdater) insertFileData(tx *sql.Tx, stmts applyStmts, delta FileDelta, symbolToFile map[string]string) error {
	now := time.Now()

	// 1. Insert or replace file tracking entry
	_, err := tx.Exec(`
		INSERT OR REPLACE INTO indexed_files (path, hash, mtime, indexed_at, scip_document_hash, symbol_count)
		VALUES (?, ?, ?, ?, ?, ?)
	`, delta.Path, delta.Hash, now.Unix(), now.Unix(), delta.SCIPDocumentHash, delta.SymbolCount)
	if err != nil {
		return fmt.Errorf("insert indexed_files: %w", err)
	}

	// 2. Insert file_symbols using pre-prepared stmt
	for _, sym := range delta.Symbols {
		if _, err := stmts.symbol.Exec(delta.Path, sym.ID); err != nil {
			return fmt.Errorf("insert file_symbol for %s: %w", sym.ID, err)
		}
	}

	// 3. Insert call edges using pre-prepared stmt (v1.1)
	if len(delta.CallEdges) > 0 {
		if err := u.insertCallEdgesWithStmt(stmts.call, delta); err != nil {
			return fmt.Errorf("insert callgraph: %w", err)
		}
	}

	// 4. Update file_deps for transitive invalidation (v2)
	// skipDelete=true: deleteFileData already cleared file_deps for this path
	if len(delta.Refs) > 0 && symbolToFile != nil {
		if err := u.depTracker.updateFileDepsWithStmt(tx, stmts.deps, delta.Path, delta.Refs, symbolToFile, true); err != nil {
			u.logger.Warn("Failed to update file_deps", "path", delta.Path, "error", err.Error())
		}
	}

	u.logger.Debug("Inserted file data",
		"path", delta.Path,
		"symbolCount", len(delta.Symbols),
		"refCount", len(delta.Refs),
		"callEdges", len(delta.CallEdges),
	)

	return nil
}

// insertCallEdgesWithStmt inserts call edges for a file using a pre-prepared statement.
func (u *IndexUpdater) insertCallEdgesWithStmt(stmt *sql.Stmt, delta FileDelta) error {
	for _, edge := range delta.CallEdges {
		// Use nil for caller_id (may be empty for top-level calls)
		var callerID interface{}
		if edge.CallerID != "" {
			callerID = edge.CallerID
		}

		var endCol interface{}
		if edge.EndColumn > 0 {
			endCol = edge.EndColumn
		}

		if _, err := stmt.Exec(callerID, edge.CalleeID, edge.CallerFile,
			edge.Line, edge.Column, endCol); err != nil {
			return err
		}
	}
	return nil
}

// bulkInsertFileSymbols inserts file_symbols rows using batched multi-row VALUES.
// Batches of 499 rows keep the parameter count safely under SQLite's 32766 limit.
func bulkInsertFileSymbols(tx *sql.Tx, filePath string, syms []Symbol) error {
	const rowsPerBatch = 499
	for i := 0; i < len(syms); i += rowsPerBatch {
		chunk := syms[i:min(i+rowsPerBatch, len(syms))]
		var sb strings.Builder
		sb.WriteString("INSERT OR IGNORE INTO file_symbols (file_path, symbol_id) VALUES ")
		args := make([]interface{}, 0, len(chunk)*2)
		for j, sym := range chunk {
			if j > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString("(?,?)")
			args = append(args, filePath, sym.ID)
		}
		if _, err := tx.Exec(sb.String(), args...); err != nil {
			return fmt.Errorf("bulk insert file_symbols: %w", err)
		}
	}
	return nil
}

// UpdateIndexState updates the index metadata after an incremental update
func (u *IndexUpdater) UpdateIndexState(filesUpdated int, commit string) error {
	if err := u.store.SetIndexStatePartial(filesUpdated); err != nil {
		return fmt.Errorf("failed to set index state: %w", err)
	}

	if commit != "" {
		if err := u.store.SetLastIndexedCommit(commit); err != nil {
			return fmt.Errorf("failed to set indexed commit: %w", err)
		}
	}

	return nil
}

// SetFullIndexComplete marks a full reindex as complete
// Call this after a full (non-incremental) index
func (u *IndexUpdater) SetFullIndexComplete(commit string) error {
	if err := u.store.SetIndexStateFull(); err != nil {
		return fmt.Errorf("failed to set full index state: %w", err)
	}

	if commit != "" {
		if err := u.store.SetLastIndexedCommit(commit); err != nil {
			return fmt.Errorf("failed to set indexed commit: %w", err)
		}
	}

	return nil
}

// PopulateFromFullIndex populates the file tracking tables from a full SCIP index.
// This should be called after a full reindex to enable incremental updates.
// v1.1: Also populates callgraph table for call edges.
// v2.0: Also populates file_deps and clears rescan_queue.
func (u *IndexUpdater) PopulateFromFullIndex(extractor *SCIPExtractor) error {
	index, err := extractor.LoadIndex()
	if err != nil {
		return fmt.Errorf("failed to load SCIP index: %w", err)
	}

	u.logger.Info("Populating incremental tracking from full index", "documentCount", len(index.Documents))

	// Phase 1: Collect indices of relevant documents
	type docEntry struct {
		idx    int
		change ChangedFile
	}
	var entries []docEntry
	for i, doc := range index.Documents {
		entries = append(entries, docEntry{
			idx:    i,
			change: ChangedFile{Path: doc.RelativePath, ChangeType: ChangeAdded},
		})
	}

	// Phase 2: Extract file deltas in parallel — CPU-bound, one goroutine per GOMAXPROCS
	deltas := make([]FileDelta, len(entries))
	nWorkers := runtime.GOMAXPROCS(0)
	sem := make(chan struct{}, nWorkers)
	var wg sync.WaitGroup
	for j, entry := range entries {
		j, entry := j, entry
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			deltas[j] = extractor.extractFileDelta(index.Documents[entry.idx], entry.change)
		}()
	}
	wg.Wait()

	// Build symbol-to-file map from all extracted symbols
	symbolToFile := make(map[string]string, len(deltas)*10)
	for _, delta := range deltas {
		for _, sym := range delta.Symbols {
			symbolToFile[sym.ID] = delta.Path
		}
	}

	now := time.Now()

	// Bulk-load PRAGMA tuning — safe because a failed full index is always re-run.
	// synchronous=OFF: skip fsync on WAL writes (startup default: NORMAL).
	// cache_size=-131072: 128 MB page cache vs startup's 64 MB — keeps more B-tree
	//   nodes warm during the unique-key checks in callgraph/file_symbols.
	// wal_autocheckpoint=0: disable automatic WAL checkpoints so the batch-loop
	//   transactions aren't interrupted by checkpoint I/O mid-populate. One explicit
	//   TRUNCATE checkpoint runs after the loop.
	// (mmap_size and temp_store are already set at connection open.)
	for _, pragma := range []string{
		"PRAGMA synchronous=OFF",
		"PRAGMA cache_size=-131072",
		"PRAGMA wal_autocheckpoint=0",
	} {
		if _, err := u.db.Exec(pragma); err != nil {
			u.logger.Warn("bulk PRAGMA failed", "pragma", pragma, "error", err.Error())
		}
	}
	defer func() {
		// Restore normal settings and do a single final WAL checkpoint.
		for _, pragma := range []string{
			"PRAGMA synchronous=NORMAL",
			"PRAGMA cache_size=-64000",
			"PRAGMA wal_autocheckpoint=1000",
		} {
			if _, err := u.db.Exec(pragma); err != nil {
				u.logger.Warn("bulk PRAGMA restore failed", "pragma", pragma, "error", err.Error())
			}
		}
		if _, err := u.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			u.logger.Warn("WAL checkpoint after bulk load failed", "error", err.Error())
		}
	}()

	// Clear existing data in one transaction before the bulk insert
	if err := u.db.WithTx(func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM file_symbols`,
			`DELETE FROM indexed_files`,
			`DELETE FROM callgraph`,
			`DELETE FROM file_deps`,
			`DELETE FROM rescan_queue`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return fmt.Errorf("clear tables: %w", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// Insert in batches of 1000 files per transaction.
	// Keeps the WAL file bounded and allows incremental checkpointing.
	const batchSize = 1000
	totalCallEdges := 0
	totalDeps := 0

	for i := 0; i < len(deltas); i += batchSize {
		batch := deltas[i:min(i+batchSize, len(deltas))]
		var batchCallEdges, batchDeps int

		if err := u.db.WithTx(func(tx *sql.Tx) error {
			// Prepare statements once per batch — reused across all files in the tx
			fileStmt, err := tx.Prepare(`
				INSERT INTO indexed_files (path, hash, mtime, indexed_at, scip_document_hash, symbol_count)
				VALUES (?, ?, ?, ?, ?, ?)
			`)
			if err != nil {
				return fmt.Errorf("prepare indexed_files insert: %w", err)
			}
			defer fileStmt.Close() //nolint:errcheck

			callStmt, err := tx.Prepare(`
				INSERT OR REPLACE INTO callgraph
				(caller_id, callee_id, caller_file, call_line, call_col, call_end_col)
				VALUES (?, ?, ?, ?, ?, ?)
			`)
			if err != nil {
				return fmt.Errorf("prepare callgraph insert: %w", err)
			}
			defer callStmt.Close() //nolint:errcheck

			depsStmt, err := tx.Prepare(`INSERT OR IGNORE INTO file_deps (dependent_file, defining_file) VALUES (?, ?)`)
			if err != nil {
				return fmt.Errorf("prepare file_deps insert: %w", err)
			}
			defer depsStmt.Close() //nolint:errcheck

			for _, delta := range batch {
				// 1. File tracking
				if _, err := fileStmt.Exec(delta.Path, delta.Hash, now.Unix(), now.Unix(),
					delta.SCIPDocumentHash, delta.SymbolCount); err != nil {
					return fmt.Errorf("insert indexed_file for %s: %w", delta.Path, err)
				}

				// 2. Symbol mappings — batched multi-row INSERT
				if len(delta.Symbols) > 0 {
					if err := bulkInsertFileSymbols(tx, delta.Path, delta.Symbols); err != nil {
						return fmt.Errorf("bulk insert file_symbols for %s: %w", delta.Path, err)
					}
				}

				// 3. Call edges — prepared stmt, one exec per edge
				if len(delta.CallEdges) > 0 {
					if err := u.insertCallEdgesWithStmt(callStmt, delta); err != nil {
						return fmt.Errorf("insert callgraph for %s: %w", delta.Path, err)
					}
					batchCallEdges += len(delta.CallEdges)
				}

				// 4. File dependencies — table already cleared, skipDelete=true
				if len(delta.Refs) > 0 {
					if err := u.depTracker.updateFileDepsWithStmt(tx, depsStmt, delta.Path, delta.Refs, symbolToFile, true); err != nil {
						u.logger.Warn("Failed to update file_deps", "path", delta.Path, "error", err.Error())
					} else {
						batchDeps += len(delta.Refs)
					}
				}
			}
			return nil
		}); err != nil {
			return err
		}

		totalCallEdges += batchCallEdges
		totalDeps += batchDeps
	}

	u.logger.Info("Full index populated",
		"files", len(deltas),
		"callEdges", totalCallEdges,
		"fileDeps", totalDeps,
	)

	return nil
}

// PopulateFromFullIndexStreaming is a memory-efficient replacement for
// PopulateFromFullIndex. Instead of loading the entire SCIPIndex into memory
// (6.45 GB for a 50k-doc repo), it makes two lightweight streaming passes over
// the on-disk SCIP file:
//
//   - Pass 1: build the symbol→file map (~160 MB for 2M symbols)
//   - Pass 2: extract deltas + write SQL in 1000-file batches, resolving
//     file_deps on-the-fly using the map from pass 1
//
// Peak heap is dominated by the symbol→file map rather than the full index.
func (u *IndexUpdater) PopulateFromFullIndexStreaming(extractor *SCIPExtractor) error {
	u.logger.Info("Streaming populate: pass 1 — building symbol→file map")

	// Pass 1: lightweight scan to build symbolToFile.
	// Uses StreamDocuments (raw proto) to avoid convertDocument allocations.
	// Only looks at definition occurrences; ignores everything else.
	const defRole = int32(1)                       // scippb.SymbolRole_Definition == 1
	symbolToFile := make(map[string]string, 1<<17) // pre-alloc 128k
	if err := scip.StreamDocuments(extractor.indexPath, func(pbDoc *scippb.Document) error {
		for _, occ := range pbDoc.Occurrences {
			if occ.SymbolRoles&defRole != 0 && occ.Symbol != "" {
				symbolToFile[occ.Symbol] = pbDoc.RelativePath
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("streaming pass 1: %w", err)
	}
	u.logger.Info("Streaming populate: symbol map ready", "symbols", len(symbolToFile))

	// Bulk-load PRAGMA tuning — safe because a failed full index is always re-run.
	// synchronous=OFF: skip fsync on WAL writes (startup default: NORMAL).
	// cache_size=-131072: 128 MB page cache vs startup's 64 MB — keeps more B-tree
	//   nodes warm during the unique-key checks in callgraph/file_symbols.
	// wal_autocheckpoint=0: disable automatic WAL checkpoints so the batch-loop
	//   transactions aren't interrupted by checkpoint I/O mid-populate. One explicit
	//   TRUNCATE checkpoint runs after the loop.
	// (mmap_size and temp_store are already set at connection open.)
	for _, pragma := range []string{
		"PRAGMA synchronous=OFF",
		"PRAGMA cache_size=-131072",
		"PRAGMA wal_autocheckpoint=0",
	} {
		if _, err := u.db.Exec(pragma); err != nil {
			u.logger.Warn("bulk PRAGMA failed", "pragma", pragma, "error", err.Error())
		}
	}
	defer func() {
		// Restore normal settings and do a single final WAL checkpoint.
		for _, pragma := range []string{
			"PRAGMA synchronous=NORMAL",
			"PRAGMA cache_size=-64000",
			"PRAGMA wal_autocheckpoint=1000",
		} {
			if _, err := u.db.Exec(pragma); err != nil {
				u.logger.Warn("bulk PRAGMA restore failed", "pragma", pragma, "error", err.Error())
			}
		}
		if _, err := u.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			u.logger.Warn("WAL checkpoint after bulk load failed", "error", err.Error())
		}
	}()

	// Clear existing tables.
	if err := u.db.WithTx(func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM file_symbols`,
			`DELETE FROM indexed_files`,
			`DELETE FROM callgraph`,
			`DELETE FROM file_deps`,
			`DELETE FROM rescan_queue`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return fmt.Errorf("clear tables: %w", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// Pass 2: stream documents, extract deltas, write in 1000-file batches.
	const batchSize = 1000
	now := time.Now()
	var batch []FileDelta
	totalFiles, totalCallEdges, totalDeps := 0, 0, 0

	flushBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		var batchCallEdges, batchDeps int
		if err := u.db.WithTx(func(tx *sql.Tx) error {
			fileStmt, err := tx.Prepare(`
				INSERT INTO indexed_files (path, hash, mtime, indexed_at, scip_document_hash, symbol_count)
				VALUES (?, ?, ?, ?, ?, ?)
			`)
			if err != nil {
				return fmt.Errorf("prepare indexed_files: %w", err)
			}
			defer fileStmt.Close() //nolint:errcheck

			callStmt, err := tx.Prepare(`
				INSERT OR REPLACE INTO callgraph
				(caller_id, callee_id, caller_file, call_line, call_col, call_end_col)
				VALUES (?, ?, ?, ?, ?, ?)
			`)
			if err != nil {
				return fmt.Errorf("prepare callgraph: %w", err)
			}
			defer callStmt.Close() //nolint:errcheck

			depsStmt, err := tx.Prepare(`INSERT OR IGNORE INTO file_deps (dependent_file, defining_file) VALUES (?, ?)`)
			if err != nil {
				return fmt.Errorf("prepare file_deps: %w", err)
			}
			defer depsStmt.Close() //nolint:errcheck

			for _, delta := range batch {
				if _, err := fileStmt.Exec(delta.Path, delta.Hash, now.Unix(), now.Unix(),
					delta.SCIPDocumentHash, delta.SymbolCount); err != nil {
					return fmt.Errorf("insert indexed_file %s: %w", delta.Path, err)
				}
				if len(delta.Symbols) > 0 {
					if err := bulkInsertFileSymbols(tx, delta.Path, delta.Symbols); err != nil {
						return fmt.Errorf("bulk insert file_symbols %s: %w", delta.Path, err)
					}
				}
				if len(delta.CallEdges) > 0 {
					if err := u.insertCallEdgesWithStmt(callStmt, delta); err != nil {
						return fmt.Errorf("insert callgraph %s: %w", delta.Path, err)
					}
					batchCallEdges += len(delta.CallEdges)
				}
				if len(delta.Refs) > 0 {
					if err := u.depTracker.updateFileDepsWithStmt(tx, depsStmt, delta.Path, delta.Refs, symbolToFile, true); err != nil {
						u.logger.Warn("file_deps update failed", "path", delta.Path, "error", err.Error())
					} else {
						batchDeps += len(delta.Refs)
					}
				}
			}
			return nil
		}); err != nil {
			return err
		}
		totalCallEdges += batchCallEdges
		totalDeps += batchDeps
		totalFiles += len(batch)
		batch = batch[:0]
		return nil
	}

	u.logger.Info("Streaming populate: pass 2 — extracting deltas + writing SQL")
	if err := scip.StreamDocuments(extractor.indexPath, func(pbDoc *scippb.Document) error {
		change := ChangedFile{Path: pbDoc.RelativePath, ChangeType: ChangeAdded}
		delta := extractor.extractFileDeltaFromProto(pbDoc, change)
		batch = append(batch, delta)
		if len(batch) >= batchSize {
			return flushBatch()
		}
		return nil
	}); err != nil {
		return fmt.Errorf("streaming pass 2: %w", err)
	}
	if err := flushBatch(); err != nil {
		return err
	}

	u.logger.Info("Streaming populate complete",
		"files", totalFiles,
		"callEdges", totalCallEdges,
		"fileDeps", totalDeps,
	)
	return nil
}

// GetDependencyTracker returns the dependency tracker for external access
func (u *IndexUpdater) GetDependencyTracker() *DependencyTracker {
	return u.depTracker
}

// GetUpdateStats returns statistics about the current update
func (u *IndexUpdater) GetUpdateStats(delta *SymbolDelta) DeltaStats {
	return delta.Stats
}
