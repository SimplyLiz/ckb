package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// OutputFormat represents the output format type
type OutputFormat string

const (
	FormatJSON          OutputFormat = "json"
	FormatHuman         OutputFormat = "human"
	FormatSARIF         OutputFormat = "sarif"
	FormatMarkdown      OutputFormat = "markdown"
	FormatGitHubActions OutputFormat = "github-actions"
	FormatCodeClimate   OutputFormat = "codeclimate"
	FormatCompliance    OutputFormat = "compliance"
)

// FormatResponse formats a response according to the specified format
func FormatResponse(resp interface{}, format OutputFormat) (string, error) {
	switch format {
	case FormatJSON:
		return formatJSON(resp)
	case FormatHuman:
		return formatHuman(resp)
	default:
		return "", fmt.Errorf("unsupported format: %s", format)
	}
}

// formatJSON formats the response as JSON
func formatJSON(resp interface{}) (string, error) {
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return string(data), nil
}

// formatHuman formats the response in human-readable format
func formatHuman(resp interface{}) (string, error) {
	switch v := resp.(type) {
	case *Response:
		return formatResponseHuman(v)
	case *StatusResponseCLI:
		return formatStatusHuman(v)
	case *DoctorResponseCLI:
		return formatDoctorHuman(v)
	case *SearchResponseCLI:
		return formatSearchHuman(v)
	case *SymbolResponseCLI:
		return formatSymbolHuman(v)
	case *ReferencesResponseCLI:
		return formatRefsHuman(v)
	case *ArchitectureResponseCLI:
		return formatArchHuman(v)
	case *ImpactResponseCLI:
		return formatImpactHuman(v)
	case *PRSummaryResponseCLI:
		return formatPRSummaryHuman(v)
	case *CallgraphResponseCLI:
		return formatCallgraphHuman(v)
	case *ModuleOverviewResponseCLI:
		return formatModuleOverviewHuman(v)
	case *JustifyResponseCLI:
		return formatJustifyHuman(v)
	case *HotspotsResponseCLI:
		return formatHotspotsHuman(v)
	case *ComplexityResponseCLI:
		return formatComplexityHuman(v)
	case *EntrypointsResponseCLI:
		return formatEntrypointsHuman(v)
	case *TraceResponseCLI:
		return formatTraceHuman(v)
	case *JobsListResponseCLI:
		return formatJobsListHuman(v)
	case *DeadCodeResponseCLI:
		return formatDeadCodeHuman(v), nil
	case *BreakingResponseCLI:
		return formatBreakingHuman(v), nil
	case *DiffSummaryResponseCLI:
		return formatDiffSummaryHuman(v), nil
	case *ConceptsResponseCLI:
		return formatConceptsHuman(v), nil
	case *ChangeSetResponseCLI:
		return formatChangeSetHuman(v), nil
	default:
		// For types without human formatters, output JSON with a note
		json, err := formatJSON(resp)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(Human format not available for this command, showing JSON)\n\n%s", json), nil
	}
}

// formatResponseHuman formats a generic Response in human-readable format
func formatResponseHuman(resp *Response) (string, error) {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("CKB v%s\n", resp.CkbVersion))
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	// Provenance
	if resp.Provenance != nil {
		b.WriteString("Provenance:\n")
		repoStateId := resp.Provenance.RepoStateId
		if len(repoStateId) > 12 {
			repoStateId = repoStateId[:12]
		}
		b.WriteString(fmt.Sprintf("  Repo State: %s (dirty: %v, mode: %s)\n",
			repoStateId,
			resp.Provenance.RepoStateDirty,
			resp.Provenance.RepoStateMode))

		if resp.Provenance.Completeness != nil {
			b.WriteString(fmt.Sprintf("  Completeness: %.1f%% (%s)\n",
				resp.Provenance.Completeness.Score*100,
				resp.Provenance.Completeness.Source))
		}

		if len(resp.Provenance.Backends) > 0 {
			b.WriteString("  Backends:\n")
			for _, backend := range resp.Provenance.Backends {
				b.WriteString(fmt.Sprintf("    - %s (%dms)\n", backend.BackendId, backend.DurationMs))
			}
		}

		if len(resp.Provenance.Warnings) > 0 {
			b.WriteString("  Warnings:\n")
			for _, w := range resp.Provenance.Warnings {
				b.WriteString(fmt.Sprintf("    ! %s\n", w))
			}
		}

		b.WriteString(fmt.Sprintf("  Query Duration: %dms\n", resp.Provenance.QueryDurationMs))
		b.WriteString("\n")
	}

	// Facts - this will be type-specific, so we just indicate there are facts
	b.WriteString("Facts: (see JSON output for details)\n\n")

	// Drilldowns
	if len(resp.Drilldowns) > 0 {
		b.WriteString("Suggested Follow-ups:\n")
		for i, d := range resp.Drilldowns {
			b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, d.Label))
			b.WriteString(fmt.Sprintf("     $ ckb %s\n", d.Query))
		}
	}

	return b.String(), nil
}

