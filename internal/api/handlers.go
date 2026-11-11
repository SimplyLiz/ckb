package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/query"
)

// StatusResponse represents the system status response
type StatusResponse struct {
	Status     string                 `json:"status"`
	Timestamp  time.Time              `json:"timestamp"`
	CkbVersion string                 `json:"ckbVersion"`
	Repository map[string]interface{} `json:"repository"`
	Backends   []BackendInfo          `json:"backends"`
	Cache      CacheInfo              `json:"cache"`
	Healthy    bool                   `json:"healthy"`
}

// BackendInfo represents backend status information
type BackendInfo struct {
	ID           string   `json:"id"`
	Available    bool     `json:"available"`
	Healthy      bool     `json:"healthy"`
	Capabilities []string `json:"capabilities"`
	Details      string   `json:"details,omitempty"`
}

// CacheInfo represents cache information
type CacheInfo struct {
	QueriesCached int     `json:"queriesCached"`
	ViewsCached   int     `json:"viewsCached"`
	HitRate       float64 `json:"hitRate"`
	SizeBytes     int64   `json:"sizeBytes"`
}

// DoctorAPIResponse represents the doctor diagnostic response
type DoctorAPIResponse struct {
	Status    string            `json:"status"`
	Timestamp time.Time         `json:"timestamp"`
	Healthy   bool              `json:"healthy"`
	Checks    []DiagnosticCheck `json:"checks"`
}

// DiagnosticCheck represents a single diagnostic check
type DiagnosticCheck struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"` // "pass", "warn", "fail"
	Message string   `json:"message,omitempty"`
	Fixes   []string `json:"fixes,omitempty"`
}

// SymbolResponse represents a symbol lookup response
type SymbolResponse struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Kind          string          `json:"kind"`
	Location      *LocationInfo   `json:"location,omitempty"`
	Module        string          `json:"module,omitempty"`
	Signature     string          `json:"signature,omitempty"`
	Visibility    string          `json:"visibility,omitempty"`
	Documentation string          `json:"documentation,omitempty"`
	Provenance    *ProvenanceInfo `json:"provenance,omitempty"`
}

// LocationInfo represents location information
type LocationInfo struct {
	FileID      string `json:"fileId"`
	Path        string `json:"path"`
	StartLine   int    `json:"startLine"`
	StartColumn int    `json:"startColumn"`
	EndLine     int    `json:"endLine,omitempty"`
	EndColumn   int    `json:"endColumn,omitempty"`
}

// ProvenanceInfo represents response provenance
type ProvenanceInfo struct {
	RepoStateID     string `json:"repoStateId"`
	RepoStateDirty  bool   `json:"repoStateDirty"`
	QueryDurationMs int64  `json:"queryDurationMs"`
}

// SearchResponse represents a symbol search response
type SearchResponse struct {
	Query      string          `json:"query"`
	Results    []SearchResult  `json:"results"`
	Total      int             `json:"total"`
	HasMore    bool            `json:"hasMore"`
	Timestamp  time.Time       `json:"timestamp"`
	Provenance *ProvenanceInfo `json:"provenance,omitempty"`
}

// SearchResult represents a search result item
type SearchResult struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Kind       string        `json:"kind"`
	Module     string        `json:"module,omitempty"`
	Location   *LocationInfo `json:"location,omitempty"`
	Visibility string        `json:"visibility,omitempty"`
	Score      float64       `json:"score"`
}

// ReferencesResponse represents a find references response
type ReferencesResponse struct {
	SymbolID   string            `json:"symbolId"`
	References []ReferenceResult `json:"references"`
	Total      int               `json:"total"`
	Timestamp  time.Time         `json:"timestamp"`
	Provenance *ProvenanceInfo   `json:"provenance,omitempty"`
}

// ReferenceResult represents a single reference result
type ReferenceResult struct {
	Location *LocationInfo `json:"location"`
	Kind     string        `json:"kind"`
	Context  string        `json:"context,omitempty"`
	IsTest   bool          `json:"isTest,omitempty"`
}

