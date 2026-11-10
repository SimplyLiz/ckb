package query

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/git"
	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/diff"
	"github.com/SimplyLiz/CodeMCP/internal/lip"
	"github.com/SimplyLiz/CodeMCP/internal/secrets"
	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// ReviewPROptions configures the unified PR review.
type ReviewPROptions struct {
	BaseBranch string        `json:"baseBranch"` // default: "main"
	HeadBranch string        `json:"headBranch"` // default: HEAD
	Policy     *ReviewPolicy `json:"policy"`     // Quality gates (or from .ckb/review.json)
	Checks     []string      `json:"checks"`     // Filter which checks to run (default: all)
	SkipChecks []string      `json:"skipChecks"` // Checks to exclude (complement of Checks)
	MaxInline  int           `json:"maxInline"`  // Max inline suggestions (default: 10)
	Staged     bool          `json:"staged"`     // Review staged changes instead of branch diff
	Scope      string        `json:"scope"`      // Filter to path prefix or symbol name
	LLM        bool          `json:"llm"`        // Use LLM for narrative generation

	// NoAutoFetch disables the automatic `git fetch origin <base>` fallback
	// when the base ref is missing locally. Useful in air-gapped CI or when
	// the pipeline forbids network activity outside the checkout step.
	NoAutoFetch bool `json:"noAutoFetch"`
}

// ReviewPolicy defines quality gates and behavior.
type ReviewPolicy struct {
	// Gates
	BlockBreakingChanges bool    `json:"blockBreakingChanges"` // default: true
	BlockSecrets         bool    `json:"blockSecrets"`         // default: true
	RequireTests         bool    `json:"requireTests"`         // default: false
	MaxRiskScore         float64 `json:"maxRiskScore"`         // default: 0.7 (0 = disabled)
	MaxComplexityDelta   int     `json:"maxComplexityDelta"`   // default: 0 (disabled)
	MaxFiles             int     `json:"maxFiles"`             // default: 0 (disabled)

	// Behavior
	FailOnLevel string `json:"failOnLevel"` // "error" (default), "warning", "none"
	HoldTheLine bool   `json:"holdTheLine"` // Only flag issues on changed lines (default: true)

	// Large PR handling
	SplitThreshold int `json:"splitThreshold"` // Suggest split above N files (default: 50)

	// Generated file detection
	GeneratedPatterns []string `json:"generatedPatterns"` // Glob patterns
	GeneratedMarkers  []string `json:"generatedMarkers"`  // Comment markers in first 10 lines

	// Safety-critical paths
	CriticalPaths    []string `json:"criticalPaths"`    // Glob patterns
	CriticalSeverity string   `json:"criticalSeverity"` // default: "error"

	// Traceability (commit-to-ticket linkage)
	TraceabilityPatterns         []string `json:"traceabilityPatterns"`         // Regex patterns for ticket IDs
	TraceabilitySources          []string `json:"traceabilitySources"`          // Where to look: "commit-message", "branch-name"
	RequireTraceability          bool     `json:"requireTraceability"`          // Enforce ticket references
	RequireTraceForCriticalPaths bool     `json:"requireTraceForCriticalPaths"` // Only enforce for critical paths

	// Reviewer independence (regulated industry)
	RequireIndependentReview bool `json:"requireIndependentReview"` // Author != reviewer
	MinReviewers             int  `json:"minReviewers"`             // Minimum independent reviewers (default: 1)

	// Analyzer thresholds (v8.2)
	MaxBlastRadiusDelta   int     `json:"maxBlastRadiusDelta"`   // 0 = disabled
	MaxFanOut             int     `json:"maxFanOut"`             // 0 = disabled
	DeadCodeMinConfidence float64 `json:"deadCodeMinConfidence"` // default 0.8
	TestGapMinLines       int     `json:"testGapMinLines"`       // default 5
}

// ReviewPRResponse is the unified review result.
type ReviewPRResponse struct {
	CkbVersion    string              `json:"ckbVersion"`
	SchemaVersion string              `json:"schemaVersion"`
	Tool          string              `json:"tool"`
	Verdict       string              `json:"verdict"` // "pass", "warn", "fail"
	Score         int                 `json:"score"`   // 0-100
	Summary       ReviewSummary       `json:"summary"`
	Checks        []ReviewCheck       `json:"checks"`
	Findings      []ReviewFinding     `json:"findings"`
	Reviewers     []SuggestedReview   `json:"reviewers,omitempty"`
	Generated     []GeneratedFileInfo `json:"generated,omitempty"`
	// Batch 3: Large PR Intelligence
	SplitSuggestion  *PRSplitSuggestion          `json:"splitSuggestion,omitempty"`
	ChangeBreakdown  *ChangeBreakdown            `json:"changeBreakdown,omitempty"`
	ReviewEffort     *ReviewEffort               `json:"reviewEffort,omitempty"`
	ClusterReviewers []ClusterReviewerAssignment `json:"clusterReviewers,omitempty"`
	// Batch 4: Code Health & Baseline
	HealthReport *CodeHealthReport `json:"healthReport,omitempty"`
	Provenance   *Provenance       `json:"provenance,omitempty"`
	// Narrative & adaptive output
	Narrative string `json:"narrative,omitempty"` // 2-3 sentence review summary
	PRTier    string `json:"prTier"`              // "small", "medium", "large"
}

// ReviewSummary provides a high-level overview.
type ReviewSummary struct {
	TotalFiles      int      `json:"totalFiles"`
	TotalChanges    int      `json:"totalChanges"`
	GeneratedFiles  int      `json:"generatedFiles"`
	ReviewableFiles int      `json:"reviewableFiles"`
	CriticalFiles   int      `json:"criticalFiles"`
	ChecksPassed    int      `json:"checksPassed"`
	ChecksWarned    int      `json:"checksWarned"`
	ChecksFailed    int      `json:"checksFailed"`
	ChecksSkipped   int      `json:"checksSkipped"`
	TopRisks        []string `json:"topRisks"`
	Languages       []string `json:"languages"`
	ModulesChanged  int      `json:"modulesChanged"`
}

// ReviewCheck represents a single check result.
type ReviewCheck struct {
	Name     string      `json:"name"`
	Status   string      `json:"status"`   // "pass", "warn", "fail", "skip"
	Severity string      `json:"severity"` // "error", "warning", "info"
	Summary  string      `json:"summary"`
	Details  interface{} `json:"details,omitempty"`
	Duration int64       `json:"durationMs"`
}

// ReviewFinding is a single actionable finding.
type ReviewFinding struct {
	Check      string  `json:"check"`
	Severity   string  `json:"severity"` // "error", "warning", "info"
	File       string  `json:"file"`
	StartLine  int     `json:"startLine,omitempty"`
	EndLine    int     `json:"endLine,omitempty"`
	Message    string  `json:"message"`
	Detail     string  `json:"detail,omitempty"`
	Suggestion string  `json:"suggestion,omitempty"`
	Category   string  `json:"category"`
	RuleID     string  `json:"ruleId,omitempty"`
	Hint       string  `json:"hint,omitempty"`       // e.g., "→ ckb explain <symbol>"
	Tier       int     `json:"tier"`                 // 1=blocking, 2=important, 3=informational
	Confidence float64 `json:"confidence,omitempty"` // 0.0-1.0, rule self-reported confidence
}

