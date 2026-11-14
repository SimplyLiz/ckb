package query

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	"github.com/SimplyLiz/CodeMCP/internal/output"
	"github.com/SimplyLiz/CodeMCP/internal/ownership"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// GetOwnershipOptions contains options for getOwnership.
type GetOwnershipOptions struct {
	Path           string
	IncludeBlame   bool
	IncludeHistory bool
}

// OwnerInfo represents an owner in the response.
type OwnerInfo struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"`   // "user", "team", "email"
	Scope      string  `json:"scope"`  // "maintainer", "reviewer", "contributor"
	Source     string  `json:"source"` // "codeowners", "git-blame", "declared"
	Confidence float64 `json:"confidence"`
}

// BlameContributor represents a contributor from git-blame.
type BlameContributor struct {
	Author     string  `json:"author"`
	Email      string  `json:"email"`
	LineCount  int     `json:"lineCount"`
	Percentage float64 `json:"percentage"`
}

// BlameOwnershipInfo represents git-blame ownership info.
type BlameOwnershipInfo struct {
	TotalLines   int                `json:"totalLines"`
	Contributors []BlameContributor `json:"contributors"`
}

// OwnershipHistoryEvent represents an ownership change event.
type OwnershipHistoryEvent struct {
	Pattern    string `json:"pattern"`
	OwnerID    string `json:"ownerId"`
	Event      string `json:"event"` // "added", "removed", "promoted", "demoted"
	Reason     string `json:"reason,omitempty"`
	RecordedAt string `json:"recordedAt"`
}

// GetOwnershipResponse is the response for getOwnership.
type GetOwnershipResponse struct {
	CkbVersion      string                  `json:"ckbVersion"`
	SchemaVersion   string                  `json:"schemaVersion"`
	Tool            string                  `json:"tool"`
	Path            string                  `json:"path"`
	Owners          []OwnerInfo             `json:"owners"`
	BlameOwnership  *BlameOwnershipInfo     `json:"blameOwnership,omitempty"`
	History         []OwnershipHistoryEvent `json:"history,omitempty"`
	Confidence      float64                 `json:"confidence"`
	ConfidenceBasis []ConfidenceBasisItem   `json:"confidenceBasis"`
	Limitations     []string                `json:"limitations,omitempty"`
	Provenance      *Provenance             `json:"provenance,omitempty"`
	Drilldowns      []output.Drilldown      `json:"drilldowns,omitempty"`
	// CoChangePartners lists files that frequently change together with this path
	// (from Cartographer temporal coupling). Implicitly relevant for ownership/review.
	// Only populated when the binary is built with -tags cartographer.
	CoChangePartners []string `json:"coChangePartners,omitempty"`
}

