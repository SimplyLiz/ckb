package query

import "fmt"

// CompactReview strips a review to what an agent needs to decide where to
// look: verdict and score, the checks that did not pass (passed ones by name),
// the first maxFindings findings (findings are sorted by tier, then severity),
// counts per severity for the rest, and the few fields a written review quotes
// (effort, reviewers, health). The full response runs to ~120 KB on a large
// diff; this stays in the low kilobytes. The MCP reviewPR tool returns it with
// compact: true, the CLI with `ckb review --compact`.
func CompactReview(r *ReviewPRResponse, maxFindings int) map[string]interface{} {
	var activeChecks []map[string]string
	passedNames := []string{}
	for _, c := range r.Checks {
		if c.Status == "pass" {
			passedNames = append(passedNames, c.Name)
			continue
		}
		activeChecks = append(activeChecks, map[string]string{
			"name":    c.Name,
			"status":  c.Status,
			"summary": c.Summary,
		})
	}

	top := r.Findings
	if maxFindings >= 0 && len(top) > maxFindings {
		top = top[:maxFindings]
	}
	findings := []map[string]interface{}{}
	for _, f := range top {
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
	if r.HealthReport != nil && (r.HealthReport.Degraded > 0 || r.HealthReport.Improved > 0) {
		result["health"] = map[string]interface{}{
			"degraded":     r.HealthReport.Degraded,
			"improved":     r.HealthReport.Improved,
			"averageDelta": r.HealthReport.AverageDelta,
		}
	}
	if r.ReviewEffort != nil {
		result["reviewEffort"] = map[string]interface{}{
			"estimatedHours": r.ReviewEffort.EstimatedHours,
			"complexity":     r.ReviewEffort.Complexity,
		}
	}
	if len(r.Reviewers) > 0 {
		var reviewers []map[string]string
		for i, rv := range r.Reviewers {
			if i == 3 {
				break
			}
			entry := map[string]string{"owner": rv.Owner}
			if rv.ExpertiseArea != "" {
				entry["expertiseArea"] = rv.ExpertiseArea
			}
			reviewers = append(reviewers, entry)
		}
		result["reviewers"] = reviewers
	}
	if r.SplitSuggestion != nil && r.SplitSuggestion.ShouldSplit {
		result["splitSuggestion"] = fmt.Sprintf("%d clusters — %s", len(r.SplitSuggestion.Clusters), r.SplitSuggestion.Reason)
	}

	if rest := r.Findings[len(top):]; len(rest) > 0 {
		bySeverity := map[string]int{}
		for _, f := range rest {
			bySeverity[f.Severity]++
		}
		result["remainingFindings"] = len(rest)
		result["remainingBySeverity"] = bySeverity
	}

	return result
}