// ArchitectureResponse represents an architecture overview response
type ArchitectureResponse struct {
	Timestamp       time.Time `json:"timestamp"`
	Granularity     string    `json:"granularity"`
	DetectionMethod string    `json:"detectionMethod,omitempty"`

	// Module-level fields (granularity=module)
	Modules      []ModuleInfo     `json:"modules,omitempty"`
	Dependencies []DependencyInfo `json:"dependencies,omitempty"`
	Entrypoints  []EntrypointInfo `json:"entrypoints,omitempty"`

	// Directory-level fields (granularity=directory)
	Directories           []DirectoryInfo           `json:"directories,omitempty"`
	DirectoryDependencies []DirectoryDependencyInfo `json:"directoryDependencies,omitempty"`

	// File-level fields (granularity=file)
	Files            []FileInfo           `json:"files,omitempty"`
	FileDependencies []FileDependencyInfo `json:"fileDependencies,omitempty"`

	Provenance *ProvenanceInfo `json:"provenance,omitempty"`
}

// ModuleInfo represents information about a module
type ModuleInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	Language      string `json:"language,omitempty"`
	SymbolCount   int    `json:"symbolCount"`
	FileCount     int    `json:"fileCount"`
	IncomingEdges int    `json:"incomingEdges"`
	OutgoingEdges int    `json:"outgoingEdges"`
}

// DependencyInfo represents a dependency relationship
type DependencyInfo struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"`
	Strength int    `json:"strength"`
}

// DirectoryDependencyInfo represents a dependency between directories
type DirectoryDependencyInfo struct {
	From        string   `json:"from"`
	To          string   `json:"to"`
	Kind        string   `json:"kind,omitempty"`
	ImportCount int      `json:"importCount"`
	Symbols     []string `json:"symbols,omitempty"`
}

// EntrypointInfo represents an entry point
type EntrypointInfo struct {
	FileID   string `json:"fileId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	ModuleID string `json:"moduleId"`
}

// DirectoryInfo represents a directory in directory-level architecture views
type DirectoryInfo struct {
	Path           string `json:"path"`
	FileCount      int    `json:"fileCount"`
	Language       string `json:"language,omitempty"`
	LOC            int    `json:"loc,omitempty"`
	HasIndexFile   bool   `json:"hasIndexFile"`
	IncomingEdges  int    `json:"incomingEdges"`
	OutgoingEdges  int    `json:"outgoingEdges"`
	IsIntermediate bool   `json:"isIntermediate,omitempty"`
}

// FileInfo represents a file in file-level architecture views
type FileInfo struct {
	Path          string `json:"path"`
	Language      string `json:"language,omitempty"`
	LOC           int    `json:"loc,omitempty"`
	IncomingEdges int    `json:"incomingEdges"`
	OutgoingEdges int    `json:"outgoingEdges"`
}

// FileDependencyInfo represents a file-level dependency
type FileDependencyInfo struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"`
	Line     int    `json:"line,omitempty"`
	Resolved bool   `json:"resolved"`
}

// ImpactResponse represents an impact analysis response
type ImpactResponse struct {
	SymbolID         string          `json:"symbolId"`
	Timestamp        time.Time       `json:"timestamp"`
	RiskScore        *RiskScoreInfo  `json:"riskScore,omitempty"`
	DirectImpact     []ImpactItem    `json:"directImpact"`
	TransitiveImpact []ImpactItem    `json:"transitiveImpact,omitempty"`
	ModulesAffected  []ModuleImpact  `json:"modulesAffected"`
	Provenance       *ProvenanceInfo `json:"provenance,omitempty"`
}

// RiskScoreInfo represents risk assessment
type RiskScoreInfo struct {
	Level       string  `json:"level"`
	Score       float64 `json:"score"`
	Explanation string  `json:"explanation"`
}

// ImpactItem represents an affected item
type ImpactItem struct {
	StableID   string        `json:"stableId"`
	Name       string        `json:"name,omitempty"`
	Kind       string        `json:"kind"`
	Distance   int           `json:"distance"`
	ModuleID   string        `json:"moduleId"`
	Location   *LocationInfo `json:"location,omitempty"`
	Confidence float64       `json:"confidence"`
}

