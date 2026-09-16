package storage

import (
	"database/sql"
	"fmt"
)

// Schema version tracking
// v1: Initial schema (symbol_mappings, symbol_aliases, modules, dependency_edges, caches)
// v2: Architectural Memory (ownership, hotspots, responsibilities, decisions)
// v3: Telemetry (observed_usage, observed_callers, coverage_snapshots)
// v4: Developer Intelligence (coupling_cache, risk_scores)
// v5: Doc-Symbol Linking (docs, doc_references, doc_modules, symbol_suffixes)
// v6: Incremental Indexing (indexed_files, file_symbols, index_meta)
// v7: Incremental Callgraph (callgraph table for call edge storage)
// v8: Transitive Invalidation (file_deps, rescan_queue for dependency tracking)
// v9: FTS5 Symbol Search (symbols_fts_content, symbols_fts)
// v10: Wide-Result Metrics (wide_result_metrics for MCP tool telemetry)
// v11: Response Bytes (adds response_bytes column to wide_result_metrics)
// v12: Activity Ledger (tool_calls for per-invocation MCP tool audit trail)
const currentSchemaVersion = 12

// initializeSchema creates all tables for a new database
func (db *DB) initializeSchema() error {
	return db.WithTx(func(tx *sql.Tx) error {
		// Create schema_version table first
		if err := createSchemaVersionTable(tx); err != nil {
			return err
		}

		// Create v1 application tables
		if err := createSymbolMappingsTable(tx); err != nil {
			return err
		}
		if err := createSymbolAliasesTable(tx); err != nil {
			return err
		}
		if err := createModulesTableV2(tx); err != nil {
			return err
		}
		if err := createDependencyEdgesTable(tx); err != nil {
			return err
		}
		if err := createCacheTablesTable(tx); err != nil {
			return err
		}

		// Create v2 Architectural Memory tables
		if err := createOwnershipTable(tx); err != nil {
			return err
		}
		if err := createOwnershipHistoryTable(tx); err != nil {
			return err
		}
		if err := createHotspotSnapshotsTable(tx); err != nil {
			return err
		}
		if err := createResponsibilitiesTable(tx); err != nil {
			return err
		}
		if err := createDecisionsTable(tx); err != nil {
			return err
		}
		if err := createModuleRenamesTable(tx); err != nil {
			return err
		}

		// Create v3 Telemetry tables
		if err := createTelemetryTables(tx); err != nil {
			return err
		}

		// Create v4 Developer Intelligence tables
		if err := createDeveloperIntelligenceTables(tx); err != nil {
			return err
		}

		// Create v5 Doc-Symbol Linking tables
		if err := createDocsTables(tx); err != nil {
			return err
		}

		// Create v6 Incremental Indexing tables
		if err := createIncrementalIndexingTables(tx); err != nil {
			return err
		}

		// Create v7 Callgraph table
		if err := createCallgraphTable(tx); err != nil {
			return err
		}

		// Create v8 Transitive Invalidation tables
		if err := createTransitiveInvalidationTables(tx); err != nil {
			return err
		}

		// Create v9 FTS5 Symbol Search tables
		if err := createSymbolFTSTables(tx); err != nil {
			return err
		}

		// Create v10 Wide-Result Metrics table
		if err := createWideResultMetricsTable(tx); err != nil {
			return err
		}

		// Create v12 Activity Ledger table (v11 only adds a column, no table)
		if err := createToolCallsTable(tx); err != nil {
			return err
		}

		// Set initial schema version
		if err := setSchemaVersion(tx, currentSchemaVersion); err != nil {
			return err
		}

		db.logger.Info("Database schema initialized",
			"version", currentSchemaVersion,
		)

		return nil
	})
}

// runMigrations runs any pending schema migrations
func (db *DB) runMigrations() error {
	// Get current schema version
	version, err := db.getSchemaVersion()
	if err != nil {
		return err
	}

	if version == currentSchemaVersion {
		db.logger.Debug("Database schema is up to date",
			"version", version,
		)
		return nil
	}

	db.logger.Info("Running database migrations",
		"from_version", version,
		"to_version", currentSchemaVersion,
	)

	// Run migrations sequentially
	if version < 2 {
		if err := db.migrateToV2(); err != nil {
			return fmt.Errorf("failed to migrate to v2: %w", err)
		}
	}

	if version < 3 {
		if err := db.migrateToV3(); err != nil {
			return fmt.Errorf("failed to migrate to v3: %w", err)
		}
	}

	if version < 4 {
		if err := db.migrateToV4(); err != nil {
			return fmt.Errorf("failed to migrate to v4: %w", err)
		}
	}

	if version < 5 {
		if err := db.migrateToV5(); err != nil {
			return fmt.Errorf("failed to migrate to v5: %w", err)
		}
	}

	if version < 6 {
		if err := db.migrateToV6(); err != nil {
			return fmt.Errorf("failed to migrate to v6: %w", err)
		}
	}

	if version < 7 {
		if err := db.migrateToV7(); err != nil {
			return fmt.Errorf("failed to migrate to v7: %w", err)
		}
	}

	if version < 8 {
		if err := db.migrateToV8(); err != nil {
			return fmt.Errorf("failed to migrate to v8: %w", err)
		}
	}

	if version < 9 {
		if err := db.migrateToV9(); err != nil {
			return fmt.Errorf("failed to migrate to v9: %w", err)
		}
	}

	if version < 10 {
		if err := db.migrateToV10(); err != nil {
			return fmt.Errorf("failed to migrate to v10: %w", err)
		}
	}

	if version < 11 {
		if err := db.migrateToV11(); err != nil {
			return fmt.Errorf("failed to migrate to v11: %w", err)
		}
	}

	if version < 12 {
		if err := db.migrateToV12(); err != nil {
			return fmt.Errorf("failed to migrate to v12: %w", err)
		}
	}

	return nil
}

