package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/incremental"
	"github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/indexplan"
	"github.com/SimplyLiz/CodeMCP/internal/project"
	"github.com/SimplyLiz/CodeMCP/internal/repostate"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
	"github.com/SimplyLiz/CodeMCP/internal/webhooks"
)

// RefreshManager handles incremental and full reindex operations
type RefreshManager struct {
	logger         *slog.Logger
	stdLogger      interface{ Printf(string, ...interface{}) }
	webhookManager *webhooks.Manager

	// Track pending refreshes to prevent duplicates
	pending   map[string]bool
	pendingMu sync.RWMutex
}

// NewRefreshManager creates a new refresh manager
func NewRefreshManager(logger *slog.Logger, stdLogger interface{ Printf(string, ...interface{}) }, webhookMgr *webhooks.Manager) *RefreshManager {
	return &RefreshManager{
		logger:         logger,
		stdLogger:      stdLogger,
		webhookManager: webhookMgr,
		pending:        make(map[string]bool),
	}
}

// HasPendingRefresh checks if a refresh is already pending for the given repo
func (rm *RefreshManager) HasPendingRefresh(repoPath string) bool {
	rm.pendingMu.RLock()
	defer rm.pendingMu.RUnlock()
	return rm.pending[repoPath]
}

// markPending marks a refresh as pending
func (rm *RefreshManager) markPending(repoPath string) {
	rm.pendingMu.Lock()
	defer rm.pendingMu.Unlock()
	rm.pending[repoPath] = true
}

// clearPending clears the pending state
func (rm *RefreshManager) clearPending(repoPath string) {
	rm.pendingMu.Lock()
	defer rm.pendingMu.Unlock()
	delete(rm.pending, repoPath)
}

// RefreshResult contains the result of a refresh operation
type RefreshResult struct {
	RepoPath     string        `json:"repoPath"`
	Type         string        `json:"type"` // "incremental" or "full"
	Success      bool          `json:"success"`
	Duration     time.Duration `json:"duration"`
	FilesChanged int           `json:"filesChanged,omitempty"`
	Error        string        `json:"error,omitempty"`
	Trigger      string        `json:"trigger,omitempty"`
	TriggerInfo  string        `json:"triggerInfo,omitempty"`
}

// RunIncrementalRefresh performs an incremental refresh on a repository
func (rm *RefreshManager) RunIncrementalRefresh(ctx context.Context, repoPath string) *RefreshResult {
	return rm.RunIncrementalRefreshWithTrigger(ctx, repoPath, index.TriggerStale, "")
}

