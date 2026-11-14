package query

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/architecture"
	"github.com/SimplyLiz/CodeMCP/internal/compression"
	"github.com/SimplyLiz/CodeMCP/internal/cycles"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	"github.com/SimplyLiz/CodeMCP/internal/jobs"
	"github.com/SimplyLiz/CodeMCP/internal/modules"
	"github.com/SimplyLiz/CodeMCP/internal/output"
	"github.com/SimplyLiz/CodeMCP/internal/ownership"
	"github.com/SimplyLiz/CodeMCP/internal/pathclass"
	"github.com/SimplyLiz/CodeMCP/internal/responsibilities"
)

// GetArchitectureOptions contains options for getArchitecture.
type GetArchitectureOptions struct {
	Depth               int
	IncludeExternalDeps bool
	Refresh             bool

	// v8.0: Granularity options
	Granularity    string // "module" (default), "directory", "file"
	InferModules   bool   // Infer modules from directory structure (default: true)
	TargetPath     string // Optional path to focus on (relative to repo root)
	IncludeMetrics bool   // Include aggregate metrics per directory (complexity, churn)
}

// ArchHealthSummary counts the structural findings of the whole repo, from the
// SCIP index: runtime import cycles (TS/JS and Python, see import_graph.go),
// god modules (god_modules.go) and bridges (bridges.go).
type ArchHealthSummary struct {
	BridgeCount    int      `json:"bridgeCount"`
	CycleCount     int      `json:"cycleCount"`
	GodModuleCount int      `json:"godModuleCount"`
	CycleLanguages []string `json:"cycleLanguages,omitempty"` // languages whose imports were checked for cycles
}

// GetArchitectureResponse is the response for getArchitecture.
type GetArchitectureResponse struct {
	// Module-level fields (granularity=module)
	Modules         []ModuleSummary  `json:"modules,omitempty"`
	DependencyGraph []DependencyEdge `json:"dependencyGraph,omitempty"`
	Entrypoints     []Entrypoint     `json:"entrypoints,omitempty"`

	// Directory-level fields (granularity=directory)
	Directories           []DirectorySummary        `json:"directories,omitempty"`
	DirectoryDependencies []DirectoryDependencyEdge `json:"directoryDependencies,omitempty"`

	// File-level fields (granularity=file)
	Files            []FileSummary        `json:"files,omitempty"`
	FileDependencies []FileDependencyEdge `json:"fileDependencies,omitempty"`

	// Metadata
	Granularity     string `json:"granularity"`     // "module", "directory", "file"
	DetectionMethod string `json:"detectionMethod"` // "manifest", "convention", "inferred", "fallback", "import-scan"

	// Structural findings from the SCIP index (only with one)
	ArchHealth      *ArchHealthSummary `json:"archHealth,omitempty"`
	ArchCycles      []cycles.Cycle     `json:"archCycles,omitempty"`
	ArchGodModules  []GodModule        `json:"archGodModules,omitempty"`
	ArchBridgeNodes []string           `json:"archBridgeNodes,omitempty"` // files whose removal cuts off >= 3 files

	// Standard envelope fields
	Truncated       bool                  `json:"truncated,omitempty"`
	TruncationInfo  *TruncationInfo       `json:"truncationInfo,omitempty"`
	Provenance      *Provenance           `json:"provenance"`
	Drilldowns      []output.Drilldown    `json:"drilldowns,omitempty"`
	Confidence      float64               `json:"confidence"`
	ConfidenceBasis []ConfidenceBasisItem `json:"confidenceBasis"`
	Limitations     []string              `json:"limitations,omitempty"`
}

// DirectoryMetrics contains aggregate metrics for visualization
// Added in v8.0 to support metric-based visualization (size = LOC, color = complexity)
type DirectoryMetrics struct {
	LOC           int     `json:"loc"`                     // Total lines of code
	AvgComplexity float64 `json:"avgComplexity,omitempty"` // Average cyclomatic complexity
	MaxComplexity int     `json:"maxComplexity,omitempty"` // Highest single-function complexity
	LastModified  string  `json:"lastModified,omitempty"`  // ISO 8601 timestamp of most recent change
	Churn30d      int     `json:"churn30d,omitempty"`      // Commit count in last 30 days
}

