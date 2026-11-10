package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// SCIPProcessor processes uploaded SCIP index files into CKB databases
type SCIPProcessor struct {
	storage *IndexStorage
	logger  *slog.Logger
}

// UploadMeta contains metadata provided with an upload
type UploadMeta struct {
	Commit      string   // Git commit hash
	Languages   []string // Languages (auto-detected if empty)
	IndexerName string   // e.g., "scip-go"
	IndexerVer  string   // e.g., "v0.3.0"
}

// ProcessResult contains processing statistics
type ProcessResult struct {
	RepoID      string        `json:"repo_id"`
	Commit      string        `json:"commit,omitempty"`
	Languages   []string      `json:"languages"`
	FileCount   int           `json:"file_count"`
	SymbolCount int           `json:"symbol_count"`
	RefCount    int           `json:"ref_count"`
	CallEdges   int           `json:"call_edges"`
	TotalFiles  int           `json:"total_files,omitempty"` // For delta uploads: total files in repo
	Duration    time.Duration `json:"-"`
	DurationMs  int64         `json:"duration_ms"`
}

// NewSCIPProcessor creates a new processor
func NewSCIPProcessor(storage *IndexStorage, logger *slog.Logger) *SCIPProcessor {
	return &SCIPProcessor{
		storage: storage,
		logger:  logger,
	}
}

