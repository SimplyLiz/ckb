package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/query"
)

// Display caps for formatter output. Consistent across human and markdown formats.
const (
	maxDisplayFindings = 10
	maxDisplayClusters = 10
)

var (
	reviewFormat     string
	reviewBaseBranch string
	reviewHeadBranch string
	reviewChecks     []string
	reviewCI         bool
	reviewFailOn     string
	// Policy overrides
	reviewBlockBreaking bool
	reviewBlockSecrets  bool
	reviewRequireTests  bool
	reviewMaxRisk       float64
	reviewMaxComplexity int
	reviewMaxFiles      int
	// Critical paths
	reviewCriticalPaths []string
	// Lint dedup
	reviewLintReport string
	// Traceability
	reviewTracePatterns []string
	reviewRequireTrace  bool
	// Independence
	reviewRequireIndependent bool
	reviewMinReviewers       int
	// New analyzer flags
	reviewStaged             bool
	reviewNoAutoFetch        bool
	reviewScope              string
	reviewMaxBlastRadius     int
	reviewMaxFanOut          int
	reviewDeadCodeConfidence float64
	reviewTestGapLines       int
	reviewLLM                bool
	reviewPost               string
	reviewSkipChecks         []string
)

var reviewCmd = &cobra.Command{
	Use:   "review [scope]",
	Short: "Comprehensive PR review with quality gates",
	Long: `Run a unified code review that orchestrates multiple checks in parallel:

- Breaking API changes (SCIP-based)
- Secret detection
- Affected tests
- Complexity delta (tree-sitter)
- Coupling gaps (git co-change analysis)
- Hotspot overlap
- Risk scoring
- Safety-critical path checks
- Code health scoring (8-factor weighted score)
- Dead code detection (SCIP + constant reference analysis)
- Test gap analysis (tree-sitter + coverage cross-reference)
- Blast radius / fan-out analysis (SCIP-based, informational by default)
- Comment/code drift detection (numeric mismatch)
- Format consistency (Human vs Markdown divergence)
- Bug pattern detection (tree-sitter AST: defer-in-loop, unreachable code, etc.)
- Finding baseline management

Output formats: human (default), json, markdown, github-actions

Examples:
  ckb review                              # Review current branch vs main
  ckb review --base=develop               # Custom base branch
  ckb review --staged                     # Review staged changes only
  ckb review internal/query/              # Scope to path prefix
  ckb review --checks=breaking,secrets    # Only specific checks (allowlist)
  ckb review --skip=dead-code,blast-radius,unwired  # All checks except these (denylist)
  ckb review --checks=dead-code,test-gaps,blast-radius  # New analyzers
  ckb review --checks=bug-patterns                      # AST bug pattern detection
  ckb review --llm                                      # AI-powered narrative summary
  ckb review --checks=health              # Only code health check
  ckb review --ci                         # CI mode (exit codes: 0=pass, 1=fail, 2=warn)
  ckb review --format=markdown            # PR comment ready output
  ckb review --format=github-actions      # GitHub Actions annotations
  ckb review --critical-paths=drivers/**,protocol/**  # Safety-critical paths
  ckb review baseline save --tag=v1.0     # Save finding baseline
  ckb review baseline diff                # Compare against baseline

Large-repo incremental workflow (when full SCIP takes > 30 min):

  # One-time: build the full index (do this nightly in CI or as needed)
  ckb index --force

  # On each PR: incremental update (seconds, not minutes)
  ckb index

  # Review — skip the 3 checks that need fresh full SCIP for accuracy:
  #   dead-code:    reference graphs stale for changed symbols
  #   blast-radius: caller index stale for new/moved functions
  #   unwired:      entrypoint reachability stale for new exports
  ckb review --skip=dead-code,blast-radius,unwired

  # Or run those checks separately when you have time for a fresh full index:
  ckb review --checks=dead-code,blast-radius,unwired`,
	Args: cobra.MaximumNArgs(1),
	Run:  runReview,
}

