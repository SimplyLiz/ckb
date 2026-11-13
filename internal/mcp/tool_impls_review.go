package mcp

import (
	"context"

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

// compactReviewResponse is query.CompactReview with the first 10 findings and
// a pointer to the MCP tools that drill into them.
func compactReviewResponse(r *query.ReviewPRResponse) map[string]interface{} {
	result := query.CompactReview(r, 10)
	result["drillDown"] = "Use findReferences, explainSymbol, analyzeImpact, or traceUsage to investigate specific findings"
	return result
}
