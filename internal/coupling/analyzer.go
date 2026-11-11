package coupling

import (
	"context"
	"log/slog"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Analyzer performs co-change pattern analysis on files
type Analyzer struct {
	repoRoot string
	logger   *slog.Logger
}

// NewAnalyzer creates a new coupling analyzer
func NewAnalyzer(repoRoot string, logger *slog.Logger) *Analyzer {
	return &Analyzer{
		repoRoot: repoRoot,
		logger:   logger,
	}
}

// Analyze performs coupling analysis on a target file or symbol
// maxFilesPerCommit is the largest commit co-change analysis counts.
const maxFilesPerCommit = 50

func (a *Analyzer) Analyze(ctx context.Context, opts AnalyzeOptions) (*CouplingAnalysis, error) {
	// Set defaults
	if opts.MinCorrelation <= 0 {
		opts.MinCorrelation = 0.3
	}
	if opts.WindowDays <= 0 {
		opts.WindowDays = 365
	}
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.RepoRoot == "" {
		opts.RepoRoot = a.repoRoot
	}

	// Resolve target to file path
	targetFile := a.resolveToFile(opts.Target)

	a.logger.Debug("Starting coupling analysis",
		"target", targetFile,
		"minCorrelation", opts.MinCorrelation,
		"windowDays", opts.WindowDays)

	// Calculate the "since" date
	since := time.Now().AddDate(0, 0, -opts.WindowDays)

	// Get all commits that touched the target file
	targetCommits, err := a.getFileCommits(ctx, targetFile, since)
	if err != nil {
		return nil, err
	}

	if len(targetCommits) == 0 {
		return &CouplingAnalysis{
			Target: struct {
				Symbol         string `json:"symbol,omitempty"`
				File           string `json:"file"`
				CommitCount    int    `json:"commitCount"`
				AnalysisWindow struct {
					From time.Time `json:"from"`
					To   time.Time `json:"to"`
				} `json:"analysisWindow"`
			}{
				File:        targetFile,
				CommitCount: 0,
			},
			Correlations:    []Correlation{},
			Insights:        []string{"No commits found in the analysis window"},
			Recommendations: []string{},
		}, nil
	}

	// For each commit, get all other files changed. A commit touching more than
	// maxFilesPerCommit files (an initial import, a rename sweep, a formatter run)
	// says nothing about which files belong together, and counted it made every
	// file in it 100% coupled to every other: it is left out of the count and of
	// the total, as Cartographer's co-change analysis does.
	coChangeCounts := make(map[string]int)
	counted := 0
	for _, commitHash := range targetCommits {
		filesInCommit, err := a.getFilesInCommit(ctx, commitHash)
		if err != nil {
			a.logger.Warn("Failed to get files in commit",
				"commit", commitHash,
				"error", err.Error())
			continue
		}
		if len(filesInCommit) > maxFilesPerCommit {
			continue
		}
		counted++

		for _, file := range filesInCommit {
			if file != targetFile && file != "" {
				coChangeCounts[file]++
			}
		}
	}

	// Compute correlations
	correlations := make([]Correlation, 0, len(coChangeCounts))
	for file, count := range coChangeCounts {
		correlation := float64(count) / float64(counted)

		if correlation >= opts.MinCorrelation {
			correlations = append(correlations, Correlation{
				// Repo-relative, like FilePath: a basename names nothing to
				// open, and every consumer matching against diff or file lists
				// (review's split graph, prepareChange) has paths, not names.
				File:          file,
				FilePath:      file,
				Correlation:   correlation,
				CoChangeCount: count,
				TotalChanges:  counted,
				Level:         GetCorrelationLevel(correlation),
			})
		}
	}

	sortCorrelations(correlations)

	// Apply limit
	if len(correlations) > opts.Limit {
		correlations = correlations[:opts.Limit]
	}

	// Generate insights
	insights := a.generateInsights(correlations, targetFile)

	// Generate recommendations
	recommendations := a.generateRecommendations(correlations, targetFile)

	result := &CouplingAnalysis{
		Correlations:    correlations,
		Insights:        insights,
		Recommendations: recommendations,
	}
	result.Target.File = targetFile
	result.Target.CommitCount = len(targetCommits)
	result.Target.AnalysisWindow.From = since
	result.Target.AnalysisWindow.To = time.Now()

	return result, nil
}

// getFileCommits returns commit hashes that touched a file since a given time
func (a *Analyzer) getFileCommits(ctx context.Context, filePath string, since time.Time) ([]string, error) {
	sinceStr := since.Format("2006-01-02")
	args := []string{
		"log",
		"--format=%H",
		"--since=" + sinceStr,
		"--follow",
		"--",
		filePath,
	}

	output, err := a.executeGit(ctx, args...)
	if err != nil {
		return nil, err
	}

	if output == "" {
		return []string{}, nil
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	commits := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			commits = append(commits, trimmed)
		}
	}

	return commits, nil
}

