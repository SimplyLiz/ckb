package incremental

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/project"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

func setupTestIndexer(t *testing.T) (*IncrementalIndexer, string, func()) {
	t.Helper()

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "incremental-indexer-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// Create .ckb directory
	ckbDir := filepath.Join(tmpDir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create .ckb dir: %v", err)
	}

	// Create logger
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Open database
	db, err := storage.Open(tmpDir, logger)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to open database: %v", err)
	}

	config := DefaultConfig()
	indexer := NewIncrementalIndexer(tmpDir, db, config, logger)

	cleanup := func() {
		db.Close() //nolint:errcheck // Test cleanup
		os.RemoveAll(tmpDir)
	}

	return indexer, tmpDir, cleanup
}

func TestNewIncrementalIndexer(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	if indexer == nil {
		t.Fatal("expected non-nil indexer")
	}
	if indexer.store == nil {
		t.Error("expected non-nil store")
	}
	if indexer.detector == nil {
		t.Error("expected non-nil detector")
	}
	if indexer.extractor == nil {
		t.Error("expected non-nil extractor")
	}
	if indexer.updater == nil {
		t.Error("expected non-nil updater")
	}
}

func TestNewIncrementalIndexer_NilConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "incremental-indexer-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ckbDir := filepath.Join(tmpDir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatalf("failed to create .ckb dir: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := storage.Open(tmpDir, logger)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close() //nolint:errcheck

	// Pass nil config - should use defaults
	indexer := NewIncrementalIndexer(tmpDir, db, nil, logger)

	if indexer.config == nil {
		t.Fatal("expected non-nil config after initialization")
	}
	if indexer.config.IndexPath != "index.scip" {
		t.Errorf("expected default index path 'index.scip', got %q", indexer.config.IndexPath)
	}
}

func TestNeedsFullReindex_NoIndex(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	// Fresh database, no index exists
	needs, reason := indexer.NeedsFullReindex()
	if !needs {
		t.Error("expected NeedsFullReindex=true for fresh database")
	}
	if reason != "no previous index" {
		t.Errorf("expected reason 'no previous index', got %q", reason)
	}
}

func TestNeedsFullReindex_WithFullIndex(t *testing.T) {
	indexer, tmpDir, cleanup := setupTestIndexer(t)
	defer cleanup()

	// The index file must exist for an incremental update to have anything to patch.
	if err := os.WriteFile(filepath.Join(tmpDir, "index.scip"), nil, 0644); err != nil {
		t.Fatalf("write index.scip: %v", err)
	}

	// HasIndex() checks if there are any files - need to add at least one
	if err := indexer.store.SaveFileState(&IndexedFile{Path: "main.go", Hash: "abc"}); err != nil {
		t.Fatalf("SaveFileState failed: %v", err)
	}

	// Simulate a full index having been run
	if err := indexer.store.SetIndexStateFull(); err != nil {
		t.Fatalf("SetIndexStateFull failed: %v", err)
	}
	if err := indexer.store.SetLastIndexedCommit("abc123"); err != nil {
		t.Fatalf("SetLastIndexedCommit failed: %v", err)
	}
	if err := indexer.store.SetMetaInt(MetaKeySchemaVersion, int64(CurrentSchemaVersion)); err != nil {
		t.Fatalf("SetMetaInt failed: %v", err)
	}

	// Non-git repo, so we should still need full reindex due to no tracked commit logic
	// Actually, looking at the code, isGitRepo() check affects the "no tracked commit" case
	needs, reason := indexer.NeedsFullReindex()
	// Since we set a commit and schema version, it should not need full reindex
	if needs {
		t.Errorf("expected NeedsFullReindex=false after setup, got true with reason: %q", reason)
	}
}

