// Package query provides the central query engine that coordinates all CKB operations.
// It connects backends, compression, caching, and response formatting.
package query

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
	"github.com/SimplyLiz/CodeMCP/internal/backends/git"
	"github.com/SimplyLiz/CodeMCP/internal/backends/lsp"
	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/compression"
	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	"github.com/SimplyLiz/CodeMCP/internal/hotspots"
	"github.com/SimplyLiz/CodeMCP/internal/identity"
	"github.com/SimplyLiz/CodeMCP/internal/jobs"
	"github.com/SimplyLiz/CodeMCP/internal/output"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
	"github.com/SimplyLiz/CodeMCP/internal/symbols"
	"github.com/SimplyLiz/CodeMCP/internal/tier"
)

// Engine is the central query coordinator for CKB.
type Engine struct {
	db         *storage.DB
	logger     *slog.Logger
	config     *config.Config
	compressor *compression.Compressor
	resolver   *identity.IdentityResolver
	repoRoot   string
	cache      *storage.Cache

	// Backend references
	orchestrator  *backends.Orchestrator
	scipAdapter   *scip.SCIPAdapter
	gitAdapter    *git.GitAdapter
	lspSupervisor *lsp.LspSupervisor

	// Job runner for async operations
	jobStore  *jobs.Store
	jobRunner *jobs.Runner

	// Complexity analyzer for hotspots
	complexityAnalyzer *hotspots.ComplexityAnalyzer

	// Tree-sitter symbol extractor for fallback search
	treesitterExtractor *symbols.Extractor

	// Tier detector for capability gating
	tierDetector *tier.Detector

	// Tree-sitter mutex — go-tree-sitter uses cgo and is NOT safe for
	// concurrent use. All tree-sitter calls must hold this lock.
	tsMu sync.Mutex

	// Cached repo state
	repoStateMu     sync.RWMutex
	cachedState     *RepoState
	stateComputedAt time.Time

	// LIP health, maintained by a background subscriber that keeps a long-lived
	// connection open and receives `index_changed` pushes plus per-ping health
	// snapshots. `lipHealthCheckedAt` is zero until the first frame arrives —
	// callers check it before trusting the flags.
	//
	// `lipSupported` is the set of `type` tags the daemon advertised in its
	// handshake. It gates calls to newer RPCs (StreamContext, ExplainMatch,
	// ...) on clients talking to an older daemon, instead of letting them
	// dispatch and get back an UnknownMessage. Empty when the handshake has
	// not yet completed or the daemon predates `supported_messages`.
	lipHealthMu        sync.RWMutex
	cachedLipMixed     bool
	cachedLipAvailable bool
	lipHealthCheckedAt time.Time
	lipSupported       map[string]struct{}
	lipSubCancel       context.CancelFunc
	// lipIndexProbed is true once probeHandshake has attempted an IndexStatus
	// call. lipIndexedFiles==0 with lipIndexProbed==true means the daemon is
	// reachable but has nothing indexed — surface this to the user so semantic
	// enrichment silence doesn't look like a bug.
	lipIndexProbed  bool
	lipIndexedFiles int

	// Cache stats
	cacheStatsMu sync.RWMutex
	cacheHits    int64
	cacheMisses  int64
}

// RepoState represents the current state of the repository.
type RepoState struct {
	RepoStateId         string `json:"repoStateId"`
	HeadCommit          string `json:"headCommit"`
	StagedDiffHash      string `json:"stagedDiffHash,omitempty"`
	WorkingTreeDiffHash string `json:"workingTreeDiffHash,omitempty"`
	UntrackedListHash   string `json:"untrackedListHash,omitempty"`
	Dirty               bool   `json:"dirty"`
	ComputedAt          string `json:"computedAt"`
}