// DirectorySummary represents a directory in directory-level architecture views
type DirectorySummary struct {
	Path           string            `json:"path"`
	FileCount      int               `json:"fileCount"`
	SymbolCount    int               `json:"symbolCount,omitempty"`
	Language       string            `json:"language,omitempty"`
	LOC            int               `json:"loc,omitempty"`
	Role           string            `json:"role,omitempty"` // Inferred role: api, ui, data, util, test, config, entrypoint, core
	HasIndexFile   bool              `json:"hasIndexFile"`
	IncomingEdges  int               `json:"incomingEdges"`
	OutgoingEdges  int               `json:"outgoingEdges"`
	IsIntermediate bool              `json:"isIntermediate,omitempty"`
	Metrics        *DirectoryMetrics `json:"metrics,omitempty"` // Aggregate metrics (when includeMetrics=true)
}

// DirectoryDependencyEdge represents a dependency between directories
type DirectoryDependencyEdge struct {
	From        string   `json:"from"`
	To          string   `json:"to"`
	Kind        string   `json:"kind,omitempty"`
	ImportCount int      `json:"importCount"`
	Symbols     []string `json:"symbols,omitempty"`
}

// FileSummary represents a file in file-level architecture views
type FileSummary struct {
	Path          string `json:"path"`
	Language      string `json:"language,omitempty"`
	SymbolCount   int    `json:"symbolCount,omitempty"`
	LOC           int    `json:"loc,omitempty"`
	IncomingEdges int    `json:"incomingEdges"`
	OutgoingEdges int    `json:"outgoingEdges"`
}

// FileDependencyEdge represents a dependency between files
type FileDependencyEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"`
	Line     int    `json:"line,omitempty"`
	Resolved bool   `json:"resolved"`
}

// ModuleSummary describes a module in the architecture.
type ModuleSummary struct {
	ModuleId      string `json:"moduleId"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	Language      string `json:"language,omitempty"`
	SymbolCount   int    `json:"symbolCount"`
	FileCount     int    `json:"fileCount"`
	ExportedCount int    `json:"exportedCount,omitempty"`
	IncomingEdges int    `json:"incomingEdges"`
	OutgoingEdges int    `json:"outgoingEdges"`
	IsEntrypoint  bool   `json:"isEntrypoint,omitempty"`
}

// DependencyEdge represents a dependency between modules.
type DependencyEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"` // local-file, local-module, workspace-package, external-dependency, stdlib
	Strength int    `json:"strength"`
}

// Entrypoint represents an entry point in the codebase.
type Entrypoint struct {
	ModuleId string `json:"moduleId"`
	FileId   string `json:"fileId"`
	Kind     string `json:"kind"` // main, test, script, api
	Name     string `json:"name,omitempty"`
}

