package audit

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/complexity"
	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/coupling"
	"github.com/SimplyLiz/CodeMCP/internal/pathclass"
)

// Analyzer performs risk analysis on codebases
type Analyzer struct {
	repoRoot           string
	logger             *slog.Logger
	couplingAnalyzer   *coupling.Analyzer
	complexityAnalyzer *complexity.Analyzer
	// classes keeps generated and data paths (.ckb/config.json paths) out of the audit.
	classes *pathclass.Classifier
	// settings is .ckb/config.json "audit": keywords and disabled factors.
	settings settings

	authorsOnce  sync.Once
	singleAuthor bool
}

// NewAnalyzer creates a new risk analyzer
func NewAnalyzer(repoRoot string, logger *slog.Logger) *Analyzer {
	var ca *complexity.Analyzer
	if complexity.IsAvailable() {
		ca = complexity.NewAnalyzer()
	}
	cfg, err := config.LoadConfig(repoRoot)
	if err != nil {
		cfg = nil
	}
	classes := pathclass.FromConfig(repoRoot, cfg)
	return &Analyzer{
		repoRoot:           repoRoot,
		logger:             logger,
		couplingAnalyzer:   coupling.NewAnalyzer(repoRoot, logger).WithClasses(classes),
		complexityAnalyzer: ca,
		classes:            classes,
		settings:           newSettings(cfg),
	}
}

// Analyze performs a full risk audit of the codebase
func (a *Analyzer) Analyze(ctx context.Context, opts AuditOptions) (*RiskAnalysis, error) {
	// Set defaults
	if opts.RepoRoot == "" {
		opts.RepoRoot = a.repoRoot
	}
	if opts.MinScore <= 0 {
		opts.MinScore = 40
	}
	if opts.Limit <= 0 {
		opts.Limit = 50
	}

	a.logger.Debug("Starting risk audit",
		"repoRoot", opts.RepoRoot,
		"minScore", opts.MinScore,
		"limit", opts.Limit)

	// Find all source files; generated and data paths are counted, not scored
	var leftOut pathclass.Counter
	files, err := a.findSourceFiles(opts.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to find source files: %w", err)
	}

	// Analyze each file
	items := make([]RiskItem, 0, len(files))
	for _, file := range files {
		if leftOut.Skip(a.classes, filepath.ToSlash(file)) {
			continue
		}
		item, err := a.analyzeFile(ctx, opts.RepoRoot, file)
		if err != nil {
			a.logger.Warn("Failed to analyze file",
				"file", file,
				"error", err.Error(),
			)
			continue
		}

		// Apply score filter
		if item.RiskScore >= opts.MinScore {
			// Apply factor filter if specified
			if opts.Factor != "" {
				hasFilter := false
				for _, f := range item.Factors {
					if f.Factor == opts.Factor && f.Contribution > 0 {
						hasFilter = true
						break
					}
				}
				if !hasFilter {
					continue
				}
			}

			items = append(items, *item)
		}
	}

	// Sort by risk score descending
	sort.Slice(items, func(i, j int) bool {
		return items[i].RiskScore > items[j].RiskScore
	})

	// Apply limit
	if len(items) > opts.Limit {
		items = items[:opts.Limit]
	}

	// Compute summary
	summary := a.computeSummary(items)

	// Find quick wins
	quickWins := a.findQuickWins(items)

	return &RiskAnalysis{
		Repo:       filepath.Base(opts.RepoRoot),
		AnalyzedAt: time.Now(),
		Items:      items,
		Summary:    summary,
		QuickWins:  quickWins,
		LeftOut:    leftOut.Tally(),
	}, nil
}

