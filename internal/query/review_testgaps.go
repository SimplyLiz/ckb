package query

import (
	"context"
	"fmt"
	"time"
)

// checkTestGaps finds untested functions in the changed files.
// Uses tree-sitter internally — acquires e.tsMu around AnalyzeTestGaps calls.
// When a coverage report is available, files at 0% coverage get upgraded to "warning".
func (e *Engine) checkTestGaps(ctx context.Context, changedFiles []string, opts ReviewPROptions) (ReviewCheck, []ReviewFinding) {
	start := time.Now()

	minLines := opts.Policy.TestGapMinLines
	if minLines <= 0 {
		minLines = 5
	}

	// Load coverage data if available
	var coveragePaths []string
	if e.config != nil && len(e.config.Coverage.Paths) > 0 {
		coveragePaths = e.config.Coverage.Paths
	}
	coverageMap := loadCoverageReport(e.repoRoot, coveragePaths)

	// Filter to non-test source files, cap at 20
	var sourceFiles []string
	for _, f := range changedFiles {
		if isTestFilePath(f) {
			continue
		}
		sourceFiles = append(sourceFiles, f)
		if len(sourceFiles) >= 20 {
			break
		}
	}

	var findings []ReviewFinding
	for _, file := range sourceFiles {
		if ctx.Err() != nil {
			break
		}
		e.tsMu.Lock()
		result, err := e.AnalyzeTestGaps(ctx, AnalyzeTestGapsOptions{
			Target:   file,
			MinLines: minLines,
			Limit:    10,
		})
		e.tsMu.Unlock()
		if err != nil {
			continue
		}

		for _, gap := range result.Gaps {
			hint := ""
			if gap.Function != "" {
				hint = fmt.Sprintf("→ ckb explain %s", gap.Function)
			}
			severity := "info"
			detail := ""

			// Cross-reference with coverage data
			if coverageMap != nil {
				if cov, ok := coverageMap[gap.File]; ok {
					detail = fmt.Sprintf("Coverage: %.0f%%", cov)
					if cov == 0 {
						severity = "warning" // Upgrade: 0% coverage is concerning
					}
				}
			}

			findings = append(findings, ReviewFinding{
				Check:     "test-gaps",
				Severity:  severity,
				File:      gap.File,
				StartLine: gap.StartLine,
				EndLine:   gap.EndLine,
				Message:   fmt.Sprintf("Untested function %s (complexity: %d)", gap.Function, gap.Complexity),
				Detail:    detail,
				Category:  "testing",
				RuleID:    fmt.Sprintf("ckb/test-gaps/%s", gap.Reason),
				Hint:      hint,
			})
		}
	}

	status := "pass"
	summary := "All changed functions have tests"
	if len(findings) > 0 {
		// If any findings were upgraded to warning, set status accordingly
		hasWarning := false
		for _, f := range findings {
			if f.Severity == "warning" {
				hasWarning = true
				break
			}
		}
		if hasWarning {
			status = "warn"
		} else {
			status = "info"
		}
		totalCount := len(findings)
		summary = fmt.Sprintf("%d untested function(s) in changed files", totalCount)

		// Cap findings at 10 to avoid noise (same pattern as hotspots)
		if len(findings) > 10 {
			findings = findings[:10]
			summary = fmt.Sprintf("%d untested function(s) in changed files (showing top 10)", totalCount)
		}
	}

	return ReviewCheck{
		Name:     "test-gaps",
		Status:   status,
		Severity: "info",
		Summary:  summary,
		Duration: time.Since(start).Milliseconds(),
	}, findings
}