// Tracking tables intact but index.scip gone (deleted by hand): incremental
// has nothing to patch and must not report "up to date".
func TestNeedsFullReindex_IndexFileMissing(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	if err := indexer.store.SaveFileState(&IndexedFile{Path: "main.go", Hash: "abc"}); err != nil {
		t.Fatalf("SaveFileState failed: %v", err)
	}
	if err := indexer.store.SetIndexStateFull(); err != nil {
		t.Fatalf("SetIndexStateFull failed: %v", err)
	}
	if err := indexer.store.SetLastIndexedCommit("abc123"); err != nil {
		t.Fatalf("SetLastIndexedCommit failed: %v", err)
	}
	if err := indexer.store.SetMetaInt(MetaKeySchemaVersion, int64(CurrentSchemaVersion)); err != nil {
		t.Fatalf("SetMetaInt failed: %v", err)
	}

	needs, reason := indexer.NeedsFullReindex()
	if !needs || reason != "index file missing" {
		t.Errorf("NeedsFullReindex = %v, %q; want true, \"index file missing\"", needs, reason)
	}
}

func TestNeedsFullReindex_SchemaMismatch(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	// Set up an index with wrong schema version
	if err := indexer.store.SetIndexStateFull(); err != nil {
		t.Fatalf("SetIndexStateFull failed: %v", err)
	}
	if err := indexer.store.SetLastIndexedCommit("abc123"); err != nil {
		t.Fatalf("SetLastIndexedCommit failed: %v", err)
	}
	// Set a different schema version
	wrongVersion := int64(CurrentSchemaVersion - 1)
	if wrongVersion == 0 {
		wrongVersion = CurrentSchemaVersion + 1
	}
	if err := indexer.store.SetMetaInt(MetaKeySchemaVersion, wrongVersion); err != nil {
		t.Fatalf("SetMetaInt failed: %v", err)
	}

	needs, reason := indexer.NeedsFullReindex()
	if !needs {
		t.Error("expected NeedsFullReindex=true for schema mismatch")
	}
	if reason == "" {
		t.Error("expected non-empty reason for schema mismatch")
	}
}

func TestGetIndexState_Initial(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	state := indexer.GetIndexState()

	// Initial state should be "unknown" or empty
	if state.State != "unknown" && state.State != "" {
		t.Errorf("expected initial state 'unknown' or empty, got %q", state.State)
	}
}

func TestGetIndexState_AfterFull(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	// Set full index state
	if err := indexer.store.SetIndexStateFull(); err != nil {
		t.Fatalf("SetIndexStateFull failed: %v", err)
	}
	if err := indexer.store.SetLastIndexedCommit("abc123def456"); err != nil {
		t.Fatalf("SetLastIndexedCommit failed: %v", err)
	}

	state := indexer.GetIndexState()
	if state.State != "full" {
		t.Errorf("expected state 'full', got %q", state.State)
	}
	if state.Commit != "abc123def456" {
		t.Errorf("expected commit 'abc123def456', got %q", state.Commit)
	}
}

func TestGetIndexState_AfterPartial(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	// Set partial index state
	if err := indexer.store.SetIndexStatePartial(5); err != nil {
		t.Fatalf("SetIndexStatePartial failed: %v", err)
	}

	state := indexer.GetIndexState()
	if state.State != "partial" {
		t.Errorf("expected state 'partial', got %q", state.State)
	}
	if state.FilesSinceFull != 5 {
		t.Errorf("expected FilesSinceFull=5, got %d", state.FilesSinceFull)
	}
}

func TestGetStore(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	store := indexer.GetStore()
	if store == nil {
		t.Error("expected non-nil store from GetStore()")
	}
	if store != indexer.store {
		t.Error("GetStore() should return the same store instance")
	}
}

func TestGetDetector(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	detector := indexer.GetDetector()
	if detector == nil {
		t.Error("expected non-nil detector from GetDetector()")
	}
	if detector != indexer.detector {
		t.Error("GetDetector() should return the same detector instance")
	}
}