// NewEngine creates a new query engine.
func NewEngine(repoRoot string, db *storage.DB, logger *slog.Logger, cfg *config.Config) (*Engine, error) {
	// Create compressor
	budget := compression.NewBudgetFromConfig(cfg)
	limits := compression.NewLimitsFromConfig(cfg)
	compressor := compression.NewCompressor(budget, limits)

	// Create identity resolver
	resolver := identity.NewIdentityResolver(db, logger)

	// Create orchestrator
	policy := backends.LoadQueryPolicy(cfg)
	orchestrator := backends.NewOrchestrator(policy, logger)

	// Create cache
	cache := storage.NewCache(db)

	// Initialize tree-sitter extractor if available
	var tsExtractor *symbols.Extractor
	if symbols.IsAvailable() {
		tsExtractor = symbols.NewExtractor()
	}

	engine := &Engine{
		db:                  db,
		logger:              logger,
		config:              cfg,
		compressor:          compressor,
		resolver:            resolver,
		repoRoot:            repoRoot,
		orchestrator:        orchestrator,
		cache:               cache,
		complexityAnalyzer:  hotspots.NewComplexityAnalyzer(),
		treesitterExtractor: tsExtractor,
		tierDetector:        tier.NewDetector(),
	}

	// Initialize backends
	if err := engine.initializeBackends(cfg); err != nil {
		logger.Warn("Some backends failed to initialize", "error", err.Error())
		// Don't fail - some backends are optional
	}

	// Initialize job runner
	if err := engine.initializeJobRunner(); err != nil {
		logger.Warn("Failed to initialize job runner", "error", err.Error())
		// Don't fail - async operations will be unavailable
	}

	engine.startLipSubscriber()

	return engine, nil
}

// initializeJobRunner sets up the background job runner.
func (e *Engine) initializeJobRunner() error {
	ckbDir := filepath.Join(e.repoRoot, ".ckb")

	// Open job store
	jobStore, err := jobs.OpenStore(ckbDir, e.logger)
	if err != nil {
		return err
	}
	e.jobStore = jobStore

	// Create runner with default config
	config := jobs.DefaultRunnerConfig()
	e.jobRunner = jobs.NewRunner(jobStore, e.logger, config)

	// Register job handlers
	e.registerJobHandlers()

	// Start the runner
	return e.jobRunner.Start()
}

// registerJobHandlers registers handlers for each job type.
func (e *Engine) registerJobHandlers() {
	// Refresh architecture handler
	e.jobRunner.RegisterHandler(jobs.JobTypeRefreshArchitecture, func(ctx context.Context, job *jobs.Job, progress func(int)) (interface{}, error) {
		scope, err := jobs.ParseRefreshScope(job.Scope)
		if err != nil {
			return nil, err
		}

		// Execute the refresh synchronously (we're already in async context)
		opts := RefreshArchitectureOptions{
			Scope:  scope.Scope,
			Force:  scope.Force,
			DryRun: false,
			Async:  false, // Already in async context
		}

		progress(10) // Starting

		resp, err := e.RefreshArchitecture(ctx, opts)
		if err != nil {
			return nil, err
		}

		progress(100) // Done

		// Build result from response
		result := &jobs.RefreshResult{
			Status:   resp.Status,
			Duration: fmt.Sprintf("%dms", resp.DurationMs),
			Warnings: resp.Warnings,
		}

		if resp.Changes != nil {
			result.ModulesChanged = resp.Changes.ModulesUpdated + resp.Changes.ModulesCreated
			result.OwnershipUpdated = resp.Changes.OwnershipUpdated
			result.HotspotsUpdated = resp.Changes.HotspotsUpdated
		}

		return result, nil
	})
}

// initializeBackends initializes all configured backends.
func (e *Engine) initializeBackends(cfg *config.Config) error {
	var lastErr error

	// Initialize Git backend (always available)
	gitAdapter, err := git.NewGitAdapter(cfg, e.logger)
	if err != nil {
		e.logger.Warn("Failed to initialize Git backend", "error", err.Error())
		lastErr = err
	} else {
		e.gitAdapter = gitAdapter
	}

	// Initialize SCIP backend if enabled
	if cfg.Backends.Scip.Enabled {
		scipAdapter, err := scip.NewSCIPAdapter(cfg, e.logger)
		if err != nil {
			e.logger.Warn("Failed to initialize SCIP backend", "error", err.Error())
			lastErr = err
		} else {
			e.scipAdapter = scipAdapter
			e.orchestrator.RegisterBackend(scipAdapter)
			// Update tier detector
			if scipAdapter.IsAvailable() {
				e.tierDetector.SetScipAvailable(true)

				// Background FTS population is started via StartBgTasks(), which is
				// called by production entry points after engine init. Tests skip
				// it to avoid races with synchronous FTS population.
			}
		}
	}

	// Initialize LSP supervisor if enabled
	if cfg.Backends.Lsp.Enabled {
		e.lspSupervisor = lsp.NewLspSupervisor(cfg, e.logger)
	}

	return lastErr
}

