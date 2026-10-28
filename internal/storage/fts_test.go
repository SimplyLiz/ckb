package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func setupTestFTSDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	// Create temp directory
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Open database
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	// Set pragmas
	_, _ = db.Exec("PRAGMA journal_mode=WAL")
	_, _ = db.Exec("PRAGMA foreign_keys=ON")

	cleanup := func() {
		_ = db.Close()
		os.RemoveAll(tmpDir)
	}

	return db, cleanup
}

func TestFTSManagerInitSchema(t *testing.T) {
	db, cleanup := setupTestFTSDB(t)
	defer cleanup()

	manager := NewFTSManager(db, DefaultFTSConfig())

	// Initialize schema
	err := manager.InitSchema()
	if err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	// Verify tables exist
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='symbols_fts_content'").Scan(&count)
	if err != nil || count != 1 {
		t.Error("symbols_fts_content table not created")
	}

	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='symbols_fts'").Scan(&count)
	if err != nil || count != 1 {
		t.Error("symbols_fts virtual table not created")
	}

	// Verify triggers exist
	var triggerCount int
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'symbols_fts_%'").Scan(&triggerCount)
	if err != nil || triggerCount < 3 {
		t.Errorf("expected at least 3 triggers, got %d", triggerCount)
	}
}

func TestFTSManagerBulkInsert(t *testing.T) {
	db, cleanup := setupTestFTSDB(t)
	defer cleanup()

	manager := NewFTSManager(db, DefaultFTSConfig())

	// Initialize schema first
	if err := manager.InitSchema(); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	// Insert test symbols
	symbols := []SymbolFTSRecord{
		{ID: "sym1", Name: "FooFunction", Kind: "function", Documentation: "Does foo things", FilePath: "foo.go", Language: "go"},
		{ID: "sym2", Name: "BarClass", Kind: "class", Documentation: "A bar class", FilePath: "bar.go", Language: "go"},
		{ID: "sym3", Name: "BazMethod", Kind: "method", Signature: "func BazMethod(x int) error", FilePath: "baz.go", Language: "go"},
	}

	ctx := context.Background()
	err := manager.BulkInsert(ctx, symbols)
	if err != nil {
		t.Fatalf("bulk insert failed: %v", err)
	}

	// Verify count
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM symbols_fts_content").Scan(&count)
	if err != nil || count != 3 {
		t.Errorf("expected 3 symbols, got %d", count)
	}
}

func TestFTSManagerSearch(t *testing.T) {
	db, cleanup := setupTestFTSDB(t)
	defer cleanup()

	manager := NewFTSManager(db, DefaultFTSConfig())

	// Initialize schema first
	if err := manager.InitSchema(); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	// Insert test symbols
	symbols := []SymbolFTSRecord{
		{ID: "sym1", Name: "FooFunction", Kind: "function", Documentation: "Does foo things", FilePath: "foo.go", Language: "go"},
		{ID: "sym2", Name: "BarClass", Kind: "class", Documentation: "A bar class with foo", FilePath: "bar.go", Language: "go"},
		{ID: "sym3", Name: "BazMethod", Kind: "method", Signature: "func BazMethod(x int) error", FilePath: "baz.go", Language: "go"},
		{ID: "sym4", Name: "FooBar", Kind: "function", Documentation: "Combined foobar", FilePath: "foobar.go", Language: "go"},
	}

	ctx := context.Background()
	if err := manager.BulkInsert(ctx, symbols); err != nil {
		t.Fatalf("bulk insert failed: %v", err)
	}

	tests := []struct {
		name      string
		query     string
		limit     int
		wantMin   int // minimum expected results
		wantMatch string
	}{
		{
			name:      "exact name match",
			query:     "FooFunction",
			limit:     10,
			wantMin:   1,
			wantMatch: "FooFunction",
		},
		{
			name:      "partial name match",
			query:     "Foo",
			limit:     10,
			wantMin:   2, // FooFunction and FooBar
			wantMatch: "FooFunction",
		},
		{
			name:      "documentation search",
			query:     "bar class",
			limit:     10,
			wantMin:   1,
			wantMatch: "BarClass",
		},
		{
			name:    "no match",
			query:   "nonexistent",
			limit:   10,
			wantMin: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := manager.Search(ctx, tt.query, tt.limit)
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}

			if len(results) < tt.wantMin {
				t.Errorf("expected at least %d results, got %d", tt.wantMin, len(results))
			}

			if tt.wantMatch != "" && len(results) > 0 {
				found := false
				for _, r := range results {
					if r.Name == tt.wantMatch {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected to find %s in results", tt.wantMatch)
				}
			}
		})
	}
}