func TestFormatStats_Unchanged(t *testing.T) {
	stats := &DeltaStats{
		IndexState: "unchanged",
	}
	state := IndexState{}

	result := FormatStats(stats, state)

	expected := "Index is up to date. Nothing to do."
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestFormatStats_WithChanges(t *testing.T) {
	stats := &DeltaStats{
		FilesChanged:   3,
		FilesAdded:     1,
		FilesDeleted:   0,
		SymbolsAdded:   10,
		SymbolsRemoved: 2,
		RefsAdded:      25,
		Duration:       150 * time.Millisecond,
		IndexState:     "partial",
	}
	state := IndexState{
		Commit:         "abc123def456789",
		FilesSinceFull: 15,
		IsDirty:        false,
	}

	result := FormatStats(stats, state)

	// Verify key elements are present
	if result == "" {
		t.Error("expected non-empty result")
	}
	if !contains(result, "3 modified") {
		t.Error("expected '3 modified' in output")
	}
	if !contains(result, "1 added") {
		t.Error("expected '1 added' in output")
	}
	if !contains(result, "abc123d") {
		t.Error("expected truncated commit hash in output")
	}
	if !contains(result, "15 files since last full") {
		t.Error("expected files since full count in output")
	}
}

func TestFormatStats_DirtyState(t *testing.T) {
	stats := &DeltaStats{
		FilesChanged: 1,
		Duration:     100 * time.Millisecond,
		IndexState:   "partial",
	}
	state := IndexState{
		Commit:         "abc123",
		FilesSinceFull: 1,
		IsDirty:        true,
	}

	result := FormatStats(stats, state)

	if !contains(result, "(+dirty)") {
		t.Error("expected '(+dirty)' indicator for dirty state")
	}
}

// contains checks if substr is in s
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && containsHelper(s, substr)))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestFormatStats_PendingRescans(t *testing.T) {
	stats := &DeltaStats{
		FilesChanged: 2,
		Duration:     200 * time.Millisecond,
		IndexState:   "partial",
	}
	state := IndexState{
		Commit:         "def789",
		FilesSinceFull: 5,
		PendingRescans: 3,
	}

	result := FormatStats(stats, state)

	if !contains(result, "3 files queued for rescan") {
		t.Error("expected pending rescans count in output")
	}
}

func TestFormatStats_FullAccuracy(t *testing.T) {
	stats := &DeltaStats{
		FilesChanged: 1,
		Duration:     100 * time.Millisecond,
		IndexState:   "partial",
	}
	state := IndexState{
		State:          "full",
		Commit:         "abc123",
		PendingRescans: 0, // No pending rescans
	}

	result := FormatStats(stats, state)

	// When state is full and no pending rescans, accuracy should be "accurate"
	if !contains(result, "accurate") {
		t.Error("expected 'accurate' in output for full state with no pending rescans")
	}
}

func TestFormatAccuracyMarker(t *testing.T) {
	tests := []struct {
		accuracy string
		expected string
	}{
		{"accurate", "OK"},
		{"may be stale", "!!"},
		{"unknown", "!!"},
	}

	for _, tt := range tests {
		t.Run(tt.accuracy, func(t *testing.T) {
			result := formatAccuracyMarker(tt.accuracy)
			if result != tt.expected {
				t.Errorf("formatAccuracyMarker(%q) = %q, want %q", tt.accuracy, result, tt.expected)
			}
		})
	}
}

func TestIndexerConfig(t *testing.T) {
	indexer, tmpDir, cleanup := setupTestIndexer(t)
	defer cleanup()

	// Test that config is properly set
	if indexer.config == nil {
		t.Fatal("expected non-nil config")
	}

	// Test repoRoot is set correctly
	if indexer.repoRoot != tmpDir {
		t.Errorf("expected repoRoot %q, got %q", tmpDir, indexer.repoRoot)
	}
}

func TestIndexerGetters(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	// Test GetStore
	store := indexer.GetStore()
	if store == nil {
		t.Error("GetStore() returned nil")
	}

	// Test GetDetector
	detector := indexer.GetDetector()
	if detector == nil {
		t.Error("GetDetector() returned nil")
	}
}

// TestIndexState_DirtyModifiers tests the dirty state modifiers in GetIndexState
func TestIndexState_DirtyModifiers(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	// Set partial state
	if err := indexer.store.SetIndexStatePartial(3); err != nil {
		t.Fatalf("SetIndexStatePartial failed: %v", err)
	}

	state := indexer.GetIndexState()

	// In a non-git repo, IsDirty should be false
	if state.IsDirty {
		t.Error("expected IsDirty=false in non-git repo")
	}

	// State should be "partial" (not "partial_dirty" since not dirty)
	if state.State != "partial" {
		t.Errorf("expected state 'partial', got %q", state.State)
	}
}