func init() {
	reviewCmd.Flags().StringVar(&reviewFormat, "format", "human", "Output format (human, json, markdown, github-actions, sarif, codeclimate, compliance)")
	reviewCmd.Flags().StringVar(&reviewBaseBranch, "base", "main", "Base branch to compare against")
	reviewCmd.Flags().StringVar(&reviewHeadBranch, "head", "", "Head branch (default: current branch)")
	// Keep this list in sync with the checkEnabled() gates in
	// internal/query/review.go. It omitted "unwired" for several releases,
	// which is where the recurring "20 checks vs 21 checks" doc discrepancy
	// came from — the help string was the only enumeration users could see.
	reviewCmd.Flags().StringSliceVar(&reviewChecks, "checks", nil, "Comma-separated list of checks to run (breaking,secrets,tests,complexity,coupling,hotspots,risk,critical,generated,classify,split,health,traceability,independence,dead-code,unwired,test-gaps,blast-radius,comment-drift,format-consistency,bug-patterns). Three more run only when their backend is available: arch-health and layers (Cartographer), semantic-novelty (LIP)")
	reviewCmd.Flags().StringSliceVar(&reviewSkipChecks, "skip", nil, "Comma-separated list of checks to skip (complement of --checks; useful to exclude SCIP-heavy checks on large repos)")
	reviewCmd.Flags().BoolVar(&reviewCI, "ci", false, "CI mode: exit 1 on fail, exit 2 on warn")
	reviewCmd.Flags().StringVar(&reviewFailOn, "fail-on", "", "Override fail level (error, warning, none)")

	// Policy overrides
	reviewCmd.Flags().BoolVar(&reviewBlockBreaking, "block-breaking", true, "Fail on breaking changes")
	reviewCmd.Flags().BoolVar(&reviewBlockSecrets, "block-secrets", true, "Fail on detected secrets")
	reviewCmd.Flags().BoolVar(&reviewRequireTests, "require-tests", false, "Warn if no tests cover changes")
	reviewCmd.Flags().Float64Var(&reviewMaxRisk, "max-risk", 0.7, "Maximum risk score (0 = disabled)")
	reviewCmd.Flags().IntVar(&reviewMaxComplexity, "max-complexity", 0, "Maximum complexity delta (0 = disabled)")
	reviewCmd.Flags().IntVar(&reviewMaxFiles, "max-files", 0, "Maximum file count (0 = disabled)")
	reviewCmd.Flags().StringSliceVar(&reviewCriticalPaths, "critical-paths", nil, "Glob patterns for safety-critical paths")
	reviewCmd.Flags().StringVar(&reviewLintReport, "lint-report", "", "Path to existing SARIF lint report to deduplicate against")

	// Traceability
	reviewCmd.Flags().StringSliceVar(&reviewTracePatterns, "trace-patterns", nil, "Regex patterns for ticket IDs (e.g., JIRA-\\d+)")
	reviewCmd.Flags().BoolVar(&reviewRequireTrace, "require-trace", false, "Require ticket references in commits")

	// Independence
	reviewCmd.Flags().BoolVar(&reviewRequireIndependent, "require-independent", false, "Require independent reviewer (author != reviewer)")
	reviewCmd.Flags().IntVar(&reviewMinReviewers, "min-reviewers", 0, "Minimum number of independent reviewers")

	// New analyzers
	reviewCmd.Flags().BoolVar(&reviewStaged, "staged", false, "Review staged changes instead of branch diff")
	reviewCmd.Flags().BoolVar(&reviewNoAutoFetch, "no-auto-fetch", false, "Disable automatic fetch of the base ref from origin when missing locally (for air-gapped CI)")
	reviewCmd.Flags().StringVar(&reviewScope, "scope", "", "Filter to path prefix or symbol name")
	reviewCmd.Flags().IntVar(&reviewMaxBlastRadius, "max-blast-radius", 0, "Maximum blast radius delta (0 = disabled)")
	reviewCmd.Flags().IntVar(&reviewMaxFanOut, "max-fanout", 0, "Maximum fan-out / caller count (0 = disabled)")
	reviewCmd.Flags().Float64Var(&reviewDeadCodeConfidence, "dead-code-confidence", 0.8, "Minimum confidence for dead code findings")
	reviewCmd.Flags().IntVar(&reviewTestGapLines, "test-gap-lines", 5, "Minimum function lines for test gap reporting")
	reviewCmd.Flags().BoolVar(&reviewLLM, "llm", false, "Use Claude AI for narrative summary (requires ANTHROPIC_API_KEY)")
	reviewCmd.Flags().StringVar(&reviewPost, "post", "", "Post review as PR comment (PR number or branch name, requires gh CLI)")

	rootCmd.AddCommand(reviewCmd)
}