// ModuleImpact represents impact on a module
type ModuleImpact struct {
	ModuleID    string `json:"moduleId"`
	Name        string `json:"name,omitempty"`
	ImpactCount int    `json:"impactCount"`
}

// FixScriptResponse represents a fix script response
type FixScriptResponse struct {
	Script    string    `json:"script"`
	Safe      bool      `json:"safe"`
	Commands  []string  `json:"commands"`
	Timestamp time.Time `json:"timestamp"`
}

// CacheResponse represents a cache operation response
type CacheResponse struct {
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// handleStatus returns the current system status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	statusResp, err := s.engine.GetStatus(ctx)
	if err != nil {
		InternalError(w, "Failed to get status", err)
		return
	}

	backends := make([]BackendInfo, 0, len(statusResp.Backends))
	for _, b := range statusResp.Backends {
		backends = append(backends, BackendInfo{
			ID:           b.Id,
			Available:    b.Available,
			Healthy:      b.Healthy,
			Capabilities: b.Capabilities,
			Details:      b.Details,
		})
	}

	var cache CacheInfo
	if statusResp.Cache != nil {
		cache = CacheInfo{
			QueriesCached: statusResp.Cache.QueriesCached,
			ViewsCached:   statusResp.Cache.ViewsCached,
			HitRate:       statusResp.Cache.HitRate,
			SizeBytes:     statusResp.Cache.SizeBytes,
		}
	}

	repoInfo := map[string]interface{}{}
	if statusResp.RepoState != nil {
		repoInfo["repoStateId"] = statusResp.RepoState.RepoStateId
		repoInfo["headCommit"] = statusResp.RepoState.HeadCommit
		repoInfo["dirty"] = statusResp.RepoState.Dirty
	}

	status := "operational"
	if !statusResp.Healthy {
		status = "degraded"
	}

	response := StatusResponse{
		Status:     status,
		Timestamp:  time.Now().UTC(),
		CkbVersion: statusResp.CkbVersion,
		Repository: repoInfo,
		Backends:   backends,
		Cache:      cache,
		Healthy:    statusResp.Healthy,
	}

	WriteJSON(w, response, http.StatusOK)
}

// handleDoctor performs diagnostic checks
func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	checkName := r.URL.Query().Get("check")

	doctorResp, err := s.engine.Doctor(ctx, checkName)
	if err != nil {
		InternalError(w, "Failed to run diagnostics", err)
		return
	}

	checks := make([]DiagnosticCheck, 0, len(doctorResp.Checks))
	for _, c := range doctorResp.Checks {
		fixes := make([]string, 0, len(c.SuggestedFixes))
		for _, f := range c.SuggestedFixes {
			if f.Command != "" {
				fixes = append(fixes, f.Command)
			}
		}
		checks = append(checks, DiagnosticCheck{
			Name:    c.Name,
			Status:  c.Status,
			Message: c.Message,
			Fixes:   fixes,
		})
	}

	status := "healthy"
	if !doctorResp.Healthy {
		status = "unhealthy"
	}

	response := DoctorAPIResponse{
		Status:    status,
		Timestamp: time.Now().UTC(),
		Healthy:   doctorResp.Healthy,
		Checks:    checks,
	}

	WriteJSON(w, response, http.StatusOK)
}