// formatStatusHuman formats a StatusResponseCLI in human-readable format
func formatStatusHuman(resp *StatusResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("CKB v%s\n", resp.CkbVersion))
	b.WriteString("──────────────────────────────────────────────────────────\n")

	// Active repository (if set)
	if resp.ActiveRepo != nil {
		sourceHint := ""
		switch resp.ActiveRepo.Source {
		case "env":
			sourceHint = " (from CKB_REPO)"
		case "cwd":
			sourceHint = " (from current directory)"
		case "default":
			sourceHint = " (default - run from project directory for full status)"
		}
		b.WriteString(fmt.Sprintf("Active: %s (%s)%s\n", resp.ActiveRepo.Name, resp.ActiveRepo.Path, sourceHint))
	}

	// Daemon status (show on same line group as active repo)
	if resp.DaemonStatus != nil {
		if resp.DaemonStatus.Running {
			uptimeInfo := ""
			if resp.DaemonStatus.Uptime != "" {
				uptimeInfo = fmt.Sprintf(", uptime %s", resp.DaemonStatus.Uptime)
			}
			b.WriteString(fmt.Sprintf("Daemon: running (PID %d, port %d%s)\n", resp.DaemonStatus.PID, resp.DaemonStatus.Port, uptimeInfo))
		} else {
			b.WriteString("Daemon: stopped\n")
		}
	}
	b.WriteString("\n")

	// Analysis Tier (prominent)
	if resp.Tier != nil {
		tierIcon := "◐"
		switch resp.Tier.CurrentName {
		case "Fast":
			tierIcon = "○"
		case "Standard":
			tierIcon = "◉"
		case "Full":
			tierIcon = "●"
		}

		// Show mode (explicit vs auto-detected)
		modeInfo := ""
		if resp.Tier.Mode != "" {
			if resp.Tier.Explicit {
				modeInfo = fmt.Sprintf(" [mode: %s]", resp.Tier.Mode)
			} else {
				modeInfo = " [auto-detected]"
			}
		}

		b.WriteString(fmt.Sprintf("%s Analysis Tier: %s (%s)%s\n", tierIcon, resp.Tier.CurrentName, resp.Tier.Description, modeInfo))
		b.WriteString(fmt.Sprintf("  Available Tools: %d of %d\n", len(resp.Tier.AvailableTools), len(resp.Tier.AvailableTools)+len(resp.Tier.UnavailableTools)))

		if resp.Tier.UpgradeHint != "" {
			b.WriteString(fmt.Sprintf("\n  ⚡ %s\n", resp.Tier.UpgradeHint))
		}
		b.WriteString("\n")
	}

	// Overall health
	healthIcon := "✓"
	if !resp.Healthy {
		healthIcon = "✗"
	}
	healthText := "Healthy"
	if !resp.Healthy {
		healthText = "Issues detected"
	}
	b.WriteString(fmt.Sprintf("%s System: %s\n\n", healthIcon, healthText))

	// Backends (concise)
	b.WriteString("Backends:\n")
	for _, backend := range resp.Backends {
		status := "✓"
		if !backend.Available {
			status = "✗"
		}
		details := ""
		if backend.Details != "" && backend.Available {
			details = " - " + backend.Details
		}
		b.WriteString(fmt.Sprintf("  %s %s%s\n", status, backend.ID, details))
	}
	b.WriteString("\n")

	// Repository State (compact)
	if resp.RepoState != nil {
		headCommit := resp.RepoState.HeadCommit
		if len(headCommit) > 12 {
			headCommit = headCommit[:12]
		}
		dirty := ""
		if resp.RepoState.Dirty {
			dirty = " (uncommitted changes)"
		}
		b.WriteString(fmt.Sprintf("Repository: %s%s\n", headCommit, dirty))
	}

	// Index Status
	if resp.IndexStatus != nil {
		b.WriteString("\n")
		b.WriteString("Index Status:\n")
		if !resp.IndexStatus.Exists {
			b.WriteString("  ✗ No index found\n")
			b.WriteString("  Run 'ckb index' to create one.\n")
			b.WriteString("\n")
			b.WriteString("  Commands that work without index (git-based):\n")
			b.WriteString("    hotspots, ownership, reviewers, diff-summary, pr-summary\n")
			b.WriteString("  Commands that need SCIP index:\n")
			b.WriteString("    search, refs, callgraph, impact, dead-code, trace, explain\n")
		} else if resp.IndexStatus.Fresh {
			commitInfo := ""
			if resp.IndexStatus.CommitHash != "" {
				hash := resp.IndexStatus.CommitHash
				if len(hash) > 7 {
					hash = hash[:7]
				}
				commitInfo = fmt.Sprintf(" (HEAD = %s)", hash)
			}
			b.WriteString(fmt.Sprintf("  ✓ Up to date%s\n", commitInfo))
			if resp.IndexStatus.FileCount > 0 {
				b.WriteString(fmt.Sprintf("  Files: %d\n", resp.IndexStatus.FileCount))
			}
		} else {
			b.WriteString(fmt.Sprintf("  ⚠ %s\n", resp.IndexStatus.Reason))
			b.WriteString("  Run 'ckb index' to refresh.\n")
		}
	}

	// Change Impact Analysis section
	if resp.ChangeImpactStatus != nil {
		b.WriteString("\nChange Impact Analysis:\n")

		// Coverage status
		if resp.ChangeImpactStatus.Coverage != nil {
			cov := resp.ChangeImpactStatus.Coverage
			if cov.Found {
				staleMarker := ""
				if cov.Stale {
					staleMarker = " ⚠ stale"
				}
				b.WriteString(fmt.Sprintf("  Coverage:   ✓ Found %s (%s)%s\n", cov.Path, cov.Age, staleMarker))
			} else {
				b.WriteString("  Coverage:   ⚠ Not found (test mapping will use heuristics)\n")
				if cov.GenerateCmd != "" {
					b.WriteString(fmt.Sprintf("              Generate: %s\n", cov.GenerateCmd))
				}
			}
		}

		// CODEOWNERS status
		if resp.ChangeImpactStatus.Codeowners != nil {
			co := resp.ChangeImpactStatus.Codeowners
			if co.Found {
				b.WriteString(fmt.Sprintf("  CODEOWNERS: ✓ Found %s (%d teams, %d patterns)\n",
					co.Path, co.TeamCount, co.PatternCount))
			} else {
				b.WriteString("  CODEOWNERS: ⚠ Not found (reviewer suggestions unavailable)\n")
				b.WriteString("              Create: .github/CODEOWNERS\n")
			}
		}
	}

	// Cache (one-liner)
	if resp.Cache.QueryCount > 0 {
		b.WriteString(fmt.Sprintf("\nCache: %d queries, %.0f%% hit rate, %s\n",
			resp.Cache.QueryCount, resp.Cache.HitRate*100, formatBytes(resp.Cache.SizeBytes)))
	}

	return b.String(), nil
}

