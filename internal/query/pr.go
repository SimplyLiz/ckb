package query

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// SummarizePROptions contains options for the summarize-pr tool.
type SummarizePROptions struct {
	BaseBranch       string `json:"baseBranch"`       // Base branch to compare against (default: "main")
	HeadBranch       string `json:"headBranch"`       // Head branch (default: current branch)
	IncludeOwnership bool   `json:"includeOwnership"` // Include ownership analysis (default: true)
	NoAutoFetch      bool   `json:"noAutoFetch"`      // Disable auto-fetch of missing base ref (see ReviewPROptions)
}

// SummarizePRResponse is the response for summarize-pr.
type SummarizePRResponse struct {
	CkbVersion      string            `json:"ckbVersion"`
	SchemaVersion   string            `json:"schemaVersion"`
	Tool            string            `json:"tool"`
	Summary         PRSummary         `json:"summary"`
	ChangedFiles    []PRFileChange    `json:"changedFiles"`
	ModulesAffected []PRModuleImpact  `json:"modulesAffected"`
	RiskAssessment  PRRiskAssessment  `json:"riskAssessment"`
	Reviewers       []SuggestedReview `json:"suggestedReviewers,omitempty"`
	Provenance      *Provenance       `json:"provenance,omitempty"`
}

// PRSummary provides a high-level overview of the PR.
type PRSummary struct {
	TotalFiles        int      `json:"totalFiles"`
	TotalAdditions    int      `json:"totalAdditions"`
	TotalDeletions    int      `json:"totalDeletions"`
	TotalModules      int      `json:"totalModules"`
	HotspotsTouched   int      `json:"hotspotsTouched"`
	OwnershipCoverage float64  `json:"ownershipCoverage"` // % of files where author is owner
	Languages         []string `json:"languages"`
}

// PRFileChange represents a changed file in the PR.
type PRFileChange struct {
	Path         string  `json:"path"`
	Status       string  `json:"status"` // added, modified, deleted, renamed
	Additions    int     `json:"additions"`
	Deletions    int     `json:"deletions"`
	Module       string  `json:"module,omitempty"`
	IsHotspot    bool    `json:"isHotspot,omitempty"`
	HotspotScore float64 `json:"hotspotScore,omitempty"`
	Language     string  `json:"language,omitempty"`
}

// PRModuleImpact describes the impact on a module in a PR context.
type PRModuleImpact struct {
	ModuleId     string   `json:"moduleId"`
	Name         string   `json:"name"`
	FilesChanged int      `json:"filesChanged"`
	RiskLevel    string   `json:"riskLevel"` // low, medium, high
	Reasons      []string `json:"reasons,omitempty"`
}

// PRRiskAssessment provides an overall risk assessment.
type PRRiskAssessment struct {
	Level       string   `json:"level"`   // low, medium, high
	Score       float64  `json:"score"`   // 0-1
	Factors     []string `json:"factors"` // Reasons for the risk level
	Suggestions []string `json:"suggestions,omitempty"`
}

// SuggestedReview represents a suggested reviewer.
type SuggestedReview struct {
	Owner         string  `json:"owner"`
	Reason        string  `json:"reason"`
	Coverage      float64 `json:"coverage"` // % of changed files they own
	Confidence    float64 `json:"confidence"`
	ExpertiseArea string  `json:"expertiseArea,omitempty"` // Top module/directory they own
	LastActiveAt  string  `json:"lastActiveAt,omitempty"`  // RFC3339 of last commit
	IsAuthor      bool    `json:"isAuthor,omitempty"`      // True if this person is the PR author
}