// handleGetSymbol retrieves a symbol by ID
func (s *Server) handleGetSymbol(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	symbolID := GetPathParam(r, "/symbol/")
	if symbolID == "" {
		BadRequest(w, "Symbol ID is required")
		return
	}

	ctx := r.Context()
	repoStateMode := r.URL.Query().Get("repoStateMode")
	if repoStateMode == "" {
		repoStateMode = "head"
	}

	opts := query.GetSymbolOptions{
		SymbolId:      s.remapSymbolID(w, symbolID),
		RepoStateMode: repoStateMode,
	}

	symbolResp, err := s.engine.GetSymbol(ctx, opts)
	if err != nil {
		NotFound(w, "Symbol not found: "+err.Error())
		return
	}

	if symbolResp.Symbol == nil {
		NotFound(w, "Symbol not found")
		return
	}

	response := SymbolResponse{
		ID:            symbolResp.Symbol.StableId,
		Name:          symbolResp.Symbol.Name,
		Kind:          symbolResp.Symbol.Kind,
		Module:        symbolResp.Symbol.ModuleId,
		Signature:     symbolResp.Symbol.Signature,
		Documentation: symbolResp.Symbol.Documentation,
	}

	if symbolResp.Symbol.Visibility != nil {
		response.Visibility = symbolResp.Symbol.Visibility.Visibility
	}

	if symbolResp.Symbol.Location != nil {
		response.Location = &LocationInfo{
			FileID:      symbolResp.Symbol.Location.FileId,
			Path:        symbolResp.Symbol.Location.FileId,
			StartLine:   symbolResp.Symbol.Location.StartLine,
			StartColumn: symbolResp.Symbol.Location.StartColumn,
			EndLine:     symbolResp.Symbol.Location.EndLine,
			EndColumn:   symbolResp.Symbol.Location.EndColumn,
		}
	}

	if symbolResp.Provenance != nil {
		response.Provenance = &ProvenanceInfo{
			RepoStateID:     symbolResp.Provenance.RepoStateId,
			RepoStateDirty:  symbolResp.Provenance.RepoStateDirty,
			QueryDurationMs: symbolResp.Provenance.QueryDurationMs,
		}
	}

	WriteJSON(w, response, http.StatusOK)
}

// handleSearchSymbols searches for symbols
func (s *Server) handleSearchSymbols(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	queryStr := r.URL.Query().Get("q")
	if queryStr == "" {
		BadRequest(w, "Query parameter 'q' is required")
		return
	}

	ctx := r.Context()
	scope := r.URL.Query().Get("scope")
	kindsStr := r.URL.Query().Get("kinds")
	limitStr := r.URL.Query().Get("limit")

	var kinds []string
	if kindsStr != "" {
		kinds = strings.Split(kindsStr, ",")
	}

	limit := 20
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	opts := query.SearchSymbolsOptions{
		Query: queryStr,
		Scope: scope,
		Kinds: kinds,
		Limit: limit,
	}

	searchResp, err := s.engine.SearchSymbols(ctx, opts)
	if err != nil {
		InternalError(w, "Search failed", err)
		return
	}

	results := make([]SearchResult, 0, len(searchResp.Symbols))
	for _, sym := range searchResp.Symbols {
		result := SearchResult{
			ID:     sym.StableId,
			Name:   sym.Name,
			Kind:   sym.Kind,
			Module: sym.ModuleId,
			Score:  sym.Score,
		}

		if sym.Visibility != nil {
			result.Visibility = sym.Visibility.Visibility
		}

		if sym.Location != nil {
			result.Location = &LocationInfo{
				FileID:      sym.Location.FileId,
				Path:        sym.Location.FileId,
				StartLine:   sym.Location.StartLine,
				StartColumn: sym.Location.StartColumn,
			}
		}

		results = append(results, result)
	}

	response := SearchResponse{
		Query:     queryStr,
		Results:   results,
		Total:     searchResp.TotalCount,
		HasMore:   searchResp.Truncated,
		Timestamp: time.Now().UTC(),
	}

	if searchResp.Provenance != nil {
		response.Provenance = &ProvenanceInfo{
			RepoStateID:     searchResp.Provenance.RepoStateId,
			RepoStateDirty:  searchResp.Provenance.RepoStateDirty,
			QueryDurationMs: searchResp.Provenance.QueryDurationMs,
		}
	}

	WriteJSON(w, response, http.StatusOK)
}