// GetArchitecture returns the codebase architecture.
// v5.2 compliant with hard caps: max 20 modules, 50 edges
// v8.0 extended with granularity options: module, directory, file
func (e *Engine) GetArchitecture(ctx context.Context, opts GetArchitectureOptions) (*GetArchitectureResponse, error) {
	startTime := time.Now()

	// v5.2 hard caps for module level
	const maxModules = 20
	const maxModuleEdges = 50
	// v8.0 caps for directory/file level
	const maxDirectories = 50
	const maxDirectoryEdges = 200
	const maxFiles = 200
	const maxFileEdges = 500
	const minEdgeStrength = 1 // Minimum strength to keep an edge

	// Default options
	if opts.Depth <= 0 {
		opts.Depth = 2
	}
	if opts.Granularity == "" {
		opts.Granularity = "module"
	}

	var confidenceBasis []ConfidenceBasisItem
	var limitations []string

	// Get repo state (full mode for architecture)
	repoState, err := e.GetRepoState(ctx, "full")
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	// Create import scanner for the architecture generator
	importScanner := modules.NewImportScanner(&e.config.ImportScan, e.logger)

	// Create architecture generator
	generator := architecture.NewArchitectureGenerator(e.repoRoot, e.config, importScanner, e.logger)

	// Set git adapter for metrics computation (if available and requested)
	if opts.IncludeMetrics && e.gitAdapter != nil {
		generator.SetGitAdapter(e.gitAdapter)
	}

	// Build generator options with v8.0 granularity support
	genOpts := &architecture.GeneratorOptions{
		Depth:               opts.Depth,
		IncludeExternalDeps: opts.IncludeExternalDeps,
		Refresh:             opts.Refresh,
		Granularity:         architecture.ParseGranularity(opts.Granularity),
		InferModules:        opts.InferModules,
		TargetPath:          opts.TargetPath,
		IncludeMetrics:      opts.IncludeMetrics,
	}

	// Generate architecture
	arch, err := generator.Generate(ctx, repoState.RepoStateId, genOpts)
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	// Handle different granularities
	var resp *GetArchitectureResponse
	switch arch.Granularity {
	case architecture.GranularityDirectory:
		resp, err = e.buildDirectoryLevelResponse(arch, repoState, startTime, maxDirectories, maxDirectoryEdges, confidenceBasis, limitations)
	case architecture.GranularityFile:
		resp, err = e.buildFileLevelResponse(arch, repoState, startTime, maxFiles, maxFileEdges, confidenceBasis, limitations)
	default:
		resp, err = e.buildModuleLevelResponse(arch, repoState, opts, startTime, maxModules, maxModuleEdges, minEdgeStrength, confidenceBasis, limitations)
	}
	if err != nil {
		return nil, err
	}

	// Structural findings of the whole repo from the SCIP index.
	if e.hasConsumerGraph() {
		idx := e.scipAdapter.GetIndex()
		classes := pathclass.FromConfig(e.repoRoot, e.config)
		health := &ArchHealthSummary{}
		if g := fileImportGraph(idx, e.repoRoot); g != nil && len(g.languages) > 0 {
			resp.ArchCycles = g.runtimeCycles()
			health.CycleCount = len(resp.ArchCycles)
			health.CycleLanguages = g.checkedLanguages()
		}
		resp.ArchGodModules = godModules(idx, structuralNoise(classes))
		health.GodModuleCount = len(resp.ArchGodModules)
		// Bridge files: removing one cuts off at least bridgeMinCut files.
		bridges := codeBridges(idx, e.repoRoot, classes)
		for f := range bridges {
			resp.ArchBridgeNodes = append(resp.ArchBridgeNodes, f)
		}
		sort.Strings(resp.ArchBridgeNodes)
		health.BridgeCount = len(bridges)
		resp.ArchHealth = health
	}
	return resp, nil
}

