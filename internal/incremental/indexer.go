package incremental

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/project"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

// Error types for graceful degradation
var (
	// ErrIncrementalNotSupported indicates the language doesn't support incremental indexing
	ErrIncrementalNotSupported = errors.New("incremental indexing not supported for this language")

	// ErrIndexerNotInstalled indicates the required indexer is not installed
	ErrIndexerNotInstalled = errors.New("indexer not installed")
)

// CurrentSchemaVersion should match storage.currentSchemaVersion
const CurrentSchemaVersion = 9

// IncrementalIndexer orchestrates incremental index updates
type IncrementalIndexer struct {
	repoRoot  string
	db        *storage.DB
	detector  *ChangeDetector
	extractor *SCIPExtractor
	updater   *IndexUpdater
	store     *Store
	config    *Config
	logger    *slog.Logger
}

// NewIncrementalIndexer creates a new incremental indexer
func NewIncrementalIndexer(repoRoot string, db *storage.DB, config *Config, logger *slog.Logger) *IncrementalIndexer {
	if config == nil {
		config = DefaultConfig()
	}

	// Ensure IndexPath has a sensible default
	indexPath := config.IndexPath
	if indexPath == "" {
		indexPath = "index.scip"
	}

	store := NewStore(db, logger)
	return &IncrementalIndexer{
		repoRoot:  repoRoot,
		db:        db,
		detector:  NewChangeDetector(repoRoot, store, config, logger),
		extractor: NewSCIPExtractor(repoRoot, indexPath, logger),
		updater:   NewIndexUpdater(db, store, logger),
		store:     store,
		config:    config,
		logger:    logger,
	}
}

// IndexIncremental performs an incremental index update for Go projects.
// Deprecated: Use IndexIncrementalWithLang for multi-language support.
func (i *IncrementalIndexer) IndexIncremental(ctx context.Context, since string) (*DeltaStats, error) {
	return i.IndexIncrementalWithLang(ctx, since, project.LangGo)
}

// IndexIncrementalWithLang performs an incremental index update for the specified language.
// Returns stats on success, or specific errors for graceful degradation:
//   - ErrIncrementalNotSupported: language doesn't support incremental indexing
//   - ErrIndexerNotInstalled: required indexer is not installed
func (i *IncrementalIndexer) IndexIncrementalWithLang(ctx context.Context, since string, lang project.Language) (*DeltaStats, error) {
	start := time.Now()

	// Check if language supports incremental indexing
	config := project.GetIndexerConfig(lang)
	if config == nil {
		return nil, fmt.Errorf("%w: %s", ErrIncrementalNotSupported, lang)
	}
	if !config.SupportsIncremental {
		return nil, fmt.Errorf("%w: %s (not enabled)", ErrIncrementalNotSupported, lang)
	}

	// Check if indexer is installed
	if !i.extractor.IsIndexerInstalled(config) {
		installInfo := project.GetIndexerInfo(lang)
		installCmd := ""
		if installInfo != nil {
			installCmd = installInfo.InstallCommand
		}
		i.logger.Warn("Indexer not installed for incremental mode",
			"language", string(lang),
			"indexer", config.Cmd,
			"install", installCmd,
		)
		return nil, fmt.Errorf("%w: %s (install: %s)", ErrIndexerNotInstalled, config.Cmd, installCmd)
	}

	// 1. Detect changes
	changes, err := i.detector.DetectChanges(since)
	if err != nil {
		return nil, fmt.Errorf("change detection failed: %w", err)
	}

	if len(changes) == 0 {
		stats := &DeltaStats{
			Duration:   time.Since(start),
			IndexState: "unchanged",
		}
		return stats, nil
	}

	i.logger.Info("Detected file changes", "changeCount", len(changes), "language", string(lang))

	// 2. Check if we should do full reindex instead
	totalFiles := i.store.GetTotalFileCount()
	if totalFiles > 0 && i.config.IncrementalThreshold > 0 {
		changePercent := (len(changes) * 100) / totalFiles
		if changePercent > i.config.IncrementalThreshold {
			return nil, fmt.Errorf("too many changes (%d%% of files), use --force for full reindex", changePercent)
		}
	}

	// 3. Run indexer to regenerate index
	i.logger.Info("Running indexer", "indexer", config.Cmd, "language", string(lang))
	if runErr := i.extractor.RunIndexer(config); runErr != nil {
		return nil, fmt.Errorf("%s indexer failed: %w", config.Cmd, runErr)
	}

	// 4. Extract deltas from SCIP
	delta, err := i.extractor.ExtractDeltas(changes)
	if err != nil {
		return nil, fmt.Errorf("failed to extract deltas: %w", err)
	}

	// 5. Apply changes to database
	if err := i.updater.ApplyDelta(delta); err != nil {
		return nil, fmt.Errorf("failed to apply delta: %w", err)
	}

	// 6. Update index state
	commit := i.detector.GetCurrentCommit()
	if err := i.updater.UpdateIndexState(len(delta.FileDeltas), commit); err != nil {
		return nil, fmt.Errorf("failed to update index state: %w", err)
	}

	delta.Stats.Duration = time.Since(start)
	delta.Stats.IndexState = "partial"

	i.logger.Info("Incremental index complete",
		"filesChanged", delta.Stats.FilesChanged,
		"filesAdded", delta.Stats.FilesAdded,
		"filesDeleted", delta.Stats.FilesDeleted,
		"symbolsAdded", delta.Stats.SymbolsAdded,
		"symbolsRemoved", delta.Stats.SymbolsRemoved,
		"duration", delta.Stats.Duration.String(),
		"language", string(lang),
	)

	return &delta.Stats, nil
}