// getFilesInCommit returns all files changed in a commit
func (a *Analyzer) getFilesInCommit(ctx context.Context, commitHash string) ([]string, error) {
	args := []string{
		"show",
		"--name-only",
		"--format=",
		commitHash,
	}

	output, err := a.executeGit(ctx, args...)
	if err != nil {
		return nil, err
	}

	if output == "" {
		return []string{}, nil
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			files = append(files, trimmed)
		}
	}

	return files, nil
}

// resolveToFile resolves a target (file path or symbol) to a file path
func (a *Analyzer) resolveToFile(target string) string {
	// If it's already a file path, return it
	// Could be enhanced to resolve symbol names to files via SCIP
	return target
}

// generateInsights generates insights based on correlations
func (a *Analyzer) generateInsights(correlations []Correlation, targetFile string) []string {
	insights := make([]string, 0)

	// Test file correlation
	for _, c := range correlations {
		if strings.Contains(c.FilePath, "_test") || strings.Contains(c.FilePath, ".test.") || strings.Contains(c.FilePath, "/test/") {
			percentage := int(c.Correlation * 100)
			insights = append(insights, "Changes often require test updates ("+itoa(percentage)+"% correlation)")
			break
		}
	}

	// Proto/API correlation
	for _, c := range correlations {
		if strings.HasSuffix(c.FilePath, ".proto") || strings.Contains(c.FilePath, "openapi") || strings.Contains(c.FilePath, "swagger") {
			percentage := int(c.Correlation * 100)
			insights = append(insights, "API contract changes in "+itoa(percentage)+"% of commits")
			break
		}
	}

	// High coupling count
	highCorrelation := 0
	for _, c := range correlations {
		if c.Level == "high" {
			highCorrelation++
		}
	}
	if highCorrelation >= 3 {
		insights = append(insights, "Strong coupling detected with "+itoa(highCorrelation)+" other files")
	}

	// Config file correlation
	for _, c := range correlations {
		if strings.HasSuffix(c.FilePath, ".json") || strings.HasSuffix(c.FilePath, ".yaml") || strings.HasSuffix(c.FilePath, ".yml") || strings.HasSuffix(c.FilePath, ".toml") {
			if c.Level == "high" || c.Level == "medium" {
				insights = append(insights, "Configuration often changes together ("+c.File+")")
				break
			}
		}
	}

	if len(insights) == 0 {
		insights = append(insights, "No significant coupling patterns detected")
	}

	return insights
}

// generateRecommendations generates recommendations based on correlations
func (a *Analyzer) generateRecommendations(correlations []Correlation, targetFile string) []string {
	recommendations := make([]string, 0)

	if len(correlations) == 0 {
		return recommendations
	}

	// Build recommendation for top correlated files
	topFiles := make([]string, 0, 3)
	for i, c := range correlations {
		if i >= 3 {
			break
		}
		topFiles = append(topFiles, c.File)
	}

	if len(topFiles) > 0 {
		recommendations = append(recommendations,
			"When modifying "+filepath.Base(targetFile)+", consider reviewing: "+strings.Join(topFiles, ", "))
	}

	// Specific recommendations based on patterns
	for _, c := range correlations {
		if (strings.Contains(c.FilePath, "_test") || strings.Contains(c.FilePath, ".test.")) && c.Correlation >= 0.7 {
			recommendations = append(recommendations, "Update tests in "+c.File+" ("+itoa(int(c.Correlation*100))+"% correlation)")
			break
		}
	}

	return recommendations
}

// executeGit executes a git command and returns the output
func (a *Analyzer) executeGit(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = a.repoRoot

	output, err := cmd.Output()
	if err != nil {
		// Check if it's just no matches (empty result)
		if exitErr, ok := err.(*exec.ExitError); ok {
			if len(exitErr.Stderr) > 0 && strings.Contains(string(exitErr.Stderr), "does not have any commits") {
				return "", nil
			}
		}
		return "", err
	}

	return string(output), nil
}

// itoa is a simple int to string conversion
func itoa(i int) string {
	if i < 0 {
		return "-" + uitoa(uint(-i))
	}
	return uitoa(uint(i))
}

func uitoa(u uint) string {
	if u == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for u > 0 {
		i--
		buf[i] = byte('0' + u%10)
		u /= 10
	}
	return string(buf[i:])
}

// sortCorrelations orders by correlation, strongest first. The counts come out
// of a map, so ties need an order of their own, or a limit keeps a different
// set of equally coupled files on every run.
func sortCorrelations(correlations []Correlation) {
	sort.Slice(correlations, func(i, j int) bool {
		a, b := correlations[i], correlations[j]
		if a.Correlation != b.Correlation {
			return a.Correlation > b.Correlation
		}
		if a.CoChangeCount != b.CoChangeCount {
			return a.CoChangeCount > b.CoChangeCount
		}
		return a.FilePath < b.FilePath
	})
}
