package query

import (
	"context"
	"fmt"
	"sort"

	"github.com/SimplyLiz/CodeMCP/internal/backends/git"
	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
	"github.com/SimplyLiz/CodeMCP/internal/coupling"
)

// PRSplitSuggestion contains the result of PR split analysis.
type PRSplitSuggestion struct {
	ShouldSplit     bool        `json:"shouldSplit"`
	Reason          string      `json:"reason"`
	Clusters        []PRCluster `json:"clusters"`
	EstimatedSaving string      `json:"estimatedSaving,omitempty"` // e.g., "6h → 3×2h"
}

// PRCluster represents a group of files that belong together.
type PRCluster struct {
	Name        string   `json:"name"`
	Files       []string `json:"files"`
	FileCount   int      `json:"fileCount"`
	Additions   int      `json:"additions"`
	Deletions   int      `json:"deletions"`
	Independent bool     `json:"independent"`         // Can be reviewed/merged independently
	DependsOn   []int    `json:"dependsOn,omitempty"` // Indices of clusters this depends on
	Languages   []string `json:"languages,omitempty"`
}

// suggestPRSplit analyzes the changeset and groups files into independent clusters.
// Uses module affinity, coupling data, and connected component analysis.
func (e *Engine) suggestPRSplit(ctx context.Context, diffStats []git.DiffStats, policy *ReviewPolicy) *PRSplitSuggestion {
	if policy.SplitThreshold <= 0 || len(diffStats) < policy.SplitThreshold {
		return nil
	}

	files := make([]string, len(diffStats))
	statsMap := make(map[string]git.DiffStats)
	for i, ds := range diffStats {
		files[i] = ds.FilePath
		statsMap[ds.FilePath] = ds
	}

	// Build adjacency graph: files are connected if they share a module
	// or have high coupling correlation / dependency edge
	adj := make(map[string]map[string]bool)
	for _, f := range files {
		adj[f] = make(map[string]bool)
	}

	// Connect files in the same module
	fileToModule := make(map[string]string)
	moduleFiles := make(map[string][]string)
	for _, f := range files {
		mod := e.resolveFileModule(f)
		fileToModule[f] = mod
		if mod != "" {
			moduleFiles[mod] = append(moduleFiles[mod], f)
		}
	}
	for _, group := range moduleFiles {
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				adj[group[i]][group[j]] = true
				adj[group[j]][group[i]] = true
			}
		}
	}

	// Connect files with dependency or coupling edges.
	// Cartographer uses the static import graph + git co-change in a single pass.
	// Fall back to the per-file coupling analyzer for non-Cartographer builds;
	// skip for very large PRs where the O(n) cost is prohibitive.
	if cartographer.Available() {
		e.addCartographerEdges(files, adj)
	} else if len(diffStats) <= 200 {
		e.addCouplingEdges(ctx, files, adj)
	}

	// Find connected components using BFS
	visited := make(map[string]bool)
	var components [][]string

	for _, f := range files {
		if visited[f] {
			continue
		}
		component := bfs(f, adj, visited)
		components = append(components, component)
	}

	const maxClusters = 20
	if len(components) > maxClusters {
		// Merge smallest clusters into an "other" bucket
		sort.Slice(components, func(i, j int) bool {
			return len(components[i]) > len(components[j])
		})
		var other []string
		for i := maxClusters - 1; i < len(components); i++ {
			other = append(other, components[i]...)
		}
		components = append(components[:maxClusters-1], other)
	}

	if len(components) <= 1 {
		return &PRSplitSuggestion{
			ShouldSplit: false,
			Reason:      "All files are interconnected — no independent clusters found",
		}
	}

	// Build clusters with metadata
	clusters := make([]PRCluster, 0, len(components))
	for _, comp := range components {
		c := buildCluster(comp, statsMap, fileToModule)
		clusters = append(clusters, c)
	}

	// Sort by file count descending
	sort.Slice(clusters, func(i, j int) bool {
		return clusters[i].FileCount > clusters[j].FileCount
	})

	// Name unnamed clusters
	for i := range clusters {
		if clusters[i].Name == "" {
			clusters[i].Name = fmt.Sprintf("Cluster %d", i+1)
		}
		clusters[i].Independent = true // Connected components are independent by definition
	}

	return &PRSplitSuggestion{
		ShouldSplit: true,
		Reason:      fmt.Sprintf("%d files across %d independent clusters — split recommended", len(files), len(clusters)),
		Clusters:    clusters,
	}
}