// formatDoctorHuman formats a DoctorResponseCLI in human-readable format
func formatDoctorHuman(resp *DoctorResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString("CKB Doctor\n")
	b.WriteString("──────────────────────────────────────────────────────────\n\n")

	// Checks
	for _, check := range resp.Checks {
		var icon string
		switch check.Status {
		case "pass":
			icon = "✓"
		case "warn":
			icon = "⚠"
		case "fail":
			icon = "✗"
		default:
			icon = "?"
		}

		b.WriteString(fmt.Sprintf("%s %s: %s\n", icon, check.Name, check.Message))

		// Suggested fixes
		if len(check.SuggestedFixes) > 0 && check.Status != "pass" {
			for _, fix := range check.SuggestedFixes {
				if fix.Command != "" {
					b.WriteString(fmt.Sprintf("  → %s\n", fix.Command))
				}
			}
		}
	}

	// Overall summary
	b.WriteString("\n")
	if resp.Healthy {
		b.WriteString("✓ All checks passed\n")
	} else {
		b.WriteString("Run 'ckb doctor --fix' to generate fix script\n")
	}

	return b.String(), nil
}

// formatSearchHuman formats a SearchResponseCLI in human-readable format
func formatSearchHuman(resp *SearchResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Search Results for: %s\n", resp.Query))
	b.WriteString(strings.Repeat("=", 60) + "\n\n")
	b.WriteString(fmt.Sprintf("Found %d matches\n\n", resp.TotalMatches))

	for i, sym := range resp.Symbols {
		b.WriteString(fmt.Sprintf("%d. %s (%s)\n", i+1, sym.Name, sym.Kind))
		b.WriteString(fmt.Sprintf("   ID: %s\n", sym.StableID))
		b.WriteString(fmt.Sprintf("   Module: %s\n", sym.ModuleID))
		if sym.Location != nil {
			b.WriteString(fmt.Sprintf("   Location: %s:%d\n", sym.Location.FileID, sym.Location.StartLine))
		}
		b.WriteString(fmt.Sprintf("   Relevance: %.2f\n\n", sym.RelevanceScore))
	}

	return b.String(), nil
}

// formatSymbolHuman formats a SymbolResponseCLI in human-readable format
func formatSymbolHuman(resp *SymbolResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString("Symbol Details\n")
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	b.WriteString(fmt.Sprintf("Name: %s\n", resp.Symbol.Name))
	b.WriteString(fmt.Sprintf("Kind: %s\n", resp.Symbol.Kind))
	b.WriteString(fmt.Sprintf("ID: %s\n", resp.Symbol.StableID))
	b.WriteString(fmt.Sprintf("Visibility: %s (confidence: %.2f)\n", resp.Symbol.Visibility, resp.Symbol.VisibilityConfidence))

	if resp.Symbol.ContainerName != "" {
		b.WriteString(fmt.Sprintf("Container: %s\n", resp.Symbol.ContainerName))
	}

	if resp.Location != nil {
		b.WriteString(fmt.Sprintf("\nLocation: %s:%d:%d\n", resp.Location.FileID, resp.Location.StartLine, resp.Location.StartColumn))
	}

	if resp.Module != nil {
		b.WriteString(fmt.Sprintf("\nModule: %s\n", resp.Module.ModuleID))
	}

	return b.String(), nil
}

// formatRefsHuman formats a ReferencesResponseCLI in human-readable format
func formatRefsHuman(resp *ReferencesResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("References to: %s\n", resp.SymbolID))
	b.WriteString(strings.Repeat("=", 60) + "\n\n")
	b.WriteString(fmt.Sprintf("Total references: %d\n\n", resp.TotalReferences))

	if len(resp.ByModule) > 0 {
		b.WriteString("By Module:\n")
		for _, m := range resp.ByModule {
			b.WriteString(fmt.Sprintf("  %s: %d references\n", m.ModuleID, m.Count))
		}
		b.WriteString("\n")
	}

	b.WriteString("References:\n")
	for i, ref := range resp.References {
		if ref.Location != nil {
			testMarker := ""
			if ref.IsTest {
				testMarker = " [test]"
			}
			b.WriteString(fmt.Sprintf("  %d. %s:%d (%s)%s\n", i+1, ref.Location.FileID, ref.Location.StartLine, ref.Kind, testMarker))
		}
	}

	return b.String(), nil
}

// formatArchHuman formats an ArchitectureResponseCLI in human-readable format
func formatArchHuman(resp *ArchitectureResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString("Architecture Overview\n")
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	b.WriteString(fmt.Sprintf("Modules: %d\n", len(resp.Modules)))
	b.WriteString(fmt.Sprintf("Dependencies: %d\n", len(resp.DependencyGraph)))
	b.WriteString(fmt.Sprintf("Entrypoints: %d\n\n", len(resp.Entrypoints)))

	b.WriteString("Modules:\n")
	for _, m := range resp.Modules {
		b.WriteString(fmt.Sprintf("  %s (%s)\n", m.Name, m.Language))
		b.WriteString(fmt.Sprintf("    Path: %s\n", m.RootPath))
		b.WriteString(fmt.Sprintf("    Files: %d, Symbols: %d\n", m.FileCount, m.SymbolCount))
		b.WriteString(fmt.Sprintf("    Deps: %d incoming, %d outgoing\n\n", m.IncomingEdges, m.OutgoingEdges))
	}

	if len(resp.Entrypoints) > 0 {
		b.WriteString("Entrypoints:\n")
		for _, ep := range resp.Entrypoints {
			b.WriteString(fmt.Sprintf("  %s (%s) - %s\n", ep.Name, ep.Kind, ep.FileID))
		}
	}

	return b.String(), nil
}

