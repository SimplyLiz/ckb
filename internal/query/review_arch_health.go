package query

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
	"github.com/SimplyLiz/CodeMCP/internal/cycles"
)

// ArchCycle is one runtime import cycle reported by the arch-health check.
type ArchCycle struct {
	Files   []string `json:"files"`
	Changed []string `json:"changed"` // files of the cycle this change touches
}

// ArchHealthDetails is the Details payload of the arch-health check.
type ArchHealthDetails struct {
	Cycles        []ArchCycle `json:"cycles,omitempty"`
	ProjectCycles int         `json:"projectCycles"`
	Languages     []string    `json:"languages,omitempty"` // languages whose imports were checked
}

// checkArchitecturalHealth reports runtime import cycles the change takes part
// in, from the SCIP import graph (see import_graph.go), and — when
// Cartographer is compiled in — god modules and layer violations.
//
// A cycle is reported only when the change touches one of its files: a cycle
// elsewhere in the project is no finding about this change, and reported on
// every review it was noise nobody acted on.
func (e *Engine) checkArchitecturalHealth(_ context.Context, changedFiles []string) (ReviewCheck, []ReviewFinding) {
	start := time.Now()
	var findings []ReviewFinding
	var parts []string
	details := ArchHealthDetails{}
	ran := false

	if e.hasConsumerGraph() {
		if g := fileImportGraph(e.scipAdapter.GetIndex(), e.repoRoot); g != nil && len(g.languages) > 0 {
			ran = true
			all := g.runtimeCycles()
			details.ProjectCycles = len(all)
			details.Languages = g.checkedLanguages()
			details.Cycles, findings = cycleFindings(all, changedFiles)
			switch {
			case len(details.Cycles) > 0:
				parts = append(parts, fmt.Sprintf("change is part of %d import cycle(s)", len(details.Cycles)))
			case len(all) > 0:
				parts = append(parts, fmt.Sprintf("no import cycle through changed files (%d elsewhere)", len(all)))
			default:
				parts = append(parts, "no import cycles")
			}
		}
	}

	if cartographer.Available() {
		if report, err := cartographer.Health(e.repoRoot); err == nil {
			ran = true
			parts = append(parts, fmt.Sprintf("health %.0f/100", report.HealthScore))
			if report.GodModuleCount > 0 {
				findings = append(findings, ReviewFinding{
					Check:    "arch-health",
					Severity: "warning",
					Message:  fmt.Sprintf("%d god module(s) detected (excessively connected)", report.GodModuleCount),
					Category: "architecture",
					RuleID:   "ckb/arch-health/god-modules",
				})
			}
			if report.LayerViolationCount > 0 {
				findings = append(findings, ReviewFinding{
					Check:    "arch-health",
					Severity: "warning",
					Message:  fmt.Sprintf("%d architectural layer violation(s) in project", report.LayerViolationCount),
					Category: "architecture",
					RuleID:   "ckb/arch-health/layer-violations",
				})
			}
		}
	}

	if !ran {
		return ReviewCheck{
			Name:     "arch-health",
			Status:   "skip",
			Severity: "info",
			Summary:  "No import graph: needs a SCIP index of TypeScript/JavaScript or Python",
			Duration: time.Since(start).Milliseconds(),
		}, nil
	}

	status := "pass"
	if len(findings) > 0 {
		status = "warn"
	}
	return ReviewCheck{
		Name:     "arch-health",
		Status:   status,
		Severity: "warning",
		Summary:  "Architecture: " + strings.Join(parts, ", "),
		Details:  details,
		Duration: time.Since(start).Milliseconds(),
	}, findings
}

// cycleFindings keeps the cycles with a changed file and makes one finding
// per cycle, on its first changed file.
func cycleFindings(all []cycles.Cycle, changedFiles []string) ([]ArchCycle, []ReviewFinding) {
	changed := make(map[string]bool, len(changedFiles))
	for _, f := range changedFiles {
		changed[f] = true
	}
	var out []ArchCycle
	var findings []ReviewFinding
	for _, c := range all {
		var touched []string
		for _, n := range c.Nodes {
			if changed[n] {
				touched = append(touched, n)
			}
		}
		if len(touched) == 0 {
			continue
		}
		out = append(out, ArchCycle{Files: c.Nodes, Changed: touched})
		suggestion := "Break the cycle: move what both sides need into a module neither imports, or make one import type-only or lazy"
		for _, edge := range c.Edges {
			if edge.Recommended {
				suggestion = fmt.Sprintf("Break the cycle at %s → %s (fewest import sites): move what it needs into a module neither imports, or make that import type-only or lazy", edge.From, edge.To)
				break
			}
		}
		findings = append(findings, ReviewFinding{
			Check:      "arch-health",
			Severity:   "warning",
			File:       touched[0],
			Message:    fmt.Sprintf("Runtime import cycle through %d files: %s", len(c.Nodes), cycleList(c.Nodes)),
			Suggestion: suggestion,
			Category:   "architecture",
			RuleID:     "ckb/arch-health/cycles",
		})
	}
	return out, findings
}

// cycleList names up to six files of a cycle.
func cycleList(nodes []string) string {
	const max = 6
	if len(nodes) <= max {
		return strings.Join(nodes, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(nodes[:max], ", "), len(nodes)-max)
}
