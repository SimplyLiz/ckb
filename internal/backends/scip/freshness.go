package scip

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/repostate"
)

// IndexFreshness tracks how up-to-date a SCIP index is
type IndexFreshness struct {
	// StaleAgainstHead indicates if index is behind HEAD commit
	StaleAgainstHead bool

	// StaleAgainstRepoState indicates if index is stale due to uncommitted changes
	StaleAgainstRepoState bool

	// CommitsBehindHead is the number of commits the index is behind HEAD
	CommitsBehindHead int

	// Warning is a human-readable warning message
	Warning string

	// IndexedCommit is the commit the index was built from
	IndexedCommit string

	// HeadCommit is the current HEAD commit
	HeadCommit string
}

// ComputeIndexFreshness computes the freshness of a SCIP index
func ComputeIndexFreshness(indexedCommit string, repoState *repostate.RepoState, repoRoot string) *IndexFreshness {
	freshness := &IndexFreshness{
		IndexedCommit: indexedCommit,
		HeadCommit:    repoState.HeadCommit,
	}

	// If we don't know the indexed commit, check file modification time
	if indexedCommit == "" {
		// Don't mark as stale if we simply don't have commit info
		// Many indexers (like scip-go) don't embed commit information
		// Only warn about uncommitted changes
		if repoState.Dirty {
			freshness.StaleAgainstRepoState = true
			freshness.Warning = "You have uncommitted changes that may not be reflected in the SCIP index."
		}
		return freshness
	}

	// Check if indexed commit matches HEAD
	if indexedCommit != repoState.HeadCommit {
		freshness.StaleAgainstHead = true

		// Count commits between indexed commit and HEAD
		commitsBehind, err := CountCommitsBetween(repoRoot, indexedCommit, repoState.HeadCommit)
		if err == nil {
			freshness.CommitsBehindHead = commitsBehind
		}

		if freshness.CommitsBehindHead > 0 {
			freshness.Warning = fmt.Sprintf("SCIP index is %d commit(s) behind HEAD. Consider regenerating the index.", freshness.CommitsBehindHead)
		} else {
			freshness.Warning = "SCIP index is not at HEAD commit. Consider regenerating the index."
		}
	}

	// Check if there are uncommitted changes
	if repoState.Dirty {
		freshness.StaleAgainstRepoState = true
		if freshness.Warning != "" {
			freshness.Warning += " Additionally, you have uncommitted changes."
		} else {
			freshness.Warning = "You have uncommitted changes that are not reflected in the SCIP index."
		}
	}

	return freshness
}

// IsStale returns true if the index is considered stale
func (f *IndexFreshness) IsStale() bool {
	return f.StaleAgainstHead || f.StaleAgainstRepoState
}

// IsSignificantlyStale returns true if the index is significantly out of date
// (more than 10 commits behind or has uncommitted changes)
func (f *IndexFreshness) IsSignificantlyStale() bool {
	return f.CommitsBehindHead > 10 || f.StaleAgainstRepoState
}

// GetSeverity returns the severity level of staleness
// 0 = fresh, 1 = slightly stale, 2 = moderately stale, 3 = severely stale
func (f *IndexFreshness) GetSeverity() int {
	if !f.IsStale() {
		return 0
	}

	if f.StaleAgainstRepoState {
		return 3 // Uncommitted changes are most severe
	}

	if f.CommitsBehindHead > 50 {
		return 3
	}
	if f.CommitsBehindHead > 10 {
		return 2
	}
	if f.CommitsBehindHead > 0 {
		return 1
	}

	return 1 // Default to slightly stale if we don't know the count
}

// GetCompletenessScore returns a completeness score based on freshness
// 1.0 = perfectly fresh
// 0.9 = slightly stale (1-5 commits behind)
// 0.7 = moderately stale (6-20 commits behind)
// 0.5 = severely stale (21+ commits behind)
// 0.3 = has uncommitted changes
func (f *IndexFreshness) GetCompletenessScore() float64 {
	if !f.IsStale() {
		return 1.0
	}

	// Uncommitted changes significantly reduce completeness
	if f.StaleAgainstRepoState {
		return 0.3
	}

	// Score based on commits behind
	if f.CommitsBehindHead == 0 {
		return 0.95 // Just slightly stale
	}
	if f.CommitsBehindHead <= 5 {
		return 0.9
	}
	if f.CommitsBehindHead <= 20 {
		return 0.7
	}
	if f.CommitsBehindHead <= 50 {
		return 0.5
	}

	return 0.3 // Very stale
}

// CountCommitsBetween counts the number of commits between two commits
func CountCommitsBetween(repoRoot, fromCommit, toCommit string) (int, error) {
	// Use git rev-list to count commits
	// git rev-list --count fromCommit..toCommit
	cmd := exec.Command("git", "rev-list", "--count", fmt.Sprintf("%s..%s", fromCommit, toCommit)) // #nosec G204 //nolint:gosec // git command with validated commit hashes
	cmd.Dir = repoRoot

	output, err := cmd.Output()
	if err != nil {
		// If we can't count, return -1 to indicate unknown
		return -1, err
	}

	countStr := strings.TrimSpace(string(output))
	var count int
	_, err = fmt.Sscanf(countStr, "%d", &count)
	if err != nil {
		return -1, err
	}

	return count, nil
}

// VerifyCommitExists checks if a commit exists in the repository
func VerifyCommitExists(repoRoot, commit string) bool {
	cmd := exec.Command("git", "cat-file", "-e", commit)
	cmd.Dir = repoRoot
	err := cmd.Run()
	return err == nil
}

// IsIndexFresh is a convenience function that checks if an index is fresh
func IsIndexFresh(indexedCommit string, repoState *repostate.RepoState, repoRoot string) bool {
	freshness := ComputeIndexFreshness(indexedCommit, repoState, repoRoot)
	return !freshness.IsStale()
}

// GetFreshnessWarning returns a warning message if the index is stale, empty string otherwise
func GetFreshnessWarning(indexedCommit string, repoState *repostate.RepoState, repoRoot string) string {
	freshness := ComputeIndexFreshness(indexedCommit, repoState, repoRoot)
	if freshness.IsStale() {
		return freshness.Warning
	}
	return ""
}