// buildModuleLevelResponse handles module-level architecture response (existing behavior)
func (e *Engine) buildModuleLevelResponse(arch *architecture.ArchitectureResponse, repoState *RepoState, opts GetArchitectureOptions, startTime time.Time, maxModules, maxEdges, minEdgeStrength int, confidenceBasis []ConfidenceBasisItem, limitations []string) (*GetArchitectureResponse, error) {

	confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
		Backend: "scip",
		Status:  "available",
	})

	// Convert to response format
	moduleSummaries := convertModuleSummaries(arch.Modules)
	edges := convertArchEdges(arch.DependencyGraph, opts.IncludeExternalDeps)
	entrypoints := convertArchEntrypoints(arch.Entrypoints)

	// Enrich module summaries with symbol counts from SCIP
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		for i := range moduleSummaries {
			// Count symbols for this module's path prefix
			symbolCount := e.scipAdapter.CountSymbolsByPath(moduleSummaries[i].Path)
			moduleSummaries[i].SymbolCount = symbolCount
		}
	}

	// Compute edge counts for modules
	computeEdgeCounts(moduleSummaries, edges)

	// Sort modules by impact (incoming edges DESC) with deterministic tie-breaker
	sort.Slice(moduleSummaries, func(i, j int) bool {
		if moduleSummaries[i].IncomingEdges != moduleSummaries[j].IncomingEdges {
			return moduleSummaries[i].IncomingEdges > moduleSummaries[j].IncomingEdges
		}
		if moduleSummaries[i].SymbolCount != moduleSummaries[j].SymbolCount {
			return moduleSummaries[i].SymbolCount > moduleSummaries[j].SymbolCount
		}
		return moduleSummaries[i].ModuleId < moduleSummaries[j].ModuleId
	})

	// v5.2: Prune edges - keep only those with strength >= minEdgeStrength
	originalEdgeCount := len(edges)
	prunedEdges := make([]DependencyEdge, 0, len(edges))
	for _, edge := range edges {
		if edge.Strength >= minEdgeStrength {
			prunedEdges = append(prunedEdges, edge)
		}
	}
	edges = prunedEdges

	// v5.2: Sort edges by strength DESC, then lexical tie-breaker
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Strength != edges[j].Strength {
			return edges[i].Strength > edges[j].Strength
		}
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})

	// v5.2: Apply edge cap
	var truncationInfo *TruncationInfo
	if len(edges) > maxEdges {
		limitations = append(limitations, "Edge count exceeded; showing top 50 by strength")
		edges = edges[:maxEdges]
	}

	// v5.2: Apply module cap
	if len(moduleSummaries) > maxModules {
		truncationInfo = &TruncationInfo{
			Reason:        "max-modules",
			OriginalCount: len(moduleSummaries),
			ReturnedCount: maxModules,
		}
		limitations = append(limitations, "Module count exceeded; showing top 20 by impact")
		moduleSummaries = moduleSummaries[:maxModules]
	}

	// Track if we pruned edges
	if originalEdgeCount > len(edges) && len(limitations) == 0 {
		limitations = append(limitations, "Some weak edges pruned")
	}

	// Compute confidence
	confidence := 0.89 // Partial static analysis (SCIP available)
	if len(limitations) > 0 {
		confidence = 0.79 // With limitations
	}

	// Build completeness
	completeness := CompletenessInfo{
		Score:  1.0,
		Reason: "full-backend",
	}

	// Build provenance
	provenance := e.buildProvenance(repoState, "full", startTime, nil, completeness)

	// Generate drilldowns
	var compTrunc *compression.TruncationInfo
	if truncationInfo != nil {
		compTrunc = &compression.TruncationInfo{
			Reason:        compression.TruncMaxModules,
			OriginalCount: truncationInfo.OriginalCount,
			ReturnedCount: truncationInfo.ReturnedCount,
		}
	}

	var topModule *output.Module
	if len(moduleSummaries) > 0 {
		topModule = &output.Module{
			ModuleId: moduleSummaries[0].ModuleId,
			Name:     moduleSummaries[0].Name,
		}
	}

	drilldowns := e.generateDrilldowns(compTrunc, completeness, "", topModule)

	return &GetArchitectureResponse{
		Modules:         moduleSummaries,
		DependencyGraph: edges,
		Entrypoints:     entrypoints,
		Granularity:     string(arch.Granularity),
		DetectionMethod: arch.DetectionMethod,
		Truncated:       truncationInfo != nil,
		TruncationInfo:  truncationInfo,
		Provenance:      provenance,
		Drilldowns:      drilldowns,
		Confidence:      confidence,
		ConfidenceBasis: confidenceBasis,
		Limitations:     limitations,
	}, nil
}