// formatImpactHuman formats an ImpactResponseCLI in human-readable format
func formatImpactHuman(resp *ImpactResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Impact Analysis: %s\n", resp.SymbolID))
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	if resp.RiskScore != nil {
		b.WriteString(fmt.Sprintf("Risk Level: %s (score: %.2f)\n", resp.RiskScore.Level, resp.RiskScore.Score))
		b.WriteString(fmt.Sprintf("Explanation: %s\n\n", resp.RiskScore.Explanation))
	}

	b.WriteString(fmt.Sprintf("Direct Impact: %d symbols\n", len(resp.DirectImpact)))
	b.WriteString(fmt.Sprintf("Transitive Impact: %d symbols\n", len(resp.TransitiveImpact)))
	b.WriteString(fmt.Sprintf("Modules Affected: %d\n\n", len(resp.ModulesAffected)))

	if len(resp.ModulesAffected) > 0 {
		b.WriteString("Affected Modules:\n")
		for _, m := range resp.ModulesAffected {
			b.WriteString(fmt.Sprintf("  %s: %d symbols\n", m.ModuleID, m.ImpactCount))
		}
		b.WriteString("\n")
	}

	if len(resp.DirectImpact) > 0 {
		b.WriteString("Direct Dependencies:\n")
		for _, item := range resp.DirectImpact[:min(10, len(resp.DirectImpact))] {
			b.WriteString(fmt.Sprintf("  - %s (%s)\n", item.Name, item.Kind))
		}
		if len(resp.DirectImpact) > 10 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(resp.DirectImpact)-10))
		}
	}

	return b.String(), nil
}

// formatBytes formats byte size in human-readable format
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// formatPRSummaryHuman formats a PRSummaryResponseCLI in human-readable format
func formatPRSummaryHuman(resp *PRSummaryResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString("PR Summary\n")
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	// Summary stats
	b.WriteString(fmt.Sprintf("Files Changed: %d (+%d/-%d)\n",
		resp.Summary.TotalFiles, resp.Summary.TotalAdditions, resp.Summary.TotalDeletions))
	b.WriteString(fmt.Sprintf("Modules Affected: %d\n", resp.Summary.TotalModules))
	if resp.Summary.HotspotsTouched > 0 {
		b.WriteString(fmt.Sprintf("Hotspots Touched: %d ⚠\n", resp.Summary.HotspotsTouched))
	}
	if len(resp.Summary.Languages) > 0 {
		b.WriteString(fmt.Sprintf("Languages: %s\n", strings.Join(resp.Summary.Languages, ", ")))
	}
	b.WriteString("\n")

	// Risk Assessment
	riskIcon := "✓"
	if resp.RiskAssessment.Level == "high" {
		riskIcon = "⚠"
	} else if resp.RiskAssessment.Level == "critical" {
		riskIcon = "✗"
	}
	b.WriteString(fmt.Sprintf("%s Risk Level: %s (score: %.1f)\n", riskIcon, resp.RiskAssessment.Level, resp.RiskAssessment.Score))
	if len(resp.RiskAssessment.Factors) > 0 {
		b.WriteString("  Factors:\n")
		for _, f := range resp.RiskAssessment.Factors {
			b.WriteString(fmt.Sprintf("    - %s\n", f))
		}
	}
	b.WriteString("\n")

	// Modules affected
	if len(resp.ModulesAffected) > 0 {
		b.WriteString("Modules Affected:\n")
		for _, m := range resp.ModulesAffected {
			b.WriteString(fmt.Sprintf("  %s: %d files (%s risk)\n", m.Name, m.FilesChanged, m.RiskLevel))
		}
		b.WriteString("\n")
	}

	// Suggested reviewers
	if len(resp.Reviewers) > 0 {
		b.WriteString("Suggested Reviewers:\n")
		for _, r := range resp.Reviewers {
			b.WriteString(fmt.Sprintf("  %s - %s (%.0f%% coverage)\n", r.Owner, r.Reason, r.Coverage*100))
		}
		b.WriteString("\n")
	}

	// Changed files (summarized)
	if len(resp.ChangedFiles) > 0 {
		b.WriteString(fmt.Sprintf("Changed Files (%d total):\n", len(resp.ChangedFiles)))
		shown := min(10, len(resp.ChangedFiles))
		for _, f := range resp.ChangedFiles[:shown] {
			hotspot := ""
			if f.IsHotspot {
				hotspot = " [hotspot]"
			}
			b.WriteString(fmt.Sprintf("  %s %s (+%d/-%d)%s\n", f.Status, f.Path, f.Additions, f.Deletions, hotspot))
		}
		if len(resp.ChangedFiles) > shown {
			b.WriteString(fmt.Sprintf("  ... and %d more files\n", len(resp.ChangedFiles)-shown))
		}
	}

	return b.String(), nil
}

// formatCallgraphHuman formats a CallgraphResponseCLI in human-readable format
func formatCallgraphHuman(resp *CallgraphResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Call Graph for: %s\n", resp.Root))
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	b.WriteString(fmt.Sprintf("Nodes: %d, Edges: %d\n\n", len(resp.Nodes), len(resp.Edges)))

	// Group by callers and callees based on depth
	callers := make([]CallgraphNodeCLI, 0)
	callees := make([]CallgraphNodeCLI, 0)

	for _, n := range resp.Nodes {
		if n.Depth < 0 {
			callers = append(callers, n)
		} else if n.Depth > 0 {
			callees = append(callees, n)
		}
	}

	if len(callers) > 0 {
		b.WriteString("Callers (who calls this):\n")
		for _, n := range callers[:min(15, len(callers))] {
			b.WriteString(fmt.Sprintf("  %s (%s)\n", n.Name, n.Role))
		}
		if len(callers) > 15 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(callers)-15))
		}
		b.WriteString("\n")
	}

	if len(callees) > 0 {
		b.WriteString("Callees (what this calls):\n")
		for _, n := range callees[:min(15, len(callees))] {
			b.WriteString(fmt.Sprintf("  %s (%s)\n", n.Name, n.Role))
		}
		if len(callees) > 15 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(callees)-15))
		}
	}

	return b.String(), nil
}

