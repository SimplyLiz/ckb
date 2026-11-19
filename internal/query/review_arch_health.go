package query

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/cycles"
	"github.com/SimplyLiz/CodeMCP/internal/pathclass"
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
	Languages     []string    `json:"languages,omitempty"`  // languages whose imports were checked
	GodModules    []GodModule `json:"godModules,omitempty"` // changed files over both god-module thresholds
	Bridges       []string    `json:"bridges,omitempty"`    // changed files that are bridges
}

// checkArchitecturalHealth reports runtime import cycles the change takes part
// in, from the SCIP import graph (see import_graph.go), and changed files that
// are god modules (god_modules.go) or bridges (bridges.go).
//
// Only what the change touches is reported: a cycle or god module elsewhere in
// the project is no finding about this change, and Cartographer's project-wide
// counts, reported on every review, were noise nobody acted on.
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

	if e.hasConsumerGraph() {
		ran = true
		idx := e.scipAdapter.GetIndex()
		classes := pathclass.FromConfig(e.repoRoot, e.config)
		changed := make(map[string]bool, len(changedFiles))
		for _, f := range changedFiles {
			changed[f] = true
		}
		for _, m := range godModules(idx, structuralNoise(classes)) {
			if !changed[m.File] {
				continue
			}
			details.GodModules = append(details.GodModules, m)
			findings = append(findings, ReviewFinding{
				Check:      "arch-health",
				Severity:   "warning",
				File:       m.File,
				Message:    fmt.Sprintf("God module: depends on %d files/packages and declares %d symbols", m.Dependencies, m.Declarations),
				Suggestion: "Split by concern: move a group of declarations, with the dependencies only they need, into its own file",
				Category:   "architecture",
				RuleID:     "ckb/arch-health/god-module",
			})
		}
		bridges := codeBridges(idx, e.repoRoot, classes)
		for _, f := range changedFiles {
			cut, ok := bridges[f]
			if !ok {
				continue
			}
			details.Bridges = append(details.Bridges, f)
			findings = append(findings, ReviewFinding{
				Check:    "arch-health",
				Severity: "info",
				File:     f,
				Message:  fmt.Sprintf("Bridge: the only link to %d files, which reach the rest of the code through it alone", cut),
				Category: "architecture",
				RuleID:   "ckb/arch-health/bridge",
			})
		}
		if n := len(details.GodModules); n > 0 {
			parts = append(parts, fmt.Sprintf("%d changed god module(s)", n))
		}
		if n := len(details.Bridges); n > 0 {
			parts = append(parts, fmt.Sprintf("%d changed bridge(s)", n))
		}
	}

	if !ran {
		return ReviewCheck{
			Name:     "arch-health",
			Status:   "skip",
			Severity: "info",
			Summary:  "No SCIP index: architecture not checked",
			Duration: time.Since(start).Milliseconds(),
		}, nil
	}

	status := "pass"
	for _, f := range findings {
		if f.Severity != "info" {
			status = "warn"
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "no god module or bridge among changed files")
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