// buildDirectoryLevelResponse handles directory-level architecture response (v8.0)
func (e *Engine) buildDirectoryLevelResponse(arch *architecture.ArchitectureResponse, repoState *RepoState, startTime time.Time, maxDirectories, maxEdges int, confidenceBasis []ConfidenceBasisItem, limitations []string) (*GetArchitectureResponse, error) {
	confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
		Backend: "import-scan",
		Status:  "available",
	})

	// Convert to response format
	directories := convertDirectorySummaries(arch.Directories)
	edges := convertDirectoryEdges(arch.DirectoryDependencies)

	// Apply limits
	var truncationInfo *TruncationInfo
	if len(directories) > maxDirectories {
		truncationInfo = &TruncationInfo{
			Reason:        "max-directories",
			OriginalCount: len(directories),
			ReturnedCount: maxDirectories,
		}
		limitations = append(limitations, "Directory count exceeded; showing top directories")
		directories = directories[:maxDirectories]
	}
	if len(edges) > maxEdges {
		limitations = append(limitations, "Edge count exceeded; showing top edges by strength")
		edges = edges[:maxEdges]
	}

	confidence := 0.75 // Heuristic-based detection
	if len(limitations) > 0 {
		confidence = 0.65
	}

	completeness := CompletenessInfo{
		Score:  0.9,
		Reason: "inferred-directories",
	}

	provenance := e.buildProvenance(repoState, "full", startTime, nil, completeness)

	return &GetArchitectureResponse{
		Directories:           directories,
		DirectoryDependencies: edges,
		Granularity:           "directory",
		DetectionMethod:       arch.DetectionMethod,
		Truncated:             truncationInfo != nil,
		TruncationInfo:        truncationInfo,
		Provenance:            provenance,
		Confidence:            confidence,
		ConfidenceBasis:       confidenceBasis,
		Limitations:           limitations,
	}, nil
}

// buildFileLevelResponse handles file-level architecture response (v8.0)
func (e *Engine) buildFileLevelResponse(arch *architecture.ArchitectureResponse, repoState *RepoState, startTime time.Time, maxFiles, maxEdges int, confidenceBasis []ConfidenceBasisItem, limitations []string) (*GetArchitectureResponse, error) {
	confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
		Backend: "import-scan",
		Status:  "available",
	})

	// Convert to response format
	files := convertFileSummaries(arch.Files)
	edges := convertFileEdges(arch.FileDependencies)

	// Apply limits
	var truncationInfo *TruncationInfo
	if len(files) > maxFiles {
		truncationInfo = &TruncationInfo{
			Reason:        "max-files",
			OriginalCount: len(files),
			ReturnedCount: maxFiles,
		}
		limitations = append(limitations, "File count exceeded; showing top files by connectivity")
		files = files[:maxFiles]
	}
	if len(edges) > maxEdges {
		limitations = append(limitations, "Edge count exceeded; showing top edges")
		edges = edges[:maxEdges]
	}

	confidence := 0.80 // Direct import parsing
	if len(limitations) > 0 {
		confidence = 0.70
	}

	completeness := CompletenessInfo{
		Score:  0.95,
		Reason: "import-scan",
	}

	provenance := e.buildProvenance(repoState, "full", startTime, nil, completeness)

	return &GetArchitectureResponse{
		Files:            files,
		FileDependencies: edges,
		Granularity:      "file",
		DetectionMethod:  arch.DetectionMethod,
		Truncated:        truncationInfo != nil,
		TruncationInfo:   truncationInfo,
		Provenance:       provenance,
		Confidence:       confidence,
		ConfidenceBasis:  confidenceBasis,
		Limitations:      limitations,
	}, nil
}

// convertDirectorySummaries converts architecture directory summaries to response format.
func convertDirectorySummaries(archDirs []architecture.DirectorySummary) []DirectorySummary {
	result := make([]DirectorySummary, 0, len(archDirs))
	for _, d := range archDirs {
		summary := DirectorySummary{
			Path:           d.Path,
			FileCount:      d.FileCount,
			SymbolCount:    d.SymbolCount,
			Language:       d.Language,
			LOC:            d.LOC,
			Role:           d.Role,
			HasIndexFile:   d.HasIndexFile,
			IncomingEdges:  d.IncomingEdges,
			OutgoingEdges:  d.OutgoingEdges,
			IsIntermediate: d.IsIntermediate,
		}
		// Copy metrics if present
		if d.Metrics != nil {
			summary.Metrics = &DirectoryMetrics{
				LOC:           d.Metrics.LOC,
				AvgComplexity: d.Metrics.AvgComplexity,
				MaxComplexity: d.Metrics.MaxComplexity,
				LastModified:  d.Metrics.LastModified,
				Churn30d:      d.Metrics.Churn30d,
			}
		}
		result = append(result, summary)
	}
	return result
}