// formatModuleOverviewHuman formats a ModuleOverviewResponseCLI in human-readable format
func formatModuleOverviewHuman(resp *ModuleOverviewResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString("Module Overview\n")
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	b.WriteString(fmt.Sprintf("Module: %s\n", resp.Module.Name))
	b.WriteString(fmt.Sprintf("Path: %s\n", resp.Module.Path))
	b.WriteString(fmt.Sprintf("Files: %d, Symbols: %d\n\n", resp.Size.FileCount, resp.Size.SymbolCount))

	if len(resp.RecentCommits) > 0 {
		b.WriteString("Recent Commits:\n")
		for _, c := range resp.RecentCommits[:min(5, len(resp.RecentCommits))] {
			b.WriteString(fmt.Sprintf("  %s\n", c))
		}
	}

	return b.String(), nil
}

// formatJustifyHuman formats a JustifyResponseCLI in human-readable format
func formatJustifyHuman(resp *JustifyResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Symbol Justification: %s\n", resp.SymbolId))
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	// Verdict
	verdictIcon := "?"
	switch resp.Verdict {
	case "keep":
		verdictIcon = "✓"
	case "investigate":
		verdictIcon = "⚠"
	case "remove":
		verdictIcon = "✗"
	}
	b.WriteString(fmt.Sprintf("%s Verdict: %s (confidence: %.0f%%)\n\n", verdictIcon, resp.Verdict, resp.Confidence*100))

	b.WriteString(fmt.Sprintf("Reasoning: %s\n", resp.Reasoning))

	return b.String(), nil
}

// formatHotspotsHuman formats a HotspotsResponseCLI in human-readable format
func formatHotspotsHuman(resp *HotspotsResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString("Code Hotspots\n")
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	b.WriteString(fmt.Sprintf("Total Hotspots: %d (period: %s)\n\n", resp.TotalCount, resp.TimeWindow))

	for i, h := range resp.Hotspots {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, h.FilePath))
		b.WriteString(fmt.Sprintf("   Score: %.2f, Risk: %s\n", h.Score, h.RiskLevel))
		b.WriteString(fmt.Sprintf("   Changes: %d, Authors: %d\n", h.Churn.ChangeCount, h.Churn.AuthorCount))
		b.WriteString("\n")
	}

	return b.String(), nil
}

// formatComplexityHuman formats a ComplexityResponseCLI in human-readable format
func formatComplexityHuman(resp *ComplexityResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString("Complexity Analysis\n")
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	b.WriteString(fmt.Sprintf("File: %s (%s)\n\n", resp.File, resp.Language))

	// Summary
	b.WriteString(fmt.Sprintf("Functions: %d\n", resp.Summary.FunctionCount))
	b.WriteString(fmt.Sprintf("Average Cyclomatic: %.2f\n", resp.Summary.AverageCyclomatic))
	b.WriteString(fmt.Sprintf("Average Cognitive: %.2f\n", resp.Summary.AverageCognitive))
	b.WriteString(fmt.Sprintf("Max Cyclomatic: %d\n", resp.Summary.MaxCyclomatic))
	b.WriteString(fmt.Sprintf("Max Cognitive: %d\n\n", resp.Summary.MaxCognitive))

	if len(resp.Functions) > 0 {
		b.WriteString("Functions by Complexity:\n")
		for i, f := range resp.Functions[:min(20, len(resp.Functions))] {
			riskMarker := ""
			if f.Risk == "high" {
				riskMarker = " ⚠"
			}
			b.WriteString(fmt.Sprintf("  %d. %s: cyclomatic=%d, cognitive=%d%s\n",
				i+1, f.Name, f.Cyclomatic, f.Cognitive, riskMarker))
			b.WriteString(fmt.Sprintf("     Lines %d-%d\n", f.StartLine, f.EndLine))
		}
		if len(resp.Functions) > 20 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(resp.Functions)-20))
		}
	}

	return b.String(), nil
}

// formatEntrypointsHuman formats an EntrypointsResponseCLI in human-readable format
func formatEntrypointsHuman(resp *EntrypointsResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString("Entrypoints\n")
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	b.WriteString(fmt.Sprintf("Total Entrypoints: %d\n\n", resp.TotalCount))

	// Group by type
	byType := make(map[string][]EntrypointDetailCLI)
	for _, ep := range resp.Entrypoints {
		byType[ep.Type] = append(byType[ep.Type], ep)
	}

	for epType, eps := range byType {
		b.WriteString(fmt.Sprintf("%s (%d):\n", epType, len(eps)))
		for _, ep := range eps[:min(10, len(eps))] {
			b.WriteString(fmt.Sprintf("  %s\n", ep.Name))
			if ep.Location != nil {
				b.WriteString(fmt.Sprintf("    %s:%d\n", ep.Location.FileID, ep.Location.StartLine))
			}
		}
		if len(eps) > 10 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(eps)-10))
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}

