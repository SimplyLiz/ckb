package query

import (
	"context"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
	"github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/tier"
	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// StatusResponse is the response for getStatus.
type StatusResponse struct {
	CkbVersion      string             `json:"ckbVersion"`
	Healthy         bool               `json:"healthy"`
	Tier            *tier.TierInfo     `json:"tier"`
	RepoState       *RepoState         `json:"repoState"`
	Backends        []BackendStatus    `json:"backends"`
	Cache           *CacheStatus       `json:"cache"`
	LastRefresh     *index.LastRefresh `json:"lastRefresh,omitempty"`
	QueryDurationMs int64              `json:"queryDurationMs"`
}

// BackendStatus describes the status of a backend.
type BackendStatus struct {
	Id           string   `json:"id"`
	Available    bool     `json:"available"`
	Healthy      bool     `json:"healthy"`
	Capabilities []string `json:"capabilities"`
	Details      string   `json:"details,omitempty"`
	Warning      string   `json:"warning,omitempty"`
}

// CacheStatus describes the cache state.
type CacheStatus struct {
	QueriesCached int     `json:"queriesCached"`
	ViewsCached   int     `json:"viewsCached"`
	HitRate       float64 `json:"hitRate"`
	SizeBytes     int64   `json:"sizeBytes"`
}

// GetStatus returns the current system status.
func (e *Engine) GetStatus(ctx context.Context) (*StatusResponse, error) {
	startTime := time.Now()

	// Report the index that is on disk, not the one loaded at startup.
	if _, err := e.EnsureSCIPLoaded(ctx); err != nil {
		e.logger.Warn("failed to reload changed SCIP index", "error", err.Error())
	}

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "head")
	if err != nil {
		e.logger.Warn("failed to get repo state",
			"error", err.Error(),
		)
	}

	// Get backend statuses
	backendStatuses := e.getBackendStatuses(ctx)

	// Determine overall health
	healthy := true
	for _, bs := range backendStatuses {
		if bs.Available && !bs.Healthy {
			healthy = false
			break
		}
	}

	// Get cache status
	cacheStatus := e.getCacheStatus()

	// Get tier info
	tierInfo := e.GetTierInfo()

	// Get last refresh info from index metadata
	var lastRefresh *index.LastRefresh
	ckbDir := filepath.Join(e.repoRoot, ".ckb")
	if meta, err := index.LoadMeta(ckbDir); err == nil && meta != nil {
		lastRefresh = meta.LastRefresh
	}

	return &StatusResponse{
		CkbVersion:      version.Version,
		Healthy:         healthy,
		Tier:            &tierInfo,
		RepoState:       repoState,
		Backends:        backendStatuses,
		Cache:           cacheStatus,
		LastRefresh:     lastRefresh,
		QueryDurationMs: time.Since(startTime).Milliseconds(),
	}, nil
}