// convertDirectoryEdges converts architecture directory edges to response format.
func convertDirectoryEdges(archEdges []architecture.DirectoryDependencyEdge) []DirectoryDependencyEdge {
	result := make([]DirectoryDependencyEdge, 0, len(archEdges))
	for _, e := range archEdges {
		result = append(result, DirectoryDependencyEdge{
			From:        e.From,
			To:          e.To,
			Kind:        string(e.Kind),
			ImportCount: e.ImportCount,
			Symbols:     e.Symbols,
		})
	}
	return result
}

// convertFileSummaries converts architecture file summaries to response format.
func convertFileSummaries(archFiles []architecture.FileSummary) []FileSummary {
	result := make([]FileSummary, 0, len(archFiles))
	for _, f := range archFiles {
		result = append(result, FileSummary{
			Path:          f.Path,
			Language:      f.Language,
			SymbolCount:   f.SymbolCount,
			LOC:           f.LOC,
			IncomingEdges: f.IncomingEdges,
			OutgoingEdges: f.OutgoingEdges,
		})
	}
	return result
}

// convertFileEdges converts architecture file edges to response format.
func convertFileEdges(archEdges []architecture.FileDependencyEdge) []FileDependencyEdge {
	result := make([]FileDependencyEdge, 0, len(archEdges))
	for _, e := range archEdges {
		result = append(result, FileDependencyEdge{
			From:     e.From,
			To:       e.To,
			Kind:     string(e.Kind),
			Line:     e.Line,
			Resolved: e.Resolved,
		})
	}
	return result
}

// convertModuleSummaries converts architecture module summaries to response format.
func convertModuleSummaries(archModules []architecture.ModuleSummary) []ModuleSummary {
	result := make([]ModuleSummary, 0, len(archModules))

	for _, m := range archModules {
		result = append(result, ModuleSummary{
			ModuleId:    m.ModuleId,
			Name:        m.Name,
			Path:        m.RootPath,
			Language:    m.Language,
			SymbolCount: m.SymbolCount,
			FileCount:   m.FileCount,
		})
	}

	return result
}

// convertArchEdges converts architecture dependency edges to response format.
func convertArchEdges(archEdges []architecture.DependencyEdge, includeExternal bool) []DependencyEdge {
	edges := make([]DependencyEdge, 0, len(archEdges))

	for _, edge := range archEdges {
		// Filter external dependencies if not requested
		kindStr := string(edge.Kind)
		if !includeExternal && kindStr == "external-dependency" {
			continue
		}

		edges = append(edges, DependencyEdge{
			From:     edge.From,
			To:       edge.To,
			Kind:     kindStr,
			Strength: edge.Strength,
		})
	}

	return edges
}

// convertArchEntrypoints converts architecture entrypoints to response format.
func convertArchEntrypoints(archEntrypoints []architecture.Entrypoint) []Entrypoint {
	entrypoints := make([]Entrypoint, 0, len(archEntrypoints))

	for _, ep := range archEntrypoints {
		entrypoints = append(entrypoints, Entrypoint{
			ModuleId: ep.ModuleId,
			FileId:   ep.FileId,
			Kind:     ep.Kind,
			Name:     ep.Name,
		})
	}

	return entrypoints
}

// computeEdgeCounts updates modules with edge counts.
func computeEdgeCounts(modules []ModuleSummary, edges []DependencyEdge) {
	incoming := make(map[string]int)
	outgoing := make(map[string]int)

	for _, edge := range edges {
		outgoing[edge.From]++
		incoming[edge.To]++
	}

	for i := range modules {
		modules[i].IncomingEdges = incoming[modules[i].ModuleId]
		modules[i].OutgoingEdges = outgoing[modules[i].ModuleId]
	}
}

// v6.0 Architectural Memory - RefreshArchitecture

// RefreshArchitectureOptions contains options for refreshArchitecture.
type RefreshArchitectureOptions struct {
	// Scope determines what to refresh: "all", "modules", "ownership", "hotspots", "responsibilities"
	Scope string

	// Force refresh even if data is fresh
	Force bool

	// DryRun previews changes without making them
	DryRun bool

	// Async runs the refresh in the background and returns immediately with a job ID
	Async bool
}