// handleFindReferences finds references to a symbol
func (s *Server) handleFindReferences(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	symbolID := GetPathParam(r, "/refs/")
	if symbolID == "" {
		BadRequest(w, "Symbol ID is required")
		return
	}

	ctx := r.Context()
	scope := r.URL.Query().Get("scope")
	includeTests := r.URL.Query().Get("includeTests") == "true"
	limitStr := r.URL.Query().Get("limit")

	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	opts := query.FindReferencesOptions{
		SymbolId:     s.remapSymbolID(w, symbolID),
		Scope:        scope,
		IncludeTests: includeTests,
		Limit:        limit,
	}

	refsResp, err := s.engine.FindReferences(ctx, opts)
	if err != nil {
		InternalError(w, "Find references failed", err)
		return
	}

	refs := make([]ReferenceResult, 0, len(refsResp.References))
	for _, ref := range refsResp.References {
		result := ReferenceResult{
			Kind:    ref.Kind,
			Context: ref.Context,
			IsTest:  ref.IsTest,
		}

		if ref.Location != nil {
			result.Location = &LocationInfo{
				FileID:      ref.Location.FileId,
				Path:        ref.Location.FileId,
				StartLine:   ref.Location.StartLine,
				StartColumn: ref.Location.StartColumn,
				EndLine:     ref.Location.EndLine,
				EndColumn:   ref.Location.EndColumn,
			}
		}

		refs = append(refs, result)
	}

	response := ReferencesResponse{
		SymbolID:   symbolID,
		References: refs,
		Total:      refsResp.TotalCount,
		Timestamp:  time.Now().UTC(),
	}

	if refsResp.Provenance != nil {
		response.Provenance = &ProvenanceInfo{
			RepoStateID:     refsResp.Provenance.RepoStateId,
			RepoStateDirty:  refsResp.Provenance.RepoStateDirty,
			QueryDurationMs: refsResp.Provenance.QueryDurationMs,
		}
	}

	WriteJSON(w, response, http.StatusOK)
}

// handleGetArchitecture returns architecture overview
func (s *Server) handleGetArchitecture(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	depthStr := r.URL.Query().Get("depth")
	includeExternal := r.URL.Query().Get("includeExternalDeps") == "true"
	refresh := r.URL.Query().Get("refresh") == "true"
	granularity := r.URL.Query().Get("granularity")
	inferModules := r.URL.Query().Get("inferModules") != "false" // default true
	targetPath := r.URL.Query().Get("targetPath")

	depth := 2
	if depthStr != "" {
		if d, err := strconv.Atoi(depthStr); err == nil && d > 0 {
			depth = d
		}
	}

	if granularity == "" {
		granularity = "module"
	}

	opts := query.GetArchitectureOptions{
		Depth:               depth,
		IncludeExternalDeps: includeExternal,
		Refresh:             refresh,
		Granularity:         granularity,
		InferModules:        inferModules,
		TargetPath:          targetPath,
	}

	archResp, err := s.engine.GetArchitecture(ctx, opts)
	if err != nil {
		InternalError(w, "Architecture analysis failed", err)
		return
	}

	response := ArchitectureResponse{
		Timestamp:       time.Now().UTC(),
		Granularity:     archResp.Granularity,
		DetectionMethod: archResp.DetectionMethod,
	}

	switch archResp.Granularity {
	case "directory":
		directories := make([]DirectoryInfo, 0, len(archResp.Directories))
		for _, d := range archResp.Directories {
			directories = append(directories, DirectoryInfo{
				Path:           d.Path,
				FileCount:      d.FileCount,
				Language:       d.Language,
				LOC:            d.LOC,
				HasIndexFile:   d.HasIndexFile,
				IncomingEdges:  d.IncomingEdges,
				OutgoingEdges:  d.OutgoingEdges,
				IsIntermediate: d.IsIntermediate,
			})
		}
		response.Directories = directories

		dirDeps := make([]DirectoryDependencyInfo, 0, len(archResp.DirectoryDependencies))
		for _, d := range archResp.DirectoryDependencies {
			dirDeps = append(dirDeps, DirectoryDependencyInfo{
				From:        d.From,
				To:          d.To,
				Kind:        d.Kind,
				ImportCount: d.ImportCount,
				Symbols:     d.Symbols,
			})
		}
		response.DirectoryDependencies = dirDeps

	case "file":
		files := make([]FileInfo, 0, len(archResp.Files))
		for _, f := range archResp.Files {
			files = append(files, FileInfo{
				Path:          f.Path,
				Language:      f.Language,
				LOC:           f.LOC,
				IncomingEdges: f.IncomingEdges,
				OutgoingEdges: f.OutgoingEdges,
			})
		}
		response.Files = files

		fileDeps := make([]FileDependencyInfo, 0, len(archResp.FileDependencies))
		for _, d := range archResp.FileDependencies {
			fileDeps = append(fileDeps, FileDependencyInfo{
				From:     d.From,
				To:       d.To,
				Kind:     d.Kind,
				Line:     d.Line,
				Resolved: d.Resolved,
			})
		}
		response.FileDependencies = fileDeps

	default:
		// Module-level (default)
		modules := make([]ModuleInfo, 0, len(archResp.Modules))
		for _, m := range archResp.Modules {
			modules = append(modules, ModuleInfo{
				ID:            m.ModuleId,
				Name:          m.Name,
				Path:          m.Path,
				Language:      m.Language,
				SymbolCount:   m.SymbolCount,
				FileCount:     m.FileCount,
				IncomingEdges: m.IncomingEdges,
				OutgoingEdges: m.OutgoingEdges,
			})
		}
		response.Modules = modules

		deps := make([]DependencyInfo, 0, len(archResp.DependencyGraph))
		for _, d := range archResp.DependencyGraph {
			deps = append(deps, DependencyInfo{
				From:     d.From,
				To:       d.To,
				Kind:     d.Kind,
				Strength: d.Strength,
			})
		}
		response.Dependencies = deps

		entrypoints := make([]EntrypointInfo, 0, len(archResp.Entrypoints))
		for _, ep := range archResp.Entrypoints {
			entrypoints = append(entrypoints, EntrypointInfo{
				FileID:   ep.FileId,
				Name:     ep.Name,
				Kind:     ep.Kind,
				ModuleID: ep.ModuleId,
			})
		}
		response.Entrypoints = entrypoints
	}

	if archResp.Provenance != nil {
		response.Provenance = &ProvenanceInfo{
			RepoStateID:     archResp.Provenance.RepoStateId,
			RepoStateDirty:  archResp.Provenance.RepoStateDirty,
			QueryDurationMs: archResp.Provenance.QueryDurationMs,
		}
	}

	WriteJSON(w, response, http.StatusOK)
}

