package graph

import (
	"context"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// EdgeWeights defines weights for different edge types.
type EdgeWeights struct {
	Call       float64 // Caller -> Callee
	Reference  float64 // Symbol -> Referenced symbol
	Definition float64 // Reference -> Definition
	TypeOf     float64 // Instance -> Type
	Implements float64 // Type -> Interface
	Module     float64 // Same-module symbols
}

// DefaultEdgeWeights returns sensible defaults for edge weights.
func DefaultEdgeWeights() EdgeWeights {
	return EdgeWeights{
		Call:       1.0,
		Reference:  0.8,
		Definition: 0.9,
		TypeOf:     0.6,
		Implements: 0.7,
		Module:     0.3,
	}
}

// BuildFromSCIP constructs a graph from a SCIP index.
// This extracts call relationships and references to build the symbol graph.
func BuildFromSCIP(ctx context.Context, idx *scip.SCIPIndex, weights EdgeWeights) (*Graph, error) {
	g := NewGraph()

	if idx == nil {
		return g, nil
	}

	// Build a map of symbol definitions
	symbolDefs := make(map[string]*scip.Location)
	for _, doc := range idx.Documents {
		for _, occ := range doc.Occurrences {
			if occ.SymbolRoles&scip.SymbolRoleDefinition != 0 {
				symbolDefs[occ.Symbol] = &scip.Location{
					FileId:    doc.RelativePath,
					StartLine: int(occ.Range[0]), // #nosec G115 -- SCIP int32 fits in int
					EndLine:   int(occ.Range[0]), // #nosec G115 -- SCIP int32 fits in int
				}
			}
		}
	}

	// Extract edges from occurrences
	for _, doc := range idx.Documents {
		// Group occurrences by container (function/method)
		var currentContainer string

		for _, occ := range doc.Occurrences {
			// Try to determine container from symbol hierarchy
			container := extractContainer(occ.Symbol)
			if container != "" && occ.SymbolRoles&scip.SymbolRoleDefinition != 0 {
				currentContainer = occ.Symbol
			}

			// Add reference edges
			if occ.SymbolRoles&scip.SymbolRoleDefinition == 0 {
				// This is a reference (not a definition)
				refSymbol := occ.Symbol

				// Add reference edge from referencing location to referenced symbol
				if currentContainer != "" && currentContainer != refSymbol {
					// Determine edge type
					weight := weights.Reference
					kind := "reference"

					// A reference to something callable is a call
					if idx.IsCallable(refSymbol) {
						weight = weights.Call
						kind = "call"
					}

					g.AddEdge(currentContainer, refSymbol, weight, kind)
				}
			}
		}
	}

	// Add module-level edges (symbols in same module are weakly connected)
	moduleSymbols := make(map[string][]string)
	for symbol := range symbolDefs {
		module := extractModule(symbol)
		if module != "" {
			moduleSymbols[module] = append(moduleSymbols[module], symbol)
		}
	}

	for _, symbols := range moduleSymbols {
		// Only add module edges for small modules to avoid explosion
		if len(symbols) <= 50 {
			for i, s1 := range symbols {
				for j := i + 1; j < len(symbols); j++ {
					s2 := symbols[j]
					g.AddEdge(s1, s2, weights.Module, "module")
					g.AddEdge(s2, s1, weights.Module, "module")
				}
			}
		}
	}

	return g, nil
}

// BuildFromCallGraph constructs a graph from call graph data.
func BuildFromCallGraph(cg *scip.CallGraph, weights EdgeWeights) *Graph {
	g := NewGraph()

	if cg == nil || cg.Root == nil {
		return g
	}

	// Add root
	g.AddNode(cg.Root.SymbolID)

	// Add edges from call graph
	for _, edge := range cg.Edges {
		weight := weights.Call
		if edge.Kind == "reference" {
			weight = weights.Reference
		}
		g.AddEdge(edge.From, edge.To, weight, edge.Kind)
	}

	return g
}

// extractContainer extracts the containing symbol from a symbol ID.
// For example: `package/module`/Class#method() -> `package/module`/Class#
func extractContainer(symbol string) string {
	// Look for method/function indicators
	for i := len(symbol) - 1; i >= 0; i-- {
		if symbol[i] == '#' || symbol[i] == '.' {
			// Found potential container boundary
			if i > 0 {
				return symbol[:i+1]
			}
			break
		}
		if symbol[i] == '/' {
			// Hit module boundary without finding container
			break
		}
	}
	return ""
}

// extractModule extracts the module path from a symbol ID.
func extractModule(symbol string) string {
	// Find the backtick-enclosed module path
	start := -1
	for i, c := range symbol {
		if c == '`' {
			if start < 0 {
				start = i
			} else {
				return symbol[start : i+1]
			}
		}
	}
	return ""
}

// SymbolGraphStats returns statistics about the graph.
type SymbolGraphStats struct {
	TotalNodes   int     `json:"totalNodes"`
	TotalEdges   int     `json:"totalEdges"`
	CallEdges    int     `json:"callEdges"`
	RefEdges     int     `json:"refEdges"`
	ModuleEdges  int     `json:"moduleEdges"`
	AvgOutDegree float64 `json:"avgOutDegree"`
}

// Stats returns statistics about the graph.
func (g *Graph) Stats() SymbolGraphStats {
	stats := SymbolGraphStats{
		TotalNodes: g.numNodes,
		TotalEdges: g.NumEdges(),
	}

	// Count edge types
	for from, targets := range g.edgeKinds {
		_ = from
		for _, kind := range targets {
			switch kind {
			case "call":
				stats.CallEdges++
			case "reference":
				stats.RefEdges++
			case "module":
				stats.ModuleEdges++
			}
		}
	}

	if g.numNodes > 0 {
		stats.AvgOutDegree = float64(stats.TotalEdges) / float64(g.numNodes)
	}

	return stats
}