// SummarizePR generates a summary of changes between branches.
func (e *Engine) SummarizePR(ctx context.Context, opts SummarizePROptions) (*SummarizePRResponse, error) {
	startTime := time.Now()

	// Default values
	if opts.BaseBranch == "" {
		opts.BaseBranch = "main"
	}

	// Get changed files from git
	if e.gitAdapter == nil {
		return nil, fmt.Errorf("git adapter not available")
	}

	// Get diff stats between branches.
	// If no head branch specified, defaults to HEAD -- this compares
	// baseRef..HEAD (via GetCommitRangeDiff below), not the working tree.
	// For working-tree/staged diffs, use AnalyzeChangeSet instead.
	headRef := opts.HeadBranch
	if headRef == "" {
		headRef = "HEAD"
	}
	var baseRef string
	if opts.NoAutoFetch {
		if verr := e.gitAdapter.VerifyRef(opts.BaseBranch); verr != nil {
			return nil, fmt.Errorf("failed to resolve base ref %q: %w", opts.BaseBranch, verr)
		}
		baseRef = opts.BaseBranch
	} else {
		resolved, rerr := e.gitAdapter.EnsureRef(opts.BaseBranch)
		if rerr != nil {
			return nil, fmt.Errorf("failed to resolve base ref %q: %w", opts.BaseBranch, rerr)
		}
		baseRef = resolved
		opts.BaseBranch = resolved
	}
	diffStats, err := e.gitAdapter.GetCommitRangeDiff(baseRef, headRef)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff: %w", err)
	}

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "head")
	if err != nil {
		repoState = &RepoState{RepoStateId: "unknown"}
	}

	// Analyze changed files
	changedFiles := make([]PRFileChange, 0, len(diffStats))
	languages := make(map[string]bool)
	moduleFiles := make(map[string]int)
	totalAdditions := 0
	totalDeletions := 0
	hotspotCount := 0

	// Fetch hotspots once and build a lookup map (instead of per-file).
	hotspotScores := e.getHotspotScoreMap(ctx)

	for _, df := range diffStats {
		// Determine status from DiffStats flags
		status := "modified"
		if df.IsNew {
			status = "added"
		} else if df.IsDeleted {
			status = "deleted"
		} else if df.IsRenamed {
			status = "renamed"
		}

		change := PRFileChange{
			Path:      df.FilePath,
			Status:    status,
			Additions: df.Additions,
			Deletions: df.Deletions,
			Language:  detectLanguage(df.FilePath),
		}

		totalAdditions += df.Additions
		totalDeletions += df.Deletions

		if change.Language != "" {
			languages[change.Language] = true
		}

		// Try to map file to module
		module := e.resolveFileModule(df.FilePath)
		if module != "" {
			change.Module = module
			moduleFiles[module]++
		}

		// Check if file is a hotspot
		if score, ok := hotspotScores[df.FilePath]; ok && score > 0.5 {
			change.IsHotspot = true
			change.HotspotScore = score
			hotspotCount++
		}

		changedFiles = append(changedFiles, change)
	}

	// Build module impacts
	modulesAffected := make([]PRModuleImpact, 0, len(moduleFiles))
	for moduleId, fileCount := range moduleFiles {
		risk := "low"
		var reasons []string

		if fileCount > 5 {
			risk = "medium"
			reasons = append(reasons, fmt.Sprintf("Many files changed (%d)", fileCount))
		}
		if fileCount > 10 {
			risk = "high"
		}

		modulesAffected = append(modulesAffected, PRModuleImpact{
			ModuleId:     moduleId,
			Name:         moduleId,
			FilesChanged: fileCount,
			RiskLevel:    risk,
			Reasons:      reasons,
		})
	}

	// Sort modules by files changed
	sort.Slice(modulesAffected, func(i, j int) bool {
		return modulesAffected[i].FilesChanged > modulesAffected[j].FilesChanged
	})

	// Build language list
	langList := make([]string, 0, len(languages))
	for lang := range languages {
		langList = append(langList, lang)
	}
	sort.Strings(langList)

	// Calculate risk assessment
	risk := calculatePRRisk(len(changedFiles), totalAdditions+totalDeletions, hotspotCount, len(modulesAffected))

	// Get suggested reviewers
	var reviewers []SuggestedReview
	if opts.IncludeOwnership {
		reviewers = e.getSuggestedReviewers(ctx, changedFiles)
	}

	// Calculate ownership coverage
	ownershipCoverage := 0.0
	if len(reviewers) > 0 {
		ownershipCoverage = reviewers[0].Coverage
	}

	summary := PRSummary{
		TotalFiles:        len(changedFiles),
		TotalAdditions:    totalAdditions,
		TotalDeletions:    totalDeletions,
		TotalModules:      len(modulesAffected),
		HotspotsTouched:   hotspotCount,
		OwnershipCoverage: ownershipCoverage,
		Languages:         langList,
	}

	return &SummarizePRResponse{
		CkbVersion:      version.Version,
		SchemaVersion:   "6.1",
		Tool:            "summarizePr",
		Summary:         summary,
		ChangedFiles:    changedFiles,
		ModulesAffected: modulesAffected,
		RiskAssessment:  risk,
		Reviewers:       reviewers,
		Provenance: &Provenance{
			RepoStateId:     repoState.RepoStateId,
			RepoStateDirty:  repoState.Dirty,
			QueryDurationMs: time.Since(startTime).Milliseconds(),
		},
	}, nil
}

