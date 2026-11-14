package mcp

import (
	"context"

	"github.com/SimplyLiz/CodeMCP/internal/envelope"
	"github.com/SimplyLiz/CodeMCP/internal/query"
)

// toolFindUnwiredModules finds exported symbols not reachable from entrypoints.
func (s *MCPServer) toolFindUnwiredModules(params map[string]interface{}) (*envelope.Response, error) {
	opts := query.FindUnwiredModulesOptions{}

	// Scope
	if scopeRaw, ok := params["scope"].([]interface{}); ok {
		for _, v := range scopeRaw {
			if str, ok := v.(string); ok {
				opts.Scope = append(opts.Scope, str)
			}
		}
	}

	// Exclude patterns
	if patternsRaw, ok := params["excludePatterns"].([]interface{}); ok {
		for _, p := range patternsRaw {
			if str, ok := p.(string); ok {
				opts.ExcludePatterns = append(opts.ExcludePatterns, str)
			}
		}
	}

	// Min confidence
	opts.MinConfidence = 0.80
	if v, ok := params["minConfidence"].(float64); ok {
		opts.MinConfidence = v
	}

	// Include types
	if v, ok := params["includeTypes"].(bool); ok {
		opts.IncludeTypes = v
	}

	// Max nodes (reachable set budget)
	opts.MaxNodes = 100000
	if v, ok := params["maxNodes"].(float64); ok {
		opts.MaxNodes = int(v)
	}

	// Limit
	opts.Limit = 100
	if v, ok := params["limit"].(float64); ok {
		opts.Limit = int(v)
	}

	ctx := context.Background()
	result, err := s.engine().FindUnwiredModules(ctx, opts)
	if err != nil {
		return nil, err
	}

	resp := NewToolResponse().Data(result)
	if result != nil && result.ReachableCount == 0 {
		resp.Warning("No entrypoints detected. Ensure your project has main/server/CLI entry files.")
	}
	if result != nil && result.Partial {
		resp.Warning("Reachable set budget exhausted — results may be incomplete. Increase maxNodes for a full scan.")
	}

	return resp.Build(), nil
}