// RefreshArchitectureChanges tracks what was changed during refresh.
type RefreshArchitectureChanges struct {
	ModulesUpdated          int `json:"modulesUpdated,omitempty"`
	ModulesCreated          int `json:"modulesCreated,omitempty"`
	OwnershipUpdated        int `json:"ownershipUpdated,omitempty"`
	HotspotsUpdated         int `json:"hotspotsUpdated,omitempty"`
	ResponsibilitiesUpdated int `json:"responsibilitiesUpdated,omitempty"`
}

// RefreshArchitectureResponse is the response for refreshArchitecture.
type RefreshArchitectureResponse struct {
	CkbVersion    string                      `json:"ckbVersion"`
	SchemaVersion string                      `json:"schemaVersion"`
	Tool          string                      `json:"tool"`
	Status        string                      `json:"status"` // "completed", "skipped", "queued"
	Scope         string                      `json:"scope"`
	Changes       *RefreshArchitectureChanges `json:"changes,omitempty"`
	DurationMs    int64                       `json:"durationMs,omitempty"`
	DryRun        bool                        `json:"dryRun,omitempty"`
	JobId         string                      `json:"jobId,omitempty"` // Set when Async=true
	Warnings      []string                    `json:"warnings,omitempty"`
	Provenance    *Provenance                 `json:"provenance,omitempty"`
}