// migrateToV2 migrates the database from v1 to v2 (Architectural Memory)
func (db *DB) migrateToV2() error {
	return db.WithTx(func(tx *sql.Tx) error {
		db.logger.Info("Migrating database to v2 (Architectural Memory)")

		// Add new columns to modules table
		alterStatements := []string{
			"ALTER TABLE modules ADD COLUMN boundaries TEXT",
			"ALTER TABLE modules ADD COLUMN responsibility TEXT",
			"ALTER TABLE modules ADD COLUMN owner_ref TEXT",
			"ALTER TABLE modules ADD COLUMN tags TEXT",
			"ALTER TABLE modules ADD COLUMN source TEXT NOT NULL DEFAULT 'inferred'",
			"ALTER TABLE modules ADD COLUMN confidence REAL NOT NULL DEFAULT 0.5",
			"ALTER TABLE modules ADD COLUMN confidence_basis TEXT",
			"ALTER TABLE modules ADD COLUMN updated_at TEXT",
		}

		for _, stmt := range alterStatements {
			if _, err := tx.Exec(stmt); err != nil {
				// SQLite will error if column already exists, which is fine
				db.logger.Debug("ALTER TABLE statement",
					"statement", stmt,
					"error", err.Error(),
				)
			}
		}

		// Create new v2 tables
		if err := createOwnershipTable(tx); err != nil {
			return err
		}
		if err := createOwnershipHistoryTable(tx); err != nil {
			return err
		}
		if err := createHotspotSnapshotsTable(tx); err != nil {
			return err
		}
		if err := createResponsibilitiesTable(tx); err != nil {
			return err
		}
		if err := createDecisionsTable(tx); err != nil {
			return err
		}
		if err := createModuleRenamesTable(tx); err != nil {
			return err
		}

		// Update schema version
		if err := setSchemaVersion(tx, 2); err != nil {
			return err
		}

		db.logger.Info("Database migrated to v2")
		return nil
	})
}

// getSchemaVersion gets the current schema version
func (db *DB) getSchemaVersion() (int, error) {
	// Check if schema_version table exists
	var tableName string
	err := db.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name='schema_version'
	`).Scan(&tableName)

	if err == sql.ErrNoRows {
		// Table doesn't exist, this is a new database
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	// Get version
	var version int
	err = db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&version)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	return version, nil
}

// setSchemaVersion sets the schema version
func setSchemaVersion(tx *sql.Tx, version int) error {
	_, err := tx.Exec("DELETE FROM schema_version")
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO schema_version (version) VALUES (?)", version)
	return err
}

// createSchemaVersionTable creates the schema_version tracking table
func createSchemaVersionTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER NOT NULL
		)
	`)
	return err
}