// GetOwnership returns ownership information for a file or path.
func (e *Engine) GetOwnership(ctx context.Context, opts GetOwnershipOptions) (*GetOwnershipResponse, error) {
	startTime := time.Now()

	var confidenceBasis []ConfidenceBasisItem
	var limitations []string
	var allOwners []OwnerInfo

	// Normalize path
	normalizedPath := opts.Path
	if !filepath.IsAbs(normalizedPath) {
		normalizedPath = filepath.Clean(normalizedPath)
	} else {
		// Make path relative to repo root
		relPath, err := filepath.Rel(e.repoRoot, normalizedPath)
		if err == nil {
			normalizedPath = relPath
		}
	}

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "head")
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	// Try to find and parse CODEOWNERS
	codeownersPath := ownership.FindCodeownersFile(e.repoRoot)
	var codeownersFile *ownership.CodeownersFile
	if codeownersPath != "" {
		codeownersFile, err = ownership.ParseCodeownersFile(codeownersPath)
		if err != nil {
			limitations = append(limitations, "Failed to parse CODEOWNERS: "+err.Error())
		} else {
			// Get CODEOWNERS owners for this path
			codeownersOwners := codeownersFile.GetOwnersForPath(normalizedPath)
			if len(codeownersOwners) > 0 {
				owners := ownership.CodeownersToOwners(codeownersOwners)
				for _, o := range owners {
					allOwners = append(allOwners, OwnerInfo{
						ID:         o.ID,
						Type:       o.Type,
						Scope:      o.Scope,
						Source:     o.Source,
						Confidence: o.Confidence,
					})
				}
				confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
					Backend: "codeowners",
					Status:  "available",
				})
			}
		}
	} else {
		limitations = append(limitations, "No CODEOWNERS file found")
	}

	// Get git-blame ownership if requested
	var blameOwnership *BlameOwnershipInfo
	if opts.IncludeBlame {
		blameConfig := ownership.DefaultBlameConfig()
		blameResult, blameErr := ownership.RunGitBlame(e.repoRoot, normalizedPath)
		if blameErr != nil {
			limitations = append(limitations, "Git blame failed: "+blameErr.Error())
		} else {
			blameOwn := ownership.ComputeBlameOwnership(blameResult, blameConfig)

			// Add blame owners to the list
			blameOwners := ownership.BlameOwnershipToOwners(blameOwn)
			for _, o := range blameOwners {
				allOwners = append(allOwners, OwnerInfo{
					ID:         o.ID,
					Type:       o.Type,
					Scope:      o.Scope,
					Source:     o.Source,
					Confidence: o.Confidence,
				})
			}

			// Build blame ownership info
			contributors := make([]BlameContributor, 0, len(blameOwn.Contributors))
			for _, c := range blameOwn.Contributors {
				contributors = append(contributors, BlameContributor{
					Author:     c.Author,
					Email:      c.Email,
					LineCount:  c.LineCount,
					Percentage: c.Percentage,
				})
			}

			blameOwnership = &BlameOwnershipInfo{
				TotalLines:   blameOwn.TotalLines,
				Contributors: contributors,
			}

			confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
				Backend: "git-blame",
				Status:  "available",
			})
		}
	}

	// Get history if requested
	var history []OwnershipHistoryEvent
	if opts.IncludeHistory {
		if e.db != nil {
			ownershipRepo := storage.NewOwnershipRepository(e.db)
			records, histErr := ownershipRepo.GetHistoryByPattern(normalizedPath, 50)
			if histErr != nil {
				limitations = append(limitations, "Failed to query ownership history: "+histErr.Error())
			} else {
				for _, r := range records {
					history = append(history, OwnershipHistoryEvent{
						Pattern:    r.Pattern,
						OwnerID:    r.OwnerID,
						Event:      r.Event,
						Reason:     r.Reason,
						RecordedAt: r.RecordedAt.Format(time.RFC3339),
					})
				}
			}
		} else {
			limitations = append(limitations, "Storage unavailable; ownership history requires index data")
		}
	}

	// Compute overall confidence
	confidence := 0.5 // Base confidence
	if len(allOwners) > 0 {
		// Higher confidence if we have CODEOWNERS
		hasCODEOWNERS := false
		for _, o := range allOwners {
			if o.Source == "codeowners" {
				hasCODEOWNERS = true
				break
			}
		}
		if hasCODEOWNERS {
			confidence = 1.0
		} else if blameOwnership != nil {
			confidence = 0.79
		}
	}

	// Build completeness
	completeness := CompletenessInfo{
		Score:  1.0,
		Reason: "ownership-computed",
	}
	if len(limitations) > 0 {
		completeness.Score = 0.7
		completeness.Reason = "partial-ownership"
	}

	// Build provenance
	provenance := e.buildProvenance(repoState, "head", startTime, nil, completeness)

	// Generate drilldowns
	var drilldowns []output.Drilldown
	if len(allOwners) > 0 {
		// Suggest exploring the module
		drilldowns = append(drilldowns, output.Drilldown{
			Label:          "Explore module",
			Query:          "getModuleOverview for " + filepath.Dir(normalizedPath),
			RelevanceScore: 0.8,
		})
	}

	// Augment with Cartographer co-change partners — files that frequently change
	// together with this path. These are implicit co-owners even if not in CODEOWNERS.
	var coChangePartners []string
	if cartographer.Available() {
		if pairs, err := cartographer.GitCochange(e.repoRoot, 0, minHiddenCoChanges); err == nil {
			for _, p := range strongCochangePairs(pairs, prepareMinCorrelation) {
				partner := ""
				if p.FileA == normalizedPath {
					partner = p.FileB
				} else if p.FileB == normalizedPath {
					partner = p.FileA
				}
				if partner != "" && !isCouplingNoiseFile(partner) {
					coChangePartners = append(coChangePartners, partner)
				}
				if len(coChangePartners) >= 5 {
					break
				}
			}
		}
	}

	return &GetOwnershipResponse{
		CkbVersion:       "6.0",
		SchemaVersion:    "6.0",
		Tool:             "getOwnership",
		Path:             normalizedPath,
		Owners:           allOwners,
		BlameOwnership:   blameOwnership,
		History:          history,
		Confidence:       confidence,
		ConfidenceBasis:  confidenceBasis,
		Limitations:      limitations,
		Provenance:       provenance,
		Drilldowns:       drilldowns,
		CoChangePartners: coChangePartners,
	}, nil
}