// RunIncrementalRefreshWithTrigger performs an incremental refresh with trigger info
func (rm *RefreshManager) RunIncrementalRefreshWithTrigger(ctx context.Context, repoPath string, trigger index.RefreshTrigger, triggerInfo string) *RefreshResult {
	rm.markPending(repoPath)
	defer rm.clearPending(repoPath)

	start := time.Now()
	result := &RefreshResult{
		RepoPath:    repoPath,
		Type:        "incremental",
		Trigger:     string(trigger),
		TriggerInfo: triggerInfo,
	}

	rm.stdLogger.Printf("Starting incremental refresh for %s (trigger: %s)", repoPath, trigger)

	stats, fallback, err := rm.runIncrementalUpdate(ctx, repoPath)
	if err != nil {
		rm.stdLogger.Printf("Failed to open database for %s: %v", repoPath, err)
		result.Error = fmt.Sprintf("database error: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	if fallback != "" {
		rm.stdLogger.Printf("Incremental not possible for %s: %s, falling back to full", repoPath, fallback)
		return rm.RunFullReindexWithTrigger(ctx, repoPath, trigger, triggerInfo)
	}

	result.Success = true
	result.Duration = time.Since(start)
	result.FilesChanged = stats.FilesAdded + stats.FilesChanged + stats.FilesDeleted

	// Update metadata so freshness check stays in sync
	ckbDir := filepath.Join(repoPath, ".ckb")
	if rs, rsErr := repostate.ComputeRepoState(repoPath); rsErr == nil {
		meta, metaErr := index.LoadMeta(ckbDir)
		if metaErr == nil && meta != nil {
			meta.CommitHash = rs.HeadCommit
			meta.RepoStateID = rs.RepoStateID
			meta.LastRefresh = &index.LastRefresh{
				At:          time.Now(),
				Trigger:     trigger,
				TriggerInfo: triggerInfo,
				DurationMs:  result.Duration.Milliseconds(),
			}
			if saveErr := meta.Save(ckbDir); saveErr != nil {
				rm.stdLogger.Printf("Warning: could not update index metadata for %s: %v", repoPath, saveErr)
			}
		}
	}

	rm.stdLogger.Printf("Incremental refresh completed for %s: %d files changed in %v",
		repoPath, result.FilesChanged, result.Duration.Round(time.Millisecond))

	// Emit webhook event
	rm.emitWebhookEvent("index.updated", repoPath, map[string]interface{}{
		"type":         "incremental",
		"filesChanged": result.FilesChanged,
		"duration":     result.Duration.String(),
	})

	return result
}

// runIncrementalUpdate runs an incremental update, with the same preconditions as
// 'ckb index'. A non-empty fallback says why a full reindex has to run instead;
// everything here is released by then, the index lock in particular. An error
// means the repo's database can't be opened, which a full reindex wouldn't fix.
func (rm *RefreshManager) runIncrementalUpdate(ctx context.Context, repoPath string) (stats *incremental.DeltaStats, fallback string, err error) {
	db, err := storage.Open(repoPath, rm.logger)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = db.Close() }()

	lang, _, _ := project.DetectAllLanguages(repoPath)
	if lang == project.LangUnknown {
		return nil, "could not detect project language", nil
	}
	if indexplan.IsNested(repoPath, lang) {
		return nil, "nested project", nil
	}

	indexer := incremental.NewIncrementalIndexer(repoPath, db, indexplan.IncrementalConfig(repoPath), rm.logger)
	if needsFull, reason := indexer.NeedsFullReindex(); needsFull {
		return nil, reason, nil
	}
	if canUse, reason := indexer.CanUseIncremental(lang); !canUse {
		return nil, reason, nil
	}

	lock, lockErr := index.AcquireLock(filepath.Join(repoPath, ".ckb"))
	if lockErr != nil {
		return nil, fmt.Sprintf("could not acquire lock: %v", lockErr), nil
	}
	defer lock.Release()

	stats, indexErr := indexer.IndexIncrementalWithLang(ctx, "", lang)
	if indexErr != nil {
		return nil, indexErr.Error(), nil
	}
	return stats, "", nil
}

// emitWebhookEvent sends a webhook event with the given data
func (rm *RefreshManager) emitWebhookEvent(eventType, source string, data map[string]interface{}) {
	if rm.webhookManager == nil {
		return
	}

	dataBytes, err := json.Marshal(data)
	if err != nil {
		rm.stdLogger.Printf("Warning: failed to marshal webhook data: %v", err)
		return
	}

	_ = rm.webhookManager.Emit(&webhooks.Event{
		Type:      webhooks.EventType(eventType),
		Source:    source,
		Timestamp: time.Now(),
		Data:      dataBytes,
	})
}

// RunFullReindex performs a full reindex on a repository
func (rm *RefreshManager) RunFullReindex(ctx context.Context, repoPath string) *RefreshResult {
	return rm.RunFullReindexWithTrigger(ctx, repoPath, index.TriggerStale, "")
}