func runReview(cmd *cobra.Command, args []string) {
	start := time.Now()
	logger := newLogger(reviewFormat)

	repoRoot := mustGetRepoRoot()
	engine := mustGetEngine(repoRoot, logger)
	ctx := newContext()

	policy := query.DefaultReviewPolicy()
	policy.BlockBreakingChanges = reviewBlockBreaking
	policy.BlockSecrets = reviewBlockSecrets
	policy.RequireTests = reviewRequireTests
	policy.MaxRiskScore = reviewMaxRisk
	policy.MaxComplexityDelta = reviewMaxComplexity
	policy.MaxFiles = reviewMaxFiles
	if reviewFailOn != "" {
		policy.FailOnLevel = reviewFailOn
	}
	if len(reviewCriticalPaths) > 0 {
		policy.CriticalPaths = reviewCriticalPaths
	}
	if len(reviewTracePatterns) > 0 {
		policy.TraceabilityPatterns = reviewTracePatterns
		policy.RequireTraceability = true
	}
	if reviewRequireTrace {
		policy.RequireTraceability = true
	}
	if reviewRequireIndependent {
		policy.RequireIndependentReview = true
	}
	if reviewMinReviewers > 0 {
		policy.MinReviewers = reviewMinReviewers
	}
	if reviewMaxBlastRadius > 0 {
		policy.MaxBlastRadiusDelta = reviewMaxBlastRadius
	}
	if reviewMaxFanOut > 0 {
		policy.MaxFanOut = reviewMaxFanOut
	}
	policy.DeadCodeMinConfidence = reviewDeadCodeConfidence
	policy.TestGapMinLines = reviewTestGapLines

	// Validate inputs
	if reviewMaxRisk < 0 {
		fmt.Fprintf(os.Stderr, "Error: --max-risk must be >= 0 (got %.2f)\n", reviewMaxRisk)
		os.Exit(1)
	}
	if reviewFailOn != "" {
		validLevels := map[string]bool{"error": true, "warning": true, "none": true}
		if !validLevels[reviewFailOn] {
			fmt.Fprintf(os.Stderr, "Error: --fail-on must be one of: error, warning, none (got %q)\n", reviewFailOn)
			os.Exit(1)
		}
	}

	// Positional arg overrides --scope
	scope := reviewScope
	if len(args) > 0 {
		scope = args[0]
	}

	opts := query.ReviewPROptions{
		BaseBranch:  reviewBaseBranch,
		HeadBranch:  reviewHeadBranch,
		Policy:      policy,
		Checks:      reviewChecks,
		SkipChecks:  reviewSkipChecks,
		Staged:      reviewStaged,
		Scope:       scope,
		LLM:         reviewLLM,
		NoAutoFetch: reviewNoAutoFetch,
	}

	warnIfLIPIndexEmpty(engine, repoRoot)

	response, err := engine.ReviewPR(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running review: %v\n", err)
		os.Exit(1)
	}

	// Deduplicate against external lint report
	if reviewLintReport != "" {
		suppressed, lintErr := deduplicateLintFindings(response, reviewLintReport)
		if lintErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not parse lint report: %v\n", lintErr)
		} else if suppressed > 0 {
			logger.Debug("Deduplicated findings against lint report",
				"suppressed", suppressed, "remaining", len(response.Findings))
		}
	}

	// Format output
	var output string
	switch OutputFormat(reviewFormat) {
	case FormatMarkdown:
		output = formatReviewMarkdown(response)
	case FormatGitHubActions:
		output = formatReviewGitHubActions(response)
	case FormatCompliance:
		output = formatReviewCompliance(response)
	case FormatSARIF:
		var fmtErr error
		output, fmtErr = formatReviewSARIF(response)
		if fmtErr != nil {
			fmt.Fprintf(os.Stderr, "Error formatting SARIF: %v\n", fmtErr)
			os.Exit(1)
		}
	case FormatCodeClimate:
		var fmtErr error
		output, fmtErr = formatReviewCodeClimate(response)
		if fmtErr != nil {
			fmt.Fprintf(os.Stderr, "Error formatting CodeClimate: %v\n", fmtErr)
			os.Exit(1)
		}
	case FormatJSON:
		var fmtErr error
		output, fmtErr = formatJSON(response)
		if fmtErr != nil {
			fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", fmtErr)
			os.Exit(1)
		}
	default:
		output = formatReviewHuman(response)
	}

	fmt.Println(output)

	// Post review as PR comment if --post is set
	if reviewPost != "" {
		if postErr := postReviewComment(response, reviewPost); postErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to post review comment: %v\n", postErr)
		}
	}

	logger.Debug("Review completed",
		"baseBranch", reviewBaseBranch,
		"headBranch", reviewHeadBranch,
		"verdict", response.Verdict,
		"score", response.Score,
		"checks", len(response.Checks),
		"findings", len(response.Findings),
		"duration", time.Since(start).Milliseconds(),
	)

	// CI mode exit codes
	if reviewCI {
		switch response.Verdict {
		case "fail":
			os.Exit(1)
		case "warn":
			os.Exit(2)
		}
	}
}

// warnIfLIPIndexEmpty prints a stderr advisory when the LIP daemon is
// reachable but has no content indexed. Semantic enrichment (stream_context,
// query_expansion, explain_match) silently returns empty in that case, which
// is indistinguishable from "feature disabled" from the user's side — so we
// tell them and show the exact command to build the index.
//
// The warning goes to stderr so it doesn't pollute JSON/SARIF stdout that
// CI pipelines parse. Suppressed in --ci mode to keep CI logs tight.
func warnIfLIPIndexEmpty(engine *query.Engine, repoRoot string) {
	if reviewCI {
		return
	}
	s := engine.LIPStatus()
	if !s.Reachable || s.IndexedFiles > 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "⚠ LIP daemon reachable but has no index for this workspace.")
	fmt.Fprintln(os.Stderr, "  Semantic enrichment (related symbols, query expansion, explain-match) will be skipped.")
	fmt.Fprintf(os.Stderr, "  Run:  lip index %s\n\n", repoRoot)
}

// --- Output Formatters ---