// GetTierInfo returns the current analysis tier information.
func (e *Engine) GetTierInfo() tier.TierInfo {
	// Refresh SCIP availability in case index was created/deleted
	if e.scipAdapter != nil {
		e.tierDetector.SetScipAvailable(e.scipAdapter.IsAvailable())
	}
	return e.tierDetector.GetTierInfo()
}

// GetTier returns the current analysis tier.
func (e *Engine) GetTier() tier.AnalysisTier {
	if e.scipAdapter != nil {
		e.tierDetector.SetScipAvailable(e.scipAdapter.IsAvailable())
	}
	return e.tierDetector.DetectTier()
}

// SetTierMode sets the requested tier mode (fast, standard, full, or auto).
// This affects how the tier is resolved for subsequent operations.
func (e *Engine) SetTierMode(mode tier.TierMode) {
	e.tierDetector.SetRequestedMode(mode)
}

// ValidateTierMode validates that the requested tier can be satisfied.
// Returns an error if the tier requirements are not met.
func (e *Engine) ValidateTierMode() error {
	if e.scipAdapter != nil {
		e.tierDetector.SetScipAvailable(e.scipAdapter.IsAvailable())
	}
	_, err := e.tierDetector.ResolveTier()
	return err
}

// GetRepoState returns the current repository state.
func (e *Engine) GetRepoState(ctx context.Context, mode string) (*RepoState, error) {
	e.repoStateMu.RLock()
	// Use cached state if fresh enough (< 5 seconds)
	if e.cachedState != nil && time.Since(e.stateComputedAt) < 5*time.Second {
		state := e.cachedState
		e.repoStateMu.RUnlock()
		return state, nil
	}
	e.repoStateMu.RUnlock()

	// Compute fresh state
	state, err := e.computeRepoState(ctx)
	if err != nil {
		return nil, err
	}

	e.repoStateMu.Lock()
	e.cachedState = state
	e.stateComputedAt = time.Now()
	e.repoStateMu.Unlock()

	return state, nil
}