// analyzeFile analyzes a single file for risk
func (a *Analyzer) analyzeFile(ctx context.Context, repoRoot, file string) (*RiskItem, error) {
	factors := make([]RiskFactor, 0, 8)
	fullPath := filepath.Join(repoRoot, file)

	// 1. Complexity (0-20 contribution) with per-function breakdown
	totalComplexity, functionRisks := a.getComplexityDetailed(ctx, fullPath)
	complexityContrib := min(float64(totalComplexity)/100, 1.0) * 20
	factors = append(factors, RiskFactor{
		Factor:       FactorComplexity,
		Value:        fmt.Sprintf("%d", totalComplexity),
		Weight:       RiskWeights[FactorComplexity],
		Contribution: complexityContrib,
	})

	// 2. Test coverage (0-20, inverted)
	// Simplified: check if test file exists
	hasTests := a.hasTestFile(file)
	coverageContrib := 0.0
	var coverageValue string
	if !hasTests {
		coverageContrib = 15.0 // Penalize missing tests
		coverageValue = "no test file"
	} else {
		coverageValue = "test file exists"
	}
	factors = append(factors, RiskFactor{
		Factor:       FactorTestCoverage,
		Value:        coverageValue,
		Weight:       RiskWeights[FactorTestCoverage],
		Contribution: coverageContrib,
	})

	// 3. Bus factor
	busFactor, activeContributor := a.getBusFactor(ctx, file)
	busContrib := 0.0
	if busFactor == 1 {
		busContrib = 15.0
	} else if busFactor == 2 {
		busContrib = 7.0
	}
	busValue := fmt.Sprintf("%d", busFactor)
	if activeContributor != "" {
		busValue += fmt.Sprintf(" (%s only)", activeContributor)
	}
	// With one author in the whole history every file has bus factor 1; that
	// is the repo's situation, not this file's risk, and "assign a backup
	// owner" has nobody to assign.
	if busFactor == 1 && a.singleAuthorRepo(ctx) {
		busContrib = 0
		busValue += "; single-author repo, not scored"
	}
	factors = append(factors, RiskFactor{
		Factor:       FactorBusFactor,
		Value:        busValue,
		Weight:       RiskWeights[FactorBusFactor],
		Contribution: busContrib,
	})

	// 4. Staleness
	monthsSinceTouch := a.getMonthsSinceLastTouch(ctx, file)
	stalenessContrib := min(float64(monthsSinceTouch)/24, 1.0) * 10
	factors = append(factors, RiskFactor{
		Factor:       FactorStaleness,
		Value:        fmt.Sprintf("%d months", monthsSinceTouch),
		Weight:       RiskWeights[FactorStaleness],
		Contribution: stalenessContrib,
	})

	// 5. Security-sensitive keywords
	securityKeywords := a.detectSecurityKeywords(fullPath)
	securityContrib := 0.0
	securityValue := "none"
	if len(securityKeywords) > 0 {
		securityContrib = 15.0
		if len(securityKeywords) > 3 {
			securityValue = strings.Join(securityKeywords[:3], ", ") + "..."
		} else {
			securityValue = strings.Join(securityKeywords, ", ")
		}
	}
	factors = append(factors, RiskFactor{
		Factor:       FactorSecuritySensitive,
		Value:        securityValue,
		Weight:       RiskWeights[FactorSecuritySensitive],
		Contribution: securityContrib,
	})

	// 6. Error rate (would need telemetry, placeholder for now)
	factors = append(factors, RiskFactor{
		Factor:       FactorErrorRate,
		Value:        "n/a",
		Weight:       RiskWeights[FactorErrorRate],
		Contribution: 0,
	})

	// 7. Co-change coupling
	coupledFiles, coupledLeftOut := a.getCoupledFilesCount(ctx, file)
	couplingContrib := min(float64(coupledFiles)/10, 1.0) * 5
	couplingValue := fmt.Sprintf("%d files", coupledFiles)
	if coupledLeftOut > 0 {
		couplingValue += fmt.Sprintf(" (%d in data/generated paths left out)", coupledLeftOut)
	}
	factors = append(factors, RiskFactor{
		Factor:       FactorCoChangeCoupling,
		Value:        couplingValue,
		Weight:       RiskWeights[FactorCoChangeCoupling],
		Contribution: couplingContrib,
	})

	// 8. Churn
	churn := a.getRecentChurn(ctx, file, 90)
	churnContrib := min(float64(churn)/20, 1.0) * 5
	factors = append(factors, RiskFactor{
		Factor:       FactorChurn,
		Value:        fmt.Sprintf("%d commits (90d)", churn),
		Weight:       RiskWeights[FactorChurn],
		Contribution: churnContrib,
	})

	// Factors disabled in .ckb/config.json audit.disabledFactors stay listed
	// but score nothing.
	for i := range factors {
		if a.settings.disabled[factors[i].Factor] {
			factors[i].Contribution = 0
			factors[i].Value = "disabled (audit.disabledFactors)"
		}
	}

	// Calculate total risk score
	totalScore := 0.0
	for _, f := range factors {
		totalScore += f.Contribution
	}

	// Generate recommendation
	recommendation := a.generateRecommendation(factors)

	return &RiskItem{
		File:               file,
		RiskScore:          totalScore,
		RiskLevel:          GetRiskLevel(totalScore),
		Factors:            factors,
		Recommendation:     recommendation,
		FunctionComplexity: functionRisks,
	}, nil
}