// RunFullReindexWithTrigger performs a full reindex with trigger info
func (rm *RefreshManager) RunFullReindexWithTrigger(ctx context.Context, repoPath string, trigger index.RefreshTrigger, triggerInfo string) *RefreshResult {
	start := time.Now()
	result := &RefreshResult{
		RepoPath:    repoPath,
		Type:        "full",
		Trigger:     string(trigger),
		TriggerInfo: triggerInfo,
	}

	rm.stdLogger.Printf("Starting full reindex for %s (trigger: %s)", repoPath, trigger)

	// Detect language
	lang, manifest, _ := project.DetectAllLanguages(repoPath)
	if lang == project.LangUnknown {
		result.Error = "could not detect project language"
		result.Duration = time.Since(start)
		rm.stdLogger.Printf("Full reindex failed for %s: %s", repoPath, result.Error)
		return result
	}

	// Same recipe as 'ckb index': nested manifests, custom index paths, and the
	// languages whose command needs building.
	plan, _, failure := indexplan.Build(repoPath, lang, manifest, "", indexplan.ResolveIndexPath(repoPath))
	if failure != nil {
		result.Error = failure.Message
		result.Duration = time.Since(start)
		rm.stdLogger.Printf("Full reindex failed for %s: %s", repoPath, result.Error)
		return result
	}

	// Check context cancellation
	select {
	case <-ctx.Done():
		result.Error = "cancelled"
		result.Duration = time.Since(start)
		return result
	default:
	}

	// Acquire lock
	ckbDir := filepath.Join(repoPath, ".ckb")
	lock, err := index.AcquireLock(ckbDir)
	if err != nil {
		result.Error = fmt.Sprintf("could not acquire lock: %v", err)
		result.Duration = time.Since(start)
		rm.stdLogger.Printf("Full reindex skipped for %s: %s", repoPath, result.Error)
		return result
	}
	defer lock.Release()

	// Run indexer
	parts := strings.Fields(plan.Command)
	if len(parts) == 0 {
		result.Error = "empty indexer command"
		result.Duration = time.Since(start)
		return result
	}

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...) // #nosec G204 //nolint:gosec // command from trusted indexer config
	cmd.Dir = plan.IndexerDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		result.Error = fmt.Sprintf("indexer failed: %v (%s)", err, strings.TrimSpace(stderr.String()))
		result.Duration = time.Since(start)
		rm.stdLogger.Printf("Full reindex failed for %s: %s", repoPath, result.Error)
		return result
	}

	if err := indexplan.Finalize(repoPath, plan); err != nil {
		result.Error = err.Error()
		result.Duration = time.Since(start)
		rm.stdLogger.Printf("Full reindex failed for %s: %s", repoPath, result.Error)
		return result
	}

	// An indexer can exit 0 without producing anything. That must not be recorded
	// as a fresh index.
	if _, statErr := os.Stat(plan.IndexPath); statErr != nil {
		result.Error = fmt.Sprintf("indexer completed but %s was not created", plan.IndexPath)
		result.Duration = time.Since(start)
		rm.stdLogger.Printf("Full reindex failed for %s: %s", repoPath, result.Error)
		return result
	}

	duration := time.Since(start)

	// Update metadata with refresh trigger info
	meta := &index.IndexMeta{
		CreatedAt:   time.Now(),
		Duration:    duration.Round(time.Millisecond * 100).String(),
		Indexer:     plan.Indexer.CheckCommand,
		IndexerArgs: parts,
		LastRefresh: &index.LastRefresh{
			At:          time.Now(),
			Trigger:     trigger,
			TriggerInfo: triggerInfo,
			DurationMs:  duration.Milliseconds(),
		},
	}

	if rs, rsErr := repostate.ComputeRepoState(repoPath); rsErr == nil {
		meta.CommitHash = rs.HeadCommit
		meta.RepoStateID = rs.RepoStateID
	}

	if saveErr := meta.Save(ckbDir); saveErr != nil {
		rm.stdLogger.Printf("Warning: could not save index metadata for %s: %v", repoPath, saveErr)
	}

	// Without tracking data the next incremental refresh has nothing to start from.
	if project.SupportsIncrementalIndexing(lang) {
		rm.populateIncrementalTracking(repoPath)
	}

	result.Success = true
	result.Duration = duration

	rm.stdLogger.Printf("Full reindex completed for %s in %v (trigger: %s)", repoPath, duration.Round(time.Millisecond), trigger)

	// Emit webhook event
	rm.emitWebhookEvent("index.updated", repoPath, map[string]interface{}{
		"type":     "full",
		"duration": result.Duration.String(),
	})

	return result
}

// populateIncrementalTracking sets up the tracking tables after a full reindex.
func (rm *RefreshManager) populateIncrementalTracking(repoPath string) {
	db, err := storage.Open(repoPath, rm.logger)
	if err != nil {
		rm.stdLogger.Printf("Warning: could not open database for incremental tracking of %s: %v", repoPath, err)
		return
	}
	defer func() { _ = db.Close() }()

	indexer := incremental.NewIncrementalIndexer(repoPath, db, indexplan.IncrementalConfig(repoPath), rm.logger)
	if err := indexer.PopulateAfterFullIndex(); err != nil {
		rm.stdLogger.Printf("Warning: could not populate incremental tracking for %s: %v", repoPath, err)
	}
}