// handleAnalyzeImpact analyzes the impact of changing a symbol
func (s *Server) handleAnalyzeImpact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	symbolID := GetPathParam(r, "/impact/")
	if symbolID == "" {
		BadRequest(w, "Symbol ID is required")
		return
	}

	ctx := r.Context()
	depthStr := r.URL.Query().Get("depth")
	includeTests := r.URL.Query().Get("includeTests") == "true"

	depth := 2
	if depthStr != "" {
		if d, err := strconv.Atoi(depthStr); err == nil && d > 0 {
			depth = d
		}
	}

	opts := query.AnalyzeImpactOptions{
		SymbolId:     s.remapSymbolID(w, symbolID),
		Depth:        depth,
		IncludeTests: includeTests,
	}

	impactResp, err := s.engine.AnalyzeImpact(ctx, opts)
	if err != nil {
		InternalError(w, "Impact analysis failed", err)
		return
	}

	directImpact := make([]ImpactItem, 0, len(impactResp.DirectImpact))
	for _, item := range impactResp.DirectImpact {
		impactItem := ImpactItem{
			StableID:   item.StableId,
			Name:       item.Name,
			Kind:       item.Kind,
			Distance:   item.Distance,
			ModuleID:   item.ModuleId,
			Confidence: item.Confidence,
		}
		if item.Location != nil {
			impactItem.Location = &LocationInfo{
				FileID:      item.Location.FileId,
				Path:        item.Location.FileId,
				StartLine:   item.Location.StartLine,
				StartColumn: item.Location.StartColumn,
			}
		}
		directImpact = append(directImpact, impactItem)
	}

	transitiveImpact := make([]ImpactItem, 0, len(impactResp.TransitiveImpact))
	for _, item := range impactResp.TransitiveImpact {
		impactItem := ImpactItem{
			StableID:   item.StableId,
			Name:       item.Name,
			Kind:       item.Kind,
			Distance:   item.Distance,
			ModuleID:   item.ModuleId,
			Confidence: item.Confidence,
		}
		if item.Location != nil {
			impactItem.Location = &LocationInfo{
				FileID:      item.Location.FileId,
				Path:        item.Location.FileId,
				StartLine:   item.Location.StartLine,
				StartColumn: item.Location.StartColumn,
			}
		}
		transitiveImpact = append(transitiveImpact, impactItem)
	}

	modulesAffected := make([]ModuleImpact, 0, len(impactResp.ModulesAffected))
	for _, m := range impactResp.ModulesAffected {
		modulesAffected = append(modulesAffected, ModuleImpact{
			ModuleID:    m.ModuleId,
			Name:        m.Name,
			ImpactCount: m.ImpactCount,
		})
	}

	response := ImpactResponse{
		SymbolID:         symbolID,
		Timestamp:        time.Now().UTC(),
		DirectImpact:     directImpact,
		TransitiveImpact: transitiveImpact,
		ModulesAffected:  modulesAffected,
	}

	if impactResp.RiskScore != nil {
		response.RiskScore = &RiskScoreInfo{
			Level:       impactResp.RiskScore.Level,
			Score:       impactResp.RiskScore.Score,
			Explanation: impactResp.RiskScore.Explanation,
		}
	}

	if impactResp.Provenance != nil {
		response.Provenance = &ProvenanceInfo{
			RepoStateID:     impactResp.Provenance.RepoStateId,
			RepoStateDirty:  impactResp.Provenance.RepoStateDirty,
			QueryDurationMs: impactResp.Provenance.QueryDurationMs,
		}
	}

	WriteJSON(w, response, http.StatusOK)
}