// formatTraceHuman formats a TraceResponseCLI in human-readable format
func formatTraceHuman(resp *TraceResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Usage Trace: %s\n", resp.TargetSymbol))
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	b.WriteString(fmt.Sprintf("Found %d usage path(s)\n\n", resp.TotalPathsFound))

	for i, path := range resp.Paths {
		b.WriteString(fmt.Sprintf("Path %d (%s):\n", i+1, path.PathType))
		for j, node := range path.Nodes {
			indent := strings.Repeat("  ", j)
			kind := ""
			if node.Kind != "" {
				kind = fmt.Sprintf(" (%s)", node.Kind)
			}
			b.WriteString(fmt.Sprintf("%s→ %s%s\n", indent, node.Name, kind))
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}

// formatJobsListHuman formats a JobsListResponseCLI in human-readable format
func formatJobsListHuman(resp *JobsListResponseCLI) (string, error) {
	var b strings.Builder

	b.WriteString("Background Jobs\n")
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	b.WriteString(fmt.Sprintf("Total Jobs: %d\n\n", resp.TotalCount))

	if len(resp.Jobs) == 0 {
		b.WriteString("No jobs found.\n")
		return b.String(), nil
	}

	for _, job := range resp.Jobs {
		statusIcon := "○"
		switch job.Status {
		case "completed":
			statusIcon = "✓"
		case "failed":
			statusIcon = "✗"
		case "running":
			statusIcon = "◐"
		case "cancelled":
			statusIcon = "⊘"
		}
		// Handle short job IDs safely
		jobIDShort := job.ID
		if len(job.ID) > 8 {
			jobIDShort = job.ID[:8]
		}
		b.WriteString(fmt.Sprintf("%s [%s] %s (%s)\n", statusIcon, jobIDShort, job.Type, job.Status))
		b.WriteString(fmt.Sprintf("  Created: %s\n", job.CreatedAt))
		if job.Progress > 0 && job.Progress < 100 {
			b.WriteString(fmt.Sprintf("  Progress: %d%%\n", job.Progress))
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}

// formatDiffSummaryHuman formats a DiffSummaryResponseCLI in human-readable format
func formatDiffSummaryHuman(resp *DiffSummaryResponseCLI) string {
	var b strings.Builder

	b.WriteString("Diff Summary\n")
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	// Selector info
	b.WriteString(fmt.Sprintf("Scope: %s (%s)\n", resp.Selector.Type, resp.Selector.Value))
	b.WriteString(fmt.Sprintf("Confidence: %.0f%%\n\n", resp.Confidence*100))

	// One-liner summary
	if resp.Summary.OneLiner != "" {
		b.WriteString(fmt.Sprintf("Summary: %s\n\n", resp.Summary.OneLiner))
	}

	// Key changes
	if len(resp.Summary.KeyChanges) > 0 {
		b.WriteString("Key Changes:\n")
		for _, change := range resp.Summary.KeyChanges {
			b.WriteString(fmt.Sprintf("  • %s\n", change))
		}
		b.WriteString("\n")
	}

	// Risk overview
	if resp.Summary.RiskOverview != "" {
		b.WriteString(fmt.Sprintf("Risk: %s\n\n", resp.Summary.RiskOverview))
	}

	// Changed files
	if len(resp.ChangedFiles) > 0 {
		b.WriteString(fmt.Sprintf("Changed Files (%d):\n", len(resp.ChangedFiles)))
		shown := min(15, len(resp.ChangedFiles))
		for _, f := range resp.ChangedFiles[:shown] {
			riskMarker := ""
			if f.RiskLevel == "high" {
				riskMarker = " ⚠"
			} else if f.RiskLevel == "critical" {
				riskMarker = " ✗"
			}
			b.WriteString(fmt.Sprintf("  %s %s (+%d/-%d)%s\n", f.ChangeType, f.FilePath, f.Additions, f.Deletions, riskMarker))
		}
		if len(resp.ChangedFiles) > shown {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(resp.ChangedFiles)-shown))
		}
		b.WriteString("\n")
	}

	// Symbols affected
	if len(resp.SymbolsAffected) > 0 {
		b.WriteString(fmt.Sprintf("Symbols Affected (%d):\n", len(resp.SymbolsAffected)))
		shown := min(10, len(resp.SymbolsAffected))
		for _, s := range resp.SymbolsAffected[:shown] {
			markers := ""
			if s.IsPublicAPI {
				markers += " [public]"
			}
			if s.IsEntrypoint {
				markers += " [entrypoint]"
			}
			b.WriteString(fmt.Sprintf("  %s %s (%s)%s\n", s.ChangeType, s.Name, s.Kind, markers))
		}
		if len(resp.SymbolsAffected) > shown {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(resp.SymbolsAffected)-shown))
		}
		b.WriteString("\n")
	}

	// Risk signals
	if len(resp.RiskSignals) > 0 {
		b.WriteString("Risk Signals:\n")
		for _, r := range resp.RiskSignals {
			icon := "○"
			if r.Severity == "high" {
				icon = "⚠"
			} else if r.Severity == "critical" {
				icon = "✗"
			}
			b.WriteString(fmt.Sprintf("  %s [%s] %s\n", icon, r.Type, r.Description))
		}
		b.WriteString("\n")
	}

	// Limitations
	if len(resp.Limitations) > 0 {
		b.WriteString("Limitations:\n")
		for _, l := range resp.Limitations {
			b.WriteString(fmt.Sprintf("  • %s\n", l))
		}
	}

	return b.String()
}