// findingTier maps a check name to its tier.
// Tier 1: breaking changes, secrets, safety-critical — must fix.
// Tier 2: coupling, complexity, risk, health — should fix.
// Tier 3: hotspots, tests, generated, traceability, independence — nice to know.
func findingTier(check string) int {
	switch check {
	case "breaking", "secrets", "critical":
		return 1
	case "coupling", "complexity", "risk", "health", "dead-code", "blast-radius", "bug-patterns", "layers", "arch-health":
		return 2
	case "test-gaps", "comment-drift", "format-consistency":
		return 3
	default:
		return 3
	}
}

// GeneratedFileInfo tracks a detected generated file.
type GeneratedFileInfo struct {
	File       string `json:"file"`
	Reason     string `json:"reason"`
	SourceFile string `json:"sourceFile,omitempty"`
}

// DefaultReviewPolicy returns sensible defaults.
func DefaultReviewPolicy() *ReviewPolicy {
	return &ReviewPolicy{
		BlockBreakingChanges: true,
		BlockSecrets:         true,
		FailOnLevel:          "error",
		HoldTheLine:          true,
		SplitThreshold:       50,
		GeneratedPatterns: []string{
			// Generic generated markers
			"*.generated.*", "*_generated.go", "*_gen.go",
			// Protobuf/gRPC
			"*.pb.go", "*.pb.cc", "*.pb.h", "*.pb.ts", "*.pb.js",
			"*_grpc.pb.go", "*_pb2.py", "*_pb2_grpc.py",
			// Parser generators
			"parser.tab.c", "lex.yy.c",
			// API generators
			"*.swagger.json", "*.openapi.json",
			// Bundled/minified
			"*.min.js", "*.min.css", "*.bundle.js", "*.chunk.js",
			"**/dist/*.js", "**/dist/*.css",
			// Go generators
			"*_string.go", "*_enumer.go", "wire_gen.go", "*_mock.go",
			// Dart/Flutter
			"*.g.dart", "*.freezed.dart", "*.mocks.dart",
			// GraphQL
			"*__generated__*",
		},
		GeneratedMarkers: []string{
			"DO NOT EDIT", "Generated by", "AUTO-GENERATED", "This file is generated",
			"Code generated", "Automatically generated",
			"swagger-codegen", "openapi-generator",
			"@generated", "protoc-gen", "graphql-codegen",
			"Code generated by", "generated by stringer", "generated by mockgen",
			"generated by Wire", "GENERATED BY THE COMMAND ABOVE",
		},
		CriticalSeverity:      "error",
		DeadCodeMinConfidence: 0.8,
		TestGapMinLines:       5,
	}
}