func formatReviewHuman(resp *query.ReviewPRResponse) string {
	var b strings.Builder

	// --- Header: verdict + stats, no score (#7) ---
	verdictIcon := "✓"
	verdictLabel := "PASS"
	switch resp.Verdict {
	case "fail":
		verdictIcon = "✗"
		verdictLabel = "FAIL"
	case "warn":
		verdictIcon = "⚠"
		verdictLabel = "WARN"
	}

	b.WriteString(fmt.Sprintf("CKB Review: %s %s  ·  %d files  ·  %d lines\n",
		verdictIcon, verdictLabel, resp.Summary.TotalFiles, resp.Summary.TotalChanges))
	b.WriteString(strings.Repeat("═", 56) + "\n")

	if resp.Summary.GeneratedFiles > 0 || resp.Summary.CriticalFiles > 0 {
		b.WriteString(fmt.Sprintf("%d reviewable", resp.Summary.ReviewableFiles))
		if resp.Summary.GeneratedFiles > 0 {
			b.WriteString(fmt.Sprintf(" · %d generated (excluded)", resp.Summary.GeneratedFiles))
		}
		if resp.Summary.CriticalFiles > 0 {
			b.WriteString(fmt.Sprintf(" · %d critical", resp.Summary.CriticalFiles))
		}
		b.WriteString("\n")
	}

	if strings.HasPrefix(resp.VerdictReason, "no check warned, but") {
		b.WriteString(wrapIndent("Not a pass: "+strings.TrimPrefix(resp.VerdictReason, "no check warned, but "), "  ", 72) + "\n")
	}

	// Narrative
	if resp.Narrative != "" {
		b.WriteString("\n" + wrapIndent(resp.Narrative, "  ", 72) + "\n")
	}
	b.WriteString("\n")

	for _, l := range resp.Limitations {
		b.WriteString(wrapIndent("Not seen: "+l.Message, "  ", 72) + "\n")
		for _, ex := range l.Examples {
			b.WriteString("    " + truncateRunes(ex, 100) + "\n")
		}
		b.WriteString("\n")
	}

	// --- Checks: collapse passes into one line (#4) ---
	b.WriteString("Checks:\n")
	var passNames []string
	for _, c := range resp.Checks {
		switch c.Status {
		case "fail":
			b.WriteString(fmt.Sprintf("  ✗ %-20s %s\n", c.Name, c.Summary))
		case "warn":
			b.WriteString(fmt.Sprintf("  ⚠ %-20s %s\n", c.Name, c.Summary))
		case "info":
			b.WriteString(fmt.Sprintf("  ○ %-20s %s\n", c.Name, c.Summary))
		case "skip":
			// A skipped check didn't look: saying nothing would read as a pass.
			b.WriteString(fmt.Sprintf("  – %-20s %s\n", c.Name, c.Summary))
		case "pass":
			passNames = append(passNames, c.Name)
		}
	}
	if len(passNames) > 0 {
		b.WriteString(fmt.Sprintf("  ✓ %s\n", strings.Join(passNames, " · ")))
	}
	b.WriteString("\n")

	// --- Top Findings: filter summary restatements (#1), group co-changes (#2) ---
	if len(resp.Findings) > 0 {
		actionable, tier3Count := filterActionableFindings(resp.Findings)
		grouped := groupCoChangeFindings(actionable)
		if len(grouped) > 0 {
			b.WriteString("Top Findings:\n")
			limit := maxDisplayFindings
			if len(grouped) < limit {
				limit = len(grouped)
			}
			for _, g := range grouped[:limit] {
				loc := g.file
				if loc == "" {
					loc = "(global)"
				}
				b.WriteString(fmt.Sprintf("  ⚠ %s\n", loc))
				for _, msg := range g.messages {
					b.WriteString(fmt.Sprintf("      %s\n", msg))
				}
				if g.hint != "" {
					b.WriteString(fmt.Sprintf("      %s\n", g.hint))
				}
			}
			remaining := len(grouped) - limit
			if remaining > 0 || tier3Count > 0 {
				parts := []string{}
				if remaining > 0 {
					parts = append(parts, fmt.Sprintf("%d more findings", remaining))
				}
				if tier3Count > 0 {
					parts = append(parts, fmt.Sprintf("%d informational", tier3Count))
				}
				b.WriteString(fmt.Sprintf("  ... and %s\n", strings.Join(parts, ", ")))
			}
			b.WriteString("\n")
		}
	}

	// --- Review Effort: cap absurd estimates ---
	if resp.ReviewEffort != nil {
		estimate := formatEffortEstimate(resp.ReviewEffort, resp.SplitSuggestion,
			resp.Summary.TotalFiles, resp.Summary.TotalChanges)
		b.WriteString(fmt.Sprintf("Estimated Review: %s\n", estimate))
		if resp.ReviewEffort.EstimatedMinutes <= 480 && resp.PRTier != "large" {
			for _, f := range resp.ReviewEffort.Factors {
				b.WriteString(fmt.Sprintf("  · %s\n", f))
			}
		}
		b.WriteString("\n")
	}

	// Change Breakdown — skip for large PRs
	if resp.PRTier != "large" && resp.ChangeBreakdown != nil && len(resp.ChangeBreakdown.Summary) > 0 {
		b.WriteString("Change Breakdown:\n")
		cats := sortedMapKeys(resp.ChangeBreakdown.Summary)
		for _, cat := range cats {
			b.WriteString(fmt.Sprintf("  %-12s %d files\n", cat, resp.ChangeBreakdown.Summary[cat]))
		}
		b.WriteString("\n")
	}

	// PR Split Suggestion
	if resp.SplitSuggestion != nil && resp.SplitSuggestion.ShouldSplit {
		b.WriteString("PR Split:\n")
		clusterLimit := maxDisplayClusters
		clusters := resp.SplitSuggestion.Clusters
		if len(clusters) > clusterLimit {
			clusters = clusters[:clusterLimit]
		}
		for _, c := range clusters {
			b.WriteString(fmt.Sprintf("  %-22s %d files  +%d −%d\n",
				c.Name, c.FileCount, c.Additions, c.Deletions))
		}
		if len(resp.SplitSuggestion.Clusters) > clusterLimit {
			b.WriteString(fmt.Sprintf("  ... %d more  (ckb review --split for full list)\n",
				len(resp.SplitSuggestion.Clusters)-clusterLimit))
		}
		b.WriteString("\n")
	}

	// --- Code Health: collapse for large PRs (#5) ---
	if resp.HealthReport != nil && len(resp.HealthReport.Deltas) > 0 {
		if resp.PRTier == "large" {
			// One-liner for large PRs — only show if something degraded
			if resp.HealthReport.Degraded > 0 {
				worst := worstDegraded(resp.HealthReport.Deltas)
				b.WriteString(fmt.Sprintf("Code Health: %d degraded (avg %+.1f) · worst: %s (%s→%s)\n\n",
					resp.HealthReport.Degraded, resp.HealthReport.AverageDelta,
					worst.File, worst.GradeBefore, worst.Grade))
			} else {
				// Count new files
				newCount := 0
				for _, d := range resp.HealthReport.Deltas {
					if d.NewFile {
						newCount++
					}
				}
				if newCount > 0 {
					b.WriteString(fmt.Sprintf("Code Health: 0 degraded · %d new (avg %d)\n\n",
						newCount, avgHealth(resp.HealthReport.Deltas)))
				}
			}
		} else {
			// Per-file detail for small/medium PRs
			b.WriteString("Code Health:\n")
			shown := 0
			for _, d := range resp.HealthReport.Deltas {
				if d.Delta == 0 && !d.NewFile {
					continue
				}
				if shown >= 10 {
					continue
				}
				arrow := "→"
				label := ""
				if d.NewFile {
					arrow = "★"
					label = " (new)"
				} else if d.Delta < 0 {
					arrow = "↓"
				} else if d.Delta > 0 {
					arrow = "↑"
				}
				confLabel := ""
				if d.Confidence < 0.6 {
					confLabel = " (low confidence)"
				}
				if !d.Parseable {
					confLabel += " [unparseable]"
				}
				b.WriteString(fmt.Sprintf("  %s %s %s (%d)%s%s\n",
					d.Grade, arrow, d.File, d.HealthAfter, label, confLabel))
				shown++
			}
			if resp.HealthReport.Degraded > 0 || resp.HealthReport.Improved > 0 {
				b.WriteString(fmt.Sprintf("  %d degraded · %d improved · avg %+.1f\n",
					resp.HealthReport.Degraded, resp.HealthReport.Improved, resp.HealthReport.AverageDelta))
			}
			b.WriteString("\n")
		}
	}

	// --- Reviewers: clean email display (#6) ---
	if len(resp.Reviewers) > 0 {
		b.WriteString("Reviewers: ")
		var parts []string
		for _, r := range resp.Reviewers {
			name := formatReviewerName(r.Owner)
			parts = append(parts, fmt.Sprintf("%s (%.0f%%)", name, r.Coverage*100))
		}
		b.WriteString(strings.Join(parts, " · "))
		b.WriteString("\n")
	}

	return b.String()
}