// getBackendStatuses returns the status of all backends.
func (e *Engine) getBackendStatuses(ctx context.Context) []BackendStatus {
	statuses := make([]BackendStatus, 0)

	// SCIP backend
	scipStatus := BackendStatus{
		Id:           "scip",
		Capabilities: []string{"symbol-search", "find-references", "goto-definition"},
	}
	if e.scipAdapter != nil {
		scipStatus.Available = e.scipAdapter.IsAvailable()
		scipStatus.Healthy = scipStatus.Available
		if !scipStatus.Available {
			scipStatus.Details = "Index not found. Run SCIP indexer to create."
		} else {
			info := e.scipAdapter.GetIndexInfo()
			if info != nil {
				scipStatus.Details = "Symbols: " + strconv.Itoa(info.SymbolCount) + ", Documents: " + strconv.Itoa(info.DocumentCount)
			}
		}
	}
	statuses = append(statuses, scipStatus)

	// LSP backend
	lspStatus := BackendStatus{
		Id:           "lsp",
		Capabilities: []string{"symbol-search", "find-references", "goto-definition", "hover"},
	}
	if e.lspSupervisor != nil {
		stats := e.lspSupervisor.GetStats()
		if processCount, ok := stats["totalProcesses"].(int); ok {
			lspStatus.Available = processCount > 0
			lspStatus.Healthy = lspStatus.Available
			lspStatus.Details = formatServerCount(processCount)
		}
	}
	statuses = append(statuses, lspStatus)

	// Git backend
	gitStatus := BackendStatus{
		Id:           "git",
		Capabilities: []string{"blame", "history", "churn"},
	}
	if e.gitAdapter != nil {
		gitStatus.Available = e.gitAdapter.IsAvailable()
		gitStatus.Healthy = gitStatus.Available
		gitStatus.Details = "Ready"
	}
	statuses = append(statuses, gitStatus)

	// Cartographer backend (optional — only present when built with -tags cartographer)
	cartographerStatus := BackendStatus{
		Id:        "cartographer",
		Available: cartographer.Available(),
		Capabilities: []string{
			"layer-analysis",      // CheckLayers in PR review
			"health-scoring",      // MapProject in getArchitecture, Health in review pipeline
			"hidden-coupling",     // HiddenCoupling in PR coupling check
			"churn-analysis",      // GitChurn in blast-radius check
			"cochange-analysis",   // GitCochange in getHotspots
			"dead-code-detection", // UnreferencedSymbols in PR dead-code check
			"simulate-change",     // SimulateChange in analyzeImpact
			"semidiff",            // Semidiff in summarizeDiff
			"module-skeleton",     // GetModuleContext in getModuleOverview
			"skeleton-extraction", // SkeletonMap in exportForLLM
			"ranked-skeleton",     // RankedSkeleton in exportForLLM (tokenBudget)
		},
	}
	if cartographer.Available() {
		if ver, err := cartographer.Version(); err == nil {
			cartographerStatus.Healthy = true
			cartographerStatus.Details = "v" + ver
		} else {
			cartographerStatus.Healthy = false
			cartographerStatus.Warning = err.Error()
		}
	}
	statuses = append(statuses, cartographerStatus)

	return statuses
}

// getCacheStatus returns the current cache status.
func (e *Engine) getCacheStatus() *CacheStatus {
	if e.db == nil {
		return &CacheStatus{}
	}

	// Query basic stats from DB
	var queryCount int
	var sizeBytes int64
	row := e.db.QueryRow("SELECT COUNT(*), COALESCE(SUM(LENGTH(value_json)), 0) FROM query_cache")
	if err := row.Scan(&queryCount, &sizeBytes); err != nil {
		return &CacheStatus{}
	}

	// Get view cache count
	var viewCount int
	row = e.db.QueryRow("SELECT COUNT(*) FROM view_cache")
	if err := row.Scan(&viewCount); err != nil {
		viewCount = 0
	}

	// Calculate hit rate from in-memory stats
	e.cacheStatsMu.RLock()
	hits := e.cacheHits
	misses := e.cacheMisses
	e.cacheStatsMu.RUnlock()

	var hitRate float64
	total := hits + misses
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return &CacheStatus{
		QueriesCached: queryCount,
		ViewsCached:   viewCount,
		HitRate:       hitRate,
		SizeBytes:     sizeBytes,
	}
}

// formatServerCount formats the LSP server count message.
func formatServerCount(count int) string {
	if count == 0 {
		return "No servers running"
	}
	if count == 1 {
		return "1 server configured"
	}
	return strconv.Itoa(count) + " server(s) configured"
}

// SystemInfo contains system information for diagnostics.
type SystemInfo struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"goVersion"`
	NumCPU    int    `json:"numCpu"`
}

// GetSystemInfo returns system information.
func (e *Engine) GetSystemInfo() *SystemInfo {
	return &SystemInfo{
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		GoVersion: runtime.Version(),
		NumCPU:    runtime.NumCPU(),
	}
}