// CanUseIncremental checks if incremental indexing is available for a language.
// Returns (canUse, reason) where reason explains why if canUse is false.
func (i *IncrementalIndexer) CanUseIncremental(lang project.Language) (bool, string) {
	config := project.GetIndexerConfig(lang)
	if config == nil {
		return false, fmt.Sprintf("no indexer configured for %s", lang)
	}
	if !config.SupportsIncremental {
		return false, fmt.Sprintf("incremental not enabled for %s", lang)
	}
	if !i.extractor.IsIndexerInstalled(config) {
		installInfo := project.GetIndexerInfo(lang)
		if installInfo != nil {
			return false, fmt.Sprintf("%s not installed (run: %s)", config.Cmd, installInfo.InstallCommand)
		}
		return false, fmt.Sprintf("%s not installed", config.Cmd)
	}
	return true, ""
}

// NeedsFullReindex checks if we need a full reindex
func (i *IncrementalIndexer) NeedsFullReindex() (bool, string) {
	// No previous index
	if !i.store.HasIndex() {
		return true, "no previous index"
	}

	// The tracking tables can be intact while the index file itself is gone
	// (deleted by hand, lost with a checkout). Incremental updates patch an
	// existing file; without one there is nothing to patch, and reporting
	// "up to date" would leave the repo with metadata but no index.
	if _, err := os.Stat(i.indexFilePath()); err != nil {
		return true, "index file missing"
	}

	// Schema version mismatch
	schemaVersion := i.store.GetSchemaVersion()
	if schemaVersion != 0 && schemaVersion != CurrentSchemaVersion {
		return true, fmt.Sprintf("schema version mismatch (have %d, need %d)",
			schemaVersion, CurrentSchemaVersion)
	}

	// No tracked commit (can't compute diff)
	if i.store.GetLastIndexedCommit() == "" && i.detector.isGitRepo() {
		return true, "no tracked commit"
	}

	return false, ""
}

// indexFilePath is the absolute path of the SCIP index this indexer updates.
func (i *IncrementalIndexer) indexFilePath() string {
	p := i.config.IndexPath
	if p == "" {
		p = "index.scip"
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(i.repoRoot, p)
	}
	return p
}

// GetIndexState returns current index state for UI display
func (i *IncrementalIndexer) GetIndexState() IndexState {
	state := i.store.GetIndexState()

	// Check if dirty (uncommitted changes)
	if i.detector.isGitRepo() {
		state.IsDirty = i.detector.HasDirtyWorkingTree()
	}

	// Compute effective state
	// partial_dirty: index is partial AND there are uncommitted changes
	// This helps UX: "run ckb index to incorporate working tree changes"
	baseState := state.State
	if state.IsDirty && baseState == "partial" {
		state.State = "partial_dirty"
	} else if state.IsDirty && baseState == "full" {
		state.State = "full_dirty"
	}

	return state
}

