package query

import (
	"fmt"
	"math"

	"github.com/SimplyLiz/CodeMCP/internal/backends/git"
)

// ReviewEffort estimates the time needed to review a PR.
type ReviewEffort struct {
	EstimatedMinutes int      `json:"estimatedMinutes"` // Total estimated review time
	EstimatedHours   float64  `json:"estimatedHours"`   // Same as minutes but as hours
	Factors          []string `json:"factors"`          // What drives the estimate
	Complexity       string   `json:"complexity"`       // "trivial", "moderate", "complex", "very-complex"
}

// estimateReviewEffort calculates estimated review time based on PR metrics.
//
// Based on research (Microsoft, Google code review studies):
// - ~200 LOC/hour for new code
// - ~300 LOC/hour for refactored/modified code
// - ~500 LOC/hour for moved/test/config code (quick scan)
// - Cognitive overhead per file switch: ~2 min
// - Cross-module context switch: ~5 min
// - Critical path files: +10 min each
func estimateReviewEffort(diffStats []git.DiffStats, breakdown *ChangeBreakdown, criticalFiles int, modules int) *ReviewEffort {
	if len(diffStats) == 0 {
		return &ReviewEffort{
			EstimatedMinutes: 0,
			Complexity:       "trivial",
		}
	}

	var factors []string
	totalMinutes := 0.0

	// Base time from lines of code (weighted by classification)
	locMinutes := 0.0
	if breakdown != nil {
		for _, c := range breakdown.Classifications {
			ds := findDiffStat(diffStats, c.File)
			if ds == nil {
				continue
			}
			lines := ds.Additions + ds.Deletions
			switch c.Category {
			case CategoryNew:
				locMinutes += float64(lines) / 200.0 * 60 // 200 LOC/hr
			case CategoryRefactor, CategoryModified, CategoryChurn:
				locMinutes += float64(lines) / 300.0 * 60 // 300 LOC/hr
			case CategoryMoved, CategoryTest, CategoryConfig:
				locMinutes += float64(lines) / 500.0 * 60 // 500 LOC/hr (quick scan)
			case CategoryGenerated:
				// Skip — not reviewed
			}
		}
	} else {
		// Fallback without classification
		for _, ds := range diffStats {
			lines := ds.Additions + ds.Deletions
			locMinutes += float64(lines) / 250.0 * 60 // 250 LOC/hr average
		}
	}
	totalMinutes += locMinutes
	if locMinutes > 0 {
		factors = append(factors, fmt.Sprintf("%.0f min from %d LOC", locMinutes, totalLOC(diffStats)))
	}

	// File switch overhead: ~2 min per file
	fileSwitchMinutes := float64(len(diffStats)) * 2.0
	totalMinutes += fileSwitchMinutes
	// Listed whenever it counts: hidden below 6 files, 4 files' 8 minutes made a
	// 13-minute estimate whose factors added up to 4.
	if fileSwitchMinutes > 0 {
		factors = append(factors, fmt.Sprintf("%.0f min from %d file switches", fileSwitchMinutes, len(diffStats)))
	}

	// Module context switches: ~5 min per module beyond the first
	if modules > 1 {
		moduleMinutes := float64(modules-1) * 5.0
		totalMinutes += moduleMinutes
		factors = append(factors, fmt.Sprintf("%.0f min from %d module context switches", moduleMinutes, modules-1))
	}

	// Critical files: add 50% overhead per critical file
	if criticalFiles > 0 {
		criticalMinutes := float64(criticalFiles) * 10.0
		totalMinutes += criticalMinutes
		factors = append(factors, fmt.Sprintf("%.0f min for %d critical files", criticalMinutes, criticalFiles))
	}

	// Floor at 5 minutes
	minutes := int(math.Ceil(totalMinutes))
	if minutes < 5 && len(diffStats) > 0 {
		minutes = 5
	}

	complexity := "trivial"
	switch {
	case minutes > 240:
		complexity = "very-complex"
	case minutes > 60:
		complexity = "complex"
	case minutes > 20:
		complexity = "moderate"
	}

	return &ReviewEffort{
		EstimatedMinutes: minutes,
		EstimatedHours:   math.Round(float64(minutes)/60.0*10) / 10, // 1 decimal
		Factors:          factors,
		Complexity:       complexity,
	}
}

func findDiffStat(diffStats []git.DiffStats, file string) *git.DiffStats {
	for i := range diffStats {
		if diffStats[i].FilePath == file {
			return &diffStats[i]
		}
	}
	return nil
}

func totalLOC(diffStats []git.DiffStats) int {
	total := 0
	for _, ds := range diffStats {
		total += ds.Additions + ds.Deletions
	}
	return total
}
