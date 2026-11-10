package mcp

import (
	"context"
	"fmt"

	"github.com/SimplyLiz/CodeMCP/internal/envelope"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	"github.com/SimplyLiz/CodeMCP/internal/query"
)

// toolReviewPR runs a comprehensive PR review with quality gates.
func (s *MCPServer) toolReviewPR(params map[string]interface{}) (*envelope.Response, error) {
	ctx := context.Background()

	// Parse baseBranch
	baseBranch := "main"
	if v, ok := params["baseBranch"].(string); ok && v != "" {
		baseBranch = v
	}

	// Parse headBranch
	headBranch := ""
	if v, ok := params["headBranch"].(string); ok {
		headBranch = v
	}

	// Parse checks filter
	var checks []string
	if v, ok := params["checks"].([]interface{}); ok {
		for _, c := range v {
			if cs, ok := c.(string); ok {
				checks = append(checks, cs)
			}
		}
	}

	// Parse failOnLevel
	failOnLevel := ""
	if v, ok := params["failOnLevel"].(string); ok {
		failOnLevel = v
	}

	// Parse staged
	staged := false
	if v, ok := params["staged"].(bool); ok {
		staged = v
	}

	// Parse scope
	scope := ""
	if v, ok := params["scope"].(string); ok {
		scope = v
	}

	// Parse compact mode — returns ~900 tokens instead of ~30k
	compact := false
	if v, ok := params["compact"].(bool); ok {
		compact = v
	}

	// Parse skip (denylist — complement of checks)
	var skipChecks []string
	if v, ok := params["skip"].([]interface{}); ok {
		for _, c := range v {
			if cs, ok := c.(string); ok {
				skipChecks = append(skipChecks, cs)
			}
		}
	}

	// Parse critical paths
	var criticalPaths []string
	if v, ok := params["criticalPaths"].([]interface{}); ok {
		for _, p := range v {
			if ps, ok := p.(string); ok {
				criticalPaths = append(criticalPaths, ps)
			}
		}
	}

	policy := query.DefaultReviewPolicy()
	if failOnLevel != "" {
		policy.FailOnLevel = failOnLevel
	}
	if len(criticalPaths) > 0 {
		policy.CriticalPaths = criticalPaths
	}

	s.logger.Debug("Executing reviewPR",
		"baseBranch", baseBranch,
		"headBranch", headBranch,
		"checks", checks,
		"staged", staged,
		"scope", scope,
		"compact", compact,
	)

	result, err := s.engine().ReviewPR(ctx, query.ReviewPROptions{
		BaseBranch: baseBranch,
		HeadBranch: headBranch,
		Policy:     policy,
		Checks:     checks,
		SkipChecks: skipChecks,
		Staged:     staged,
		Scope:      scope,
	})
	if err != nil {
		return nil, errors.NewOperationError("review PR", err)
	}

	if compact {
		return NewToolResponse().
			Data(compactReviewResponse(result)).
			Build(), nil
	}

	return NewToolResponse().
		Data(result).
		Build(), nil
}

// compactReviewResponse strips the full response to only what an LLM needs
// for decision-making: verdict, non-pass checks, top findings, and action items.
// Reduces response from ~120KB (~30k tokens) to ~4KB (~1k tokens).
func compactReviewResponse(r *query.ReviewPRResponse) map[string]interface{} {
	// Only include checks that aren't "pass" — those are the interesting ones
	var activeChecks []map[string]string
	var passedNames []string
	for _, c := range r.Checks {
		if c.Status == "pass" {
			passedNames = append(passedNames, c.Name)
		} else {
			activeChecks = append(activeChecks, map[string]string{
				"name":    c.Name,
				"status":  c.Status,
				"summary": c.Summary,
			})
		}
	}

	// Top 10 findings with just what the LLM needs
	topFindings := r.Findings
	if len(topFindings) > 10 {
		topFindings = topFindings[:10]
	}
	var findings []map[string]interface{}
	for _, f := range topFindings {
		entry := map[string]interface{}{
			"check":    f.Check,
			"severity": f.Severity,
			"file":     f.File,
			"message":  f.Message,
		}
		if f.StartLine > 0 {
			entry["line"] = f.StartLine
		}
		if f.RuleID != "" {
			entry["ruleId"] = f.RuleID
		}
		if f.Hint != "" {
			entry["hint"] = f.Hint
		}
		findings = append(findings, entry)
	}

	result := map[string]interface{}{
		"verdict":   r.Verdict,
		"score":     r.Score,
		"narrative": r.Narrative,
		"prTier":    r.PRTier,
		"summary": map[string]interface{}{
			"totalFiles":   r.Summary.TotalFiles,
			"totalChanges": r.Summary.TotalChanges,
			"modules":      r.Summary.ModulesChanged,
			"languages":    r.Summary.Languages,
		},
		"activeChecks": activeChecks,
		"passedChecks": passedNames,
		"findings":     findings,
	}

	if r.VerdictReason != "" {
		result["verdictReason"] = r.VerdictReason
	}
	// What the review could not see is worth more to an agent than another finding.
	if len(r.Limitations) > 0 {
		result["limitations"] = r.Limitations
	}

	// Add health summary if present
	if r.HealthReport != nil && (r.HealthReport.Degraded > 0 || r.HealthReport.Improved > 0) {
		result["health"] = map[string]interface{}{
			"degraded":     r.HealthReport.Degraded,
			"improved":     r.HealthReport.Improved,
			"averageDelta": r.HealthReport.AverageDelta,
		}
	}

	// Add split suggestion if present
	if r.SplitSuggestion != nil && r.SplitSuggestion.ShouldSplit {
		result["splitSuggestion"] = fmt.Sprintf("%d clusters — %s", len(r.SplitSuggestion.Clusters), r.SplitSuggestion.Reason)
	}

	// Add remaining findings count
	if len(r.Findings) > 10 {
		result["remainingFindings"] = len(r.Findings) - 10
	}

	// Drill-down hint
	result["drillDown"] = "Use findReferences, explainSymbol, analyzeImpact, or traceUsage to investigate specific findings"

	return result
}