// createSymbolMappingsTable creates the symbol_mappings table
// Section 4.3: Symbol mappings with tombstones
func createSymbolMappingsTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS symbol_mappings (
			stable_id TEXT PRIMARY KEY,
			state TEXT NOT NULL CHECK(state IN ('active', 'deleted', 'unknown')),
			backend_stable_id TEXT,
			fingerprint_json TEXT NOT NULL,
			location_json TEXT NOT NULL,
			definition_version_id TEXT,
			definition_version_semantics TEXT,
			last_verified_at TEXT NOT NULL,
			last_verified_state_id TEXT NOT NULL,
			deleted_at TEXT,
			deleted_in_state_id TEXT,

			-- Constraints
			CHECK(
				(state = 'deleted' AND deleted_at IS NOT NULL AND deleted_in_state_id IS NOT NULL) OR
				(state != 'deleted' AND deleted_at IS NULL AND deleted_in_state_id IS NULL)
			)
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create symbol_mappings table: %w", err)
	}

	// Create indexes for common queries
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_symbol_mappings_state ON symbol_mappings(state)",
		"CREATE INDEX IF NOT EXISTS idx_symbol_mappings_backend_stable_id ON symbol_mappings(backend_stable_id)",
		"CREATE INDEX IF NOT EXISTS idx_symbol_mappings_last_verified_state_id ON symbol_mappings(last_verified_state_id)",
		"CREATE INDEX IF NOT EXISTS idx_symbol_mappings_deleted_in_state_id ON symbol_mappings(deleted_in_state_id)",
	}

	for _, indexSQL := range indexes {
		if _, err := tx.Exec(indexSQL); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// createSymbolAliasesTable creates the symbol_aliases table
// Section 4.4: Alias/redirect table
func createSymbolAliasesTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS symbol_aliases (
			old_stable_id TEXT NOT NULL,
			new_stable_id TEXT NOT NULL,
			reason TEXT NOT NULL,
			confidence REAL NOT NULL CHECK(confidence >= 0.0 AND confidence <= 1.0),
			created_at TEXT NOT NULL,
			created_state_id TEXT NOT NULL,

			PRIMARY KEY (old_stable_id, new_stable_id),
			FOREIGN KEY (old_stable_id) REFERENCES symbol_mappings(stable_id) ON DELETE CASCADE,
			FOREIGN KEY (new_stable_id) REFERENCES symbol_mappings(stable_id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create symbol_aliases table: %w", err)
	}

	// Create indexes
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_symbol_aliases_new_stable_id ON symbol_aliases(new_stable_id)",
		"CREATE INDEX IF NOT EXISTS idx_symbol_aliases_created_state_id ON symbol_aliases(created_state_id)",
	}

	for _, indexSQL := range indexes {
		if _, err := tx.Exec(indexSQL); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// createModulesTable creates the modules table
//
//nolint:unused // reserved for future use
func createModulesTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS modules (
			module_id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			root_path TEXT NOT NULL,
			manifest_type TEXT,
			detected_at TEXT NOT NULL,
			state_id TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create modules table: %w", err)
	}

	// Create indexes
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_modules_name ON modules(name)",
		"CREATE INDEX IF NOT EXISTS idx_modules_root_path ON modules(root_path)",
		"CREATE INDEX IF NOT EXISTS idx_modules_state_id ON modules(state_id)",
	}

	for _, indexSQL := range indexes {
		if _, err := tx.Exec(indexSQL); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// createDependencyEdgesTable creates the dependency_edges table
func createDependencyEdgesTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS dependency_edges (
			from_module TEXT NOT NULL,
			to_module TEXT NOT NULL,
			kind TEXT NOT NULL,
			strength INTEGER NOT NULL,

			PRIMARY KEY (from_module, to_module),
			FOREIGN KEY (from_module) REFERENCES modules(module_id) ON DELETE CASCADE,
			FOREIGN KEY (to_module) REFERENCES modules(module_id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create dependency_edges table: %w", err)
	}

	// Create indexes
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_dependency_edges_to_module ON dependency_edges(to_module)",
		"CREATE INDEX IF NOT EXISTS idx_dependency_edges_kind ON dependency_edges(kind)",
	}

	for _, indexSQL := range indexes {
		if _, err := tx.Exec(indexSQL); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// createCacheTablesTable creates cache tables for all cache tiers
// Section 9: Cache tiers (query, view, negative)
func createCacheTablesTable(tx *sql.Tx) error {
	// Query cache table (TTL 300s, key includes headCommit)
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS query_cache (
			key TEXT PRIMARY KEY,
			value_json TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			state_id TEXT NOT NULL,
			head_commit TEXT NOT NULL,
			created_at TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create query_cache table: %w", err)
	}

	// View cache table (TTL 3600s, key includes repoStateId)
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS view_cache (
			key TEXT PRIMARY KEY,
			value_json TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			state_id TEXT NOT NULL,
			created_at TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create view_cache table: %w", err)
	}

	// Negative cache table (TTL 60s, key includes repoStateId)
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS negative_cache (
			key TEXT PRIMARY KEY,
			error_type TEXT NOT NULL,
			error_message TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			state_id TEXT NOT NULL,
			created_at TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create negative_cache table: %w", err)
	}

	// Create indexes for cache cleanup and lookup
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_query_cache_expires_at ON query_cache(expires_at)",
		"CREATE INDEX IF NOT EXISTS idx_query_cache_state_id ON query_cache(state_id)",
		"CREATE INDEX IF NOT EXISTS idx_view_cache_expires_at ON view_cache(expires_at)",
		"CREATE INDEX IF NOT EXISTS idx_view_cache_state_id ON view_cache(state_id)",
		"CREATE INDEX IF NOT EXISTS idx_negative_cache_expires_at ON negative_cache(expires_at)",
		"CREATE INDEX IF NOT EXISTS idx_negative_cache_state_id ON negative_cache(state_id)",
		"CREATE INDEX IF NOT EXISTS idx_negative_cache_error_type ON negative_cache(error_type)",
	}

	for _, indexSQL := range indexes {
		if _, err := tx.Exec(indexSQL); err != nil {
			return fmt.Errorf("failed to create cache index: %w", err)
		}
	}

	return nil
}

// ============================================================================
// v2 Schema: Architectural Memory Tables
// ============================================================================

// createModulesTableV2 creates the modules table with v2 columns
func createModulesTableV2(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS modules (
			module_id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			root_path TEXT NOT NULL,
			manifest_type TEXT,
			detected_at TEXT NOT NULL,
			state_id TEXT NOT NULL,
			-- v2 columns for Architectural Memory
			boundaries TEXT,                              -- JSON: {public: [], internal: []}
			responsibility TEXT,                          -- one-sentence description
			owner_ref TEXT,                               -- link to ownership
			tags TEXT,                                    -- JSON array
			source TEXT NOT NULL DEFAULT 'inferred',      -- "declared" | "inferred"
			confidence REAL NOT NULL DEFAULT 0.5,
			confidence_basis TEXT,                        -- JSON array
			updated_at TEXT
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create modules table: %w", err)
	}

	// Create indexes
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_modules_name ON modules(name)",
		"CREATE INDEX IF NOT EXISTS idx_modules_root_path ON modules(root_path)",
		"CREATE INDEX IF NOT EXISTS idx_modules_state_id ON modules(state_id)",
		"CREATE INDEX IF NOT EXISTS idx_modules_source ON modules(source)",
	}

	for _, indexSQL := range indexes {
		if _, err := tx.Exec(indexSQL); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// createOwnershipTable creates the ownership table
// Stores ownership rules with source and confidence
func createOwnershipTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS ownership (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pattern TEXT NOT NULL,                        -- glob pattern (e.g., "internal/api/**")
			owners TEXT NOT NULL,                         -- JSON array of Owner objects
			scope TEXT NOT NULL,                          -- "maintainer" | "reviewer" | "contributor"
			source TEXT NOT NULL,                         -- "codeowners" | "git-blame" | "declared" | "inferred"
			confidence REAL NOT NULL,
			updated_at TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create ownership table: %w", err)
	}

	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_ownership_pattern ON ownership(pattern)",
		"CREATE INDEX IF NOT EXISTS idx_ownership_source ON ownership(source)",
	}

	for _, indexSQL := range indexes {
		if _, err := tx.Exec(indexSQL); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// createOwnershipHistoryTable creates the ownership_history table (append-only)
// Tracks ownership changes over time for auditing
func createOwnershipHistoryTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS ownership_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pattern TEXT NOT NULL,
			owner_id TEXT NOT NULL,                       -- @username, @org/team, or email
			event TEXT NOT NULL,                          -- "added" | "removed" | "promoted" | "demoted"
			reason TEXT,                                  -- e.g., "git_blame_shift", "codeowners_update"
			recorded_at TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create ownership_history table: %w", err)
	}

	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_ownership_history_pattern ON ownership_history(pattern)",
		"CREATE INDEX IF NOT EXISTS idx_ownership_history_recorded_at ON ownership_history(recorded_at)",
	}

	for _, indexSQL := range indexes {
		if _, err := tx.Exec(indexSQL); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// createHotspotSnapshotsTable creates the hotspot_snapshots table (append-only)
// Stores time-series hotspot metrics for trend analysis
func createHotspotSnapshotsTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS hotspot_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			target_id TEXT NOT NULL,                      -- file path, module ID, or symbol ID
			target_type TEXT NOT NULL,                    -- "file" | "module" | "symbol"
			snapshot_date TEXT NOT NULL,                  -- ISO 8601 date
			churn_commits_30d INTEGER,
			churn_commits_90d INTEGER,
			churn_authors_30d INTEGER,
			complexity_cyclomatic REAL,
			complexity_cognitive REAL,
			coupling_afferent INTEGER,                    -- incoming dependencies
			coupling_efferent INTEGER,                    -- outgoing dependencies
			coupling_instability REAL,                    -- efferent / (afferent + efferent)
			score REAL NOT NULL                           -- composite hotspot score
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create hotspot_snapshots table: %w", err)
	}

	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_hotspot_target ON hotspot_snapshots(target_id, snapshot_date)",
		"CREATE INDEX IF NOT EXISTS idx_hotspot_date ON hotspot_snapshots(snapshot_date)",
		"CREATE INDEX IF NOT EXISTS idx_hotspot_type ON hotspot_snapshots(target_type)",
	}

	for _, indexSQL := range indexes {
		if _, err := tx.Exec(indexSQL); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// createResponsibilitiesTable creates the responsibilities table
// Stores module/file responsibility descriptions
func createResponsibilitiesTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS responsibilities (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			target_id TEXT NOT NULL,                      -- module ID, file path, or symbol ID
			target_type TEXT NOT NULL,                    -- "module" | "file" | "symbol"
			summary TEXT NOT NULL,                        -- one-sentence description
			capabilities TEXT,                            -- JSON array of capabilities
			source TEXT NOT NULL,                         -- "declared" | "inferred" | "llm-generated"
			confidence REAL NOT NULL,
			updated_at TEXT NOT NULL,
			verified_at TEXT                              -- human verification timestamp
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create responsibilities table: %w", err)
	}

	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_responsibilities_target ON responsibilities(target_id)",
		"CREATE INDEX IF NOT EXISTS idx_responsibilities_type ON responsibilities(target_type)",
		"CREATE INDEX IF NOT EXISTS idx_responsibilities_source ON responsibilities(source)",
	}

	for _, indexSQL := range indexes {
		if _, err := tx.Exec(indexSQL); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	// Create FTS5 virtual table for full-text search
	if _, err := tx.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS responsibilities_fts USING fts5(
			target_id,
			summary,
			capabilities,
			content='responsibilities',
			content_rowid='id'
		)
	`); err != nil {
		return fmt.Errorf("failed to create responsibilities_fts table: %w", err)
	}

	return nil
}

// createDecisionsTable creates the decisions table
// Stores ADR metadata (content is in markdown files)
func createDecisionsTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS decisions (
			id TEXT PRIMARY KEY,                          -- "ADR-001" style
			title TEXT NOT NULL,
			status TEXT NOT NULL,                         -- "proposed" | "accepted" | "deprecated" | "superseded"
			affected_modules TEXT,                        -- JSON array of module IDs
			file_path TEXT NOT NULL,                      -- relative path to .md file
			author TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create decisions table: %w", err)
	}

	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_decisions_status ON decisions(status)",
		"CREATE INDEX IF NOT EXISTS idx_decisions_created_at ON decisions(created_at)",
	}

	for _, indexSQL := range indexes {
		if _, err := tx.Exec(indexSQL); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	// Create FTS5 virtual table for full-text search
	if _, err := tx.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS decisions_fts USING fts5(
			id,
			title,
			content='decisions',
			content_rowid='rowid'
		)
	`); err != nil {
		return fmt.Errorf("failed to create decisions_fts table: %w", err)
	}

	return nil
}