// RefreshArchitecture rebuilds the architectural model from sources.
// This is a v6.0 heavy operation (up to 30s) that refreshes modules, ownership,
// hotspots, and/or responsibilities based on the specified scope.
func (e *Engine) RefreshArchitecture(ctx context.Context, opts RefreshArchitectureOptions) (*RefreshArchitectureResponse, error) {
	startTime := time.Now()

	// Default scope
	if opts.Scope == "" {
		opts.Scope = "all"
	}

	// Validate scope
	validScopes := map[string]bool{
		"all":              true,
		"modules":          true,
		"ownership":        true,
		"hotspots":         true,
		"responsibilities": true,
	}
	if !validScopes[opts.Scope] {
		return nil, e.wrapError(nil, errors.ScopeInvalid)
	}

	// Handle async mode - queue job and return immediately
	if opts.Async {
		return e.queueRefreshJob(opts)
	}

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "full")
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	changes := &RefreshArchitectureChanges{}
	var warnings []string

	// If dry run, just return what would be refreshed
	if opts.DryRun {
		return &RefreshArchitectureResponse{
			CkbVersion:    "6.0",
			SchemaVersion: "6.0",
			Tool:          "refreshArchitecture",
			Status:        "skipped",
			Scope:         opts.Scope,
			Changes:       changes,
			DurationMs:    time.Since(startTime).Milliseconds(),
			DryRun:        true,
			Warnings:      []string{"Dry run - no changes made"},
			Provenance: &Provenance{
				RepoStateId:     repoState.RepoStateId,
				RepoStateDirty:  repoState.Dirty,
				QueryDurationMs: time.Since(startTime).Milliseconds(),
			},
		}, nil
	}

	// Refresh modules if requested
	if opts.Scope == "all" || opts.Scope == "modules" {
		// Re-detect modules
		importScanner := modules.NewImportScanner(&e.config.ImportScan, e.logger)
		generator := architecture.NewArchitectureGenerator(e.repoRoot, e.config, importScanner, e.logger)

		genOpts := &architecture.GeneratorOptions{
			Refresh: true,
		}

		_, genErr := generator.Generate(ctx, repoState.RepoStateId, genOpts)
		if genErr != nil {
			warnings = append(warnings, "Module refresh had errors: "+genErr.Error())
		} else {
			changes.ModulesUpdated = 1 // Placeholder - would count actual changes
		}
	}

	// Refresh ownership if requested
	if opts.Scope == "all" || opts.Scope == "ownership" {
		codeownersPath := ownership.FindCodeownersFile(e.repoRoot)
		if codeownersPath == "" {
			warnings = append(warnings, "No CODEOWNERS file found; ownership refresh skipped")
		} else {
			codeownersFile, parseErr := ownership.ParseCodeownersFile(codeownersPath)
			if parseErr != nil {
				warnings = append(warnings, "Failed to parse CODEOWNERS: "+parseErr.Error())
			} else {
				// Re-compute ownership for each module discovered via architecture
				archOpts := GetArchitectureOptions{Depth: 2}
				archResp, archErr := e.GetArchitecture(ctx, archOpts)
				if archErr != nil {
					warnings = append(warnings, "Cannot list modules for ownership refresh: "+archErr.Error())
				} else {
					ownershipCount := 0
					blameConfig := ownership.DefaultBlameConfig()
					for _, m := range archResp.Modules {
						_ = codeownersFile.GetOwnersForPath(m.Path)
						// Run git-blame on module root path (best-effort)
						_, _ = ownership.GetFileOwnership(e.repoRoot, m.Path, codeownersFile, blameConfig)
						ownershipCount++
					}
					changes.OwnershipUpdated = ownershipCount
				}
			}
		}
	}

	// Refresh hotspots if requested
	if opts.Scope == "all" || opts.Scope == "hotspots" {
		if e.gitAdapter == nil || !e.gitAdapter.IsAvailable() {
			warnings = append(warnings, "Git backend unavailable; hotspot refresh skipped")
		} else {
			since := time.Now().AddDate(0, 0, -90).Format("2006-01-02")
			gitHotspots, hotErr := e.gitAdapter.GetHotspots(100, since)
			if hotErr != nil {
				warnings = append(warnings, "Hotspot refresh failed: "+hotErr.Error())
			} else {
				changes.HotspotsUpdated = len(gitHotspots)
			}
		}
	}

	// Refresh responsibilities if requested
	if opts.Scope == "all" || opts.Scope == "responsibilities" {
		extractor := responsibilities.NewExtractor(e.repoRoot)

		archOpts := GetArchitectureOptions{Depth: 2}
		archResp, archErr := e.GetArchitecture(ctx, archOpts)
		if archErr != nil {
			warnings = append(warnings, "Cannot list modules for responsibility refresh: "+archErr.Error())
		} else {
			respCount := 0
			for _, m := range archResp.Modules {
				_, extErr := extractor.ExtractFromModule(m.Path)
				if extErr != nil {
					continue
				}
				respCount++
			}
			changes.ResponsibilitiesUpdated = respCount
		}
	}

	durationMs := time.Since(startTime).Milliseconds()

	return &RefreshArchitectureResponse{
		CkbVersion:    "6.0",
		SchemaVersion: "6.0",
		Tool:          "refreshArchitecture",
		Status:        "completed",
		Scope:         opts.Scope,
		Changes:       changes,
		DurationMs:    durationMs,
		Warnings:      warnings,
		Provenance: &Provenance{
			RepoStateId:     repoState.RepoStateId,
			RepoStateDirty:  repoState.Dirty,
			QueryDurationMs: durationMs,
		},
	}, nil
}

// queueRefreshJob creates a job for async refresh and returns immediately.
func (e *Engine) queueRefreshJob(opts RefreshArchitectureOptions) (*RefreshArchitectureResponse, error) {
	if e.jobRunner == nil {
		return nil, e.wrapError(
			fmt.Errorf("job runner not available"),
			errors.BackendUnavailable,
		)
	}

	// Create job with scope
	scope := &jobs.RefreshScope{
		Scope: opts.Scope,
		Force: opts.Force,
	}

	job, err := jobs.NewJob(jobs.JobTypeRefreshArchitecture, scope)
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	// Submit to runner
	if err := e.jobRunner.Submit(job); err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	return &RefreshArchitectureResponse{
		CkbVersion:    "6.0",
		SchemaVersion: "6.0",
		Tool:          "refreshArchitecture",
		Status:        "queued",
		Scope:         opts.Scope,
		JobId:         job.ID,
		Warnings:      []string{"Job queued for async processing"},
	}, nil
}
