package query

import (
	"context"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/architecture"
	"github.com/SimplyLiz/CodeMCP/internal/modules"
	"github.com/SimplyLiz/CodeMCP/internal/unwired"
)

// FindUnwiredModulesOptions configures the unwired module detection.
type FindUnwiredModulesOptions struct {
	Scope           []string `json:"scope,omitempty"`
	ExcludePatterns []string `json:"excludePatterns,omitempty"`
	MaxNodes        int      `json:"maxNodes"`
	MinConfidence   float64  `json:"minConfidence"`
	IncludeTypes    bool     `json:"includeTypes"`
	Limit           int      `json:"limit"`
}

// FindUnwiredModulesResponse is the response from unwired module detection.
type FindUnwiredModulesResponse struct {
	UnwiredModules []unwired.UnwiredModule `json:"unwiredModules"`
	Summary        unwired.Summary         `json:"summary"`
	Entrypoints    []string                `json:"entrypoints"`
	ReachableCount int                     `json:"reachableCount"`
	Partial        bool                    `json:"partial,omitempty"`
	Truncated      bool                    `json:"truncated,omitempty"`
	Provenance     *Provenance             `json:"provenance,omitempty"`
}

// FindUnwiredModules detects exported symbols that are never transitively
// reachable from application entrypoints. This catches the "built but never
// plugged in" pattern where modules exist and are tested but aren't wired
// into the main request/execution pipeline.
func (e *Engine) FindUnwiredModules(ctx context.Context, opts FindUnwiredModulesOptions) (*FindUnwiredModulesResponse, error) {
	startTime := time.Now()

	// Apply defaults
	if opts.MinConfidence == 0 {
		opts.MinConfidence = 0.80
	}
	if opts.Limit == 0 {
		opts.Limit = 100
	}
	if opts.MaxNodes == 0 {
		opts.MaxNodes = 100000
	}

	// Check SCIP availability
	if e.scipAdapter == nil || !e.scipAdapter.IsAvailable() {
		return &FindUnwiredModulesResponse{
			Summary: unwired.Summary{ByKind: make(map[string]int)},
		}, nil
	}

	// Detect entrypoints via architecture module
	importScanner := modules.NewImportScanner(&e.config.ImportScan, e.logger)
	generator := architecture.NewArchitectureGenerator(e.repoRoot, e.config, importScanner, e.logger)

	repoState, _ := e.GetRepoState(ctx, "head")
	stateID := ""
	if repoState != nil {
		stateID = repoState.RepoStateId
	}

	arch, err := generator.Generate(ctx, stateID, &architecture.GeneratorOptions{})
	if err != nil {
		return nil, err
	}

	entrypoints := arch.Entrypoints

	// Filter to non-test entrypoints
	var appEntrypoints []architecture.Entrypoint
	seen := make(map[string]bool)
	for _, ep := range entrypoints {
		if ep.Kind != architecture.EntrypointTest && !seen[ep.FileId] {
			seen[ep.FileId] = true
			appEntrypoints = append(appEntrypoints, ep)
		}
	}

	// Also detect entrypoints from package.json scripts (e.g., "dev": "bun run src/server.ts")
	detector := unwired.NewDetector(e.scipAdapter, e.repoRoot, e.logger)
	scriptEPs := detector.DetectScriptEntrypoints()
	for _, ep := range scriptEPs {
		if !seen[ep.FileId] {
			seen[ep.FileId] = true
			appEntrypoints = append(appEntrypoints, ep)
		}
	}

	var entrypointNames []string
	for _, ep := range appEntrypoints {
		entrypointNames = append(entrypointNames, ep.FileId)
	}

	if len(appEntrypoints) == 0 {
		return &FindUnwiredModulesResponse{
			Summary:     unwired.Summary{ByKind: make(map[string]int)},
			Entrypoints: entrypointNames,
		}, nil
	}

	// Build reachable set via BFS from entrypoints
	reachable, partial := detector.BuildReachableSet(ctx, appEntrypoints, opts.MaxNodes)

	// Analyze: find exported symbols not in the reachable set
	detOpts := unwired.DetectorOptions{
		Scope:           opts.Scope,
		ExcludePatterns: opts.ExcludePatterns,
		MaxNodes:        opts.MaxNodes,
		MinConfidence:   opts.MinConfidence,
		IncludeTypes:    opts.IncludeTypes,
		Limit:           opts.Limit,
	}

	result, err := detector.Analyze(ctx, detOpts, reachable)
	if err != nil {
		return nil, err
	}

	// Build provenance
	prov := e.buildProvenance(repoState, "head", startTime, nil, CompletenessInfo{})

	return &FindUnwiredModulesResponse{
		UnwiredModules: result.UnwiredModules,
		Summary:        result.Summary,
		Entrypoints:    entrypointNames,
		ReachableCount: result.ReachableCount,
		Partial:        partial,
		Truncated:      result.Truncated,
		Provenance:     prov,
	}, nil
}