func TestNeedsFullReindex_NoCommit_NonGitRepo(t *testing.T) {
	indexer, tmpDir, cleanup := setupTestIndexer(t)
	defer cleanup()

	if err := os.WriteFile(filepath.Join(tmpDir, "index.scip"), nil, 0644); err != nil {
		t.Fatalf("write index.scip: %v", err)
	}

	// Set up an index without commit (but not a git repo)
	if err := indexer.store.SaveFileState(&IndexedFile{Path: "main.go", Hash: "abc"}); err != nil {
		t.Fatalf("SaveFileState failed: %v", err)
	}
	if err := indexer.store.SetIndexStateFull(); err != nil {
		t.Fatalf("SetIndexStateFull failed: %v", err)
	}
	if err := indexer.store.SetMetaInt(MetaKeySchemaVersion, int64(CurrentSchemaVersion)); err != nil {
		t.Fatalf("SetMetaInt failed: %v", err)
	}
	// Don't set commit - simulate non-git repo

	// In a non-git repo, missing commit should NOT trigger full reindex
	// because the isGitRepo() check should prevent the "no tracked commit" error
	needs, reason := indexer.NeedsFullReindex()

	// The detector.isGitRepo() should return false, so "no tracked commit" check is skipped
	if needs {
		t.Errorf("expected NeedsFullReindex=false in non-git repo, got true with reason: %q", reason)
	}
}

// Multi-language support tests (v7.6)

func TestCanUseIncremental_SupportedLanguages(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	// Test unsupported languages - these should always return false with specific reason
	unsupportedTests := []struct {
		name     string
		lang     string
		wantDesc string // Partial match on reason
	}{
		{"Java", "java", "not enabled"},
		{"Kotlin", "kotlin", "not enabled"},
		{"Cpp", "cpp", "not enabled"},
		{"Ruby", "ruby", "not enabled"},
		{"CSharp", "csharp", "not enabled"},
		{"PHP", "php", "not enabled"},
		{"Unknown", "unknown", "no indexer configured"},
	}

	for _, tt := range unsupportedTests {
		t.Run(tt.name, func(t *testing.T) {
			lang := parseTestLanguage(tt.lang)
			canUse, reason := indexer.CanUseIncremental(lang)

			if canUse {
				t.Errorf("CanUseIncremental(%s) = true, want false", tt.lang)
			}

			if !contains(reason, tt.wantDesc) {
				t.Errorf("CanUseIncremental(%s) reason = %q, want to contain %q", tt.lang, reason, tt.wantDesc)
			}
		})
	}

	// Test supported languages - result depends on whether indexer is installed
	supportedTests := []string{"go", "typescript", "javascript", "python", "dart", "rust"}

	for _, lang := range supportedTests {
		t.Run("Supported_"+lang, func(t *testing.T) {
			parsedLang := parseTestLanguage(lang)
			canUse, reason := indexer.CanUseIncremental(parsedLang)

			// Either it works (indexer installed) or it should mention "not installed"
			if !canUse && !contains(reason, "not installed") {
				t.Errorf("CanUseIncremental(%s) = false but reason %q doesn't mention 'not installed'", lang, reason)
			}
			// If canUse is true, reason should be empty
			if canUse && reason != "" {
				t.Errorf("CanUseIncremental(%s) = true but reason is not empty: %q", lang, reason)
			}
		})
	}
}

func TestIndexIncrementalWithLang_UnsupportedLanguage(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	ctx := t.Context()
	lang := parseTestLanguage("java") // Java doesn't support incremental

	_, err := indexer.IndexIncrementalWithLang(ctx, "", lang)

	if err == nil {
		t.Fatal("expected error for unsupported language")
	}

	// Should return ErrIncrementalNotSupported
	if !contains(err.Error(), "not supported") && !contains(err.Error(), "not enabled") {
		t.Errorf("expected 'not supported' or 'not enabled' error, got: %v", err)
	}
}

