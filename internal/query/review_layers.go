package query

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
)

// layersConfigCandidates are the places a layer config is looked for, relative
// to the repo root, in order. The first two are the ones Cartographer's own
// graph rebuild and CLI read (api.rs rebuild_graph, main.rs load_layers), so
// one file serves CKB's review check and Cartographer's tools alike;
// .cartographer/layers.toml is where CKB looked before 9.4 and stays as legacy.
var layersConfigCandidates = []string{
	"layers.toml",
	filepath.Join(".codecartographer", "layers.toml"),
	filepath.Join(".cartographer", "layers.toml"),
}

// findLayersConfig returns the absolute path of the first layer config found
// in repoRoot, or "" when there is none.
func findLayersConfig(repoRoot string) string {
	for _, rel := range layersConfigCandidates {
		if candidate := filepath.Join(repoRoot, rel); fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

// checkLayerViolations uses Cartographer to detect architectural boundary crossings
// in the files touched by the PR. Returns a skip check when Cartographer is not
// compiled in (graceful degradation — never blocks the build).
func (e *Engine) checkLayerViolations(_ context.Context, files []string, _ ReviewPROptions) (ReviewCheck, []ReviewFinding) {
	start := time.Now()

	if !cartographer.Available() {
		return ReviewCheck{
			Name:     "layers",
			Status:   "skip",
			Severity: "info",
			Summary:  "Cartographer not compiled in this build",
			Duration: time.Since(start).Milliseconds(),
		}, nil
	}

	layersPath := findLayersConfig(e.repoRoot)
	if layersPath == "" {
		// Without a config Cartographer checks against an empty one and finds
		// nothing; reporting that as "pass" would claim a check that never ran.
		return ReviewCheck{
			Name:     "layers",
			Status:   "skip",
			Severity: "info",
			Summary:  "No layers.toml (looked for " + strings.Join(layersConfigCandidates, ", ") + ")",
			Duration: time.Since(start).Milliseconds(),
		}, nil
	}
	violations, err := cartographer.CheckLayers(e.repoRoot, layersPath)
	if err != nil {
		return ReviewCheck{
			Name:     "layers",
			Status:   "skip",
			Severity: "info",
			Summary:  fmt.Sprintf("layer check skipped: %v", err),
			Duration: time.Since(start).Milliseconds(),
		}, nil
	}

	// A Cartographer lib that serializes violations in snake_case (v4.0.3 and
	// older) decodes into empty paths; matching those against the changed files
	// would turn every violation into a silent pass.
	for _, v := range violations {
		if v.SourcePath == "" || v.TargetPath == "" {
			return ReviewCheck{
				Name:     "layers",
				Status:   "skip",
				Severity: "info",
				Summary:  fmt.Sprintf("layer check skipped: Cartographer returned %d violation(s) without file paths (library too old for this CKB)", len(violations)),
				Duration: time.Since(start).Milliseconds(),
			}, nil
		}
	}

	// Index changed files for O(1) lookup.
	changedSet := make(map[string]struct{}, len(files))
	for _, f := range files {
		changedSet[f] = struct{}{}
	}

	var findings []ReviewFinding
	for _, v := range violations {
		_, srcChanged := changedSet[v.SourcePath]
		_, tgtChanged := changedSet[v.TargetPath]
		if !srcChanged && !tgtChanged {
			continue // violation not in this PR's scope
		}
		findings = append(findings, ReviewFinding{
			Check:    "layers",
			Severity: "warning",
			File:     v.SourcePath,
			Message:  fmt.Sprintf("layer violation: %s → %s (%s → %s)", v.SourcePath, v.TargetPath, v.SourceLayer, v.TargetLayer),
			Category: "architecture",
			RuleID:   "ckb/layers/boundary",
			Tier:     2,
		})
	}

	status := "pass"
	summary := "No layer violations in changed files"
	if len(findings) > 0 {
		status = "warn"
		summary = fmt.Sprintf("%d layer violation(s) in changed files", len(findings))
	}

	return ReviewCheck{
		Name:     "layers",
		Status:   status,
		Severity: "warning",
		Summary:  summary,
		Duration: time.Since(start).Milliseconds(),
	}, findings
}
