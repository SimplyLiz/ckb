package query

import (
	"context"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/cycles"
	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// FindCyclesOptions configures cycle detection.
type FindCyclesOptions struct {
	Granularity string // module, directory, file (default: directory)
	TargetPath  string // optional path to focus on
	MaxCycles   int    // max cycles to return (default: 20)
}

// CycleSummary provides aggregate stats about detected cycles.
type CycleSummary struct {
	HighSeverity   int     `json:"highSeverity"`
	MediumSeverity int     `json:"mediumSeverity"`
	LowSeverity    int     `json:"lowSeverity"`
	LargestCycle   int     `json:"largestCycle"`
	AvgCycleSize   float64 `json:"avgCycleSize"`
}

// FindCyclesResponse is the response for findCycles.
type FindCyclesResponse struct {
	AINavigationMeta
	Cycles      []cycles.Cycle `json:"cycles"`
	TotalCycles int            `json:"totalCycles"`
	Granularity string         `json:"granularity"`
	Summary     *CycleSummary  `json:"summary"`
}

// FindCycles detects circular dependencies in the codebase dependency graph.
func (e *Engine) FindCycles(ctx context.Context, opts FindCyclesOptions) (*FindCyclesResponse, error) {
	startTime := time.Now()

	// Defaults
	if opts.Granularity == "" {
		opts.Granularity = "directory"
	}
	if opts.MaxCycles <= 0 {
		opts.MaxCycles = 20
	}

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "full")
	if err != nil {
		return nil, err
	}

	// File cycles come from the SCIP import graph when the index covers a
	// language whose import kinds CKB reads: the architecture's file graph
	// holds unresolved module strings and finds none. Only static imports
	// count; type-only and lazy ones cannot form a cycle at load time.
	var result *cycles.DetectResult
	reason := "dependency-graph-cycles"
	if opts.Granularity == "file" && e.hasConsumerGraph() {
		if g := fileImportGraph(e.scipAdapter.GetIndex(), e.repoRoot); g != nil && len(g.languages) > 0 {
			result = runtimeCyclesUnder(g, opts.TargetPath, opts.MaxCycles)
			reason = "scip-import-graph-runtime-cycles"
		}
	}

	if result == nil {
		// Get architecture at the requested granularity
		archResp, err := e.GetArchitecture(ctx, GetArchitectureOptions{
			Granularity:  opts.Granularity,
			TargetPath:   opts.TargetPath,
			InferModules: true,
		})
		if err != nil {
			return nil, err
		}

		// Extract nodes and adjacency from architecture response
		nodes, adjacency, edgeMeta := extractGraph(archResp, opts.Granularity)

		// Run cycle detection
		detector := cycles.NewDetector()
		result = detector.Detect(nodes, adjacency, edgeMeta, cycles.DetectOptions{
			MaxCycles: opts.MaxCycles,
		})
	}
	result.Granularity = opts.Granularity

	// Build summary
	summary := buildCycleSummary(result.Cycles)

	// Build provenance
	var backendContribs []BackendContribution
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		backendContribs = append(backendContribs, BackendContribution{
			BackendId: "scip", Available: true, Used: true,
		})
	}

	resp := &FindCyclesResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    version.Version,
			SchemaVersion: 1,
			Tool:          "findCycles",
			Provenance: e.buildProvenance(repoState, "full", startTime, backendContribs, CompletenessInfo{
				Score:  0.85,
				Reason: reason,
			}),
		},
		Cycles:      result.Cycles,
		TotalCycles: result.TotalCycles,
		Granularity: opts.Granularity,
		Summary:     summary,
	}

	return resp, nil
}

// runtimeCyclesUnder returns the graph's runtime cycles with a file under
// target (all when target is empty), at most maxCycles.
func runtimeCyclesUnder(g *importGraph, target string, maxCycles int) *cycles.DetectResult {
	target = strings.TrimSuffix(target, "/")
	var kept []cycles.Cycle
	for _, c := range g.runtimeCycles() {
		if target != "" {
			in := false
			for _, n := range c.Nodes {
				if n == target || strings.HasPrefix(n, target+"/") {
					in = true
					break
				}
			}
			if !in {
				continue
			}
		}
		kept = append(kept, c)
	}
	res := &cycles.DetectResult{Cycles: kept, TotalCycles: len(kept)}
	if maxCycles > 0 && len(kept) > maxCycles {
		res.Cycles = kept[:maxCycles]
	}
	return res
}

// extractGraph extracts nodes, adjacency list, and edge metadata from an architecture response.
func extractGraph(arch *GetArchitectureResponse, granularity string) ([]string, map[string][]string, map[[2]string]cycles.EdgeMeta) {
	adjacency := make(map[string][]string)
	edgeMeta := make(map[[2]string]cycles.EdgeMeta)
	var nodes []string

	switch granularity {
	case "module":
		for _, m := range arch.Modules {
			nodes = append(nodes, m.ModuleId)
		}
		for _, e := range arch.DependencyGraph {
			adjacency[e.From] = append(adjacency[e.From], e.To)
			edgeMeta[[2]string{e.From, e.To}] = cycles.EdgeMeta{Strength: e.Strength}
		}

	case "directory":
		for _, d := range arch.Directories {
			nodes = append(nodes, d.Path)
		}
		for _, e := range arch.DirectoryDependencies {
			adjacency[e.From] = append(adjacency[e.From], e.To)
			edgeMeta[[2]string{e.From, e.To}] = cycles.EdgeMeta{Strength: e.ImportCount}
		}

	case "file":
		for _, f := range arch.Files {
			nodes = append(nodes, f.Path)
		}
		for _, e := range arch.FileDependencies {
			adjacency[e.From] = append(adjacency[e.From], e.To)
			strength := 1
			if e.Resolved {
				strength = 2
			}
			edgeMeta[[2]string{e.From, e.To}] = cycles.EdgeMeta{Strength: strength}
		}
	}

	return nodes, adjacency, edgeMeta
}

// buildCycleSummary computes aggregate stats from detected cycles.
func buildCycleSummary(detectedCycles []cycles.Cycle) *CycleSummary {
	summary := &CycleSummary{}
	if len(detectedCycles) == 0 {
		return summary
	}

	totalSize := 0
	for _, c := range detectedCycles {
		totalSize += c.Size
		if c.Size > summary.LargestCycle {
			summary.LargestCycle = c.Size
		}
		switch c.Severity {
		case "high":
			summary.HighSeverity++
		case "medium":
			summary.MediumSeverity++
		case "low":
			summary.LowSeverity++
		}
	}
	summary.AvgCycleSize = float64(totalSize) / float64(len(detectedCycles))

	return summary
}