// OwnershipDriftOptions contains options for getOwnershipDrift.
type OwnershipDriftOptions struct {
	Scope          string  `json:"scope"`          // Module or directory path to analyze
	Threshold      float64 `json:"threshold"`      // Drift score threshold to report (0-1, default 0.3)
	Limit          int     `json:"limit"`          // Max files to return (default 20)
	IncludeDetails bool    `json:"includeDetails"` // Include detailed blame info (default false)
}

// DriftedFile represents a file with ownership drift.
type DriftedFile struct {
	Path           string        `json:"path"`
	DriftScore     float64       `json:"driftScore"`     // 0-1, higher means more drift
	DeclaredOwners []string      `json:"declaredOwners"` // From CODEOWNERS
	ActualOwners   []ActualOwner `json:"actualOwners"`   // From git-blame
	Reason         string        `json:"reason"`         // Why drift was detected
	Recommendation string        `json:"recommendation"` // Suggested action
}

// ActualOwner represents an owner derived from git-blame.
type ActualOwner struct {
	ID         string  `json:"id"`
	Percentage float64 `json:"percentage"`
}

// OwnershipDriftResponse is the response for getOwnershipDrift.
type OwnershipDriftResponse struct {
	CkbVersion    string        `json:"ckbVersion"`
	SchemaVersion string        `json:"schemaVersion"`
	Tool          string        `json:"tool"`
	Summary       DriftSummary  `json:"summary"`
	DriftedFiles  []DriftedFile `json:"driftedFiles"`
	Limitations   []string      `json:"limitations,omitempty"`
	Provenance    *Provenance   `json:"provenance,omitempty"`
}

// DriftSummary provides high-level drift statistics.
type DriftSummary struct {
	TotalFilesAnalyzed int     `json:"totalFilesAnalyzed"`
	FilesWithDrift     int     `json:"filesWithDrift"`
	AverageDriftScore  float64 `json:"averageDriftScore"`
	MostDriftedModule  string  `json:"mostDriftedModule,omitempty"`
}

// GetOwnershipDrift analyzes ownership drift between CODEOWNERS and git-blame.
func (e *Engine) GetOwnershipDrift(ctx context.Context, opts OwnershipDriftOptions) (*OwnershipDriftResponse, error) {
	startTime := time.Now()

	// Default values
	if opts.Threshold <= 0 {
		opts.Threshold = 0.3
	}
	if opts.Limit <= 0 {
		opts.Limit = 20
	}

	var limitations []string

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "head")
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	// Find CODEOWNERS file
	codeownersPath := ownership.FindCodeownersFile(e.repoRoot)
	if codeownersPath == "" {
		limitations = append(limitations, "No CODEOWNERS file found - cannot detect drift")
		return &OwnershipDriftResponse{
			CkbVersion:    version.Version,
			SchemaVersion: "6.1",
			Tool:          "getOwnershipDrift",
			Summary: DriftSummary{
				TotalFilesAnalyzed: 0,
				FilesWithDrift:     0,
			},
			DriftedFiles: []DriftedFile{},
			Limitations:  limitations,
			Provenance: &Provenance{
				RepoStateId:     repoState.RepoStateId,
				RepoStateDirty:  repoState.Dirty,
				QueryDurationMs: time.Since(startTime).Milliseconds(),
			},
		}, nil
	}

	codeownersFile, err := ownership.ParseCodeownersFile(codeownersPath)
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	// Determine scope
	scopePath := e.repoRoot
	if opts.Scope != "" {
		scopePath = filepath.Join(e.repoRoot, opts.Scope)
	}

	// Collect source files to analyze
	var filesToAnalyze []string
	err = filepath.Walk(scopePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip inaccessible files
		}
		if info.IsDir() {
			// Skip hidden directories
			if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		// Only analyze source code files
		if isSourceFile(path) {
			relPath, _ := filepath.Rel(e.repoRoot, path)
			filesToAnalyze = append(filesToAnalyze, relPath)
		}
		return nil
	})
	if err != nil {
		limitations = append(limitations, "Error walking directory: "+err.Error())
	}

	// Analyze each file for drift
	var driftedFiles []DriftedFile
	totalDrift := 0.0
	moduleDriftCounts := make(map[string]int)

	blameConfig := ownership.DefaultBlameConfig()

	for _, filePath := range filesToAnalyze {
		// Get declared owners from CODEOWNERS
		declaredOwners := codeownersFile.GetOwnersForPath(filePath)
		if len(declaredOwners) == 0 {
			continue // No declared ownership, skip
		}

		// Get actual owners from git-blame
		blameResult, blameErr := ownership.RunGitBlame(e.repoRoot, filePath)
		if blameErr != nil {
			continue // Skip files we can't blame
		}

		blameOwnership := ownership.ComputeBlameOwnership(blameResult, blameConfig)
		if blameOwnership.TotalLines == 0 {
			continue
		}

		// Calculate drift score
		driftScore := calculateDriftScore(declaredOwners, blameOwnership.Contributors)

		if driftScore >= opts.Threshold {
			// Build actual owners list
			actualOwners := make([]ActualOwner, 0, len(blameOwnership.Contributors))
			for _, c := range blameOwnership.Contributors {
				actualOwners = append(actualOwners, ActualOwner{
					ID:         c.Author,
					Percentage: c.Percentage,
				})
			}

			// Determine reason and recommendation
			reason, recommendation := getDriftReasonAndRecommendation(driftScore, declaredOwners, blameOwnership.Contributors)

			driftedFiles = append(driftedFiles, DriftedFile{
				Path:           filePath,
				DriftScore:     driftScore,
				DeclaredOwners: declaredOwners,
				ActualOwners:   actualOwners,
				Reason:         reason,
				Recommendation: recommendation,
			})

			// Track module drift
			module := filepath.Dir(filePath)
			moduleDriftCounts[module]++
		}

		totalDrift += driftScore
	}

	// Sort by drift score (highest first)
	for i := 0; i < len(driftedFiles)-1; i++ {
		for j := i + 1; j < len(driftedFiles); j++ {
			if driftedFiles[j].DriftScore > driftedFiles[i].DriftScore {
				driftedFiles[i], driftedFiles[j] = driftedFiles[j], driftedFiles[i]
			}
		}
	}

	// Limit results
	if len(driftedFiles) > opts.Limit {
		driftedFiles = driftedFiles[:opts.Limit]
	}

	// Find most drifted module
	mostDriftedModule := ""
	maxDriftCount := 0
	for module, count := range moduleDriftCounts {
		if count > maxDriftCount {
			maxDriftCount = count
			mostDriftedModule = module
		}
	}

	// Calculate average drift
	avgDrift := 0.0
	if len(filesToAnalyze) > 0 {
		avgDrift = totalDrift / float64(len(filesToAnalyze))
	}

	return &OwnershipDriftResponse{
		CkbVersion:    version.Version,
		SchemaVersion: "6.1",
		Tool:          "getOwnershipDrift",
		Summary: DriftSummary{
			TotalFilesAnalyzed: len(filesToAnalyze),
			FilesWithDrift:     len(driftedFiles),
			AverageDriftScore:  avgDrift,
			MostDriftedModule:  mostDriftedModule,
		},
		DriftedFiles: driftedFiles,
		Limitations:  limitations,
		Provenance: &Provenance{
			RepoStateId:     repoState.RepoStateId,
			RepoStateDirty:  repoState.Dirty,
			QueryDurationMs: time.Since(startTime).Milliseconds(),
		},
	}, nil
}