// migrateToV3 migrates the database from v2 to v3 (Telemetry)
func (db *DB) migrateToV3() error {
	return db.WithTx(func(tx *sql.Tx) error {
		db.logger.Info("Migrating database to v3 (Telemetry)")

		// Create new v3 telemetry tables
		if err := createTelemetryTables(tx); err != nil {
			return err
		}

		// Update schema version
		if err := setSchemaVersion(tx, 3); err != nil {
			return err
		}

		db.logger.Info("Database migrated to v3")
		return nil
	})
}

// createTelemetryTables creates tables for runtime telemetry (v3)
func createTelemetryTables(tx *sql.Tx) error {
	tables := []string{
		// Observed usage aggregates (matched symbols only)
		`CREATE TABLE IF NOT EXISTS observed_usage (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			symbol_id TEXT NOT NULL,
			match_quality TEXT NOT NULL,
			match_confidence REAL NOT NULL,
			period TEXT NOT NULL,
			period_type TEXT NOT NULL,
			call_count INTEGER NOT NULL,
			error_count INTEGER DEFAULT 0,
			service_version TEXT,
			source TEXT NOT NULL,
			ingested_at TEXT NOT NULL,
			UNIQUE(symbol_id, period, source)
		)`,

		// Unmatched events (separate table for clean deduplication)
		`CREATE TABLE IF NOT EXISTS observed_unmatched (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			service_name TEXT NOT NULL,
			function_name TEXT NOT NULL,
			namespace TEXT,
			file_path TEXT,
			period TEXT NOT NULL,
			period_type TEXT NOT NULL,
			call_count INTEGER NOT NULL,
			error_count INTEGER DEFAULT 0,
			unmatch_reason TEXT,
			source TEXT NOT NULL,
			ingested_at TEXT NOT NULL
		)`,

		// Caller breakdown (optional, opt-in)
		`CREATE TABLE IF NOT EXISTS observed_callers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			symbol_id TEXT NOT NULL,
			caller_service TEXT NOT NULL,
			period TEXT NOT NULL,
			call_count INTEGER NOT NULL,
			UNIQUE(symbol_id, caller_service, period)
		)`,

		// Telemetry sync log
		`CREATE TABLE IF NOT EXISTS telemetry_sync_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source TEXT NOT NULL,
			started_at TEXT NOT NULL,
			completed_at TEXT,
			status TEXT NOT NULL,
			events_received INTEGER,
			events_matched_exact INTEGER,
			events_matched_strong INTEGER,
			events_matched_weak INTEGER,
			events_unmatched INTEGER,
			service_versions TEXT,
			coverage_score REAL,
			coverage_level TEXT,
			error TEXT
		)`,

		// Coverage snapshots (for trend tracking)
		`CREATE TABLE IF NOT EXISTS coverage_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			snapshot_date TEXT NOT NULL,
			attribute_coverage REAL,
			match_coverage REAL,
			service_coverage REAL,
			overall_score REAL,
			overall_level TEXT,
			warnings TEXT
		)`,
	}

	for _, stmt := range tables {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("failed to create telemetry table: %w", err)
		}
	}

	// Create indexes
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_observed_symbol ON observed_usage(symbol_id)",
		"CREATE INDEX IF NOT EXISTS idx_observed_period ON observed_usage(period)",
		"CREATE INDEX IF NOT EXISTS idx_observed_quality ON observed_usage(match_quality)",
		"CREATE INDEX IF NOT EXISTS idx_observed_calls ON observed_usage(call_count DESC)",
		"CREATE INDEX IF NOT EXISTS idx_unmatched_service ON observed_unmatched(service_name)",
		"CREATE INDEX IF NOT EXISTS idx_unmatched_function ON observed_unmatched(function_name)",
		"CREATE INDEX IF NOT EXISTS idx_unmatched_period ON observed_unmatched(period)",
		"CREATE INDEX IF NOT EXISTS idx_callers_symbol ON observed_callers(symbol_id)",
		"CREATE INDEX IF NOT EXISTS idx_coverage_date ON coverage_snapshots(snapshot_date)",
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("failed to create telemetry index: %w", err)
		}
	}

	return nil
}