// TestFTSManagerSearchExactCasePriority locks down the fix for the
// evidence-output-bugs ranking regression: unicode61 (FTS5's default
// tokenizer) folds case, so bm25() alone can't tell "Model" from "model"
// apart and LIMIT can truncate the pool before Go-side ranking ever sees
// the case-exact match. searchExact must put a case-sensitive exact name
// match first — ahead of bm25 — so a small LIMIT (as SearchSymbols uses for
// GetSymbol's single-result resolution) can't drop it.
func TestFTSManagerSearchExactCasePriority(t *testing.T) {
	db, cleanup := setupTestFTSDB(t)
	defer cleanup()

	manager := NewFTSManager(db, DefaultFTSConfig())
	if err := manager.InitSchema(); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	// Order matters here: the field/property rows are inserted (and thus
	// get lower rowids) before the class, so a naive bm25/rowid tie-break
	// would put them first — exactly what regressed in the TS fixture.
	symbols := []SymbolFTSRecord{
		{ID: "field1", Name: "model", Kind: "parameter", Signature: "DefaultService.model", FilePath: "service.go", Language: "go"},
		{ID: "field2", Name: "model", Kind: "property", Signature: "DefaultService.model", FilePath: "service.go", Language: "go"},
		{ID: "class1", Name: "Model", Kind: "class", Signature: "Model", FilePath: "model.go", Language: "go"},
	}
	ctx := context.Background()
	if err := manager.BulkInsert(ctx, symbols); err != nil {
		t.Fatalf("bulk insert failed: %v", err)
	}

	// Even with a tiny limit (mirrors SearchSymbols' opts.Limit*ftsMultiplier
	// for a Limit:1 caller), the case-sensitive exact match must come back.
	results, err := manager.Search(ctx, "Model", 2)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if got := results[0]; got.Name != "Model" || got.Kind != "class" {
		t.Errorf("top result = %+v, want case-sensitive exact match Name=Model Kind=class", got)
	}

	// A case-fold-only query should still prefer the type-like kind over
	// the property/parameter when there's no case-sensitive winner at all.
	results, err = manager.Search(ctx, "MODEL", 2)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if got := results[0]; got.Kind != "class" {
		t.Errorf("top case-fold-only result kind = %q, want %q (type-like kind should tie-break over property/parameter)", got.Kind, "class")
	}
}

func TestFTSManagerGetStats(t *testing.T) {
	db, cleanup := setupTestFTSDB(t)
	defer cleanup()

	manager := NewFTSManager(db, DefaultFTSConfig())

	// Initialize schema first
	if err := manager.InitSchema(); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	// Insert test symbols
	symbols := []SymbolFTSRecord{
		{ID: "sym1", Name: "Func1", Kind: "function", FilePath: "a.go"},
		{ID: "sym2", Name: "Func2", Kind: "function", FilePath: "b.go"},
	}

	ctx := context.Background()
	if err := manager.BulkInsert(ctx, symbols); err != nil {
		t.Fatalf("bulk insert failed: %v", err)
	}

	stats, err := manager.GetStats(ctx)
	if err != nil {
		t.Fatalf("get stats failed: %v", err)
	}

	indexedSymbols, ok := stats["indexed_symbols"].(int)
	if !ok {
		t.Error("indexed_symbols not in stats")
	}
	if indexedSymbols != 2 {
		t.Errorf("expected 2 indexed symbols, got %d", indexedSymbols)
	}
}

func TestFTSManagerClear(t *testing.T) {
	db, cleanup := setupTestFTSDB(t)
	defer cleanup()

	manager := NewFTSManager(db, DefaultFTSConfig())

	// Initialize schema first
	if err := manager.InitSchema(); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	// Insert test symbols
	symbols := []SymbolFTSRecord{
		{ID: "sym1", Name: "Func1", Kind: "function", FilePath: "a.go"},
	}

	ctx := context.Background()
	if err := manager.BulkInsert(ctx, symbols); err != nil {
		t.Fatalf("bulk insert failed: %v", err)
	}

	// Clear
	if err := manager.Clear(ctx); err != nil {
		t.Fatalf("clear failed: %v", err)
	}

	// Verify empty
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM symbols_fts_content").Scan(&count)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 symbols after clear, got %d", count)
	}
}

func TestFTSManagerRebuild(t *testing.T) {
	db, cleanup := setupTestFTSDB(t)
	defer cleanup()

	manager := NewFTSManager(db, DefaultFTSConfig())

	// Initialize schema first
	if err := manager.InitSchema(); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	// Insert test symbols
	symbols := []SymbolFTSRecord{
		{ID: "sym1", Name: "Func1", Kind: "function", FilePath: "a.go"},
	}

	ctx := context.Background()
	if err := manager.BulkInsert(ctx, symbols); err != nil {
		t.Fatalf("bulk insert failed: %v", err)
	}

	// Rebuild should not error
	if err := manager.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
}

func TestFTSManagerVacuum(t *testing.T) {
	db, cleanup := setupTestFTSDB(t)
	defer cleanup()

	manager := NewFTSManager(db, DefaultFTSConfig())

	// Initialize schema first
	if err := manager.InitSchema(); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	ctx := context.Background()

	// Vacuum (optimize) should not error
	if err := manager.Vacuum(ctx); err != nil {
		t.Fatalf("vacuum failed: %v", err)
	}
}

func TestEscapeFTS5Query(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{input: "simple", expected: "simple"},
		{input: `with"quotes`, expected: `with""quotes`},
		{input: "star*", expected: `star\*`},
		{input: "(parens)", expected: `\(parens\)`},
		{input: `"quoted*"`, expected: `""quoted\*""`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := escapeFTS5Query(tt.input)
			if result != tt.expected {
				t.Errorf("escapeFTS5Query(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestDefaultFTSConfig(t *testing.T) {
	cfg := DefaultFTSConfig()

	if cfg.TriggerThreshold != 1000 {
		t.Errorf("expected TriggerThreshold=1000, got %d", cfg.TriggerThreshold)
	}
	if !cfg.Enabled {
		t.Error("expected Enabled=true")
	}
	if !cfg.RebuildOnFullSync {
		t.Error("expected RebuildOnFullSync=true")
	}
}