// findSourceFiles finds all source files in the repository
func (a *Analyzer) findSourceFiles(repoRoot string) ([]string, error) {
	var files []string

	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip inaccessible files
		}

		if info.IsDir() {
			name := info.Name()
			if name == "node_modules" || name == "vendor" || name == ".git" || name == "__pycache__" || name == ".ckb" {
				return filepath.SkipDir
			}
			return nil
		}

		ext := filepath.Ext(path)
		if isSourceFile(ext) {
			relPath, err := filepath.Rel(repoRoot, path)
			if err == nil {
				files = append(files, relPath)
			}
		}

		return nil
	})

	return files, err
}

// getComplexityDetailed returns total complexity and per-function breakdown.
// When the tree-sitter complexity analyzer is available, delegates to it for
// accurate per-function cyclomatic+cognitive scores. Falls back to string-counting heuristic.
func (a *Analyzer) getComplexityDetailed(ctx context.Context, filePath string) (int, []FunctionRisk) {
	// Try tree-sitter analyzer first
	if a.complexityAnalyzer != nil {
		fc, err := a.complexityAnalyzer.AnalyzeFile(ctx, filePath)
		if err == nil && fc != nil && fc.Error == "" && len(fc.Functions) > 0 {
			// Convert to FunctionRisk and sort by cyclomatic descending
			risks := make([]FunctionRisk, 0, len(fc.Functions))
			for _, f := range fc.Functions {
				risks = append(risks, FunctionRisk{
					Name:       f.Name,
					StartLine:  f.StartLine,
					EndLine:    f.EndLine,
					Cyclomatic: f.Cyclomatic,
					Cognitive:  f.Cognitive,
					Lines:      f.Lines,
				})
			}
			sort.Slice(risks, func(i, j int) bool {
				return risks[i].Cyclomatic > risks[j].Cyclomatic
			})
			// Cap at top 10 per file
			if len(risks) > 10 {
				risks = risks[:10]
			}
			return fc.TotalCyclomatic, risks
		}
	}

	// Fallback: simple heuristic, no per-function breakdown
	return a.getComplexityHeuristic(filePath), nil
}

// getComplexityHeuristic estimates complexity based on string counting.
func (a *Analyzer) getComplexityHeuristic(filePath string) int {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return 0
	}

	text := string(content)
	complexity := 1 // Base complexity

	complexity += strings.Count(text, "if ") + strings.Count(text, "if(")
	complexity += strings.Count(text, "else ")
	complexity += strings.Count(text, "for ") + strings.Count(text, "for(")
	complexity += strings.Count(text, "while ") + strings.Count(text, "while(")
	complexity += strings.Count(text, "switch ") + strings.Count(text, "switch(")
	complexity += strings.Count(text, "case ")
	complexity += strings.Count(text, "catch ") + strings.Count(text, "catch(")
	complexity += strings.Count(text, " && ")
	complexity += strings.Count(text, " || ")
	complexity += strings.Count(text, " ? ")

	return complexity
}