// createModuleRenamesTable creates the module_renames table
// Tracks module ID changes for stable ID resolution
func createModuleRenamesTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS module_renames (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			old_id TEXT NOT NULL,
			new_id TEXT NOT NULL,
			renamed_at TEXT NOT NULL,
			reason TEXT                                   -- "directory_rename" | "manual" | "merge"
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create module_renames table: %w", err)
	}

	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_module_renames_old ON module_renames(old_id)",
		"CREATE INDEX IF NOT EXISTS idx_module_renames_new ON module_renames(new_id)",
	}

	for _, indexSQL := range indexes {
		if _, err := tx.Exec(indexSQL); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// ============================================================================
// v4 Schema: Developer Intelligence Tables
// ============================================================================

// migrateToV4 migrates the database from v3 to v4 (Developer Intelligence)
func (db *DB) migrateToV4() error {
	return db.WithTx(func(tx *sql.Tx) error {
		db.logger.Info("Migrating database to v4 (Developer Intelligence)")

		// Create new v4 Developer Intelligence tables
		if err := createDeveloperIntelligenceTables(tx); err != nil {
			return err
		}

		// Update schema version
		if err := setSchemaVersion(tx, 4); err != nil {
			return err
		}

		db.logger.Info("Database migrated to v4")
		return nil
	})
}

// createDeveloperIntelligenceTables creates tables for v6.5 features
func createDeveloperIntelligenceTables(tx *sql.Tx) error {
	tables := []string{
		// Coupling cache - precomputed co-change correlations
		`CREATE TABLE IF NOT EXISTS coupling_cache (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			file_path TEXT NOT NULL,
			correlated_file TEXT NOT NULL,
			correlation REAL NOT NULL,
			co_change_count INTEGER NOT NULL,
			total_changes INTEGER NOT NULL,
			computed_at TEXT NOT NULL,
			UNIQUE(file_path, correlated_file)
		)`,

		// Risk scores cache - computed risk scores for files
		`CREATE TABLE IF NOT EXISTS risk_scores (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			file_path TEXT NOT NULL UNIQUE,
			risk_score REAL NOT NULL,
			risk_level TEXT NOT NULL,
			factors TEXT NOT NULL,
			computed_at TEXT NOT NULL
		)`,
	}

	for _, stmt := range tables {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("failed to create developer intelligence table: %w", err)
		}
	}

	// Create indexes
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_coupling_file ON coupling_cache(file_path)",
		"CREATE INDEX IF NOT EXISTS idx_coupling_correlation ON coupling_cache(correlation DESC)",
		"CREATE INDEX IF NOT EXISTS idx_coupling_correlated ON coupling_cache(correlated_file)",
		"CREATE INDEX IF NOT EXISTS idx_risk_score ON risk_scores(risk_score DESC)",
		"CREATE INDEX IF NOT EXISTS idx_risk_level ON risk_scores(risk_level)",
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("failed to create developer intelligence index: %w", err)
		}
	}

	return nil
}

// ============================================================================
// v5 Schema: Doc-Symbol Linking Tables
// ============================================================================

// migrateToV5 migrates the database from v4 to v5 (Doc-Symbol Linking)
func (db *DB) migrateToV5() error {
	return db.WithTx(func(tx *sql.Tx) error {
		db.logger.Info("Migrating database to v5 (Doc-Symbol Linking)")

		// Create new v5 doc linking tables
		if err := createDocsTables(tx); err != nil {
			return err
		}

		// Update schema version
		if err := setSchemaVersion(tx, 5); err != nil {
			return err
		}

		db.logger.Info("Database migrated to v5")
		return nil
	})
}