// computeRepoState computes the current repository state from git.
func (e *Engine) computeRepoState(ctx context.Context) (*RepoState, error) {
	if e.gitAdapter == nil || !e.gitAdapter.IsAvailable() {
		return &RepoState{
			RepoStateId: "unknown",
			Dirty:       true,
			ComputedAt:  time.Now().UTC().Format(time.RFC3339),
		}, nil
	}

	state, err := e.gitAdapter.GetRepoState()
	if err != nil {
		e.logger.Warn("failed to get repo state from git", "error", err.Error())
		//nolint:nilerr // return fallback state on git errors
		return &RepoState{
			RepoStateId: "unknown",
			Dirty:       true,
			ComputedAt:  time.Now().UTC().Format(time.RFC3339),
		}, nil
	}

	return &RepoState{
		RepoStateId:         state.RepoStateID,
		HeadCommit:          state.HeadCommit,
		StagedDiffHash:      state.StagedDiffHash,
		WorkingTreeDiffHash: state.WorkingTreeDiffHash,
		UntrackedListHash:   state.UntrackedListHash,
		Dirty:               state.Dirty,
		ComputedAt:          time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// Provenance contains metadata about how a response was generated.
type Provenance struct {
	RepoStateId     string                `json:"repoStateId"`
	RepoStateDirty  bool                  `json:"repoStateDirty"`
	RepoStateMode   string                `json:"repoStateMode"`
	Backends        []BackendContribution `json:"backends"`
	Completeness    CompletenessInfo      `json:"completeness"`
	CachedAt        string                `json:"cachedAt,omitempty"`
	QueryDurationMs int64                 `json:"queryDurationMs"`
	Warnings        []string              `json:"warnings,omitempty"`
	Timeouts        []string              `json:"timeouts,omitempty"`
	Truncations     []string              `json:"truncations,omitempty"`
}

// BackendContribution describes a backend's contribution to a response.
type BackendContribution struct {
	BackendId    string  `json:"backendId"`
	Available    bool    `json:"available"`
	Used         bool    `json:"used"`
	ResultCount  int     `json:"resultCount,omitempty"`
	DurationMs   int64   `json:"durationMs,omitempty"`
	Completeness float64 `json:"completeness,omitempty"`
}

// CompletenessInfo describes the completeness of results.
type CompletenessInfo struct {
	Score   float64 `json:"score"`
	Reason  string  `json:"reason"`
	Details string  `json:"details,omitempty"`
}

// buildProvenance creates provenance metadata for a response.
func (e *Engine) buildProvenance(
	repoState *RepoState,
	mode string,
	startTime time.Time,
	contributions []BackendContribution,
	completeness CompletenessInfo,
) *Provenance {
	var warnings []string
	var timeouts []string

	return &Provenance{
		RepoStateId:     repoState.RepoStateId,
		RepoStateDirty:  repoState.Dirty,
		RepoStateMode:   mode,
		Backends:        contributions,
		Completeness:    completeness,
		QueryDurationMs: time.Since(startTime).Milliseconds(),
		Warnings:        warnings,
		Timeouts:        timeouts,
	}
}

// sortAndEncode applies deterministic sorting and encoding to response data (kept for future use)
var _ = (*Engine).sortAndEncode

func (e *Engine) sortAndEncode(data interface{}) ([]byte, error) {
	return output.DeterministicEncode(data)
}

// generateDrilldowns creates contextual drilldowns based on truncation and completeness.
func (e *Engine) generateDrilldowns(
	truncation *compression.TruncationInfo,
	completeness CompletenessInfo,
	symbolId string,
	topModule *output.Module,
) []output.Drilldown {
	ctx := &compression.DrilldownContext{
		Budget: e.compressor.GetBudget(),
	}

	if truncation != nil {
		ctx.TruncationReason = truncation.Reason
	}

	ctx.Completeness = compression.CompletenessInfo{
		Score:            completeness.Score,
		IsBestEffort:     completeness.Reason == "best-effort-lsp",
		IsWorkspaceReady: completeness.Reason != "workspace-not-ready",
	}

	ctx.SymbolId = symbolId
	ctx.TopModule = topModule

	return compression.GenerateDrilldowns(ctx)
}

// wrapError converts an error to a CKB error with suggestions.
func (e *Engine) wrapError(err error, code errors.ErrorCode) *errors.CkbError {
	if ckbErr, ok := err.(*errors.CkbError); ok {
		return ckbErr
	}
	return errors.NewCkbError(code, err.Error(), nil, nil, nil)
}

// DB returns the underlying database connection.
func (e *Engine) DB() *storage.DB {
	return e.db
}

// Config returns the engine's loaded configuration.
func (e *Engine) Config() *config.Config {
	return e.config
}

// Close shuts down the query engine.
func (e *Engine) Close() error {
	var lastErr error

	if e.lipSubCancel != nil {
		e.lipSubCancel()
	}

	// Stop job runner first
	if e.jobRunner != nil {
		if err := e.jobRunner.Stop(10 * time.Second); err != nil {
			lastErr = err
		}
	}

	// Close job store
	if e.jobStore != nil {
		if err := e.jobStore.Close(); err != nil {
			lastErr = err
		}
	}

	if e.orchestrator != nil {
		if err := e.orchestrator.Shutdown(); err != nil {
			lastErr = err
		}
	}

	if e.lspSupervisor != nil {
		if err := e.lspSupervisor.Shutdown(); err != nil {
			lastErr = err
		}
	}

	return lastErr
}

// Job management methods

// GetJob retrieves a job by ID.
func (e *Engine) GetJob(jobID string) (*jobs.Job, error) {
	if e.jobRunner == nil {
		return nil, errors.NewCkbError(errors.BackendUnavailable, "job runner not available", nil, nil, nil)
	}
	return e.jobRunner.GetJob(jobID)
}

// ListJobs lists jobs with optional filters.
func (e *Engine) ListJobs(opts jobs.ListJobsOptions) (*jobs.ListJobsResponse, error) {
	if e.jobRunner == nil {
		return nil, errors.NewCkbError(errors.BackendUnavailable, "job runner not available", nil, nil, nil)
	}
	return e.jobRunner.ListJobs(opts)
}

// CancelJob cancels a queued or running job.
func (e *Engine) CancelJob(jobID string) error {
	if e.jobRunner == nil {
		return errors.NewCkbError(errors.BackendUnavailable, "job runner not available", nil, nil, nil)
	}
	return e.jobRunner.Cancel(jobID)
}

// SubmitJob submits a new job for background processing.
func (e *Engine) SubmitJob(job *jobs.Job) error {
	if e.jobRunner == nil {
		return errors.NewCkbError(errors.BackendUnavailable, "job runner not available", nil, nil, nil)
	}
	return e.jobRunner.Submit(job)
}

// GetJobRunnerStats returns statistics about the job runner.
func (e *Engine) GetJobRunnerStats() map[string]interface{} {
	if e.jobRunner == nil {
		return map[string]interface{}{"available": false}
	}
	stats := e.jobRunner.Stats()
	stats["available"] = true
	return stats
}

// GetRepoRoot returns the repository root path.
func (e *Engine) GetRepoRoot() string {
	return e.repoRoot
}

// GetScipBackend returns the SCIP adapter (may be nil).
func (e *Engine) GetScipBackend() *scip.SCIPAdapter {
	return e.scipAdapter
}

// GetGitBackend returns the Git adapter (may be nil).
func (e *Engine) GetGitBackend() *git.GitAdapter {
	return e.gitAdapter
}

// GetLspSupervisor returns the LSP supervisor (may be nil).
func (e *Engine) GetLspSupervisor() *lsp.LspSupervisor {
	return e.lspSupervisor
}

// GetConfig returns the engine configuration.
func (e *Engine) GetConfig() *config.Config {
	return e.config
}

// ActiveBackendName returns the name of the highest-quality backend currently
// serving requests: "scip" when available, "lsp" when the LSP supervisor is
// configured, otherwise "tree-sitter". This is the name that should be set on
// envelope.Meta.Backend so callers can see what accuracy tier they are getting.
func (e *Engine) ActiveBackendName() string {
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		return "scip"
	}
	if e.lspSupervisor != nil {
		return "lsp"
	}
	return "tree-sitter"
}

// GetDB returns the storage database.
func (e *Engine) GetDB() *storage.DB {
	return e.db
}

// StartBgTasks launches background maintenance goroutines (FTS population, etc.).
// Call this after NewEngine() in production entry points. Tests that need
// deterministic FTS state should call PopulateFTSFromSCIP synchronously instead.
func (e *Engine) StartBgTasks() {
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		go func() {
			ctx := context.Background()
			if err := e.PopulateFTSFromSCIP(ctx); err != nil {
				e.logger.Warn("Failed to populate FTS from SCIP", "error", err.Error())
			}
		}()
	}
}

