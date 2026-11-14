package query

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

// checkUnwiredModules finds exported symbols in changed files that are never
// transitively reachable from application entrypoints.
func (e *Engine) checkUnwiredModules(ctx context.Context, changedFiles []string, opts ReviewPROptions) (ReviewCheck, []ReviewFinding) {
	start := time.Now()

	// Build scope from changed file directories
	dirSet := make(map[string]bool)
	for _, f := range changedFiles {
		dirSet[filepath.Dir(f)] = true
	}
	dirs := make([]string, 0, len(dirSet))
	for d := range dirSet {
		dirs = append(dirs, d)
	}

	resp, err := e.FindUnwiredModules(ctx, FindUnwiredModulesOptions{
		Scope:         dirs,
		MinConfidence: 0.85, // Higher bar for PR review to reduce noise
		Limit:         30,
	})
	if err != nil {
		return ReviewCheck{
			Name:     "unwired",
			Status:   "skip",
			Severity: "info",
			Summary:  fmt.Sprintf("Could not analyze: %v", err),
			Duration: time.Since(start).Milliseconds(),
		}, nil
	}

	// Nothing to walk from: no main, no server, no framework convention CKB
	// knows. That is not a pass.
	if len(resp.Entrypoints) == 0 {
		return ReviewCheck{
			Name:     "unwired",
			Status:   "skip",
			Severity: "info",
			Summary:  "No entrypoints found, so reachability was not analyzed",
			Duration: time.Since(start).Milliseconds(),
		}, nil
	}

	// Filter to only items that are in changed files
	changedFileSet := make(map[string]bool)
	for _, f := range changedFiles {
		changedFileSet[f] = true
	}

	var findings []ReviewFinding
	for _, mod := range resp.UnwiredModules {
		for _, item := range mod.Items {
			if !changedFileSet[item.FilePath] {
				continue
			}

			findings = append(findings, ReviewFinding{
				Check:     "unwired",
				Severity:  "info",
				File:      item.FilePath,
				StartLine: item.LineNumber,
				Message: fmt.Sprintf(
					"Unwired: %s (%s) — %s (refs: %d, confidence: %.0f%%)",
					item.SymbolName, item.Kind, item.Reason, item.ReferenceCount, item.Confidence*100,
				),
				Suggestion: "Verify this symbol is called from the main execution pipeline, or remove if no longer needed.",
				Category:   "unwired",
				RuleID:     "ckb/unwired/not-reachable",
				Tier:       2,
				Confidence: item.Confidence,
			})
		}
	}

	status := "pass"
	summary := "All exported symbols are reachable from entrypoints"
	if len(findings) > 0 {
		status = "warn"
		summary = fmt.Sprintf("%d unwired symbol(s) in changed files", len(findings))
	}
	if resp.Partial {
		summary += " (partial — reachable set budget exhausted)"
	}

	return ReviewCheck{
		Name:     "unwired",
		Status:   status,
		Severity: "info",
		Summary:  summary,
		Details: map[string]any{
			"entrypoints":    resp.Entrypoints,
			"reachableCount": resp.ReachableCount,
			"partial":        resp.Partial,
			"totalUnwired":   resp.Summary.UnwiredCount,
		},
		Duration: time.Since(start).Milliseconds(),
	}, findings
}