// createDocsTables creates tables for v7.3 doc-symbol linking
func createDocsTables(tx *sql.Tx) error {
	tables := []string{
		// Documents table - tracks indexed documentation files
		`CREATE TABLE IF NOT EXISTS docs (
			path TEXT PRIMARY KEY,
			doc_type TEXT NOT NULL,
			title TEXT,
			hash TEXT NOT NULL,
			last_indexed INTEGER NOT NULL
		)`,

		// References table - symbol mentions in docs
		`CREATE TABLE IF NOT EXISTS doc_references (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			doc_path TEXT NOT NULL,
			raw_text TEXT NOT NULL,
			normalized_text TEXT NOT NULL,
			symbol_id TEXT,
			symbol_name TEXT,
			line INTEGER NOT NULL,
			col INTEGER NOT NULL,
			context TEXT,
			detection_method TEXT NOT NULL,
			resolution TEXT NOT NULL,
			candidates TEXT,
			confidence REAL DEFAULT 1.0,
			last_resolved INTEGER NOT NULL,
			FOREIGN KEY (doc_path) REFERENCES docs(path) ON DELETE CASCADE
		)`,

		// Module links table - explicit doc↔module associations
		`CREATE TABLE IF NOT EXISTS doc_modules (
			doc_path TEXT NOT NULL,
			module_id TEXT NOT NULL,
			line INTEGER NOT NULL,
			PRIMARY KEY (doc_path, module_id),
			FOREIGN KEY (doc_path) REFERENCES docs(path) ON DELETE CASCADE
		)`,

		// Suffix index - precomputed suffix segments for fast matching
		`CREATE TABLE IF NOT EXISTS symbol_suffixes (
			suffix TEXT NOT NULL,
			symbol_id TEXT NOT NULL,
			segment_count INTEGER NOT NULL,
			PRIMARY KEY (suffix, symbol_id)
		)`,

		// Docs meta - stores symbol index version for staleness detection
		`CREATE TABLE IF NOT EXISTS docs_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}

	for _, stmt := range tables {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("failed to create docs table: %w", err)
		}
	}

	// Create indexes
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_docs_type ON docs(doc_type)",
		"CREATE INDEX IF NOT EXISTS idx_refs_doc_path ON doc_references(doc_path)",
		"CREATE INDEX IF NOT EXISTS idx_refs_symbol_id ON doc_references(symbol_id)",
		"CREATE INDEX IF NOT EXISTS idx_refs_normalized ON doc_references(normalized_text)",
		"CREATE INDEX IF NOT EXISTS idx_refs_resolution ON doc_references(resolution)",
		"CREATE INDEX IF NOT EXISTS idx_doc_modules_module ON doc_modules(module_id)",
		"CREATE INDEX IF NOT EXISTS idx_suffixes_suffix ON symbol_suffixes(suffix)",
		"CREATE INDEX IF NOT EXISTS idx_suffixes_segments ON symbol_suffixes(segment_count)",
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("failed to create docs index: %w", err)
		}
	}

	return nil
}

// ============================================================================
// v6 Schema: Incremental Indexing Tables
// ============================================================================

// migrateToV6 migrates the database from v5 to v6 (Incremental Indexing)
func (db *DB) migrateToV6() error {
	return db.WithTx(func(tx *sql.Tx) error {
		db.logger.Info("Migrating database to v6 (Incremental Indexing)")

		// Create new v6 incremental indexing tables
		if err := createIncrementalIndexingTables(tx); err != nil {
			return err
		}

		// Update schema version
		if err := setSchemaVersion(tx, 6); err != nil {
			return err
		}

		db.logger.Info("Database migrated to v6")
		return nil
	})
}

// createIncrementalIndexingTables creates tables for incremental SCIP indexing
func createIncrementalIndexingTables(tx *sql.Tx) error {
	tables := []string{
		// Track indexed files for change detection
		`CREATE TABLE IF NOT EXISTS indexed_files (
			path TEXT PRIMARY KEY,
			hash TEXT NOT NULL,
			mtime INTEGER,
			indexed_at INTEGER NOT NULL,
			scip_document_hash TEXT,
			symbol_count INTEGER DEFAULT 0
		)`,

		// Map files to their symbols for fast invalidation
		`CREATE TABLE IF NOT EXISTS file_symbols (
			file_path TEXT NOT NULL,
			symbol_id TEXT NOT NULL,
			PRIMARY KEY (file_path, symbol_id)
		)`,

		// Key-value store for incremental index state
		`CREATE TABLE IF NOT EXISTS index_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}

	for _, stmt := range tables {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("failed to create incremental indexing table: %w", err)
		}
	}

	// Create indexes for efficient queries
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_indexed_files_indexed ON indexed_files(indexed_at)",
		"CREATE INDEX IF NOT EXISTS idx_file_symbols_file ON file_symbols(file_path)",
		"CREATE INDEX IF NOT EXISTS idx_file_symbols_symbol ON file_symbols(symbol_id)",
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("failed to create incremental indexing index: %w", err)
		}
	}

	return nil
}

// ============================================================================
// v7: Incremental Callgraph
// ============================================================================

// migrateToV7 migrates the database from v6 to v7 (Incremental Callgraph)
func (db *DB) migrateToV7() error {
	return db.WithTx(func(tx *sql.Tx) error {
		db.logger.Info("Migrating database to v7 (Incremental Callgraph)")

		// Create new v7 callgraph table
		if err := createCallgraphTable(tx); err != nil {
			return err
		}

		// Update schema version
		if err := setSchemaVersion(tx, 7); err != nil {
			return err
		}

		db.logger.Info("Database migrated to v7")
		return nil
	})
}