// DisableBgFTS is now a no-op kept for backward compatibility. Background tasks
// are no longer started inside NewEngine; call StartBgTasks() explicitly.
func (e *Engine) DisableBgFTS() {}

// ClearAllCache clears all cache entries (query, view, and negative caches).
func (e *Engine) ClearAllCache() error {
	if e.cache == nil {
		return nil
	}

	var errs []error

	if err := e.cache.InvalidateAllQueryCache(); err != nil {
		errs = append(errs, fmt.Errorf("failed to clear query cache: %w", err))
	}

	if err := e.cache.InvalidateAllViewCache(); err != nil {
		errs = append(errs, fmt.Errorf("failed to clear view cache: %w", err))
	}

	if err := e.cache.InvalidateAllNegativeCache(); err != nil {
		errs = append(errs, fmt.Errorf("failed to clear negative cache: %w", err))
	}

	// Reset cache stats
	e.cacheStatsMu.Lock()
	e.cacheHits = 0
	e.cacheMisses = 0
	e.cacheStatsMu.Unlock()

	if len(errs) > 0 {
		return errs[0] // Return first error
	}

	return nil
}

// TelemetrySymbol represents a symbol for telemetry dead code analysis.
type TelemetrySymbol struct {
	ID   string
	Name string
	File string
	Kind string
}

// GetAllSymbols returns all symbols from the SCIP index for telemetry analysis.
func (e *Engine) GetAllSymbols() ([]TelemetrySymbol, error) {
	if e.scipAdapter == nil || !e.scipAdapter.IsAvailable() {
		return nil, fmt.Errorf("SCIP backend not available")
	}

	scipSymbols := e.scipAdapter.AllSymbols()
	if scipSymbols == nil {
		return nil, nil
	}

	symbols := make([]TelemetrySymbol, 0, len(scipSymbols))
	for _, sym := range scipSymbols {
		// Get the file from the first occurrence if available
		file := ""
		// DisplayName is the human-readable name
		name := sym.DisplayName
		if name == "" {
			name = sym.Symbol
		}

		symbols = append(symbols, TelemetrySymbol{
			ID:   sym.Symbol,
			Name: name,
			File: file, // File comes from occurrences, not symbol info
			Kind: fmt.Sprintf("%d", sym.Kind),
		})
	}

	return symbols, nil
}

// GetReferenceCount returns the number of static references to a symbol.
func (e *Engine) GetReferenceCount(symbolId string) (int, error) {
	if e.scipAdapter == nil || !e.scipAdapter.IsAvailable() {
		return 0, nil // Return 0 if SCIP not available
	}

	return e.scipAdapter.GetReferenceCount(symbolId), nil
}
