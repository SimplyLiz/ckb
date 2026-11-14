package mcp

import (
	"context"
	"path/filepath"

	"github.com/SimplyLiz/CodeMCP/internal/envelope"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	"github.com/SimplyLiz/CodeMCP/internal/perf"
	"github.com/SimplyLiz/CodeMCP/internal/query"
)

// toolScanPerformance detects hidden coupling: file pairs that co-change
// frequently in git but have no link in the SCIP index.
func (s *MCPServer) toolScanPerformance(params map[string]interface{}) (*envelope.Response, error) {
	minCorrelation := 0.3
	if v, ok := params["minCorrelation"].(float64); ok {
		minCorrelation = v
	}

	minCoChanges := 3
	if v, ok := params["minCoChanges"].(float64); ok {
		minCoChanges = int(v)
	}

	limit := 50
	if v, ok := params["limit"].(float64); ok {
		limit = int(v)
	}

	var scope []string
	if v, ok := params["scope"].([]interface{}); ok {
		for _, item := range v {
			if s, ok := item.(string); ok {
				scope = append(scope, s)
			}
		}
	}

	result, err := s.engine().ScanHiddenCoupling(context.Background(), perf.ScanOptions{
		Scope:          scope,
		MinCorrelation: minCorrelation,
		MinCoChanges:   minCoChanges,
		Limit:          limit,
	})
	if err != nil {
		return nil, errors.NewOperationError("scan performance", err)
	}

	return NewToolResponse().
		Data(result).
		Build(), nil
}

// toolAnalyzeStructuralPerf detects loop call sites in high-churn files using
// tree-sitter AST analysis. It combines git churn data with static code structure
// to surface O(n) and O(n²) patterns that do not appear in profiling until load.
func (s *MCPServer) toolAnalyzeStructuralPerf(params map[string]interface{}) (*envelope.Response, error) {
	limit := 100
	if v, ok := params["limit"].(float64); ok {
		limit = int(v)
	}

	windowDays := 90
	if v, ok := params["windowDays"].(float64); ok {
		windowDays = int(v)
	}

	minChurnCount := 3
	if v, ok := params["minChurnCount"].(float64); ok {
		minChurnCount = int(v)
	}

	var scope []string
	if v, ok := params["scope"].([]interface{}); ok {
		for _, item := range v {
			if s, ok := item.(string); ok {
				scope = append(scope, s)
			}
		}
	}

	repoRoot := s.engine().GetRepoRoot()
	ctx := context.Background()

	// Gather entrypoint files from the query engine so the structural analyzer
	// can mark call sites in entrypoints as higher severity.
	entrypointFiles := gatherEntrypointFiles(ctx, s.engine())

	analyzer := perf.NewAnalyzer(repoRoot, s.logger)
	result, err := analyzer.AnalyzeStructural(ctx, perf.StructuralPerfOptions{
		Scope:           scope,
		Limit:           limit,
		WindowDays:      windowDays,
		MinChurnCount:   minChurnCount,
		EntrypointFiles: entrypointFiles,
	})
	if err != nil {
		return nil, errors.NewOperationError("analyze structural performance", err)
	}

	return NewToolResponse().
		Data(result).
		Build(), nil
}

// gatherEntrypointFiles returns repo-relative file paths of known system entrypoints
// by querying the engine's ListEntrypoints. Returns an empty slice on error — the
// structural analysis degrades gracefully without entrypoint data.
func gatherEntrypointFiles(ctx context.Context, eng *query.Engine) []string {
	if eng == nil {
		return nil
	}
	resp, err := eng.ListEntrypoints(ctx, query.ListEntrypointsOptions{Limit: 50})
	if err != nil || resp == nil {
		return nil
	}

	repoRoot := eng.GetRepoRoot()
	var files []string
	seen := make(map[string]bool)
	for _, ep := range resp.Entrypoints {
		if ep.Location == nil || ep.Location.FileId == "" {
			continue
		}
		// Location.FileId may be absolute or repo-relative. Normalize to repo-relative.
		path := ep.Location.FileId
		if filepath.IsAbs(path) {
			rel, err := filepath.Rel(repoRoot, path)
			if err == nil {
				path = filepath.ToSlash(rel)
			}
		}
		if !seen[path] {
			seen[path] = true
			files = append(files, path)
		}
	}
	return files
}