// createCallgraphTable creates the callgraph table for incremental call edge storage
// Edges are owned by the caller file (caller-owned edges invariant).
// PK is location-anchored: (caller_file, call_line, call_col, callee_id)
func createCallgraphTable(tx *sql.Tx) error {
	// Create callgraph table
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS callgraph (
			caller_id    TEXT,              -- May be NULL if caller unresolved
			callee_id    TEXT NOT NULL,     -- Target symbol being called
			caller_file  TEXT NOT NULL,     -- File containing the call (for deletion)
			call_line    INTEGER NOT NULL,  -- 1-indexed line number
			call_col     INTEGER NOT NULL,  -- 1-indexed column number
			call_end_col INTEGER,           -- Optional: end column for nested calls
			PRIMARY KEY (caller_file, call_line, call_col, callee_id)
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create callgraph table: %w", err)
	}

	// Create indexes for efficient queries
	indexes := []string{
		// Fast deletion by file (incremental update)
		"CREATE INDEX IF NOT EXISTS idx_callgraph_caller_file ON callgraph(caller_file)",
		// "What does X call?" queries
		"CREATE INDEX IF NOT EXISTS idx_callgraph_caller_id ON callgraph(caller_id)",
		// "What calls X?" queries
		"CREATE INDEX IF NOT EXISTS idx_callgraph_callee_id ON callgraph(callee_id)",
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("failed to create callgraph index: %w", err)
		}
	}

	return nil
}

// ============================================================================
// v8: Transitive Invalidation
// ============================================================================

// migrateToV8 migrates the database from v7 to v8 (Transitive Invalidation)
func (db *DB) migrateToV8() error {
	return db.WithTx(func(tx *sql.Tx) error {
		db.logger.Info("Migrating database to v8 (Transitive Invalidation)")

		// Create new v8 transitive invalidation tables
		if err := createTransitiveInvalidationTables(tx); err != nil {
			return err
		}

		// Update schema version
		if err := setSchemaVersion(tx, 8); err != nil {
			return err
		}

		db.logger.Info("Database migrated to v8")
		return nil
	})
}

// createTransitiveInvalidationTables creates tables for file dependency tracking
// and rescan queue for transitive invalidation (v2)
func createTransitiveInvalidationTables(tx *sql.Tx) error {
	tables := []string{
		// file_deps: tracks which files depend on which files
		// dependent_file uses symbols defined in defining_file
		// Only tracks internal files (not external deps like stdlib)
		`CREATE TABLE IF NOT EXISTS file_deps (
			dependent_file TEXT NOT NULL,  -- File that references symbols
			defining_file  TEXT NOT NULL,  -- File that defines those symbols
			PRIMARY KEY (dependent_file, defining_file)
		)`,

		// rescan_queue: files that need to be rescanned due to dependency changes
		// Used for transitive invalidation in lazy/eager/deferred modes
		`CREATE TABLE IF NOT EXISTS rescan_queue (
			file_path   TEXT PRIMARY KEY,
			reason      TEXT NOT NULL,      -- 'dep_change' | 'budget_exceeded' | 'manual'
			depth       INTEGER NOT NULL,   -- BFS hop count from original change
			enqueued_at INTEGER NOT NULL,   -- Unix timestamp
			attempts    INTEGER NOT NULL DEFAULT 0
		)`,
	}

	for _, stmt := range tables {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("failed to create transitive invalidation table: %w", err)
		}
	}

	// Create indexes
	indexes := []string{
		// Fast lookup of what depends on a changed file
		"CREATE INDEX IF NOT EXISTS idx_file_deps_defining ON file_deps(defining_file)",
		// Queue ordering
		"CREATE INDEX IF NOT EXISTS idx_rescan_queue_time ON rescan_queue(enqueued_at)",
		"CREATE INDEX IF NOT EXISTS idx_rescan_queue_depth ON rescan_queue(depth)",
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("failed to create transitive invalidation index: %w", err)
		}
	}

	return nil
}

// ============================================================================
// v9 Schema: FTS5 Symbol Search
// ============================================================================

// migrateToV9 migrates the database from v8 to v9 (FTS5 Symbol Search)
func (db *DB) migrateToV9() error {
	return db.WithTx(func(tx *sql.Tx) error {
		db.logger.Info("Migrating database to v9 (FTS5 Symbol Search)")

		// Create FTS5 tables
		if err := createSymbolFTSTables(tx); err != nil {
			return err
		}

		// Update schema version
		if err := setSchemaVersion(tx, 9); err != nil {
			return err
		}

		db.logger.Info("Database migrated to v9")
		return nil
	})
}

// createSymbolFTSTables creates the FTS5 tables for symbol search
func createSymbolFTSTables(tx *sql.Tx) error {
	// Create the base content table for FTS5
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS symbols_fts_content (
			rowid INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT UNIQUE NOT NULL,
			name TEXT NOT NULL,
			kind TEXT,
			documentation TEXT,
			signature TEXT,
			file_path TEXT,
			language TEXT,
			indexed_at TEXT NOT NULL DEFAULT (datetime('now'))
		)
	`); err != nil {
		return fmt.Errorf("failed to create symbols_fts_content table: %w", err)
	}

	// Create indexes on content table
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_symbols_fts_content_id ON symbols_fts_content(id)",
		"CREATE INDEX IF NOT EXISTS idx_symbols_fts_content_kind ON symbols_fts_content(kind)",
		"CREATE INDEX IF NOT EXISTS idx_symbols_fts_content_language ON symbols_fts_content(language)",
	}
	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("failed to create symbols_fts index: %w", err)
		}
	}

	// Create FTS5 virtual table
	if _, err := tx.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS symbols_fts USING fts5(
			name,
			documentation,
			signature,
			content='symbols_fts_content',
			content_rowid='rowid'
		)
	`); err != nil {
		return fmt.Errorf("failed to create symbols_fts table: %w", err)
	}

	// Create triggers for automatic sync
	triggers := []string{
		// After INSERT trigger
		`CREATE TRIGGER IF NOT EXISTS symbols_fts_ai AFTER INSERT ON symbols_fts_content BEGIN
			INSERT INTO symbols_fts(rowid, name, documentation, signature)
			VALUES (new.rowid, new.name, new.documentation, new.signature);
		END`,

		// After UPDATE trigger
		`CREATE TRIGGER IF NOT EXISTS symbols_fts_au AFTER UPDATE ON symbols_fts_content BEGIN
			INSERT INTO symbols_fts(symbols_fts, rowid, name, documentation, signature)
			VALUES ('delete', old.rowid, old.name, old.documentation, old.signature);
			INSERT INTO symbols_fts(rowid, name, documentation, signature)
			VALUES (new.rowid, new.name, new.documentation, new.signature);
		END`,

		// After DELETE trigger
		`CREATE TRIGGER IF NOT EXISTS symbols_fts_ad AFTER DELETE ON symbols_fts_content BEGIN
			INSERT INTO symbols_fts(symbols_fts, rowid, name, documentation, signature)
			VALUES ('delete', old.rowid, old.name, old.documentation, old.signature);
		END`,
	}

	for _, trigger := range triggers {
		if _, err := tx.Exec(trigger); err != nil {
			return fmt.Errorf("failed to create symbols_fts trigger: %w", err)
		}
	}

	return nil
}