// ReviewPR performs a comprehensive PR review by orchestrating multiple checks in parallel.
func (e *Engine) ReviewPR(ctx context.Context, opts ReviewPROptions) (*ReviewPRResponse, error) {
	startTime := time.Now()

	// Apply defaults
	if opts.BaseBranch == "" {
		opts.BaseBranch = "main"
	}
	if opts.HeadBranch == "" {
		opts.HeadBranch = "HEAD"
	}
	if opts.Policy == nil {
		opts.Policy = DefaultReviewPolicy()
	}
	// Merge config defaults into policy (config provides repo-level defaults,
	// callers can override per-invocation)
	if e.config != nil {
		rc := e.config.Review
		mergeReviewConfig(opts.Policy, &rc)
	}
	if opts.MaxInline <= 0 {
		opts.MaxInline = 10
	}

	if e.gitAdapter == nil {
		return nil, fmt.Errorf("git adapter not available")
	}

	// Get changed files
	var diffStats []git.DiffStats
	var err error
	if opts.Staged {
		diffStats, err = e.gitAdapter.GetStagedDiff()
	} else {
		// Resolve base ref — shallow CI clones often lack it locally; EnsureRef
		// fetches from origin and returns a resolvable form (e.g. "origin/main").
		if opts.NoAutoFetch {
			if verr := e.gitAdapter.VerifyRef(opts.BaseBranch); verr != nil {
				return nil, fmt.Errorf("failed to resolve base ref %q: %w", opts.BaseBranch, verr)
			}
		} else {
			resolved, rerr := e.gitAdapter.EnsureRef(opts.BaseBranch)
			if rerr != nil {
				return nil, fmt.Errorf("failed to resolve base ref %q: %w", opts.BaseBranch, rerr)
			}
			opts.BaseBranch = resolved
		}
		diffStats, err = e.gitAdapter.GetCommitRangeDiff(opts.BaseBranch, opts.HeadBranch)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get diff: %w", err)
	}

	// Apply scope filter
	if opts.Scope != "" {
		diffStats = e.filterDiffByScope(ctx, diffStats, opts.Scope)
	}

	// Build changed-lines map for HoldTheLine filtering
	var changedLinesMap map[string]map[int]bool
	if opts.Policy.HoldTheLine {
		var rawDiff string
		if opts.Staged {
			rawDiff, _ = e.gitAdapter.GetStagedDiffUnified()
		} else {
			rawDiff, _ = e.gitAdapter.GetCommitRangeDiffUnified(opts.BaseBranch, opts.HeadBranch)
		}
		if rawDiff != "" {
			changedLinesMap = buildChangedLinesMap(rawDiff)
		}
	}

	if len(diffStats) == 0 {
		return &ReviewPRResponse{
			CkbVersion:    version.Version,
			SchemaVersion: "8.2",
			Tool:          "reviewPR",
			Verdict:       "pass",
			Score:         100,
			Summary:       ReviewSummary{},
			Checks:        []ReviewCheck{},
			Findings:      []ReviewFinding{},
		}, nil
	}

	// Build file list and basic stats
	changedFiles := make([]string, 0, len(diffStats))
	languages := make(map[string]bool)
	modules := make(map[string]bool)
	totalAdditions := 0
	totalDeletions := 0

	for _, df := range diffStats {
		changedFiles = append(changedFiles, df.FilePath)
		totalAdditions += df.Additions
		totalDeletions += df.Deletions
		if lang := detectLanguage(df.FilePath); lang != "" {
			languages[lang] = true
		}
		if mod := e.resolveFileModule(df.FilePath); mod != "" {
			modules[mod] = true
		}
	}

	// Detect generated files
	generatedSet := make(map[string]bool)
	var generatedFiles []GeneratedFileInfo
	for _, df := range diffStats {
		if info, ok := detectGeneratedFile(e.repoRoot, df.FilePath, opts.Policy); ok {
			generatedSet[df.FilePath] = true
			generatedFiles = append(generatedFiles, info)
		}
	}

	// Build reviewable file list (excluding generated)
	reviewableFiles := make([]string, 0, len(changedFiles))
	for _, f := range changedFiles {
		if !generatedSet[f] {
			reviewableFiles = append(reviewableFiles, f)
		}
	}

	// Run checks in parallel
	checkEnabled := func(name string) bool {
		// SkipChecks is evaluated first — explicit exclusion wins.
		for _, c := range opts.SkipChecks {
			if c == name {
				return false
			}
		}
		// Checks allowlist: if non-empty, only listed checks run.
		if len(opts.Checks) == 0 {
			return true
		}
		for _, c := range opts.Checks {
			if c == name {
				return true
			}
		}
		return false
	}

	var mu sync.Mutex
	var checks []ReviewCheck
	var findings []ReviewFinding

	addCheck := func(c ReviewCheck) {
		mu.Lock()
		checks = append(checks, c)
		mu.Unlock()
	}
	addFindings := func(ff []ReviewFinding) {
		mu.Lock()
		findings = append(findings, ff...)
		mu.Unlock()
	}

	var wg sync.WaitGroup

	// The symbol-level change set, shared by the tests and blast-radius checks:
	// computed once, by whichever of them asks first, while the other waits.
	changeSet := sync.OnceValues(func() (*AnalyzeChangeSetResponse, error) {
		return e.AnalyzeChangeSet(ctx, affectedTestsChangeSetOptions(opts.BaseBranch, opts.Staged))
	})

	// Check: Breaking Changes
	if checkEnabled("breaking") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkBreakingChanges(ctx, opts)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Check: Secrets
	if checkEnabled("secrets") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkSecrets(ctx, reviewableFiles)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Check: Affected Tests
	if checkEnabled("tests") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkAffectedTests(ctx, opts, changeSet)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Pre-compute hotspot score map once (no tree-sitter — uses SkipComplexity).
	// Shared by checkHotspots and checkRiskScore to avoid duplicate GetHotspots calls.
	var hotspotScores map[string]float64
	if checkEnabled("hotspots") || checkEnabled("risk") {
		hotspotScores = e.getHotspotScoreMapFast(ctx)
	}

	// Tree-sitter checks — go-tree-sitter cgo is NOT thread-safe. Each check
	// runs in its own goroutine but acquires e.tsMu around tree-sitter calls.
	// Non-tree-sitter work (git subprocesses, scoring) runs without the lock,
	// so checks overlap their I/O with each other.
	var healthReport *CodeHealthReport

	if checkEnabled("complexity") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkComplexityDelta(ctx, reviewableFiles, opts)
			addCheck(c)
			addFindings(ff)
		}()
	}

	if checkEnabled("health") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff, report := e.checkCodeHealth(ctx, reviewableFiles, opts)
			addCheck(c)
			addFindings(ff)
			mu.Lock()
			healthReport = report
			mu.Unlock()
		}()
	}

	// Hotspots — uses pre-computed scores, no tree-sitter needed.
	if checkEnabled("hotspots") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkHotspotsWithScores(ctx, reviewableFiles, hotspotScores)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Risk — uses pre-computed data, no tree-sitter or SummarizePR needed.
	if checkEnabled("risk") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkRiskScoreFast(ctx, diffStats, reviewableFiles, modules, hotspotScores, opts)
			addCheck(c)
			addFindings(ff)
		}()
	}

	if checkEnabled("test-gaps") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkTestGaps(ctx, reviewableFiles, opts)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Check: Coupling Gaps
	if checkEnabled("coupling") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkCouplingGaps(ctx, reviewableFiles, diffStats)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Check: Dead Code (SCIP-only, parallel safe)
	if checkEnabled("dead-code") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkDeadCode(ctx, changedFiles, opts)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Check: Unwired Modules (SCIP-only, parallel safe)
	if checkEnabled("unwired") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkUnwiredModules(ctx, changedFiles, opts)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Check: Blast Radius (SCIP-only, parallel safe)
	if checkEnabled("blast-radius") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkBlastRadius(ctx, changedFiles, opts, changeSet)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Check: Critical Paths
	if checkEnabled("critical") && len(opts.Policy.CriticalPaths) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkCriticalPaths(ctx, reviewableFiles, opts)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Check: Traceability (commit-to-ticket linkage)
	if checkEnabled("traceability") && (opts.Policy.RequireTraceability || opts.Policy.RequireTraceForCriticalPaths) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkTraceability(ctx, reviewableFiles, opts)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Check: Reviewer Independence
	if checkEnabled("independence") && opts.Policy.RequireIndependentReview {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkReviewerIndependence(ctx, opts)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Check: Format Consistency
	if checkEnabled("format-consistency") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkFormatConsistency(ctx, reviewableFiles)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Check: Bug Patterns (tree-sitter AST analysis)
	if checkEnabled("bug-patterns") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkBugPatternsWithDiff(ctx, reviewableFiles, opts)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Check: Comment/Code Drift
	if checkEnabled("comment-drift") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkCommentDrift(ctx, reviewableFiles)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Check: Layer violations (cartographer — skip gracefully if not compiled in)
	if checkEnabled("layers") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkLayerViolations(ctx, reviewableFiles, opts)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Check: Architectural health (cartographer — skip gracefully if not compiled in)
	if checkEnabled("arch-health") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkArchitecturalHealth(ctx, reviewableFiles)
			addCheck(c)
			addFindings(ff)
		}()
	}

	// Check: Generated files (info only)
	if checkEnabled("generated") && len(generatedFiles) > 0 {
		addCheck(ReviewCheck{
			Name:     "generated",
			Status:   "info",
			Severity: "info",
			Summary:  fmt.Sprintf("%d generated files detected and excluded", len(generatedFiles)),
		})
	}

	// Check: Semantic novelty (LIP-powered, best-effort — silently skipped when LIP
	// is unavailable). Flags files whose embeddings are far from the rest of the
	// codebase; high novelty often correlates with missing test coverage.
	if checkEnabled("semantic-novelty") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ff := e.checkSemanticNovelty(ctx, changedFiles)
			if c.Name != "" {
				addCheck(c)
				addFindings(ff)
			}
		}()
	}

	wg.Wait()

	// Post-filter findings to changed lines only when HoldTheLine is enabled
	if opts.Policy.HoldTheLine && changedLinesMap != nil {
		findings = filterByChangedLines(findings, changedLinesMap)
	}

	// Filter dismissed findings (user feedback from .ckb/review-dismissals.json)
	dismissals := LoadDismissals(e.repoRoot)
	if len(dismissals.Dismissals) > 0 {
		findings, _ = dismissals.FilterDismissed(findings)
	}

	// Reconcile check summaries with post-filtered findings. Checks set
	// their summary before HoldTheLine/dismissal filtering, so a check
	// may report "5 new bug patterns" while all 5 findings were dropped
	// because they're on unchanged lines. Update summaries to match.
	reconcileCheckSummaries(checks, findings)

	// Sort checks by severity (fail first, then warn, then pass)
	sortChecks(checks)

	// Sort findings by severity and assign tiers
	sortFindings(findings)
	for i := range findings {
		findings[i].Tier = findingTier(findings[i].Check)
	}

	// Calculate summary
	summary := ReviewSummary{
		TotalFiles:      len(changedFiles),
		TotalChanges:    totalAdditions + totalDeletions,
		GeneratedFiles:  len(generatedFiles),
		ReviewableFiles: len(reviewableFiles),
		ModulesChanged:  len(modules),
	}

	for lang := range languages {
		summary.Languages = append(summary.Languages, lang)
	}
	sort.Strings(summary.Languages)

	for _, c := range checks {
		switch c.Status {
		case "pass":
			summary.ChecksPassed++
		case "warn":
			summary.ChecksWarned++
		case "fail":
			summary.ChecksFailed++
		case "skip", "info":
			summary.ChecksSkipped++
		}
	}

	// Build top risks from failed/warned checks
	for _, c := range checks {
		if (c.Status == "fail" || c.Status == "warn") && len(summary.TopRisks) < 3 {
			summary.TopRisks = append(summary.TopRisks, c.Summary)
		}
	}

	// Calculate score
	score := calculateReviewScore(checks, findings)

	// Determine verdict
	verdict := determineVerdict(checks, opts.Policy)

	// Count critical files
	for _, f := range findings {
		if f.Category == "critical" {
			summary.CriticalFiles++
		}
	}

	// Get suggested reviewers
	prFiles := make([]PRFileChange, 0, len(reviewableFiles))
	for _, df := range diffStats {
		if !generatedSet[df.FilePath] {
			prFiles = append(prFiles, PRFileChange{Path: df.FilePath})
		}
	}
	reviewers := e.getSuggestedReviewers(ctx, prFiles)

	// --- Batch 3: Large PR Intelligence ---

	// Change classification
	var breakdown *ChangeBreakdown
	if checkEnabled("classify") || len(diffStats) >= 10 {
		breakdown = e.classifyChanges(ctx, diffStats, generatedSet, opts)
	}

	// PR split suggestion (when above threshold)
	var splitSuggestion *PRSplitSuggestion
	var clusterReviewers []ClusterReviewerAssignment
	if checkEnabled("split") || len(diffStats) >= opts.Policy.SplitThreshold {
		splitSuggestion = e.suggestPRSplit(ctx, diffStats, opts.Policy)
		if splitSuggestion != nil && splitSuggestion.ShouldSplit {
			clusterReviewers = e.assignClusterReviewers(ctx, splitSuggestion.Clusters)

			// Add split check
			addCheck(ReviewCheck{
				Name:     "split",
				Status:   "warn",
				Severity: "warning",
				Summary:  splitSuggestion.Reason,
				Details:  splitSuggestion,
			})
		}
	}

	// Review effort estimation
	effort := estimateReviewEffort(diffStats, breakdown, summary.CriticalFiles, len(modules))

	// Re-sort after adding split check
	sortChecks(checks)

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "head")
	if err != nil {
		repoState = &RepoState{RepoStateId: "unknown"}
	}

	resp := &ReviewPRResponse{
		CkbVersion:       version.Version,
		SchemaVersion:    "8.2",
		Tool:             "reviewPR",
		Verdict:          verdict,
		Score:            score,
		Summary:          summary,
		Checks:           checks,
		Findings:         findings,
		Reviewers:        reviewers,
		Generated:        generatedFiles,
		SplitSuggestion:  splitSuggestion,
		ChangeBreakdown:  breakdown,
		ReviewEffort:     effort,
		ClusterReviewers: clusterReviewers,
		HealthReport:     healthReport,
		Narrative:        generateNarrative(summary, checks, findings, splitSuggestion),
		PRTier:           determinePRTier(summary.TotalChanges),
		Provenance: &Provenance{
			RepoStateId:     repoState.RepoStateId,
			RepoStateDirty:  repoState.Dirty,
			QueryDurationMs: time.Since(startTime).Milliseconds(),
		},
	}

	// Optional LLM narrative (replaces deterministic one on success)
	if opts.LLM {
		if llmNarrative, err := e.generateLLMNarrative(ctx, resp); err == nil {
			resp.Narrative = llmNarrative
		}
	}

	return resp, nil
}

