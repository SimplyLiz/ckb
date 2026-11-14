package query

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/git"
	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
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

	// Connect files that reference or import each other, or change together.
	e.addSplitEdges(ctx, files, adj)

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

// addSplitEdges joins changed files that depend on each other — one
// references a symbol the other defines (SCIP, any language) or imports it
// (TS/Python import graph) — or that change together (co-change score >= 0.5,
// strong signal only). One pass over the index and one cached history, so
// large PRs need no cap.
func (e *Engine) addSplitEdges(ctx context.Context, files []string, adj map[string]map[string]bool) {
	fileSet := make(map[string]bool, len(files))
	for _, f := range files {
		fileSet[f] = true
	}
	link := func(a, b string) {
		if a != b && fileSet[a] && fileSet[b] {
			adj[a][b] = true
			adj[b][a] = true
		}
	}

	if e.hasConsumerGraph() {
		idx := e.scipAdapter.GetIndex()
		for _, f := range files {
			doc := idx.DocumentsByPath[f]
			if doc == nil {
				continue
			}
			for _, occ := range doc.Occurrences {
				if occ == nil || occ.SymbolRoles&scip.SymbolRoleDefinition != 0 || strings.HasPrefix(occ.Symbol, "local ") {
					continue
				}
				if def := idx.DefinitionIndex[occ.Symbol]; def != nil && def.Doc != nil {
					link(f, def.Doc.RelativePath)
				}
			}
		}
		if g := fileImportGraph(idx, e.repoRoot); g != nil {
			for _, f := range files {
				for to := range g.edges[f] {
					link(f, to)
				}
			}
		}
	}

	for _, f := range files {
		if ctx.Err() != nil {
			return
		}
		for _, p := range e.cochangePartners(ctx, f, 0.5, 0, nil) {
			link(p.FileA, p.FileB)
		}
	}
}

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
