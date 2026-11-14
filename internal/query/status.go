package query

import (
	"context"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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
		lspStatus.Available, lspStatus.Healthy, lspStatus.Details = e.lspBackendState()
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

// lspBackendState derives LSP availability from configuration and PATH, not from
// running processes. Servers start lazily on the first query, so an idle server is
// healthy as long as its binary can be started:
//   - running server(s): available, healthy
//   - configured, binary on PATH, not yet started: available, healthy
//   - configured but no binary on PATH: unavailable, details name the missing binaries
//   - nothing configured for the repo's languages: unavailable
//
// Running servers are always listed, even if the repo's languages would not select them.
func (e *Engine) lspBackendState() (available, healthy bool, details string) {
	servers := e.lspSupervisor.ConfiguredServers()
	if e.config != nil && len(servers) > 0 {
		relevant := e.filterRelevantLspServers(e.config.Backends.Lsp.Servers)
		kept := servers[:0]
		for _, s := range servers {
			if _, ok := relevant[s.Language]; ok || s.Running {
				kept = append(kept, s)
			}
		}
		servers = kept
	}

	if len(servers) == 0 {
		return false, false, "No LSP servers configured"
	}

	var parts []string
	for _, s := range servers {
		switch {
		case s.Running:
			available = true
			parts = append(parts, s.Language+": running")
		case s.OnPath:
			available = true
			parts = append(parts, s.Language+": configured, starts on demand")
		default:
			parts = append(parts, s.Language+": "+s.Command+" not found on PATH")
		}
	}
	return available, available, strings.Join(parts, "; ")
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