// ProcessUpload processes an uploaded SCIP file into a repo's database
func (p *SCIPProcessor) ProcessUpload(repoID string, scipPath string, meta UploadMeta) (*ProcessResult, error) {
	start := time.Now()

	// Load SCIP index
	index, err := scip.LoadSCIPIndex(scipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load SCIP index: %w", err)
	}

	p.logger.Info("Loaded SCIP index",
		"repo_id", repoID,
		"documents", len(index.Documents),
	)

	// Open/create database
	dbPath := p.storage.DBPath(repoID)
	db, err := p.openDatabase(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Initialize schema
	if err := p.initSchema(db); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	// Process in transaction
	result := &ProcessResult{
		RepoID:    repoID,
		Commit:    meta.Commit,
		Languages: []string{},
	}

	if err := p.processIndex(db, index, result); err != nil {
		return nil, fmt.Errorf("failed to process index: %w", err)
	}

	// Extract commit from SCIP metadata if not provided
	if result.Commit == "" && index.IndexedCommit != "" {
		result.Commit = index.IndexedCommit
	}

	// Update index_meta
	if err := p.updateMeta(db, result); err != nil {
		p.logger.Warn("Failed to update index_meta",
			"error", err.Error(),
		)
	}

	// Update storage metadata
	if err := p.storage.UpdateLastUpload(repoID); err != nil {
		p.logger.Warn("Failed to update storage metadata",
			"error", err.Error(),
		)
	}

	result.Duration = time.Since(start)
	result.DurationMs = result.Duration.Milliseconds()

	p.logger.Info("Processed SCIP index",
		"repo_id", repoID,
		"files", result.FileCount,
		"symbols", result.SymbolCount,
		"call_edges", result.CallEdges,
		"duration_ms", result.DurationMs,
	)

	return result, nil
}

// ProcessDeltaUpload processes a partial SCIP upload containing only changed files
func (p *SCIPProcessor) ProcessDeltaUpload(repoID string, scipPath string, meta *DeltaUploadRequest) (*ProcessResult, error) {
	start := time.Now()

	// Load partial SCIP index
	index, err := scip.LoadSCIPIndex(scipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load SCIP index: %w", err)
	}

	p.logger.Info("Loaded delta SCIP index",
		"repo_id", repoID,
		"documents", len(index.Documents),
		"changed_files", len(meta.ChangedFiles),
	)

	// Open existing database
	dbPath := p.storage.DBPath(repoID)
	db, err := p.openDatabase(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Get total file count before changes
	var totalFiles int
	if err := db.QueryRow("SELECT COUNT(*) FROM indexed_files").Scan(&totalFiles); err != nil {
		totalFiles = 0
	}

	result := &ProcessResult{
		RepoID:     repoID,
		Commit:     meta.TargetCommit,
		Languages:  []string{},
		TotalFiles: totalFiles,
	}

	// Process delta in transaction
	if err := p.processDeltaIndex(db, index, meta, result); err != nil {
		return nil, fmt.Errorf("failed to process delta: %w", err)
	}

	// Update index_meta with new commit
	if err := p.updateMeta(db, result); err != nil {
		p.logger.Warn("Failed to update index_meta",
			"error", err.Error(),
		)
	}

	// Update storage metadata
	if err := p.storage.UpdateLastUpload(repoID); err != nil {
		p.logger.Warn("Failed to update storage metadata",
			"error", err.Error(),
		)
	}

	result.Duration = time.Since(start)
	result.DurationMs = result.Duration.Milliseconds()

	p.logger.Info("Processed delta SCIP index",
		"repo_id", repoID,
		"files", result.FileCount,
		"symbols", result.SymbolCount,
		"call_edges", result.CallEdges,
		"duration_ms", result.DurationMs,
	)

	return result, nil
}

// processDeltaIndex applies changes from a partial SCIP index
func (p *SCIPProcessor) processDeltaIndex(db *sql.DB, index *scip.SCIPIndex, meta *DeltaUploadRequest, result *ProcessResult) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Build change map for quick lookup
	changeMap := make(map[string]*DeltaChangedFile)
	for i := range meta.ChangedFiles {
		cf := &meta.ChangedFiles[i]
		changeMap[cf.Path] = cf
		if cf.OldPath != "" {
			changeMap[cf.OldPath] = cf
		}
	}

	// Prepare statements
	deleteSymbolsStmt, err := tx.Prepare("DELETE FROM symbol_mappings WHERE json_extract(location_json, '$.file_path') = ?")
	if err != nil {
		return err
	}
	defer func() { _ = deleteSymbolsStmt.Close() }()

	deleteCallsStmt, err := tx.Prepare("DELETE FROM callgraph WHERE caller_file = ?")
	if err != nil {
		return err
	}
	defer func() { _ = deleteCallsStmt.Close() }()

	deleteFileStmt, err := tx.Prepare("DELETE FROM indexed_files WHERE path = ?")
	if err != nil {
		return err
	}
	defer func() { _ = deleteFileStmt.Close() }()

	symbolStmt, err := tx.Prepare(`
		INSERT INTO symbol_mappings (
			stable_id, state, backend_stable_id, fingerprint_json, location_json,
			last_verified_at, last_verified_state_id
		) VALUES (?, 'active', ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer func() { _ = symbolStmt.Close() }()

	fileStmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO indexed_files (path, hash, indexed_at, symbol_count)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer func() { _ = fileStmt.Close() }()

	callStmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO callgraph (caller_id, callee_id, caller_file, call_line, call_col, call_end_col)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer func() { _ = callStmt.Close() }()

	now := time.Now().Format(time.RFC3339)
	langSet := make(map[string]bool)

	// First, delete data for all changed files
	for _, cf := range meta.ChangedFiles {
		pathToDelete := cf.Path
		if cf.OldPath != "" {
			pathToDelete = cf.OldPath // For renames, delete old path
		}

		_, _ = deleteSymbolsStmt.Exec(pathToDelete)
		_, _ = deleteCallsStmt.Exec(pathToDelete)
		_, _ = deleteFileStmt.Exec(pathToDelete)

		// Also delete new path for renames/additions to avoid duplicates
		if cf.OldPath != "" || cf.ChangeType == "modified" {
			_, _ = deleteSymbolsStmt.Exec(cf.Path)
			_, _ = deleteCallsStmt.Exec(cf.Path)
			_, _ = deleteFileStmt.Exec(cf.Path)
		}
	}

	// Process documents from SCIP index (only contains changed files)
	for _, doc := range index.Documents {
		cf, isTracked := changeMap[doc.RelativePath]
		if !isTracked {
			// Skip files not in our change list
			continue
		}

		// Skip if deleted (shouldn't have SCIP doc, but be safe)
		if cf.ChangeType == "deleted" {
			continue
		}

		if doc.Language != "" {
			langSet[doc.Language] = true
		}

		fileSymbolCount := 0

		// Build symbol info map for this document
		calls := newDocCalls(doc)
		symbolInfo := make(map[string]*scip.SymbolInformation)
		for _, sym := range doc.Symbols {
			symbolInfo[sym.Symbol] = sym
		}

		// Process occurrences
		for _, occ := range doc.Occurrences {
			if isLocalSymbol(occ.Symbol) {
				continue
			}

			if occ.SymbolRoles&scip.SymbolRoleDefinition != 0 {
				fingerprint := buildFingerprint(doc, occ, symbolInfo)
				location := buildLocation(doc.RelativePath, occ)

				_, err := symbolStmt.Exec(
					occ.Symbol,
					occ.Symbol,
					fingerprint,
					location,
					now,
					"delta-upload",
				)
				if err == nil {
					result.SymbolCount++
					fileSymbolCount++
				}
			} else {
				result.RefCount++

				if calls.isCallee(occ.Symbol, symbolInfo) {
					line, col, endCol := parseRange(occ.Range)
					callerID := calls.callerAt(line)

					_, err := callStmt.Exec(
						nullString(callerID),
						occ.Symbol,
						doc.RelativePath,
						line,
						col,
						nullInt(endCol),
					)
					if err == nil {
						result.CallEdges++
					}
				}
			}
		}

		// Insert/update file record
		fileHash := computeFileHash(doc)
		_, _ = fileStmt.Exec(doc.RelativePath, fileHash, time.Now().Unix(), fileSymbolCount)
		result.FileCount++
	}

	for lang := range langSet {
		result.Languages = append(result.Languages, lang)
	}

	return tx.Commit()
}

// openDatabase opens or creates a SQLite database
func (p *SCIPProcessor) openDatabase(dbPath string) (*sql.DB, error) {
	// Ensure directory exists
	if err := ensureDir(filepath.Dir(dbPath)); err != nil {
		return nil, err
	}

	connStr := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000", dbPath)
	db, err := sql.Open("sqlite", connStr)
	if err != nil {
		return nil, err
	}

	// Set pragmas for performance
	pragmas := []string{
		"PRAGMA busy_timeout=5000",
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=-64000", // 64MB
		"PRAGMA temp_store=MEMORY",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to set pragma: %w", err)
		}
	}

	return db, nil
}

// initSchema creates required tables if they don't exist
func (p *SCIPProcessor) initSchema(db *sql.DB) error {
	// Schema version table
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER NOT NULL
		)
	`); err != nil {
		return err
	}

	// Symbol mappings table (simplified for index server)
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS symbol_mappings (
			stable_id TEXT PRIMARY KEY,
			state TEXT NOT NULL DEFAULT 'active',
			backend_stable_id TEXT,
			fingerprint_json TEXT NOT NULL,
			location_json TEXT NOT NULL,
			definition_version_id TEXT,
			definition_version_semantics TEXT,
			last_verified_at TEXT NOT NULL,
			last_verified_state_id TEXT NOT NULL,
			deleted_at TEXT,
			deleted_in_state_id TEXT
		)
	`); err != nil {
		return err
	}

	// Indexed files table
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS indexed_files (
			path TEXT PRIMARY KEY,
			hash TEXT NOT NULL,
			mtime INTEGER,
			indexed_at INTEGER NOT NULL,
			scip_document_hash TEXT,
			symbol_count INTEGER DEFAULT 0
		)
	`); err != nil {
		return err
	}

	// Callgraph table
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS callgraph (
			caller_id TEXT,
			callee_id TEXT NOT NULL,
			caller_file TEXT NOT NULL,
			call_line INTEGER NOT NULL,
			call_col INTEGER NOT NULL,
			call_end_col INTEGER,
			PRIMARY KEY (caller_file, call_line, call_col, callee_id)
		)
	`); err != nil {
		return err
	}

	// Index meta table
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS index_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`); err != nil {
		return err
	}

	// Create indexes
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_symbol_mappings_state ON symbol_mappings(state)",
		"CREATE INDEX IF NOT EXISTS idx_callgraph_caller_file ON callgraph(caller_file)",
		"CREATE INDEX IF NOT EXISTS idx_callgraph_caller_id ON callgraph(caller_id)",
		"CREATE INDEX IF NOT EXISTS idx_callgraph_callee_id ON callgraph(callee_id)",
	}

	for _, idx := range indexes {
		if _, err := db.Exec(idx); err != nil {
			return err
		}
	}

	return nil
}