// formatConceptsHuman formats a ConceptsResponseCLI in human-readable format
func formatConceptsHuman(resp *ConceptsResponseCLI) string {
	var b strings.Builder

	b.WriteString("Key Concepts\n")
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	b.WriteString(fmt.Sprintf("Found %d concepts (confidence: %.0f%%)\n\n", resp.TotalFound, resp.Confidence*100))

	for i, c := range resp.Concepts {
		b.WriteString(fmt.Sprintf("%d. %s", i+1, c.Name))
		if c.Category != "" {
			b.WriteString(fmt.Sprintf(" [%s]", c.Category))
		}
		b.WriteString("\n")

		if c.Description != "" {
			b.WriteString(fmt.Sprintf("   %s\n", c.Description))
		}

		b.WriteString(fmt.Sprintf("   Occurrences: %d, Score: %.2f\n", c.Occurrences, c.Score))

		if len(c.Files) > 0 {
			shown := min(3, len(c.Files))
			b.WriteString(fmt.Sprintf("   Files: %s", strings.Join(c.Files[:shown], ", ")))
			if len(c.Files) > shown {
				b.WriteString(fmt.Sprintf(" (+%d more)", len(c.Files)-shown))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Limitations
	if len(resp.Limitations) > 0 {
		b.WriteString("Limitations:\n")
		for _, l := range resp.Limitations {
			b.WriteString(fmt.Sprintf("  • %s\n", l))
		}
	}

	return b.String()
}

// formatChangeSetHuman formats a ChangeSetResponseCLI in human-readable format
func formatChangeSetHuman(resp *ChangeSetResponseCLI) string {
	var b strings.Builder

	b.WriteString("Change Impact Analysis\n")
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	// Summary
	if resp.Summary != nil {
		b.WriteString("Summary:\n")
		b.WriteString(fmt.Sprintf("  Files Changed:    %d\n", resp.Summary.FilesChanged))
		b.WriteString(fmt.Sprintf("  Symbols Changed:  %d\n", resp.Summary.SymbolsChanged))
		b.WriteString(fmt.Sprintf("  Direct Impact:    %d symbols\n", resp.Summary.DirectlyAffected))
		b.WriteString(fmt.Sprintf("  Transitive Impact: %d symbols\n", resp.Summary.TransitivelyAffected))
		b.WriteString("\n")
	}

	// Risk Score
	if resp.RiskScore != nil {
		riskIcon := "✓"
		if resp.RiskScore.Level == "high" {
			riskIcon = "⚠"
		} else if resp.RiskScore.Level == "critical" {
			riskIcon = "✗"
		}
		b.WriteString(fmt.Sprintf("%s Risk Level: %s (score: %.2f)\n", riskIcon, resp.RiskScore.Level, resp.RiskScore.Score))
		if resp.RiskScore.Explanation != "" {
			b.WriteString(fmt.Sprintf("  %s\n", resp.RiskScore.Explanation))
		}
		b.WriteString("\n")
	}

	// Blast Radius
	if resp.BlastRadius != nil {
		b.WriteString("Blast Radius:\n")
		b.WriteString(fmt.Sprintf("  Modules: %d, Files: %d, Callers: %d\n",
			resp.BlastRadius.ModuleCount, resp.BlastRadius.FileCount, resp.BlastRadius.UniqueCallerCount))
		b.WriteString("\n")
	}

	// Index Staleness Warning
	if resp.IndexStaleness != nil && resp.IndexStaleness.IsStale {
		b.WriteString(fmt.Sprintf("⚠ Index Warning: %s\n\n", resp.IndexStaleness.StalenessMessage))
	}

	// Changed Symbols (top items)
	if len(resp.ChangedSymbols) > 0 {
		b.WriteString(fmt.Sprintf("Changed Symbols (%d):\n", len(resp.ChangedSymbols)))
		shown := min(10, len(resp.ChangedSymbols))
		for _, s := range resp.ChangedSymbols[:shown] {
			b.WriteString(fmt.Sprintf("  %s %s\n", s.ChangeType, s.Name))
			b.WriteString(fmt.Sprintf("    %s\n", s.File))
		}
		if len(resp.ChangedSymbols) > shown {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(resp.ChangedSymbols)-shown))
		}
		b.WriteString("\n")
	}

	// Affected Symbols (top items)
	if len(resp.AffectedSymbols) > 0 {
		b.WriteString(fmt.Sprintf("Affected Downstream (%d):\n", len(resp.AffectedSymbols)))
		shown := min(10, len(resp.AffectedSymbols))
		for _, s := range resp.AffectedSymbols[:shown] {
			b.WriteString(fmt.Sprintf("  %s (%s, distance: %d)\n", s.Name, s.Kind, s.Distance))
		}
		if len(resp.AffectedSymbols) > shown {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(resp.AffectedSymbols)-shown))
		}
		b.WriteString("\n")
	}

	// Likely reviewers (ownership-based)
	if len(resp.Reviewers) > 0 {
		b.WriteString("Likely reviewers:\n")
		for _, r := range resp.Reviewers {
			b.WriteString(fmt.Sprintf("  %s (%.0f%% of changed files)\n", r.Owner, r.Coverage*100))
		}
		b.WriteString("\n")
	}

	// Recommendations
	if len(resp.Recommendations) > 0 {
		b.WriteString("Recommendations:\n")
		for _, r := range resp.Recommendations {
			icon := "→"
			if r.Severity == "warn" {
				icon = "⚠"
			} else if r.Severity == "error" {
				icon = "✗"
			}
			b.WriteString(fmt.Sprintf("  %s %s\n", icon, r.Message))
			if r.Action != "" {
				b.WriteString(fmt.Sprintf("    Action: %s\n", r.Action))
			}
		}
	}

	return b.String()
}

// changesRiskFactorLabels maps the internal risk-factor names computed by
// calculateAggregatedRisk to short, human-readable labels for `ckb changes`.
var changesRiskFactorLabels = map[string]string{
	"symbols_changed":   "symbols changed",
	"direct_impact":     "downstream paths",
	"transitive_impact": "transitive reach",
	"module_spread":     "module spread",
	"bridge_centrality": "architectural bridge",
}