// hasTestFile checks if a test file exists for the given file
func (a *Analyzer) hasTestFile(file string) bool {
	ext := filepath.Ext(file)
	base := strings.TrimSuffix(file, ext)

	// Check common test file patterns
	testPatterns := []string{
		base + "_test" + ext,
		base + ".test" + ext,
		base + ".spec" + ext,
		strings.Replace(file, "/", "/test/", 1),
		"test/" + file,
		"tests/" + file,
	}

	for _, pattern := range testPatterns {
		fullPath := filepath.Join(a.repoRoot, pattern)
		if _, err := os.Stat(fullPath); err == nil {
			return true
		}
	}

	return false
}

// getBusFactor returns the number of recent active contributors
func (a *Analyzer) getBusFactor(ctx context.Context, file string) (int, string) {
	// Get authors active in the past year
	oneYearAgo := time.Now().AddDate(-1, 0, 0).Format("2006-01-02")

	args := []string{
		"shortlog", "-sn",
		"--since=" + oneYearAgo,
		"HEAD", "--", file,
	}

	output, err := a.executeGit(ctx, args...)
	if err != nil {
		return 0, ""
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return 0, ""
	}

	// Parse first author
	firstAuthor := ""
	if len(lines) > 0 {
		parts := strings.Fields(lines[0])
		if len(parts) >= 2 {
			firstAuthor = strings.Join(parts[1:], " ")
		}
	}

	return len(lines), firstAuthor
}

// getMonthsSinceLastTouch returns months since the file was last modified
func (a *Analyzer) getMonthsSinceLastTouch(ctx context.Context, file string) int {
	args := []string{"log", "-1", "--format=%aI", "--", file}

	output, err := a.executeGit(ctx, args...)
	if err != nil || output == "" {
		return 0
	}

	timestamp, err := time.Parse(time.RFC3339, strings.TrimSpace(output))
	if err != nil {
		return 0
	}

	now := time.Now()
	years := now.Year() - timestamp.Year()
	months := int(now.Month()) - int(timestamp.Month())
	return years*12 + months
}

// detectSecurityKeywords finds security-sensitive keywords in a file
func (a *Analyzer) detectSecurityKeywords(filePath string) []string {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}

	keywords := a.settings.keywords
	if keywords == nil {
		keywords = compileKeywords(SecurityKeywords)
	}
	found := matchSecurityKeywords(string(content), keywords)
	if found == nil {
		found = make([]string, 0)
	}
	return found
}

// getCoupledFilesCount returns the number of highly coupled files, and how many
// co-changed files in generated or data paths were left out of that count.
func (a *Analyzer) getCoupledFilesCount(ctx context.Context, file string) (int, int) {
	result, err := a.couplingAnalyzer.Analyze(ctx, coupling.AnalyzeOptions{
		RepoRoot:       a.repoRoot,
		Target:         file,
		MinCorrelation: 0.5, // Only count moderately coupled files
		WindowDays:     365,
		Limit:          50,
	})

	if err != nil {
		return 0, 0
	}
	leftOut := 0
	if result.LeftOut != nil {
		leftOut = result.LeftOut.Count
	}
	return len(result.Correlations), leftOut
}

// getRecentChurn returns the number of commits in the recent period
func (a *Analyzer) getRecentChurn(ctx context.Context, file string, days int) int {
	since := time.Now().AddDate(0, 0, -days).Format("2006-01-02")

	args := []string{
		"rev-list", "--count",
		"--since=" + since,
		"HEAD", "--", file,
	}

	output, err := a.executeGit(ctx, args...)
	if err != nil {
		return 0
	}

	count := 0
	fmt.Sscanf(strings.TrimSpace(output), "%d", &count)
	return count
}