// ============================================================================
// v10 Schema: Wide-Result Metrics
// ============================================================================

// migrateToV10 migrates the database from v9 to v10 (Wide-Result Metrics)
func (db *DB) migrateToV10() error {
	return db.WithTx(func(tx *sql.Tx) error {
		db.logger.Info("Migrating database to v10 (Wide-Result Metrics)")

		// Create wide_result_metrics table
		if err := createWideResultMetricsTable(tx); err != nil {
			return err
		}

		// Update schema version
		if err := setSchemaVersion(tx, 10); err != nil {
			return err
		}

		db.logger.Info("Database migrated to v10")
		return nil
	})
}

// createWideResultMetricsTable creates the table for MCP tool telemetry
// Records per-invocation metrics for wide-result tools to track truncation rates
func createWideResultMetricsTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS wide_result_metrics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tool_name TEXT NOT NULL,
			total_results INTEGER NOT NULL,
			returned_results INTEGER NOT NULL,
			truncated_count INTEGER NOT NULL,
			estimated_tokens INTEGER NOT NULL DEFAULT 0,
			response_bytes INTEGER NOT NULL DEFAULT 0,
			execution_ms INTEGER NOT NULL DEFAULT 0,
			recorded_at TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create wide_result_metrics table: %w", err)
	}

	// Create indexes for efficient queries
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_wide_result_tool ON wide_result_metrics(tool_name)",
		"CREATE INDEX IF NOT EXISTS idx_wide_result_recorded ON wide_result_metrics(recorded_at)",
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("failed to create wide_result_metrics index: %w", err)
		}
	}

	return nil
}

// ============================================================================
// v11 Schema: Wide-Result Metrics Response Bytes
// ============================================================================

// migrateToV11 migrates the database from v10 to v11 (adds response_bytes column)
func (db *DB) migrateToV11() error {
	return db.WithTx(func(tx *sql.Tx) error {
		db.logger.Info("Migrating database to v11 (Response Bytes)")

		// Check if response_bytes column exists
		var count int
		err := tx.QueryRow(`
			SELECT COUNT(*) FROM pragma_table_info('wide_result_metrics')
			WHERE name = 'response_bytes'
		`).Scan(&count)
		if err != nil {
			return fmt.Errorf("failed to check for response_bytes column: %w", err)
		}

		if count == 0 {
			// Add response_bytes column
			_, err := tx.Exec(`ALTER TABLE wide_result_metrics ADD COLUMN response_bytes INTEGER NOT NULL DEFAULT 0`)
			if err != nil {
				return fmt.Errorf("failed to add response_bytes column: %w", err)
			}
			db.logger.Info("Added response_bytes column to wide_result_metrics")
		}

		// Update schema version
		if err := setSchemaVersion(tx, 11); err != nil {
			return err
		}

		db.logger.Info("Database migrated to v11")
		return nil
	})
}

// ============================================================================
// v12 Schema: Activity Ledger
// ============================================================================

// migrateToV12 migrates the database from v11 to v12 (Activity Ledger)
func (db *DB) migrateToV12() error {
	return db.WithTx(func(tx *sql.Tx) error {
		db.logger.Info("Migrating database to v12 (Activity Ledger)")

		// Create tool_calls table
		if err := createToolCallsTable(tx); err != nil {
			return err
		}

		// Update schema version
		if err := setSchemaVersion(tx, 12); err != nil {
			return err
		}

		db.logger.Info("Database migrated to v12")
		return nil
	})
}

// createToolCallsTable creates the tool_calls table for the MCP activity ledger.
// Records one row per MCP tool invocation: identity, timing, size, and
// (optional) per-tool structured facts. See internal/activity for the writer.
func createToolCallsTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS tool_calls (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			ts             INTEGER NOT NULL,           -- unix ms, call start
			session_id     TEXT,
			consumer       TEXT,
			tool           TEXT NOT NULL,
			params_hash    TEXT,
			params         TEXT,                       -- canonical JSON, truncated (nullable)
			target         TEXT,                       -- best-effort primary argument (nullable)
			duration_ms    INTEGER,
			response_bytes INTEGER,
			truncated      INTEGER NOT NULL DEFAULT 0,  -- 0/1
			error          TEXT,
			facts          TEXT                        -- JSON object of string->int (nullable)
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create tool_calls table: %w", err)
	}

	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_tool_calls_ts ON tool_calls(ts)",
		"CREATE INDEX IF NOT EXISTS idx_tool_calls_session_ts ON tool_calls(session_id, ts)",
		"CREATE INDEX IF NOT EXISTS idx_tool_calls_tool_ts ON tool_calls(tool, ts)",
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("failed to create tool_calls index: %w", err)
		}
	}

	return nil
}