// determinePRTier classifies a PR by total line changes.
func determinePRTier(totalChanges int) string {
	switch {
	case totalChanges < 100:
		return "small"
	case totalChanges <= 600:
		return "medium"
	default:
		return "large"
	}
}

// generateNarrative produces a deterministic 2-3 sentence review summary.
func generateNarrative(summary ReviewSummary, checks []ReviewCheck, findings []ReviewFinding, split *PRSplitSuggestion) string {
	var parts []string

	// Sentence 1: What changed
	langStr := ""
	if len(summary.Languages) > 0 {
		langStr = " (" + strings.Join(summary.Languages, ", ") + ")"
	}
	parts = append(parts, fmt.Sprintf("Changes %d files across %d modules%s.",
		summary.TotalFiles, summary.ModulesChanged, langStr))

	// Sentence 2: What's risky — pick the most important signal
	tier1Count := 0
	for _, f := range findings {
		if f.Tier == 1 {
			tier1Count++
		}
	}
	if tier1Count > 0 {
		// Summarize tier 1 issues
		riskParts := []string{}
		for _, c := range checks {
			if c.Status == "fail" {
				riskParts = append(riskParts, c.Summary)
			}
		}
		if len(riskParts) > 0 {
			parts = append(parts, strings.Join(riskParts, "; ")+".")
		}
	} else if summary.ChecksWarned > 0 {
		// Pick the 2 most distinctive warned checks — prefer checks with
		// fewer findings (they tend to be more specific/actionable).
		type warnInfo struct {
			summary      string
			findingCount int
		}
		var warns []warnInfo
		checkFindingCount := make(map[string]int)
		for _, f := range findings {
			checkFindingCount[f.Check]++
		}
		for _, c := range checks {
			if c.Status == "warn" {
				warns = append(warns, warnInfo{c.Summary, checkFindingCount[c.Name]})
			}
		}
		// Sort: fewer findings first (more specific), then alphabetically for stability
		sort.SliceStable(warns, func(i, j int) bool {
			return warns[i].findingCount < warns[j].findingCount
		})
		warnParts := []string{}
		for _, w := range warns {
			if len(warnParts) >= 2 {
				break
			}
			warnParts = append(warnParts, w.summary)
		}
		if len(warnParts) > 0 {
			parts = append(parts, strings.Join(warnParts, "; ")+".")
		}
	} else {
		parts = append(parts, "No blocking issues found.")
	}

	// Sentence 3: Where to focus or split recommendation
	if split != nil && split.ShouldSplit {
		parts = append(parts, fmt.Sprintf("Consider splitting into %d smaller PRs.",
			len(split.Clusters)))
	} else if summary.CriticalFiles > 0 {
		parts = append(parts, fmt.Sprintf("%d safety-critical files need focused review.",
			summary.CriticalFiles))
	}

	return strings.Join(parts, " ")
}

// --- Individual check implementations ---

func (e *Engine) checkBreakingChanges(ctx context.Context, opts ReviewPROptions) (ReviewCheck, []ReviewFinding) {
	start := time.Now()

	resp, err := e.CompareAPI(ctx, CompareAPIOptions{
		BaseRef:       opts.BaseBranch,
		TargetRef:     opts.HeadBranch,
		IgnorePrivate: true,
	})

	if err != nil {
		return ReviewCheck{
			Name:     "breaking",
			Status:   "skip",
			Severity: "error",
			Summary:  fmt.Sprintf("Could not analyze: %v", err),
			Duration: time.Since(start).Milliseconds(),
		}, nil
	}

	var findings []ReviewFinding

	for _, change := range resp.Changes {
		if change.Severity == "breaking" || change.Severity == "error" {
			findings = append(findings, ReviewFinding{
				Check:    "breaking",
				Severity: "error",
				File:     change.FilePath,
				Message:  change.Description,
				Category: "breaking",
				RuleID:   fmt.Sprintf("ckb/breaking/%s", change.Kind),
			})
		}
	}

	// Filter out rename pairs — a removed + added symbol in the same file
	// with the same kind is likely a rename, not a breaking change.
	findings = filterRenamePairs(findings)
	breakingCount := len(findings)

	status := "pass"
	severity := "error"
	summary := "No breaking API changes"
	if breakingCount > 0 {
		status = "fail"
		summary = fmt.Sprintf("%d breaking API change(s) detected", breakingCount)
	}

	return ReviewCheck{
		Name:     "breaking",
		Status:   status,
		Severity: severity,
		Summary:  summary,
		Duration: time.Since(start).Milliseconds(),
	}, findings
}