// handleDoctorFix returns a fix script for issues
func (s *Server) handleDoctorFix(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()

	// Run doctor first to get issues
	doctorResp, err := s.engine.Doctor(ctx, "")
	if err != nil {
		InternalError(w, "Failed to run diagnostics", err)
		return
	}

	// Generate fix script
	script := s.engine.GenerateFixScript(doctorResp)

	// Extract commands from the script
	commands := []string{}
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "echo") && line != "set -e" && !strings.HasPrefix(line, "#!/") {
			commands = append(commands, line)
		}
	}

	response := FixScriptResponse{
		Script:    script,
		Safe:      true,
		Commands:  commands,
		Timestamp: time.Now().UTC(),
	}

	WriteJSON(w, response, http.StatusOK)
}

// handleCacheWarm warms the cache by preloading common queries
func (s *Server) handleCacheWarm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()
	warmed := 0

	// Warm by loading architecture overview
	if _, err := s.engine.GetArchitecture(ctx, query.GetArchitectureOptions{}); err == nil {
		warmed++
	}

	// Warm by loading status (includes backend health checks)
	if _, err := s.engine.GetStatus(ctx); err == nil {
		warmed++
	}

	// Warm by loading hotspots
	if _, err := s.engine.GetHotspots(ctx, query.GetHotspotsOptions{Limit: 20}); err == nil {
		warmed++
	}

	response := CacheResponse{
		Status:    "success",
		Message:   fmt.Sprintf("Cache warmed with %d queries (architecture, status, hotspots)", warmed),
		Timestamp: time.Now().UTC(),
	}

	WriteJSON(w, response, http.StatusOK)
}

// handleCacheClear clears the cache
func (s *Server) handleCacheClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Clear all caches
	if err := s.engine.ClearAllCache(); err != nil {
		WriteError(w, err, http.StatusInternalServerError)
		return
	}

	response := CacheResponse{
		Status:    "success",
		Message:   "All caches cleared (query, view, negative)",
		Timestamp: time.Now().UTC(),
	}

	WriteJSON(w, response, http.StatusOK)
}

// newContext creates a context with timeout (kept for future use)
var _ = newContext

func newContext() context.Context {
	return context.Background()
}
