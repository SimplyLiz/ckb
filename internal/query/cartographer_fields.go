package query

import "github.com/SimplyLiz/CodeMCP/internal/cartographer"

// A Cartographer library that serializes a struct in snake_case while
// types.go expects camelCase (v4.0.3 and older) decodes without error into
// zero values. Shipping those as data says "risk level empty, no affected
// modules, health 0" about a change nobody analyzed. The helpers here keep a
// Cartographer result only when the fields that identify it decoded.

// usableArchImpact returns the simulation, or nil when it failed or its
// TargetModule decoded empty.
func usableArchImpact(ai *cartographer.ImpactAnalysis, err error) *cartographer.ImpactAnalysis {
	if err != nil || ai == nil || ai.TargetModule == "" {
		return nil
	}
	return ai
}

// usableGraph returns the project graph, or nil when MapProject failed or the
// graph's metadata decoded empty (no health score, no file count): then the
// god modules, bridges and per-node fields are zero values too.
func usableGraph(g *cartographer.ProjectGraph, err error) *cartographer.ProjectGraph {
	if err != nil || g == nil || g.Metadata.HealthScore == nil || g.Metadata.TotalFiles == 0 {
		return nil
	}
	return g
}