// processIndex processes all documents from the SCIP index
func (p *SCIPProcessor) processIndex(db *sql.DB, index *scip.SCIPIndex, result *ProcessResult) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Clear existing data (full replace)
	tables := []string{"symbol_mappings", "indexed_files", "callgraph"}
	for _, table := range tables {
		if _, execErr := tx.Exec(fmt.Sprintf("DELETE FROM %s", table)); execErr != nil {
			return fmt.Errorf("failed to clear %s: %w", table, execErr)
		}
	}

	// Prepare statements
	symbolStmt, err := tx.Prepare(`
		INSERT INTO symbol_mappings (
			stable_id, state, backend_stable_id, fingerprint_json, location_json,
			last_verified_at, last_verified_state_id
		) VALUES (?, 'active', ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer func() { _ = symbolStmt.Close() }()

	fileStmt, err := tx.Prepare(`
		INSERT INTO indexed_files (path, hash, indexed_at, symbol_count)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer func() { _ = fileStmt.Close() }()

	callStmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO callgraph (caller_id, callee_id, caller_file, call_line, call_col, call_end_col)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer func() { _ = callStmt.Close() }()

	// Track languages
	langSet := make(map[string]bool)
	now := time.Now().Format(time.RFC3339)

	// Process each document
	for _, doc := range index.Documents {
		if doc.Language != "" {
			langSet[doc.Language] = true
		}

		fileSymbolCount := 0

		// Build symbol info map for this document
		calls := newDocCalls(doc)
		symbolInfo := make(map[string]*scip.SymbolInformation)
		for _, sym := range doc.Symbols {
			symbolInfo[sym.Symbol] = sym
		}

		// Process occurrences
		for _, occ := range doc.Occurrences {
			// Skip local symbols
			if isLocalSymbol(occ.Symbol) {
				continue
			}

			// Definition = insert symbol
			if occ.SymbolRoles&scip.SymbolRoleDefinition != 0 {
				fingerprint := buildFingerprint(doc, occ, symbolInfo)
				location := buildLocation(doc.RelativePath, occ)

				_, err := symbolStmt.Exec(
					occ.Symbol,
					occ.Symbol,
					fingerprint,
					location,
					now,
					"upload",
				)
				if err != nil {
					p.logger.Debug("Symbol insert error",
						"symbol", occ.Symbol,
						"error", err.Error(),
					)
					continue
				}

				result.SymbolCount++
				fileSymbolCount++
			} else {
				// Reference = potential call edge
				result.RefCount++

				// Check if it's a callable (function/method)
				if calls.isCallee(occ.Symbol, symbolInfo) {
					line, col, endCol := parseRange(occ.Range)

					// Try to resolve caller
					callerID := calls.callerAt(line)

					_, err := callStmt.Exec(
						nullString(callerID),
						occ.Symbol,
						doc.RelativePath,
						line,
						col,
						nullInt(endCol),
					)
					if err == nil {
						result.CallEdges++
					}
				}
			}
		}

		// Insert file record
		fileHash := computeFileHash(doc)
		_, _ = fileStmt.Exec(doc.RelativePath, fileHash, time.Now().Unix(), fileSymbolCount)
		result.FileCount++
	}

	// Collect languages
	for lang := range langSet {
		result.Languages = append(result.Languages, lang)
	}

	return tx.Commit()
}