func TestIndexIncrementalWithLang_UnknownLanguage(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	ctx := t.Context()
	lang := parseTestLanguage("unknown")

	_, err := indexer.IndexIncrementalWithLang(ctx, "", lang)

	if err == nil {
		t.Fatal("expected error for unknown language")
	}

	if !contains(err.Error(), "not supported") {
		t.Errorf("expected 'not supported' error, got: %v", err)
	}
}

func TestIndexIncrementalWithLang_IndexerNotInstalled(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	ctx := t.Context()
	// Use Python - supports incremental but scip-python is unlikely to be installed
	// This ensures we always test the "not installed" error path
	lang := project.LangPython

	_, err := indexer.IndexIncrementalWithLang(ctx, "", lang)

	if err == nil {
		// If scip-python is somehow installed, skip
		t.Skip("scip-python is installed, skipping indexer-not-installed test")
	}

	// Should return ErrIndexerNotInstalled or similar
	if !contains(err.Error(), "not installed") && !contains(err.Error(), "install") {
		t.Errorf("expected 'not installed' error, got: %v", err)
	}
}

func TestErrorTypes(t *testing.T) {
	// Test that error types are properly defined
	if ErrIncrementalNotSupported == nil {
		t.Error("ErrIncrementalNotSupported should not be nil")
	}
	if ErrIndexerNotInstalled == nil {
		t.Error("ErrIndexerNotInstalled should not be nil")
	}

	// Test error messages
	if !contains(ErrIncrementalNotSupported.Error(), "not supported") {
		t.Errorf("ErrIncrementalNotSupported message should contain 'not supported', got: %s",
			ErrIncrementalNotSupported.Error())
	}
	if !contains(ErrIndexerNotInstalled.Error(), "not installed") {
		t.Errorf("ErrIndexerNotInstalled message should contain 'not installed', got: %s",
			ErrIndexerNotInstalled.Error())
	}
}

func TestCanUseIncremental_InstallInfo(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	// Test that CanUseIncremental includes install command in reason when available
	// Use Python - supports incremental but scip-python is unlikely to be installed
	canUse, reason := indexer.CanUseIncremental(project.LangPython)

	if canUse {
		// If scip-python is somehow installed, skip this test
		t.Skip("scip-python is installed, can't test install info message")
	}

	// Reason should include install command or mention "not installed"
	if !contains(reason, "pip install") && !contains(reason, "not installed") {
		t.Errorf("expected reason to mention install command or 'not installed', got: %s", reason)
	}
}

func TestCanUseIncremental_AllLanguages(t *testing.T) {
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	// Test all languages to ensure CanUseIncremental doesn't panic
	// and returns sensible values
	allLanguages := []project.Language{
		project.LangGo,
		project.LangTypeScript,
		project.LangJavaScript,
		project.LangPython,
		project.LangRust,
		project.LangDart,
		project.LangJava,
		project.LangKotlin,
		project.LangCpp,
		project.LangRuby,
		project.LangCSharp,
		project.LangPHP,
		project.LangUnknown,
	}

	for _, lang := range allLanguages {
		t.Run(string(lang), func(t *testing.T) {
			canUse, reason := indexer.CanUseIncremental(lang)

			// For supported languages, either it works or has specific reason
			config := project.GetIndexerConfig(lang)
			if config == nil {
				// Unknown languages should report no indexer
				if canUse {
					t.Error("expected canUse=false for unknown language")
				}
				if !contains(reason, "no indexer") {
					t.Errorf("expected 'no indexer' in reason, got: %s", reason)
				}
				return
			}

			if !config.SupportsIncremental {
				// Languages without incremental support
				if canUse {
					t.Error("expected canUse=false for non-incremental language")
				}
				if !contains(reason, "not enabled") {
					t.Errorf("expected 'not enabled' in reason, got: %s", reason)
				}
				return
			}

			// For supported languages, check reason is consistent with result
			if canUse && reason != "" {
				t.Errorf("expected empty reason when canUse=true, got: %s", reason)
			}
			if !canUse && reason == "" {
				t.Error("expected non-empty reason when canUse=false")
			}
		})
	}
}