// resolveFileModule maps a file path to its module.
func (e *Engine) resolveFileModule(filePath string) string {
	// Simple heuristic: use first two path components
	parts := strings.Split(filePath, "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return ""
}

// getHotspotScoreMap fetches hotspots once and returns a file→score map.
func (e *Engine) getHotspotScoreMap(ctx context.Context) map[string]float64 {
	resp, err := e.GetHotspots(ctx, GetHotspotsOptions{Limit: 100})
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

// getSuggestedReviewers identifies potential reviewers based on ownership.
func (e *Engine) getSuggestedReviewers(ctx context.Context, files []PRFileChange) []SuggestedReview {
	type ownerStats struct {
		fileCount int
		dirs      map[string]int // directory → file count (for expertise area)
	}
	ownerMap := make(map[string]*ownerStats)
	totalFiles := len(files)

	// Cap ownership lookups to avoid N×git-blame calls on large PRs.
	// Only run blame for the first 10 files (most expensive), CODEOWNERS-only
	// for the next 20, and skip the rest — the top owners still surface.
	const maxOwnershipLookups = 30
	for i, f := range files {
		if i >= maxOwnershipLookups {
			break
		}
		opts := GetOwnershipOptions{Path: f.Path, IncludeBlame: i < 10}
		resp, err := e.GetOwnership(ctx, opts)
		if err != nil || resp == nil {
			continue
		}

		dir := filepath.Dir(f.Path)
		for _, owner := range resp.Owners {
			stats, ok := ownerMap[owner.ID]
			if !ok {
				stats = &ownerStats{dirs: make(map[string]int)}
				ownerMap[owner.ID] = stats
			}
			stats.fileCount++
			stats.dirs[dir]++
		}
	}

	// Detect PR author from HEAD commit
	prAuthor := ""
	if e.gitAdapter != nil {
		if author, err := e.gitAdapter.GetHeadAuthorEmail(); err == nil {
			prAuthor = author
		}
	}

	// Convert to suggestions with expertise area
	var suggestions []SuggestedReview
	for owner, stats := range ownerMap {
		coverage := float64(stats.fileCount) / float64(totalFiles)

		// Find top directory for expertise area
		topDir := ""
		topCount := 0
		for dir, count := range stats.dirs {
			if count > topCount {
				topDir = dir
				topCount = count
			}
		}

		isAuthor := owner == prAuthor
		reason := fmt.Sprintf("Owns %d of %d changed files", stats.fileCount, totalFiles)
		if topDir != "" && topDir != "." {
			reason += fmt.Sprintf(" (expert: %s)", topDir)
		}
		if isAuthor {
			reason += " [author — needs independent reviewer]"
		}

		suggestions = append(suggestions, SuggestedReview{
			Owner:         owner,
			Reason:        reason,
			Coverage:      coverage,
			Confidence:    coverage,
			ExpertiseArea: topDir,
			IsAuthor:      isAuthor,
		})
	}

	// Sort: non-authors first, then by coverage
	sort.SliceStable(suggestions, func(i, j int) bool {
		if suggestions[i].IsAuthor != suggestions[j].IsAuthor {
			return !suggestions[i].IsAuthor // non-authors first
		}
		return suggestions[i].Coverage > suggestions[j].Coverage
	})

	// Limit to top 5
	if len(suggestions) > 5 {
		suggestions = suggestions[:5]
	}

	return suggestions
}

// calculatePRRisk computes the risk assessment for a PR.
func calculatePRRisk(fileCount, totalChanges, hotspotCount, moduleCount int) PRRiskAssessment {
	var factors []string
	var suggestions []string
	score := 0.0

	// File count factor
	if fileCount > 20 {
		score += 0.3
		factors = append(factors, fmt.Sprintf("Large PR with %d files", fileCount))
		suggestions = append(suggestions, "Consider splitting into smaller PRs")
	} else if fileCount > 10 {
		score += 0.15
		factors = append(factors, fmt.Sprintf("Medium-sized PR with %d files", fileCount))
	}

	// Change size factor
	if totalChanges > 1000 {
		score += 0.3
		factors = append(factors, fmt.Sprintf("High churn: %d lines changed", totalChanges))
	} else if totalChanges > 500 {
		score += 0.15
		factors = append(factors, fmt.Sprintf("Moderate churn: %d lines changed", totalChanges))
	}

	// Hotspot factor
	if hotspotCount > 0 {
		hotspotImpact := float64(hotspotCount) * 0.1
		if hotspotImpact > 0.3 {
			hotspotImpact = 0.3
		}
		score += hotspotImpact
		factors = append(factors, fmt.Sprintf("Touches %d hotspot(s)", hotspotCount))
		suggestions = append(suggestions, "Extra review recommended for hotspot files")
	}

	// Module spread factor
	if moduleCount > 5 {
		score += 0.2
		factors = append(factors, fmt.Sprintf("Spans %d modules", moduleCount))
		suggestions = append(suggestions, "Consider module-specific reviewers")
	}

	// Clamp score to [0, 1]
	if score > 1.0 {
		score = 1.0
	}

	// Determine level
	level := "low"
	if score > 0.6 {
		level = "high"
	} else if score > 0.3 {
		level = "medium"
	}

	if len(factors) == 0 {
		factors = append(factors, "Small, focused change")
	}

	return PRRiskAssessment{
		Level:       level,
		Score:       score,
		Factors:     factors,
		Suggestions: suggestions,
	}
}