// updateMeta updates the index_meta table
func (p *SCIPProcessor) updateMeta(db *sql.DB, result *ProcessResult) error {
	meta := map[string]string{
		"commit":       result.Commit,
		"indexed_at":   time.Now().Format(time.RFC3339),
		"sync_seq":     "1",
		"file_count":   fmt.Sprintf("%d", result.FileCount),
		"symbol_count": fmt.Sprintf("%d", result.SymbolCount),
	}

	for key, value := range meta {
		if value == "" {
			continue
		}
		_, err := db.Exec(`
			INSERT OR REPLACE INTO index_meta (key, value) VALUES (?, ?)
		`, key, value)
		if err != nil {
			return err
		}
	}

	return nil
}

// Helper functions

func isLocalSymbol(symbolID string) bool {
	return len(symbolID) > 6 && symbolID[:6] == "local "
}

// docCalls answers, for one document, which references are calls and which
// function each call sits in. Both come from scip.FunctionBounds, the rule the
// SCIP backend's own call graph uses: SCIP Kind values (not LSP's), the
// descriptor, and function-valued constants, with each function's extent taken
// from its EnclosingRange where the indexer gave one.
type docCalls struct {
	bounds   []scip.DefinitionBound
	callable map[string]bool
}

func newDocCalls(doc *scip.Document) docCalls {
	bounds := scip.FunctionBounds(doc)
	callable := make(map[string]bool, len(bounds))
	for _, b := range bounds {
		callable[b.Symbol] = true
	}
	return docCalls{bounds: bounds, callable: callable}
}