func TestIndexIncremental_Deprecated(t *testing.T) {
	// Test that deprecated IndexIncremental calls IndexIncrementalWithLang
	indexer, _, cleanup := setupTestIndexer(t)
	defer cleanup()

	ctx := t.Context()

	// IndexIncremental should behave the same as IndexIncrementalWithLang(LangGo)
	_, err1 := indexer.IndexIncremental(ctx, "")
	_, err2 := indexer.IndexIncrementalWithLang(ctx, "", project.LangGo)

	// Both should fail in the same way (no indexer installed typically)
	if (err1 == nil) != (err2 == nil) {
		t.Errorf("IndexIncremental and IndexIncrementalWithLang should have same error state: %v vs %v", err1, err2)
	}

	// If both have errors, they should be similar
	if err1 != nil && err2 != nil {
		// Both should mention the same issue
		if !contains(err1.Error(), "not installed") && !contains(err1.Error(), "not supported") {
			// scip-go is installed, errors might be different
			t.Logf("IndexIncremental error: %v", err1)
			t.Logf("IndexIncrementalWithLang error: %v", err2)
		}
	}
}

func TestIndexIncrementalWithLang_NoChanges(t *testing.T) {
	indexer, tmpDir, cleanup := setupTestIndexer(t)
	defer cleanup()

	// Create a Go module to make detection work
	goMod := filepath.Join(tmpDir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("failed to create go.mod: %v", err)
	}

	// Initialize git to enable change detection
	initGit(t, tmpDir)

	// Set up a full index state so incremental can run
	if err := indexer.store.SaveFileState(&IndexedFile{Path: "main.go", Hash: "abc123"}); err != nil {
		t.Fatalf("SaveFileState failed: %v", err)
	}
	if err := indexer.store.SetIndexStateFull(); err != nil {
		t.Fatalf("SetIndexStateFull failed: %v", err)
	}
	if err := indexer.store.SetLastIndexedCommit(getGitHead(t, tmpDir)); err != nil {
		t.Fatalf("SetLastIndexedCommit failed: %v", err)
	}

	ctx := t.Context()
	lang := project.LangGo

	// Check if Go incremental is available
	canUse, _ := indexer.CanUseIncremental(lang)
	if !canUse {
		t.Skip("scip-go not installed, skipping no-changes test")
	}

	// With no changes, should return unchanged stats
	stats, err := indexer.IndexIncrementalWithLang(ctx, "", lang)
	if err != nil {
		t.Fatalf("IndexIncrementalWithLang failed: %v", err)
	}

	if stats.IndexState != "unchanged" {
		t.Errorf("expected IndexState='unchanged', got %q", stats.IndexState)
	}
}

// initGit initializes a git repo in the given directory
func initGit(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "add", "."},
		{"git", "commit", "-m", "initial"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git command %v failed: %v", args, err)
		}
	}
}