// generateRecommendation generates a recommendation based on risk factors
func (a *Analyzer) generateRecommendation(factors []RiskFactor) string {
	// Find the top contributing factors
	type factorScore struct {
		factor string
		score  float64
	}

	scores := make([]factorScore, 0, len(factors))
	for _, f := range factors {
		if f.Contribution > 0 {
			scores = append(scores, factorScore{f.Factor, f.Contribution})
		}
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	if len(scores) == 0 {
		return ""
	}

	// Generate recommendation based on top factor
	switch scores[0].factor {
	case FactorComplexity:
		return "Consider refactoring to reduce complexity"
	case FactorTestCoverage:
		return "Add test coverage before making changes"
	case FactorBusFactor:
		return "Assign backup owner to reduce bus factor risk"
	case FactorStaleness:
		return "Review for potential dead code or deprecated functionality"
	case FactorSecuritySensitive:
		return "Security-sensitive code; review security practices before changes"
	case FactorCoChangeCoupling:
		return "Consider decoupling from frequently co-changed files"
	case FactorChurn:
		return "High churn may indicate design issues; consider stabilizing"
	default:
		return "Review risk factors before making changes"
	}
}

// computeSummary computes the risk summary
func (a *Analyzer) computeSummary(items []RiskItem) RiskSummary {
	summary := RiskSummary{}

	factorCounts := make(map[string]int)

	for _, item := range items {
		switch item.RiskLevel {
		case RiskLevelCritical:
			summary.Critical++
		case RiskLevelHigh:
			summary.High++
		case RiskLevelMedium:
			summary.Medium++
		case RiskLevelLow:
			summary.Low++
		}

		// Count factors
		for _, f := range item.Factors {
			if f.Contribution > 0 {
				factorCounts[f.Factor]++
			}
		}
	}

	// Top risk factors
	for factor, count := range factorCounts {
		summary.TopRiskFactors = append(summary.TopRiskFactors, TopRiskFactor{
			Factor: factor,
			Count:  count,
		})
	}
	sort.Slice(summary.TopRiskFactors, func(i, j int) bool {
		return summary.TopRiskFactors[i].Count > summary.TopRiskFactors[j].Count
	})

	return summary
}

// findQuickWins identifies low-effort, high-impact improvements
func (a *Analyzer) findQuickWins(items []RiskItem) []QuickWin {
	wins := make([]QuickWin, 0)

	// Find files with no tests but high complexity
	for _, item := range items {
		hasNoTests := false
		highComplexity := false

		for _, f := range item.Factors {
			if f.Factor == FactorTestCoverage && f.Contribution >= 10 {
				hasNoTests = true
			}
			if f.Factor == FactorComplexity && f.Contribution >= 10 {
				highComplexity = true
			}
		}

		if hasNoTests && highComplexity {
			wins = append(wins, QuickWin{
				Action: "Add tests",
				Target: item.File,
				Effort: "medium",
				Impact: "high",
			})
			if len(wins) >= 5 {
				break
			}
		}
	}

	// Find files with bus factor = 1
	for _, item := range items {
		for _, f := range item.Factors {
			if f.Factor == FactorBusFactor && f.Contribution >= 15 {
				wins = append(wins, QuickWin{
					Action: "Assign backup owner",
					Target: item.File,
					Effort: "low",
					Impact: "medium",
				})
				if len(wins) >= 10 {
					return wins
				}
				break
			}
		}
	}

	return wins
}

// executeGit executes a git command
func (a *Analyzer) executeGit(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = a.repoRoot

	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return string(output), nil
}

// Helper functions

func isSourceFile(ext string) bool {
	switch ext {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".java", ".kt", ".rs", ".rb", ".c", ".cpp", ".h", ".hpp":
		return true
	}
	return false
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