// isCallee reports whether a reference targets a callable. A target defined in
// this document is known from its definition; any other from its ID and Kind.
func (d docCalls) isCallee(symbolID string, info map[string]*scip.SymbolInformation) bool {
	if d.callable[symbolID] {
		return true
	}
	var kind int32
	if sym, ok := info[symbolID]; ok {
		kind = sym.Kind
	}
	return scip.IsCallableReference(symbolID, kind)
}

// callerAt returns the innermost function holding a 1-based line, or "" for a
// call outside every function (a top-level initializer).
func (d docCalls) callerAt(line int) string {
	l := line - 1
	best, bestSpan := "", -1
	for _, b := range d.bounds {
		if l < b.Start || l > b.End {
			continue
		}
		if span := b.End - b.Start; bestSpan < 0 || span < bestSpan {
			best, bestSpan = b.Symbol, span
		}
	}
	return best
}

func parseRange(r []int32) (line, col, endCol int) {
	if len(r) >= 1 {
		line = int(r[0]) + 1 // #nosec G115 -- SCIP int32 fits in int
	}
	if len(r) >= 2 {
		col = int(r[1]) + 1 // #nosec G115 -- SCIP int32 fits in int
	}
	if len(r) >= 4 {
		endCol = int(r[3]) + 1 // #nosec G115 -- SCIP int32 fits in int
	}
	return
}

func buildFingerprint(doc *scip.Document, occ *scip.Occurrence, info map[string]*scip.SymbolInformation) string {
	fp := map[string]interface{}{
		"symbol":   occ.Symbol,
		"language": doc.Language,
	}

	if sym, ok := info[occ.Symbol]; ok {
		fp["kind"] = sym.Kind
		if sym.DisplayName != "" {
			fp["name"] = sym.DisplayName
		}
	}

	data, _ := json.Marshal(fp)
	return string(data)
}

func buildLocation(path string, occ *scip.Occurrence) string {
	loc := map[string]interface{}{
		"file_path": path,
	}

	if len(occ.Range) >= 1 {
		loc["line"] = int(occ.Range[0]) + 1 // #nosec G115 -- SCIP int32 fits in int
	}
	if len(occ.Range) >= 2 {
		loc["col"] = int(occ.Range[1]) + 1 // #nosec G115 -- SCIP int32 fits in int
	}

	data, _ := json.Marshal(loc)
	return string(data)
}

func computeFileHash(doc *scip.Document) string {
	h := sha256.New()
	h.Write([]byte(doc.RelativePath))
	for _, occ := range doc.Occurrences {
		h.Write([]byte(occ.Symbol))
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(i int) interface{} {
	if i == 0 {
		return nil
	}
	return i
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0755)
}