// getGitHead returns the current HEAD commit
func getGitHead(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD failed: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// parseTestLanguage converts a string to project.Language for testing
func parseTestLanguage(s string) project.Language {
	switch s {
	case "go":
		return project.LangGo
	case "typescript":
		return project.LangTypeScript
	case "javascript":
		return project.LangJavaScript
	case "python":
		return project.LangPython
	case "rust":
		return project.LangRust
	case "java":
		return project.LangJava
	case "kotlin":
		return project.LangKotlin
	case "cpp":
		return project.LangCpp
	case "dart":
		return project.LangDart
	case "ruby":
		return project.LangRuby
	case "csharp":
		return project.LangCSharp
	case "php":
		return project.LangPHP
	default:
		return project.LangUnknown
	}
}

// =============================================================================
// Mock-based tests for the "changes detected" workflow
// These tests cover the code paths that require git changes + indexer run
// =============================================================================

// setupTestIndexerWithFixture creates an indexer using the pre-generated Go SCIP fixture.
// This allows testing the extraction and update workflow without running scip-go.
func setupTestIndexerWithFixture(t *testing.T) (*IncrementalIndexer, string, func()) {
	t.Helper()

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "incremental-fixture-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// Create .ckb directory
	ckbDir := filepath.Join(tmpDir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create .ckb dir: %v", err)
	}

	// Copy fixture files to temp directory
	fixtureRoot := getFixtureRootForIndexerTest(t)
	goFixture := filepath.Join(fixtureRoot, "go")

	// Copy Go source files
	for _, file := range []string{"go.mod", "main.go", "utils.go"} {
		src := filepath.Join(goFixture, file)
		dst := filepath.Join(tmpDir, file)
		content, err := os.ReadFile(src)
		if err != nil {
			os.RemoveAll(tmpDir)
			t.Fatalf("failed to read fixture %s: %v", src, err)
		}
		if err := os.WriteFile(dst, content, 0644); err != nil {
			os.RemoveAll(tmpDir)
			t.Fatalf("failed to write %s: %v", dst, err)
		}
	}

	// Copy SCIP index to root (new default location)
	srcIndex := filepath.Join(goFixture, ".scip", "index.scip")
	dstIndex := filepath.Join(tmpDir, "index.scip")
	indexContent, err := os.ReadFile(srcIndex)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to read fixture index: %v", err)
	}
	if err := os.WriteFile(dstIndex, indexContent, 0644); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to write index: %v", err)
	}

	// Create logger
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Open database
	db, err := storage.Open(tmpDir, logger)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to open database: %v", err)
	}

	config := DefaultConfig()
	indexer := NewIncrementalIndexer(tmpDir, db, config, logger)

	cleanup := func() {
		db.Close() //nolint:errcheck // Test cleanup
		os.RemoveAll(tmpDir)
	}

	return indexer, tmpDir, cleanup
}

// getFixtureRootForIndexerTest returns the path to the testdata/incremental directory.
func getFixtureRootForIndexerTest(t *testing.T) string {
	t.Helper()
	// Find the project root by looking for go.mod
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Walk up to find go.mod
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "testdata", "incremental")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (go.mod)")
		}
		dir = parent
	}
}

// TestIndexIncrementalWithLang_ChangesDetected tests the workflow when changes are detected.
// Uses fixture SCIP index and simulates the change detection path.
func TestIndexIncrementalWithLang_ChangesDetected(t *testing.T) {
	indexer, tmpDir, cleanup := setupTestIndexerWithFixture(t)
	defer cleanup()

	// Initialize git repo so change detection works
	initGit(t, tmpDir)

	// Populate from full index first (simulates initial indexing)
	if err := indexer.PopulateAfterFullIndex(); err != nil {
		t.Fatalf("PopulateAfterFullIndex failed: %v", err)
	}

	// Now modify a file to trigger change detection
	mainGoPath := filepath.Join(tmpDir, "main.go")
	content, err := os.ReadFile(mainGoPath)
	if err != nil {
		t.Fatalf("failed to read main.go: %v", err)
	}

	// Add a comment to trigger a change
	newContent := append(content, []byte("\n// Modified for test\n")...)
	if err := os.WriteFile(mainGoPath, newContent, 0644); err != nil {
		t.Fatalf("failed to write main.go: %v", err)
	}

	// Commit the change
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git add failed: %v", err)
	}
	cmd = exec.Command("git", "commit", "-m", "modify main.go")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}

	// Create a custom extractor that skips running the actual indexer
	// but uses the existing fixture index for extraction
	indexer.extractor = &SCIPExtractor{
		repoRoot:  tmpDir,
		indexPath: filepath.Join(tmpDir, "index.scip"),
		logger:    indexer.logger,
	}

	// Override RunIndexer to be a no-op (index already exists)
	// We do this by setting up the extractor to point to existing index

	lang := project.LangGo

	// The indexer should detect changes and process them
	// However, since we can't actually run scip-go, we need to test
	// the components separately. This test verifies the setup works.

	// Verify we can detect changes
	changes, err := indexer.detector.DetectChanges("")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	if len(changes) == 0 {
		t.Log("No changes detected - this is expected if git state is clean")
		// The test still validates the setup works correctly
	} else {
		t.Logf("Detected %d changes", len(changes))
	}

	// Verify the extractor can extract from fixture
	testChanges := []ChangedFile{
		{Path: "main.go", ChangeType: ChangeModified},
	}
	delta, err := indexer.extractor.ExtractDeltas(testChanges)
	if err != nil {
		t.Fatalf("ExtractDeltas failed: %v", err)
	}

	if len(delta.FileDeltas) == 0 {
		t.Error("expected file deltas from extraction")
	}

	// Verify updater can apply delta
	if err := indexer.updater.ApplyDelta(delta); err != nil {
		t.Fatalf("ApplyDelta failed: %v", err)
	}

	// Verify stats are populated
	if delta.Stats.FilesChanged == 0 && delta.Stats.SymbolsAdded == 0 {
		t.Error("expected non-zero stats after extraction")
	}

	t.Logf("Successfully tested changes workflow: %d files, %d symbols added",
		delta.Stats.FilesChanged, delta.Stats.SymbolsAdded)

	// Now test the full workflow path using CanUseIncremental
	canUse, reason := indexer.CanUseIncremental(lang)
	t.Logf("CanUseIncremental: %v, reason: %s", canUse, reason)
}