// scipStreamingThresholdBytes is the index file size above which
// PopulateFromFullIndexStreaming is used instead of PopulateFromFullIndex.
// Below this size the single-pass path is faster (no double proto-unmarshal
// overhead). Above it the GC pressure from holding the full index in memory
// dominates, and streaming's lower peak heap wins decisively.
//
// Measured crossover (Apple M4 Pro, synthetic benchmark):
//
//	small  1k docs  /  4 MB  → old path faster (+14%)
//	medium 10k docs / 80 MB  → old path faster (+20%)
//	large  50k docs / 738MB  → streaming faster (-4% warm, -83% cold GC)
const scipStreamingThresholdBytes = 200 << 20 // 200 MB

// PopulateAfterFullIndex populates tracking tables after a full reindex
// This enables subsequent incremental updates
func (i *IncrementalIndexer) PopulateAfterFullIndex() error {
	// Choose populate strategy based on index file size.
	// Streaming avoids materialising the full SCIPIndex in RAM, which eliminates
	// GC pressure on large repos. For small indexes the double-pass overhead
	// outweighs the benefit, so we keep the faster single-pass path there.
	populate := i.updater.PopulateFromFullIndex
	if fi, err := os.Stat(i.extractor.indexPath); err == nil && fi.Size() > scipStreamingThresholdBytes {
		populate = i.updater.PopulateFromFullIndexStreaming
	}
	if err := populate(i.extractor); err != nil {
		return fmt.Errorf("failed to populate from full index: %w", err)
	}

	// Mark as full index
	commit := i.detector.GetCurrentCommit()
	if err := i.updater.SetFullIndexComplete(commit); err != nil {
		return fmt.Errorf("failed to set full index complete: %w", err)
	}

	return nil
}

// GetStore returns the underlying store for external access
func (i *IncrementalIndexer) GetStore() *Store {
	return i.store
}

// GetDetector returns the change detector for external access
func (i *IncrementalIndexer) GetDetector() *ChangeDetector {
	return i.detector
}

// FormatStats formats the delta stats for display
func FormatStats(stats *DeltaStats, state IndexState) string {
	if stats.IndexState == "unchanged" {
		return "Index is up to date. Nothing to do."
	}

	commitInfo := state.Commit
	if len(commitInfo) > 7 {
		commitInfo = commitInfo[:7]
	}
	if state.IsDirty {
		commitInfo += " (+dirty)"
	}

	// Determine accuracy based on state
	reverseAccuracy := "may be stale"
	callersAccuracy := "may be stale"
	if state.State == "full" && state.PendingRescans == 0 {
		reverseAccuracy = "accurate"
		callersAccuracy = "accurate"
	}

	result := fmt.Sprintf(`
Incremental Index Complete
--------------------------
Files:   %d modified, %d added, %d deleted
Symbols: %d added, %d removed
Refs:    %d updated
Calls:   %d edges updated
Time:    %v
Commit:  %s
`,
		stats.FilesChanged, stats.FilesAdded, stats.FilesDeleted,
		stats.SymbolsAdded, stats.SymbolsRemoved,
		stats.RefsAdded,
		stats.CallsAdded,
		stats.Duration.Round(time.Millisecond),
		commitInfo,
	)

	// Add pending rescans info if any
	if state.PendingRescans > 0 {
		result += fmt.Sprintf("Pending: %d files queued for rescan\n", state.PendingRescans)
	}

	result += fmt.Sprintf(`
Accuracy:
  OK  Go to definition     - accurate
  OK  Find refs (forward)  - accurate
  %s  Find refs (reverse)  - %s
  OK  Callees (outgoing)   - accurate
  %s  Callers (incoming)   - %s

Run 'ckb index --force' for full accuracy (%d files since last full)
`,
		formatAccuracyMarker(reverseAccuracy), reverseAccuracy,
		formatAccuracyMarker(callersAccuracy), callersAccuracy,
		state.FilesSinceFull,
	)

	return result
}

// formatAccuracyMarker returns OK or !! based on accuracy string
func formatAccuracyMarker(accuracy string) string {
	if accuracy == "accurate" {
		return "OK"
	}
	return "!!"
}