// calculateDriftScore computes how much the actual ownership differs from declared.
// Returns 0-1 where 0 means no drift and 1 means complete drift.
func calculateDriftScore(declaredOwners []string, contributors []ownership.AuthorContribution) float64 {
	if len(contributors) == 0 {
		return 0
	}

	// Normalize declared owners for comparison
	declaredSet := make(map[string]bool)
	for _, owner := range declaredOwners {
		// Remove @ prefix and convert to lowercase for comparison
		normalized := strings.TrimPrefix(strings.ToLower(owner), "@")
		declaredSet[normalized] = true
	}

	// Calculate percentage of code written by non-owners
	nonOwnerPercentage := 0.0
	for _, c := range contributors {
		// Check if contributor matches any declared owner
		authorNormalized := strings.ToLower(c.Author)
		emailNormalized := strings.ToLower(c.Email)

		isOwner := false
		for owner := range declaredSet {
			if strings.Contains(authorNormalized, owner) ||
				strings.Contains(emailNormalized, owner) ||
				strings.Contains(owner, authorNormalized) {
				isOwner = true
				break
			}
		}

		if !isOwner {
			nonOwnerPercentage += c.Percentage
		}
	}

	return nonOwnerPercentage / 100.0
}

// getDriftReasonAndRecommendation returns human-readable drift explanation.
func getDriftReasonAndRecommendation(driftScore float64, declaredOwners []string, contributors []ownership.AuthorContribution) (string, string) {
	if driftScore > 0.7 {
		topContributor := ""
		if len(contributors) > 0 {
			topContributor = contributors[0].Author
		}
		return "Major drift: primary contributor differs from declared owner",
			"Consider adding " + topContributor + " as maintainer"
	}
	if driftScore > 0.5 {
		return "Moderate drift: significant contributions from non-owners",
			"Review CODEOWNERS to add active contributors"
	}
	return "Minor drift: some contributions from outside ownership",
		"Monitor for increasing drift"
}

// isSourceFile is defined in responsibilities.go