// formatChangesHuman renders the `ckb changes` human-readable report: a
// denser, evidence-first view than formatChangeSetHuman, matching the
// assessChange mock. Every inferred/heuristic item carries its label inline.
// caps lists at 10 unless verbose is set.
// countNoun renders "1 changed symbol" / "3 changed symbols".
func countNoun(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func formatChangesHuman(resp *ChangeSetResponseCLI, verbose bool) string {
	var b strings.Builder

	cap10 := func(n int) int {
		if verbose {
			return n
		}
		return min(10, n)
	}

	// Header
	branch := "current branch"
	mode := "working tree"
	if resp.Change != nil {
		if resp.Change.Branch != "" {
			branch = resp.Change.Branch
		}
		switch resp.Change.Mode {
		case "staged":
			mode = "staged"
		case "range":
			mode = "vs " + resp.Change.Base
		default:
			mode = "working tree"
		}
	}
	b.WriteString(fmt.Sprintf("Current change · %s (%s)\n\n", branch, mode))

	// Risk
	if resp.RiskScore != nil {
		b.WriteString(fmt.Sprintf("Risk: %s (%.2f)\n", strings.ToUpper(resp.RiskScore.Level), resp.RiskScore.Score))
		for _, f := range resp.RiskScore.Factors {
			label := changesRiskFactorLabels[f.Name]
			if label == "" {
				label = f.Name
			}
			evidence := f.Evidence
			if evidence == "" {
				evidence = fmt.Sprintf("value %.2f", f.Value)
			}
			b.WriteString(fmt.Sprintf("  %-26s weight %.2f   %s\n", label, f.Weight, evidence))
		}
		b.WriteString("\n")
	}

	// Counts line
	symbolsChanged, downstream, modules := 0, 0, 0
	if resp.Summary != nil {
		symbolsChanged = resp.Summary.SymbolsChanged
		downstream = resp.Summary.DirectlyAffected
	}
	modules = len(resp.ModulesAffected)
	b.WriteString(fmt.Sprintf("%s · %s · %s\n\n",
		countNoun(symbolsChanged, "changed symbol", "changed symbols"),
		countNoun(downstream, "downstream consumer", "downstream consumers"),
		countNoun(modules, "affected module", "affected modules")))

	// Affected tests
	if len(resp.AffectedTests) > 0 {
		b.WriteString(fmt.Sprintf("Affected tests (%d)\n", len(resp.AffectedTests)))
		shown := cap10(len(resp.AffectedTests))
		for _, t := range resp.AffectedTests[:shown] {
			name := strings.TrimSuffix(filepath.Base(t.FilePath), filepath.Ext(t.FilePath))
			b.WriteString(fmt.Sprintf("  %-30s%s\n", name, t.Reason))
		}
		if len(resp.AffectedTests) > shown {
			b.WriteString(fmt.Sprintf("  … and %d more\n", len(resp.AffectedTests)-shown))
		}
		b.WriteString("\n")
	}

	// Possible contract changes
	if len(resp.Contracts) > 0 {
		basis := resp.Contracts[0].Basis
		b.WriteString(fmt.Sprintf("Possible contract changes (%d)  [%s]\n", len(resp.Contracts), basis))
		shown := cap10(len(resp.Contracts))
		for _, c := range resp.Contracts[:shown] {
			b.WriteString(fmt.Sprintf("  %-20s%s\n", c.Symbol, c.File))
		}
		if len(resp.Contracts) > shown {
			b.WriteString(fmt.Sprintf("  … and %d more\n", len(resp.Contracts)-shown))
		}
		b.WriteString("\n")
	}

	// Relevant decisions
	if len(resp.Decisions) > 0 {
		b.WriteString("Relevant decisions\n")
		shown := cap10(len(resp.Decisions))
		for _, d := range resp.Decisions[:shown] {
			b.WriteString(fmt.Sprintf("  %-8s %s\n", d.ID, d.Title))
		}
		if len(resp.Decisions) > shown {
			b.WriteString(fmt.Sprintf("  … and %d more\n", len(resp.Decisions)-shown))
		}
		b.WriteString("\n")
	}

	// Likely reviewers
	if len(resp.Reviewers) > 0 {
		b.WriteString("Likely reviewers\n")
		shown := cap10(len(resp.Reviewers))
		for _, r := range resp.Reviewers[:shown] {
			b.WriteString(fmt.Sprintf("  %s (%.0f%% of changed files)\n", r.Owner, r.Coverage*100))
		}
		if len(resp.Reviewers) > shown {
			b.WriteString(fmt.Sprintf("  … and %d more\n", len(resp.Reviewers)-shown))
		}
		b.WriteString("\n")
	}

	// Potential issues: test gaps + stale index, each labeled with its basis
	var issues []string
	if resp.TestGaps != nil && resp.TestGaps.UntestedConsumers > 0 {
		issues = append(issues, fmt.Sprintf("%d downstream consumer(s) have no reaching test  (Consumer -> Changed)",
			resp.TestGaps.UntestedConsumers))
	}
	if resp.IndexStaleness != nil && resp.IndexStaleness.IsStale {
		issues = append(issues, fmt.Sprintf("index is %d commit(s) behind — results may be stale", resp.IndexStaleness.CommitsBehind))
	}
	if len(issues) > 0 {
		b.WriteString("Potential issues\n")
		for _, issue := range issues {
			b.WriteString(fmt.Sprintf("  %s\n", issue))
		}
		if verbose && resp.TestGaps != nil && len(resp.TestGaps.Examples) > 0 {
			for _, ex := range resp.TestGaps.Examples {
				b.WriteString(fmt.Sprintf("    %s\n", ex))
			}
		}
		b.WriteString("\n")
	}

	// Confidence
	if resp.Confidence != nil {
		reasons := strings.Join(resp.Confidence.Reasons, ", ")
		b.WriteString(fmt.Sprintf("Confidence: %s", resp.Confidence.Tier))
		if reasons != "" {
			b.WriteString(fmt.Sprintf(" — %s", reasons))
		}
		b.WriteString("\n")
	}

	return b.String()
}