// formatReviewerName cleans up reviewer identity for display.
// Emails become local part only; usernames get @ prefix.
func formatReviewerName(owner string) string {
	if strings.Contains(owner, "@") {
		return strings.Split(owner, "@")[0]
	}
	return "@" + owner
}

// formatEffortEstimate returns a human-readable effort string, capping absurd values.
func formatEffortEstimate(effort *query.ReviewEffort, split *query.PRSplitSuggestion, files, lines int) string {
	if effort.EstimatedMinutes > 480 {
		clusters := 0
		if split != nil {
			clusters = len(split.Clusters)
		}
		if clusters > 0 {
			return fmt.Sprintf("not feasible as a single PR (%d files, %d lines, %d clusters)",
				files, lines, clusters)
		}
		return fmt.Sprintf("not feasible as a single PR (%d files, %d lines)", files, lines)
	}
	return fmt.Sprintf("~%dmin (%s)", effort.EstimatedMinutes, effort.Complexity)
}

// wrapIndent wraps text to a given width with consistent indentation.
func wrapIndent(s, indent string, width int) string {
	words := strings.Fields(s)
	var lines []string
	line := indent
	for _, w := range words {
		if len(line)+len(w)+1 > width && line != indent {
			lines = append(lines, line)
			line = indent + w
		} else {
			if line == indent {
				line += w
			} else {
				line += " " + w
			}
		}
	}
	if line != indent {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// worstDegraded finds the file with the largest health degradation.
func worstDegraded(deltas []query.CodeHealthDelta) query.CodeHealthDelta {
	var worst query.CodeHealthDelta
	for _, d := range deltas {
		if !d.NewFile && d.Delta < worst.Delta {
			worst = d
		}
	}
	return worst
}

// groupedFinding represents one or more co-change findings collapsed into one entry.
type groupedFinding struct {
	severity string
	file     string
	messages []string
	hint     string
}

// groupCoChangeFindings collapses per-file co-change findings into single
// grouped entries, preserving insertion order so co-changes don't get pushed
// to the back behind non-grouped findings.
func groupCoChangeFindings(findings []query.ReviewFinding) []groupedFinding {
	var result []groupedFinding
	byFile := map[string]*groupedFinding{}
	groupPositions := map[string]int{} // key → index in result

	for _, f := range findings {
		if !strings.HasPrefix(f.Message, "Missing co-change:") {
			result = append(result, groupedFinding{
				severity: f.Severity,
				file:     f.File,
				messages: []string{f.Message},
				hint:     f.Hint,
			})
			continue
		}
		key := f.File
		if _, ok := byFile[key]; ok {
			byFile[key].messages = append(byFile[key].messages, f.Message)
		} else {
			g := &groupedFinding{severity: f.Severity, file: key, messages: []string{f.Message}}
			byFile[key] = g
			groupPositions[key] = len(result)
			result = append(result, groupedFinding{}) // placeholder
		}
	}
	// Fill placeholders with collapsed groups
	for key, pos := range groupPositions {
		g := byFile[key]
		var targets []string
		for _, msg := range g.messages {
			targets = append(targets, strings.TrimPrefix(msg, "Missing co-change: "))
		}
		result[pos] = groupedFinding{
			severity: g.severity,
			file:     g.file,
			messages: []string{"Usually changed with: " + strings.Join(targets, ", ")},
		}
	}
	return result
}

func formatReviewMarkdown(resp *query.ReviewPRResponse) string {
	var b strings.Builder

	// Header
	verdictEmoji := "✅"
	switch resp.Verdict {
	case "fail":
		verdictEmoji = "🔴"
	case "warn":
		verdictEmoji = "🟡"
	}

	b.WriteString(fmt.Sprintf("## CKB Review: %s %s — %d/100\n\n",
		verdictEmoji, strings.ToUpper(resp.Verdict), resp.Score))

	b.WriteString(fmt.Sprintf("**%d files** (+%d changes) · **%d modules**",
		resp.Summary.TotalFiles, resp.Summary.TotalChanges, resp.Summary.ModulesChanged))
	if len(resp.Summary.Languages) > 0 {
		b.WriteString(" · `" + strings.Join(resp.Summary.Languages, "` `") + "`")
	}
	b.WriteString("\n")

	if resp.Summary.GeneratedFiles > 0 || resp.Summary.CriticalFiles > 0 {
		b.WriteString(fmt.Sprintf("**%d reviewable**", resp.Summary.ReviewableFiles))
		if resp.Summary.GeneratedFiles > 0 {
			b.WriteString(fmt.Sprintf(" · %d generated (excluded)", resp.Summary.GeneratedFiles))
		}
		if resp.Summary.CriticalFiles > 0 {
			b.WriteString(fmt.Sprintf(" · **%d safety-critical**", resp.Summary.CriticalFiles))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")

	if strings.HasPrefix(resp.VerdictReason, "no check warned, but") {
		b.WriteString("Not a pass: " + strings.TrimPrefix(resp.VerdictReason, "no check warned, but ") + "\n\n")
	}

	// Narrative
	if resp.Narrative != "" {
		b.WriteString("> " + resp.Narrative + "\n\n")
	}

	for _, l := range resp.Limitations {
		b.WriteString("**Not seen:** " + l.Message + "\n\n")
		for _, ex := range l.Examples {
			b.WriteString("- `" + strings.ReplaceAll(truncateRunes(ex, 100), "`", "'") + "`\n")
		}
		if len(l.Examples) > 0 {
			b.WriteString("\n")
		}
	}

	// Checks table
	b.WriteString("| Check | Status | Detail |\n")
	b.WriteString("|-------|--------|--------|\n")
	for _, c := range resp.Checks {
		statusEmoji := "✅ PASS"
		switch c.Status {
		case "fail":
			statusEmoji = "🔴 FAIL"
		case "warn":
			statusEmoji = "🟡 WARN"
		case "skip":
			statusEmoji = "⚪ SKIP"
		case "info":
			statusEmoji = "ℹ️ INFO"
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", c.Name, statusEmoji, escapeMdTable(c.Summary)))
	}
	b.WriteString("\n")

	// Top Risks — the review narrative between checks and findings
	if len(resp.Summary.TopRisks) > 0 {
		b.WriteString("### Top Risks\n\n")
		for _, risk := range resp.Summary.TopRisks {
			b.WriteString(fmt.Sprintf("- %s\n", risk))
		}
		b.WriteString("\n")
	}

	// Findings — Tier 1+2 only, capped at 10
	if len(resp.Findings) > 0 {
		actionable, tier3Count := filterActionableFindings(resp.Findings)
		label := fmt.Sprintf("Findings (%d)", len(actionable))
		if tier3Count > 0 {
			label = fmt.Sprintf("Findings (%d actionable, %d informational)", len(actionable), tier3Count)
		}
		if len(actionable) > 0 {
			b.WriteString(fmt.Sprintf("<details><summary>%s</summary>\n\n", label))
			b.WriteString("| Severity | File | Finding |\n")
			b.WriteString("|----------|------|---------|\n")
			limit := maxDisplayFindings
			if len(actionable) < limit {
				limit = len(actionable)
			}
			for _, f := range actionable[:limit] {
				sevEmoji := "ℹ️"
				switch f.Severity {
				case "error":
					sevEmoji = "🔴"
				case "warning":
					sevEmoji = "🟡"
				}
				loc := f.File
				if f.StartLine > 0 {
					loc = fmt.Sprintf("`%s:%d`", f.File, f.StartLine)
				} else if f.File != "" {
					loc = fmt.Sprintf("`%s`", f.File)
				}
				msg := escapeMdTable(f.Message)
				if f.Hint != "" {
					msg += " *" + escapeMdTable(f.Hint) + "*"
				}
				b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", sevEmoji, loc, msg))
			}
			if len(actionable) > limit {
				b.WriteString(fmt.Sprintf("\n... and %d more\n", len(actionable)-limit))
			}
			b.WriteString("\n</details>\n\n")
		}
	}

	// Change Breakdown — skip for large PRs
	if resp.PRTier != "large" && resp.ChangeBreakdown != nil && len(resp.ChangeBreakdown.Summary) > 0 {
		b.WriteString("<details><summary>Change Breakdown</summary>\n\n")
		b.WriteString("| Category | Files | Review Priority |\n")
		b.WriteString("|----------|-------|-----------------|\n")
		priorityEmoji := map[string]string{
			"new": "🔴 Full review", "churn": "🔴 Stability concern",
			"refactoring": "🟡 Verify correctness", "modified": "🟡 Standard review",
			"test": "🟡 Verify coverage", "moved": "🟢 Quick check",
			"config": "🟢 Quick check", "generated": "⚪ Skip (review source)",
		}
		cats := sortedMapKeys(resp.ChangeBreakdown.Summary)
		for _, cat := range cats {
			count := resp.ChangeBreakdown.Summary[cat]
			priority := priorityEmoji[cat]
			if priority == "" {
				priority = "🟡 Review"
			}
			b.WriteString(fmt.Sprintf("| %s | %d | %s |\n", cat, count, priority))
		}
		b.WriteString("\n</details>\n\n")
	}

	// PR Split Suggestion
	if resp.SplitSuggestion != nil && resp.SplitSuggestion.ShouldSplit {
		clusters := resp.SplitSuggestion.Clusters
		clusterLimit := maxDisplayClusters
		b.WriteString(fmt.Sprintf("<details><summary>✂️ Suggested PR Split (%d clusters)</summary>\n\n",
			len(clusters)))
		b.WriteString("| Cluster | Files | Changes | Independent |\n")
		b.WriteString("|---------|-------|---------|-------------|\n")
		if len(clusters) > clusterLimit {
			clusters = clusters[:clusterLimit]
		}
		for _, c := range clusters {
			indep := "✅"
			if !c.Independent {
				indep = "❌"
			}
			b.WriteString(fmt.Sprintf("| %s | %d | +%d −%d | %s |\n",
				c.Name, c.FileCount, c.Additions, c.Deletions, indep))
		}
		if len(resp.SplitSuggestion.Clusters) > clusterLimit {
			b.WriteString(fmt.Sprintf("\n... and %d more clusters\n",
				len(resp.SplitSuggestion.Clusters)-clusterLimit))
		}
		b.WriteString("\n</details>\n\n")
	}

	// Code Health — show degraded files first, then new files; skip unchanged
	if resp.HealthReport != nil && len(resp.HealthReport.Deltas) > 0 {
		// Separate into degraded, improved, and new
		var degraded, improved, newFiles []query.CodeHealthDelta
		for _, d := range resp.HealthReport.Deltas {
			switch {
			case d.NewFile:
				newFiles = append(newFiles, d)
			case d.Delta < 0:
				degraded = append(degraded, d)
			case d.Delta > 0:
				improved = append(improved, d)
			}
		}

		healthTitle := "Code Health"
		if len(degraded) > 0 {
			healthTitle = fmt.Sprintf("Code Health — %d degraded", len(degraded))
		}
		b.WriteString(fmt.Sprintf("<details><summary>%s</summary>\n\n", healthTitle))

		if len(degraded) > 0 {
			// Check if any delta has low confidence
			hasLowConf := false
			for _, d := range resp.HealthReport.Deltas {
				if d.Confidence < 1.0 {
					hasLowConf = true
					break
				}
			}

			b.WriteString("**Degraded:**\n\n")
			if hasLowConf {
				b.WriteString("| File | Before | After | Delta | Grade | Confidence |\n")
				b.WriteString("|------|--------|-------|-------|-------|------------|\n")
			} else {
				b.WriteString("| File | Before | After | Delta | Grade |\n")
				b.WriteString("|------|--------|-------|-------|-------|\n")
			}
			limit := 10
			if len(degraded) < limit {
				limit = len(degraded)
			}
			for _, d := range degraded[:limit] {
				if hasLowConf {
					confStr := fmt.Sprintf("%.0f%%", d.Confidence*100)
					if !d.Parseable {
						confStr += " ^1"
					}
					b.WriteString(fmt.Sprintf("| `%s` | %d | %d | %+d | %s→%s | %s |\n",
						d.File, d.HealthBefore, d.HealthAfter, d.Delta, d.GradeBefore, d.Grade, confStr))
				} else {
					b.WriteString(fmt.Sprintf("| `%s` | %d | %d | %+d | %s→%s |\n",
						d.File, d.HealthBefore, d.HealthAfter, d.Delta, d.GradeBefore, d.Grade))
				}
			}
			if len(degraded) > limit {
				b.WriteString(fmt.Sprintf("\n... and %d more degraded files\n", len(degraded)-limit))
			}
			hasUnparseable := false
			for _, d := range resp.HealthReport.Deltas {
				if !d.Parseable {
					hasUnparseable = true
					break
				}
			}
			if hasUnparseable {
				b.WriteString("\n^1 File could not be parsed by tree-sitter\n")
			}
			b.WriteString("\n")
		}
		if len(improved) > 0 {
			b.WriteString(fmt.Sprintf("**Improved:** %d file(s)\n\n", len(improved)))
		}
		if len(newFiles) > 0 {
			b.WriteString(fmt.Sprintf("**New files:** %d (avg health: %d)\n\n",
				len(newFiles), avgHealth(newFiles)))
		}

		if resp.HealthReport.Degraded > 0 || resp.HealthReport.Improved > 0 {
			b.WriteString(fmt.Sprintf("%d degraded · %d improved · avg %+.1f\n",
				resp.HealthReport.Degraded, resp.HealthReport.Improved, resp.HealthReport.AverageDelta))
		}
		b.WriteString("\n</details>\n\n")
	}

	// Review Effort
	if resp.ReviewEffort != nil {
		b.WriteString(fmt.Sprintf("**Estimated review:** %s\n\n",
			formatEffortEstimate(resp.ReviewEffort, resp.SplitSuggestion,
				resp.Summary.TotalFiles, resp.Summary.TotalChanges)))
	}

	// Reviewers
	if len(resp.Reviewers) > 0 {
		var parts []string
		for _, r := range resp.Reviewers {
			parts = append(parts, fmt.Sprintf("%s (%.0f%%)", formatReviewerName(r.Owner), r.Coverage*100))
		}
		b.WriteString("**Reviewers:** " + strings.Join(parts, " · ") + "\n\n")
	}

	// Marker for update-in-place
	b.WriteString("<!-- ckb-review-marker -->\n")

	return b.String()
}

// filterActionableFindings separates Tier 1+2 (actionable) from Tier 3 (informational),
// strips summary-restatement findings, and priority-sorts the result so the
// budget cap keeps the most important findings.
func filterActionableFindings(findings []query.ReviewFinding) (actionable []query.ReviewFinding, tier3Count int) {
	for _, f := range findings {
		if isSummaryRestatement(f.Message) {
			tier3Count++
			continue
		}
		if f.Tier <= 2 {
			actionable = append(actionable, f)
		} else {
			tier3Count++
		}
	}
	// Priority sort: tier 1 first, then by severity within tier
	sort.SliceStable(actionable, func(i, j int) bool {
		return findingScore(actionable[i]) > findingScore(actionable[j])
	})
	return
}

func findingScore(f query.ReviewFinding) int {
	base := map[int]int{1: 1000, 2: 100, 3: 10}[f.Tier]
	sev := map[string]int{"error": 3, "warning": 2, "info": 1}[f.Severity]
	return base + sev
}

// isSummaryRestatement returns true for findings that just restate what's
// already visible in the header/narrative (file count, churn, hotspots, modules).
func isSummaryRestatement(msg string) bool {
	summaryPrefixes := []string{
		"Large PR with ",
		"Medium-sized PR with ",
		"High churn: ",
		"Moderate churn: ",
		"Touches ",
		"Spans ",
		"Small, focused change",
	}
	for _, p := range summaryPrefixes {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}

func avgHealth(deltas []query.CodeHealthDelta) int {
	if len(deltas) == 0 {
		return 0
	}
	total := 0
	for _, d := range deltas {
		total += d.HealthAfter
	}
	return total / len(deltas)
}

// escapeMdTable escapes pipe characters that would break markdown table formatting.
func escapeMdTable(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

func sortedMapKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func formatReviewGitHubActions(resp *query.ReviewPRResponse) string {
	var b strings.Builder

	for _, f := range resp.Findings {
		level := "notice"
		switch f.Severity {
		case "error":
			level = "error"
		case "warning":
			level = "warning"
		}

		msg := escapeGHA(f.Message)
		ruleID := escapeGHA(f.RuleID)

		if f.File != "" {
			if f.StartLine > 0 {
				b.WriteString(fmt.Sprintf("::%s file=%s,line=%d::%s [%s]\n",
					level, f.File, f.StartLine, msg, ruleID))
			} else {
				b.WriteString(fmt.Sprintf("::%s file=%s::%s [%s]\n",
					level, f.File, msg, ruleID))
			}
		} else {
			b.WriteString(fmt.Sprintf("::%s::%s [%s]\n", level, msg, ruleID))
		}
	}

	return b.String()
}

// escapeGHA escapes special characters for GitHub Actions workflow commands.
// See: https://github.com/actions/toolkit/blob/main/packages/core/src/command.ts
func escapeGHA(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
}

// postReviewComment posts the review as a PR comment using the gh CLI.
func postReviewComment(resp *query.ReviewPRResponse, prRef string) error {
	// Check if gh is available
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI not found — install from https://cli.github.com")
	}

	// Generate markdown output for the comment
	body := formatReviewMarkdown(resp)

	// Post using gh pr comment
	cmd := exec.Command("gh", "pr", "comment", prRef, "--body", body)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh pr comment failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Review posted to PR %s\n", prRef)
	return nil
}

// truncateRunes cuts s to at most n characters, not bytes, so a multi-byte
// character in a quoted code line isn't split.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
