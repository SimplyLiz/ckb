package mcp

import (
	"context"

	"github.com/SimplyLiz/CodeMCP/internal/envelope"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	"github.com/SimplyLiz/CodeMCP/internal/secrets"
)

// toolScanSecrets scans for exposed secrets in the codebase.
func (s *MCPServer) toolScanSecrets(params map[string]interface{}) (*envelope.Response, error) {
	repoRoot := s.engine().GetRepoRoot()

	// Parse options
	opts := secrets.ScanOptions{
		RepoRoot:       repoRoot,
		Scope:          secrets.ScopeWorkdir,
		ApplyAllowlist: true,
		MinEntropy:     3.5,
	}

	// Scope
	if v, ok := params["scope"].(string); ok {
		opts.Scope = secrets.ScanScope(v)
	}

	// Paths filter
	if v, ok := params["paths"].([]interface{}); ok {
		for _, p := range v {
			if ps, ok := p.(string); ok {
				opts.Paths = append(opts.Paths, ps)
			}
		}
	}

	// Exclude paths
	if v, ok := params["excludePaths"].([]interface{}); ok {
		for _, p := range v {
			if ps, ok := p.(string); ok {
				opts.ExcludePaths = append(opts.ExcludePaths, ps)
			}
		}
	}

	// Minimum severity filter
	if v, ok := params["minSeverity"].(string); ok {
		opts.MinSeverity = secrets.Severity(v)
	}

	// Git history options
	if v, ok := params["sinceCommit"].(string); ok {
		opts.SinceCommit = v
	}
	if v, ok := params["maxCommits"].(float64); ok {
		opts.MaxCommits = int(v)
	}

	// External tool options
	if v, ok := params["useGitleaks"].(bool); ok {
		opts.UseGitleaks = v
	}
	if v, ok := params["useTrufflehog"].(bool); ok {
		opts.UseTrufflehog = v
	}
	if v, ok := params["preferExternal"].(bool); ok {
		opts.PreferExternal = v
	}

	// Allowlist
	if v, ok := params["applyAllowlist"].(bool); ok {
		opts.ApplyAllowlist = v
	}

	// Create scanner
	scanner := secrets.NewScanner(repoRoot, s.logger)

	// Every scope (workdir, staged, history) goes through Scan, which fills
	// scannedAt, duration and filesScanned and applies the allowlist.
	result, err := scanner.Scan(context.Background(), opts)
	if err != nil {
		return nil, errors.NewOperationError("scan secrets", err)
	}

	// Build response
	builder := NewToolResponse().Data(result)

	// Add warnings
	if opts.UseGitleaks || opts.UseTrufflehog {
		externalUsed := false
		for _, src := range result.Sources {
			if src.Name == "gitleaks" || src.Name == "trufflehog" {
				externalUsed = true
				break
			}
		}
		if !externalUsed {
			builder.Warning("External tools requested but not available; using builtin patterns only")
		}
	}

	if opts.Scope == secrets.ScopeHistory {
		builder.Warning("History scan may include secrets that have already been rotated")
	}

	// Add summary info
	if result.Summary.TotalFindings > 0 {
		builder.Warning(formatFindingsSummary(result.Summary))
	}

	return builder.Build(), nil
}

// formatFindingsSummary creates a human-readable summary string.
func formatFindingsSummary(s secrets.ScanSummary) string {
	critical := s.BySeverity[secrets.SeverityCritical]
	high := s.BySeverity[secrets.SeverityHigh]

	if critical > 0 {
		return "CRITICAL: Found exposed secrets that need immediate attention"
	}
	if high > 0 {
		return "Found high-severity secrets that should be reviewed"
	}
	return ""
}