// filterRenamePairs removes findings that are likely renames rather than
// breaking changes. A rename produces "removed X" + "added Y" in the same
// file with the same kind — not a real API break. Both sides of a matched
// pair are removed: the "removed" finding is noise, and the "added" finding
// is not a new API symbol — it's the renamed version of the old one.
func filterRenamePairs(findings []ReviewFinding) []ReviewFinding {
	// Group by file — use sorted keys for deterministic output.
	byFile := make(map[string][]ReviewFinding)
	for _, f := range findings {
		byFile[f.File] = append(byFile[f.File], f)
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	var filtered []ReviewFinding
	for _, file := range files {
		fileFindings := byFile[file]
		// Count removed and added per kind
		removedByKind := make(map[string]int)
		addedByKind := make(map[string]int)
		for _, f := range fileFindings {
			if isRemovedFinding(f.Message) {
				removedByKind[f.RuleID]++
			} else if isAddedFinding(f.Message) {
				addedByKind[f.RuleID]++
			}
		}

		// Compute how many pairs to consume per kind (min of removed, added).
		pairsByKind := make(map[string]int)
		for kind, rem := range removedByKind {
			if add := addedByKind[kind]; add > 0 {
				pairs := rem
				if add < pairs {
					pairs = add
				}
				pairsByKind[kind] = pairs
			}
		}

		// Second pass: consume paired findings from both sides.
		removedLeft := make(map[string]int)
		addedLeft := make(map[string]int)
		for k, v := range pairsByKind {
			removedLeft[k] = v
			addedLeft[k] = v
		}

		for _, f := range fileFindings {
			kind := f.RuleID
			if isRemovedFinding(f.Message) && removedLeft[kind] > 0 {
				removedLeft[kind]--
				continue
			}
			if isAddedFinding(f.Message) && addedLeft[kind] > 0 {
				addedLeft[kind]--
				continue
			}
			filtered = append(filtered, f)
		}
	}
	return filtered
}

func isRemovedFinding(msg string) bool {
	return strings.Contains(msg, "removed") || strings.Contains(msg, "Removed")
}

func isAddedFinding(msg string) bool {
	return strings.Contains(msg, "added") || strings.Contains(msg, "Added") || strings.Contains(msg, "new")
}

func (e *Engine) checkSecrets(ctx context.Context, files []string) (ReviewCheck, []ReviewFinding) {
	start := time.Now()

	scanner := secrets.NewScanner(e.repoRoot, e.logger)
	result, err := scanner.Scan(ctx, secrets.ScanOptions{
		RepoRoot:       e.repoRoot,
		Scope:          secrets.ScopeWorkdir,
		Paths:          files,
		ApplyAllowlist: true,
		MinEntropy:     3.5,
	})

	if err != nil {
		return ReviewCheck{
			Name:     "secrets",
			Status:   "skip",
			Severity: "error",
			Summary:  fmt.Sprintf("Could not scan: %v", err),
			Duration: time.Since(start).Milliseconds(),
		}, nil
	}

	var findings []ReviewFinding
	for _, f := range result.Findings {
		if f.Suppressed {
			continue
		}
		sev := "warning"
		if f.Severity == secrets.SeverityCritical || f.Severity == secrets.SeverityHigh {
			sev = "error"
		}
		findings = append(findings, ReviewFinding{
			Check:     "secrets",
			Severity:  sev,
			File:      f.File,
			StartLine: f.Line,
			Message:   fmt.Sprintf("Potential %s detected", f.Type),
			Category:  "security",
			RuleID:    fmt.Sprintf("ckb/secrets/%s", f.Type),
		})
	}

	status := "pass"
	summary := "No secrets detected"
	count := len(findings)
	if count > 0 {
		status = "fail"
		summary = fmt.Sprintf("%d potential secret(s) found", count)
	}

	return ReviewCheck{
		Name:     "secrets",
		Status:   status,
		Severity: "error",
		Summary:  summary,
		Duration: time.Since(start).Milliseconds(),
	}, findings
}

func (e *Engine) checkAffectedTests(ctx context.Context, opts ReviewPROptions, changeSet func() (*AnalyzeChangeSetResponse, error)) (ReviewCheck, []ReviewFinding) {
	start := time.Now()

	cs, err := changeSet()
	var resp *AffectedTestsResponse
	if err == nil {
		resp = e.affectedTestsFromChangeSet(ctx, cs, start)
	}

	if err != nil {
		return ReviewCheck{
			Name:     "tests",
			Status:   "skip",
			Severity: "warning",
			Summary:  fmt.Sprintf("Could not analyze: %v", err),
			Duration: time.Since(start).Milliseconds(),
		}, nil
	}

	testCount := len(resp.Tests)
	status := "pass"
	summary := affectedTestsSummary(resp)

	var findings []ReviewFinding
	if testCount == 0 && opts.Policy.RequireTests {
		status = "warn"
		summary = "No tests found for changed code"
		findings = append(findings, ReviewFinding{
			Check:      "tests",
			Severity:   "warning",
			File:       "",
			Message:    "No tests were found that cover the changed code",
			Suggestion: "Consider adding tests for the changed functionality",
			Category:   "testing",
			RuleID:     "ckb/tests/no-coverage",
		})
	}

	return ReviewCheck{
		Name:     "tests",
		Status:   status,
		Severity: "warning",
		Summary:  summary,
		Duration: time.Since(start).Milliseconds(),
	}, findings
}

// affectedTestsSummary says how many test files the change reaches and on what
// evidence: a use of a changed symbol, or only of something in a changed file.
func affectedTestsSummary(resp *AffectedTestsResponse) string {
	total := len(resp.Tests)
	fileLevel := 0
	if resp.Summary != nil {
		fileLevel = resp.Summary.FileLevelFiles
	}
	if fileLevel == 0 {
		return fmt.Sprintf("%d test(s) cover the changes", total)
	}
	return fmt.Sprintf("%d test file(s) reach the changes: %d through a changed symbol, %d only through something else in a changed file",
		total, total-fileLevel, fileLevel)
}

func (e *Engine) checkCriticalPaths(ctx context.Context, files []string, opts ReviewPROptions) (ReviewCheck, []ReviewFinding) {
	start := time.Now()

	var findings []ReviewFinding
	critSeverity := opts.Policy.CriticalSeverity
	if critSeverity == "" {
		critSeverity = "error"
	}

	for _, file := range files {
		for _, pattern := range opts.Policy.CriticalPaths {
			matched, _ := matchGlob(pattern, file)
			if matched {
				findings = append(findings, ReviewFinding{
					Check:      "critical",
					Severity:   critSeverity,
					File:       file,
					Message:    fmt.Sprintf("Safety-critical path changed (pattern: %s)", pattern),
					Suggestion: "Requires sign-off from safety team",
					Category:   "critical",
					RuleID:     "ckb/critical/safety-path",
				})
				break // Don't double-match same file
			}
		}
	}

	status := "pass"
	summary := "No safety-critical files touched"
	if len(findings) > 0 {
		status = "fail"
		summary = fmt.Sprintf("%d safety-critical file(s) changed", len(findings))
	}

	return ReviewCheck{
		Name:     "critical",
		Status:   status,
		Severity: critSeverity,
		Summary:  summary,
		Duration: time.Since(start).Milliseconds(),
	}, findings
}

// --- Helpers ---

func sortChecks(checks []ReviewCheck) {
	order := map[string]int{"fail": 0, "warn": 1, "info": 2, "pass": 3, "skip": 4}
	sort.Slice(checks, func(i, j int) bool {
		return order[checks[i].Status] < order[checks[j].Status]
	})
}

func sortFindings(findings []ReviewFinding) {
	sevOrder := map[string]int{"error": 0, "warning": 1, "info": 2}
	sort.SliceStable(findings, func(i, j int) bool {
		// Primary: tier (1=blocking first)
		if findings[i].Tier != findings[j].Tier {
			return findings[i].Tier < findings[j].Tier
		}
		// Secondary: severity within tier
		si, sj := sevOrder[findings[i].Severity], sevOrder[findings[j].Severity]
		if si != sj {
			return si < sj
		}
		// Tertiary: file path for determinism
		return findings[i].File < findings[j].File
	})
}

func calculateReviewScore(checks []ReviewCheck, findings []ReviewFinding) int {
	score := 100

	// Cap per-check deductions so noisy checks (e.g., coupling with many
	// co-change warnings) don't overwhelm the score on their own.
	checkDeductions := make(map[string]int)
	const maxPerCheck = 20
	// Cap per-rule within a check — prevents one noisy rule from consuming
	// the entire check budget (e.g., discarded-error flooding bug-patterns).
	ruleDeductions := make(map[string]int)
	const maxPerRule = 10
	// Total deduction cap — prevents the score from becoming meaningless
	// on large PRs where many checks each hit their per-check cap.
	const maxTotalDeduction = 80
	totalDeducted := 0

	for _, f := range findings {
		if totalDeducted >= maxTotalDeduction {
			break
		}
		penalty := 0
		switch f.Severity {
		case "error":
			penalty = 10
		case "warning":
			penalty = 3
		case "info":
			penalty = 1
		}
		if penalty > 0 {
			checkCurrent := checkDeductions[f.Check]
			if checkCurrent >= maxPerCheck {
				continue
			}
			// Per-rule cap only applies when the finding has a rule ID
			if f.RuleID != "" {
				ruleCurrent := ruleDeductions[f.RuleID]
				if ruleCurrent >= maxPerRule {
					continue
				}
				apply := penalty
				if checkCurrent+apply > maxPerCheck {
					apply = maxPerCheck - checkCurrent
				}
				if ruleCurrent+apply > maxPerRule {
					apply = maxPerRule - ruleCurrent
				}
				if totalDeducted+apply > maxTotalDeduction {
					apply = maxTotalDeduction - totalDeducted
				}
				score -= apply
				checkDeductions[f.Check] = checkCurrent + apply
				ruleDeductions[f.RuleID] = ruleCurrent + apply
				totalDeducted += apply
			} else {
				apply := penalty
				if checkCurrent+apply > maxPerCheck {
					apply = maxPerCheck - checkCurrent
				}
				if totalDeducted+apply > maxTotalDeduction {
					apply = maxTotalDeduction - totalDeducted
				}
				score -= apply
				checkDeductions[f.Check] = checkCurrent + apply
				totalDeducted += apply
			}
		}
	}

	if score < 0 {
		score = 0
	}
	return score
}

func determineVerdict(checks []ReviewCheck, policy *ReviewPolicy) string {
	failLevel := policy.FailOnLevel
	if failLevel == "" {
		failLevel = "error"
	}

	hasFail := false
	hasWarn := false
	for _, c := range checks {
		if c.Status == "fail" {
			hasFail = true
		}
		if c.Status == "warn" {
			hasWarn = true
		}
	}

	switch failLevel {
	case "none":
		return "pass"
	case "warning":
		if hasFail || hasWarn {
			return "fail"
		}
	default: // "error"
		if hasFail {
			return "fail"
		}
		if hasWarn {
			return "warn"
		}
	}

	return "pass"
}

// detectGeneratedFile checks if a file is generated based on policy patterns and markers.
// Checks (in order): glob patterns, flex/yacc mappings, marker strings in first 10 lines.
func detectGeneratedFile(repoRoot, filePath string, policy *ReviewPolicy) (GeneratedFileInfo, bool) {
	// Check glob patterns
	for _, pattern := range policy.GeneratedPatterns {
		matched, _ := matchGlob(pattern, filePath)
		if matched {
			return GeneratedFileInfo{
				File:   filePath,
				Reason: fmt.Sprintf("Matches pattern %s", pattern),
			}, true
		}
	}

	// Check flex/yacc source mappings
	base := strings.TrimSuffix(filePath, ".tab.c")
	if base != filePath {
		return GeneratedFileInfo{
			File:       filePath,
			Reason:     "flex/yacc generated output",
			SourceFile: base + ".y",
		}, true
	}
	base = strings.TrimSuffix(filePath, ".yy.c")
	if base != filePath {
		return GeneratedFileInfo{
			File:       filePath,
			Reason:     "flex/yacc generated output",
			SourceFile: base + ".l",
		}, true
	}

	// Check marker strings in the first 10 lines of the file
	if len(policy.GeneratedMarkers) > 0 && repoRoot != "" {
		if marker := checkFileForMarkers(filepath.Join(repoRoot, filePath), policy.GeneratedMarkers); marker != "" {
			return GeneratedFileInfo{
				File:   filePath,
				Reason: fmt.Sprintf("Contains marker: %s", marker),
			}, true
		}
	}

	return GeneratedFileInfo{}, false
}

// checkFileForMarkers reads the first 10 lines of a file and returns the first
// matching marker string, or empty if none match.
func checkFileForMarkers(absPath string, markers []string) string {
	f, err := os.Open(absPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for i := 0; i < 10 && scanner.Scan(); i++ {
		line := scanner.Text()
		for _, marker := range markers {
			if strings.Contains(line, marker) {
				return marker
			}
		}
	}
	return ""
}

// matchGlob performs simple glob matching (supports ** and *).
func matchGlob(pattern, path string) (bool, error) {
	// Use filepath.Match for patterns without **
	if !strings.Contains(pattern, "**") {
		return matchSimpleGlob(pattern, path), nil
	}

	// Split on first ** occurrence only
	idx := strings.Index(pattern, "**")
	prefix := pattern[:idx]
	suffix := pattern[idx+2:]
	suffix = strings.TrimPrefix(suffix, "/")

	if prefix != "" && !strings.HasPrefix(path, prefix) {
		return false, nil
	}
	if suffix == "" {
		return true, nil
	}

	// For the remaining suffix, strip the prefix from the path and check
	// if any trailing segment matches the suffix (which may itself contain **)
	remaining := path
	if prefix != "" {
		remaining = strings.TrimPrefix(path, prefix)
	}

	// If the suffix contains another **, recurse
	if strings.Contains(suffix, "**") {
		// Try matching suffix against every possible substring of remaining path
		parts := strings.Split(remaining, "/")
		for i := range parts {
			candidate := strings.Join(parts[i:], "/")
			if matched, _ := matchGlob(suffix, candidate); matched {
				return true, nil
			}
		}
		return false, nil
	}

	// Simple suffix: try matching against every possible tail of the path.
	// e.g., for suffix "dist/*.js" and path "a/b/dist/index.js",
	// try "a/b/dist/index.js", "b/dist/index.js", "dist/index.js".
	parts := strings.Split(remaining, "/")
	for i := range parts {
		candidate := strings.Join(parts[i:], "/")
		if matchSimpleGlob(suffix, candidate) {
			return true, nil
		}
	}
	return false, nil
}

// matchSimpleGlob matches a pattern with * wildcards against a string.
func matchSimpleGlob(pattern, str string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == str
	}

	parts := strings.Split(pattern, "*")
	if len(parts) == 2 {
		return strings.HasPrefix(str, parts[0]) && strings.HasSuffix(str, parts[1])
	}
	// Fallback: check if all parts appear in order
	remaining := str
	for _, part := range parts {
		if part == "" {
			continue
		}
		idx := strings.Index(remaining, part)
		if idx < 0 {
			return false
		}
		remaining = remaining[idx+len(part):]
	}
	return true
}

// mergeReviewConfig applies config-level defaults to a review policy.
// Config values fill in gaps — explicit caller overrides take priority.
func mergeReviewConfig(policy *ReviewPolicy, rc *config.ReviewConfig) {
	// Only merge generated patterns/markers if policy has none (caller didn't override)
	if len(policy.GeneratedPatterns) == 0 && len(rc.GeneratedPatterns) > 0 {
		policy.GeneratedPatterns = rc.GeneratedPatterns
	} else if len(rc.GeneratedPatterns) > 0 {
		// Append config patterns to defaults
		policy.GeneratedPatterns = append(policy.GeneratedPatterns, rc.GeneratedPatterns...)
	}

	if len(policy.GeneratedMarkers) == 0 && len(rc.GeneratedMarkers) > 0 {
		policy.GeneratedMarkers = rc.GeneratedMarkers
	} else if len(rc.GeneratedMarkers) > 0 {
		policy.GeneratedMarkers = append(policy.GeneratedMarkers, rc.GeneratedMarkers...)
	}

	// Critical paths: append config to any caller-provided ones
	if len(rc.CriticalPaths) > 0 {
		policy.CriticalPaths = append(policy.CriticalPaths, rc.CriticalPaths...)
	}

	// Numeric thresholds: use config if caller left at zero/default
	if policy.MaxRiskScore == 0 && rc.MaxRiskScore > 0 {
		policy.MaxRiskScore = rc.MaxRiskScore
	}
	if policy.MaxComplexityDelta == 0 && rc.MaxComplexityDelta > 0 {
		policy.MaxComplexityDelta = rc.MaxComplexityDelta
	}
	if policy.MaxFiles == 0 && rc.MaxFiles > 0 {
		policy.MaxFiles = rc.MaxFiles
	}

	// Traceability
	if len(policy.TraceabilityPatterns) == 0 && len(rc.TraceabilityPatterns) > 0 {
		policy.TraceabilityPatterns = rc.TraceabilityPatterns
	}
	if len(policy.TraceabilitySources) == 0 && len(rc.TraceabilitySources) > 0 {
		policy.TraceabilitySources = rc.TraceabilitySources
	}
	if !policy.RequireTraceability && rc.RequireTraceability {
		policy.RequireTraceability = true
	}
	if !policy.RequireTraceForCriticalPaths && rc.RequireTraceForCriticalPaths {
		policy.RequireTraceForCriticalPaths = true
	}

	// Reviewer independence
	if !policy.RequireIndependentReview && rc.RequireIndependentReview {
		policy.RequireIndependentReview = true
	}
	if policy.MinReviewers == 0 && rc.MinReviewers > 0 {
		policy.MinReviewers = rc.MinReviewers
	}

	// Analyzer thresholds
	if policy.MaxBlastRadiusDelta == 0 && rc.MaxBlastRadiusDelta > 0 {
		policy.MaxBlastRadiusDelta = rc.MaxBlastRadiusDelta
	}
	if policy.MaxFanOut == 0 && rc.MaxFanOut > 0 {
		policy.MaxFanOut = rc.MaxFanOut
	}
	if rc.DeadCodeMinConfidence > 0 {
		policy.DeadCodeMinConfidence = rc.DeadCodeMinConfidence
	}
	if rc.TestGapMinLines > 0 {
		policy.TestGapMinLines = rc.TestGapMinLines
	}
}

// getHotspotScoreMapFast returns a file→score map without tree-sitter enrichment.
func (e *Engine) getHotspotScoreMapFast(ctx context.Context) map[string]float64 {
	resp, err := e.GetHotspots(ctx, GetHotspotsOptions{Limit: 100, SkipComplexity: true})
	if err != nil {
		return nil
	}
	scores := make(map[string]float64, len(resp.Hotspots))
	for _, h := range resp.Hotspots {
		if h.Ranking != nil {
			scores[h.FilePath] = h.Ranking.Score
		}
	}
	return scores
}

// checkHotspotsWithScores checks hotspot overlap using a pre-computed score map.
func (e *Engine) checkHotspotsWithScores(ctx context.Context, files []string, hotspotScores map[string]float64) (ReviewCheck, []ReviewFinding) {
	start := time.Now()

	// Collect all hotspot files, then emit only the top 10 by score.
	// The check summary reports the total count; individual findings are
	// limited to the most volatile files to keep the output actionable.
	type hotspotHit struct {
		file  string
		score float64
	}
	var hits []hotspotHit
	for _, f := range files {
		if score, ok := hotspotScores[f]; ok && score > 0.5 {
			hits = append(hits, hotspotHit{f, score})
		}
	}
	// Sort descending by score
	sort.Slice(hits, func(i, j int) bool {
		return hits[i].score > hits[j].score
	})

	const maxHotspotFindings = 10
	var findings []ReviewFinding
	for i, h := range hits {
		if i >= maxHotspotFindings {
			break
		}
		findings = append(findings, ReviewFinding{
			Check:    "hotspots",
			Severity: "info",
			File:     h.file,
			Message:  fmt.Sprintf("Hotspot file (score: %.2f) — extra review attention recommended", h.score),
			Category: "risk",
			RuleID:   "ckb/hotspots/volatile-file",
		})
	}

	status := "pass"
	summary := "No volatile files touched"
	if len(hits) > 0 {
		status = "info"
		summary = fmt.Sprintf("%d hotspot file(s) touched", len(hits))
		if len(hits) > maxHotspotFindings {
			summary += fmt.Sprintf(" (top %d shown)", maxHotspotFindings)
		}
	}

	return ReviewCheck{
		Name:     "hotspots",
		Status:   status,
		Severity: "info",
		Summary:  summary,
		Duration: time.Since(start).Milliseconds(),
	}, findings
}

// checkRiskScoreFast computes risk score from already-available data instead
// of calling SummarizePR (which re-does the diff and hotspot analysis).
func (e *Engine) checkRiskScoreFast(ctx context.Context, diffStats []git.DiffStats, files []string, modules map[string]bool, hotspotScores map[string]float64, opts ReviewPROptions) (ReviewCheck, []ReviewFinding) {
	start := time.Now()

	totalChanges := 0
	for _, ds := range diffStats {
		totalChanges += ds.Additions + ds.Deletions
	}
	hotspotCount := 0
	for _, f := range files {
		if score, ok := hotspotScores[f]; ok && score > 0.5 {
			hotspotCount++
		}
	}

	risk := calculatePRRisk(len(diffStats), totalChanges, hotspotCount, len(modules))

	score := risk.Score
	level := risk.Level

	status := "pass"
	severity := "warning"
	summary := fmt.Sprintf("Risk score: %.2f (%s)", score, level)

	var findings []ReviewFinding
	if opts.Policy.MaxRiskScore > 0 && score > opts.Policy.MaxRiskScore {
		status = "warn"
		for _, factor := range risk.Factors {
			findings = append(findings, ReviewFinding{
				Check:    "risk",
				Severity: "warning",
				Message:  factor,
				Category: "risk",
				RuleID:   "ckb/risk/high-score",
			})
		}
	}

	return ReviewCheck{
		Name:     "risk",
		Status:   status,
		Severity: severity,
		Summary:  summary,
		Duration: time.Since(start).Milliseconds(),
	}, findings
}

// filterDiffByScope filters diff stats by scope. If scope contains / or .
// it's treated as a path prefix; otherwise it's treated as a symbol name
// resolved via SearchSymbols.
func (e *Engine) filterDiffByScope(ctx context.Context, diffStats []git.DiffStats, scope string) []git.DiffStats {
	if strings.Contains(scope, "/") || strings.Contains(scope, ".") {
		// Path prefix filter
		var filtered []git.DiffStats
		for _, ds := range diffStats {
			if strings.HasPrefix(ds.FilePath, scope) {
				filtered = append(filtered, ds)
			}
		}
		return filtered
	}

	// Symbol name — resolve to file paths
	resp, err := e.SearchSymbols(ctx, SearchSymbolsOptions{
		Query: scope,
		Limit: 20,
	})
	if err != nil || resp == nil || len(resp.Symbols) == 0 {
		return diffStats // no match → return unfiltered
	}

	fileSet := make(map[string]bool)
	for _, sym := range resp.Symbols {
		if sym.Location != nil {
			fileSet[sym.Location.FileId] = true
		}
	}

	var filtered []git.DiffStats
	for _, ds := range diffStats {
		if fileSet[ds.FilePath] {
			filtered = append(filtered, ds)
		}
	}
	if len(filtered) == 0 {
		return diffStats // symbol found but no file overlap → return unfiltered
	}
	return filtered
}

// buildChangedLinesMap parses a unified diff and builds a map of file -> changed line numbers.
func buildChangedLinesMap(rawDiff string) map[string]map[int]bool {
	parsed, err := diff.ParseGitDiff(rawDiff)
	if err != nil || parsed == nil {
		return nil
	}

	result := make(map[string]map[int]bool)
	for i := range parsed.Files {
		cf := &parsed.Files[i]
		path := diff.GetEffectivePath(cf)
		if path == "" || path == "/dev/null" {
			continue
		}
		lines := diff.GetAllChangedLines(cf)
		if len(lines) > 0 {
			lineSet := make(map[int]bool, len(lines))
			for _, l := range lines {
				lineSet[l] = true
			}
			result[path] = lineSet
		}
	}
	return result
}

// filterByChangedLines keeps only findings on changed lines.
// File-level findings (StartLine == 0) and findings for files not in the map are kept.
// Checks that are file-level concerns (test-gaps, hotspots) are exempt — the file
// is in the diff, so the finding is relevant even if the specific line wasn't changed.
func filterByChangedLines(findings []ReviewFinding, changedLines map[string]map[int]bool) []ReviewFinding {
	// Checks exempt from line-level filtering because they report file-level concerns
	exemptChecks := map[string]bool{
		"test-gaps": true, // "function X lacks tests" is relevant if the file changed
		"hotspots":  true, // churn score is per-file
	}

	filtered := make([]ReviewFinding, 0, len(findings))
	for _, f := range findings {
		// Keep file-level findings (no specific line)
		if f.StartLine == 0 {
			filtered = append(filtered, f)
			continue
		}
		// Keep findings from exempt checks (file-level concerns)
		if exemptChecks[f.Check] {
			filtered = append(filtered, f)
			continue
		}
		// Keep findings where file isn't in the diff map (e.g., global findings)
		lineSet, ok := changedLines[f.File]
		if !ok {
			filtered = append(filtered, f)
			continue
		}
		// Keep findings on changed lines
		if lineSet[f.StartLine] {
			filtered = append(filtered, f)
		}
	}
	return filtered
}

// checkSemanticNovelty uses LIP embeddings to flag PR files that are semantically
// far from the existing codebase. High novelty (score > 0.7) often means the code
// introduces genuinely new concepts that may lack test coverage or documentation.
// Returns a zero-value ReviewCheck (Name=="") when LIP is unavailable.
func (e *Engine) checkSemanticNovelty(_ context.Context, changedFiles []string) (ReviewCheck, []ReviewFinding) {
	if len(changedFiles) == 0 {
		return ReviewCheck{}, nil
	}

	// Build file:// URIs from repo-relative paths.
	uris := make([]string, 0, len(changedFiles))
	for _, f := range changedFiles {
		if f != "" {
			uris = append(uris, "file://"+filepath.Join(e.repoRoot, f))
		}
	}
	if len(uris) == 0 {
		return ReviewCheck{}, nil
	}

	info, _ := lip.NoveltyScore(uris)
	if info == nil {
		return ReviewCheck{}, nil // LIP unavailable — silent degradation
	}

	const highNoveltyThreshold = float32(0.7)
	var findings []ReviewFinding
	for _, item := range info.PerFile {
		if item.Score >= highNoveltyThreshold {
			relPath := strings.TrimPrefix(item.URI, "file://"+e.repoRoot+"/")
			findings = append(findings, ReviewFinding{
				Check:    "semantic-novelty",
				Severity: "warn",
				File:     relPath,
				Message:  fmt.Sprintf("semantically novel (score %.2f) — verify test coverage and documentation", item.Score),
			})
		}
	}

	status := "pass"
	summary := fmt.Sprintf("semantic novelty: mean %.2f across %d files", info.Score, len(uris))
	if len(findings) > 0 {
		status = "warn"
		summary = fmt.Sprintf("%d semantically novel files (mean score %.2f)", len(findings), info.Score)
	}

	return ReviewCheck{
		Name:     "semantic-novelty",
		Status:   status,
		Severity: "warn",
		Summary:  summary,
	}, findings
}

// reconcileCheckSummaries updates check summaries to reflect post-filtered
// findings. Without this, a check may report "5 new bug patterns" while all
// 5 were dropped by HoldTheLine because they're on unchanged lines.
func reconcileCheckSummaries(checks []ReviewCheck, findings []ReviewFinding) {
	// Count surviving findings per check
	countByCheck := make(map[string]int)
	for _, f := range findings {
		countByCheck[f.Check]++
	}

	for i, c := range checks {
		remaining := countByCheck[c.Name]
		if c.Status == "warn" && remaining == 0 {
			// All findings were filtered — downgrade to pass and note it
			checks[i].Status = "pass"
			checks[i].Summary = c.Summary + " (all on unchanged lines)"
		}
	}
}