// addCartographerEdges enriches the adjacency graph using Cartographer's
// static import graph and git co-change pairs — a single pass per data source
// instead of O(n) git subprocess calls.
func (e *Engine) addCartographerEdges(files []string, adj map[string]map[string]bool) {
	fileSet := make(map[string]bool, len(files))
	for _, f := range files {
		fileSet[f] = true
	}

	// Static import edges from the dependency graph. Skip fuzzy (low-confidence)
	// edges so a split recommendation isn't driven by a fabricated dependency.
	if graph, err := cartographer.MapProject(e.repoRoot); err == nil {
		for _, edge := range graph.Edges {
			if edge.Resolution == "fuzzy" {
				continue
			}
			if fileSet[edge.Source] && fileSet[edge.Target] {
				adj[edge.Source][edge.Target] = true
				adj[edge.Target][edge.Source] = true
			}
		}
	}

	// Temporal coupling edges (co-change ≥ 0.5, strong signal only).
	if pairs, err := cartographer.GitCochange(e.repoRoot, 0, minHiddenCoChanges); err == nil {
		for _, p := range strongCochangePairs(pairs, 0.5) {
			if fileSet[p.FileA] && fileSet[p.FileB] {
				adj[p.FileA][p.FileB] = true
				adj[p.FileB][p.FileA] = true
			}
		}
	}
}

// addCouplingEdges enriches the adjacency graph with coupling data.
func (e *Engine) addCouplingEdges(ctx context.Context, files []string, adj map[string]map[string]bool) {
	analyzer := coupling.NewAnalyzer(e.repoRoot, e.logger)

	fileSet := make(map[string]bool)
	for _, f := range files {
		fileSet[f] = true
	}

	// Limit coupling lookups for performance
	limit := 20
	if len(files) < limit {
		limit = len(files)
	}

	for _, f := range files[:limit] {
		if ctx.Err() != nil {
			break
		}
		result, err := analyzer.Analyze(ctx, coupling.AnalyzeOptions{
			RepoRoot:       e.repoRoot,
			Target:         f,
			MinCorrelation: 0.5, // Higher threshold — only strong connections matter for split
			Limit:          10,
		})
		if err != nil {
			continue
		}
		for _, corr := range result.Correlations {
			if fileSet[corr.File] {
				adj[f][corr.File] = true
				adj[corr.File][f] = true
			}
		}
	}
}

// bfs performs breadth-first search to find a connected component.
func bfs(start string, adj map[string]map[string]bool, visited map[string]bool) []string {
	queue := []string{start}
	visited[start] = true
	var component []string

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		component = append(component, node)

		for neighbor := range adj[node] {
			if !visited[neighbor] {
				visited[neighbor] = true
				queue = append(queue, neighbor)
			}
		}
	}
	return component
}

// buildCluster creates a PRCluster from a list of files.
func buildCluster(files []string, statsMap map[string]git.DiffStats, fileToModule map[string]string) PRCluster {
	adds, dels := 0, 0
	moduleCounts := make(map[string]int)
	langSet := make(map[string]bool)

	for _, f := range files {
		if ds, ok := statsMap[f]; ok {
			adds += ds.Additions
			dels += ds.Deletions
		}
		if mod := fileToModule[f]; mod != "" {
			moduleCounts[mod]++
		}
		if lang := detectLanguage(f); lang != "" {
			langSet[lang] = true
		}
	}

	// Name by dominant module
	name := ""
	maxCount := 0
	for mod, count := range moduleCounts {
		if count > maxCount {
			maxCount = count
			name = mod
		}
	}

	var langs []string
	for l := range langSet {
		langs = append(langs, l)
	}
	sort.Strings(langs)

	return PRCluster{
		Name:      name,
		Files:     files,
		FileCount: len(files),
		Additions: adds,
		Deletions: dels,
		Languages: langs,
	}
}