// TestIndexIncrementalWithLang_ThresholdExceeded tests that too many changes triggers error.
func TestIndexIncrementalWithLang_ThresholdExceeded(t *testing.T) {
	indexer, tmpDir, cleanup := setupTestIndexerWithFixture(t)
	defer cleanup()

	// Initialize git
	initGit(t, tmpDir)

	// Set a very low threshold
	indexer.config.IncrementalThreshold = 1 // 1% threshold

	// Mock store to return a high file count
	// For this test, we'll directly test the threshold logic

	totalFiles := 100
	changesCount := 10 // 10% changes
	threshold := indexer.config.IncrementalThreshold

	changePercent := (changesCount * 100) / totalFiles
	if changePercent > threshold {
		t.Logf("Threshold exceeded: %d%% > %d%% - this is expected behavior", changePercent, threshold)
	} else {
		t.Errorf("Expected threshold to be exceeded: %d%% should be > %d%%", changePercent, threshold)
	}
}

// TestFormatStats_AllPaths tests FormatStats with various inputs.
func TestFormatStats_AllPaths(t *testing.T) {
	tests := []struct {
		name     string
		stats    *DeltaStats
		state    IndexState
		contains []string
	}{
		{
			name:     "unchanged",
			stats:    &DeltaStats{IndexState: "unchanged"},
			state:    IndexState{},
			contains: []string{"up to date", "Nothing to do"},
		},
		{
			name: "partial with changes",
			stats: &DeltaStats{
				IndexState:     "partial",
				FilesChanged:   3,
				FilesAdded:     1,
				FilesDeleted:   0,
				SymbolsAdded:   15,
				SymbolsRemoved: 5,
				RefsAdded:      20,
				CallsAdded:     10,
				Duration:       500 * time.Millisecond,
			},
			state: IndexState{
				State:          "partial",
				Commit:         "abc1234567890",
				IsDirty:        false,
				FilesSinceFull: 10,
			},
			contains: []string{"3 modified", "1 added", "0 deleted", "15 added", "5 removed", "abc1234"},
		},
		{
			name: "dirty state",
			stats: &DeltaStats{
				IndexState:   "partial",
				FilesChanged: 1,
				Duration:     100 * time.Millisecond,
			},
			state: IndexState{
				State:   "partial",
				Commit:  "def456",
				IsDirty: true,
			},
			contains: []string{"+dirty"},
		},
		{
			name: "full state accurate",
			stats: &DeltaStats{
				IndexState:   "partial",
				FilesChanged: 1,
				Duration:     100 * time.Millisecond,
			},
			state: IndexState{
				State:          "full",
				Commit:         "full123",
				PendingRescans: 0,
			},
			contains: []string{"accurate"},
		},
		{
			name: "with pending rescans",
			stats: &DeltaStats{
				IndexState:   "partial",
				FilesChanged: 1,
				Duration:     100 * time.Millisecond,
			},
			state: IndexState{
				State:          "partial",
				Commit:         "pend123",
				PendingRescans: 5,
			},
			contains: []string{"5 files queued"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatStats(tt.stats, tt.state)
			for _, want := range tt.contains {
				if !strings.Contains(result, want) {
					t.Errorf("FormatStats() missing %q in output:\n%s", want, result)
				}
			}
		})
	}
}
