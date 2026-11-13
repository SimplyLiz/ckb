// Package index provides index metadata persistence and freshness tracking.
package index

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/repostate"
)

const (
	// MetadataVersion is the current version of the metadata format.
	MetadataVersion = 1

	// metadataFile is the filename for index metadata.
	metadataFile = "index-meta.json"
)

// RefreshTrigger describes what caused an index refresh.
type RefreshTrigger string

const (
	TriggerManual    RefreshTrigger = "manual"
	TriggerHEAD      RefreshTrigger = "head-changed"
	TriggerIndex     RefreshTrigger = "index-changed"
	TriggerScheduled RefreshTrigger = "scheduled"
	TriggerWebhook   RefreshTrigger = "webhook"
	TriggerStale     RefreshTrigger = "stale"
)

// LastRefresh describes the most recent index refresh.
type LastRefresh struct {
	At          time.Time      `json:"at"`
	Trigger     RefreshTrigger `json:"trigger"`
	TriggerInfo string         `json:"triggerInfo,omitempty"` // e.g., "main → feature/auth"
	DurationMs  int64          `json:"durationMs"`
}

// IndexMeta contains metadata about the SCIP index.
type IndexMeta struct {
	Version     int          `json:"version"`
	CreatedAt   time.Time    `json:"createdAt"`
	CommitHash  string       `json:"commitHash"`
	RepoStateID string       `json:"repoStateId"`
	FileCount   int          `json:"fileCount"`
	Duration    string       `json:"duration"`
	Indexer     string       `json:"indexer"`
	IndexerArgs []string     `json:"indexerArgs,omitempty"`
	LastRefresh *LastRefresh `json:"lastRefresh,omitempty"`

	// Languages are the languages whose indexer output is in the index, one
	// entry each. Absent in metadata written before multi-language indexing:
	// such an index holds one language.
	Languages []LanguageIndex `json:"languages,omitempty"`
	// Skipped are the languages the repo has that the index doesn't cover,
	// with why and what fixes it, so answers about their files can say they
	// come from text search.
	Skipped []SkippedLanguage `json:"skippedLanguages,omitempty"`
}

// LanguageIndex is one language's share of a SCIP index.
type LanguageIndex struct {
	Language string `json:"language"`
	Indexer  string `json:"indexer"`
	Files    int    `json:"files"`
}

// SkippedLanguage is a detected language left out of the index.
type SkippedLanguage struct {
	Language string `json:"language"`
	Files    int    `json:"files,omitempty"`
	// Reason says why, e.g. "scip-python not installed".
	Reason string `json:"reason"`
	// Fix is the command that gets it into the index, if there is one.
	Fix string `json:"fix,omitempty"`
	// Indexer is the binary whose absence left the language out. Once it is
	// on PATH the index counts as stale (see CheckFreshness), so the next
	// reindex, watch tick or 'ckb index' brings the language in.
	Indexer string `json:"indexer,omitempty"`
}

// MultiLanguage reports whether the index merges several indexers' output.
// Incremental indexing reruns one indexer over the whole index file, which
// would drop the other languages, so it only applies to a single-language index.
func (m *IndexMeta) MultiLanguage() bool {
	return m != nil && len(m.Languages) > 1
}

// FreshnessResult describes index freshness status.
type FreshnessResult struct {
	Fresh            bool
	Reason           string
	CommitsBehind    int
	HasUncommitted   bool
	IndexedCommit    string
	CurrentCommit    string
	CurrentRepoState string
}

// LoadMeta loads index metadata from the .ckb directory.
// Returns nil without error if no metadata file exists.
func LoadMeta(ckbDir string) (*IndexMeta, error) {
	path := filepath.Join(ckbDir, metadataFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No metadata yet
		}
		return nil, fmt.Errorf("reading index metadata: %w", err)
	}

	var meta IndexMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parsing index metadata: %w", err)
	}

	// Version mismatch - treat as no metadata
	if meta.Version != MetadataVersion {
		return nil, nil
	}

	return &meta, nil
}

// Save writes index metadata to the .ckb directory.
func (m *IndexMeta) Save(ckbDir string) error {
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		return fmt.Errorf("creating .ckb directory: %w", err)
	}

	m.Version = MetadataVersion

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling index metadata: %w", err)
	}

	path := filepath.Join(ckbDir, metadataFile)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing index metadata: %w", err)
	}

	return nil
}

// CheckFreshness determines if the index is up to date.
// For git repos, compares against current repo state.
// For non-git repos, falls back to time-based staleness (24h).
func (m *IndexMeta) CheckFreshness(repoRoot string) FreshnessResult {
	result := m.checkFreshness(repoRoot)
	if result.Fresh {
		if reason := m.indexerInstalledSince(); reason != "" {
			result.Fresh = false
			result.Reason = reason
		}
	}
	return result
}

// indexerInstalledSince names a language left out for want of its indexer
// whose indexer is on PATH now: the index is missing what one build would add.
func (m *IndexMeta) indexerInstalledSince() string {
	for _, s := range m.Skipped {
		if s.Indexer == "" {
			continue
		}
		if _, err := exec.LookPath(s.Indexer); err == nil {
			return fmt.Sprintf("%s is installed now; %s is not in the index yet", s.Indexer, s.Language)
		}
	}
	return ""
}

func (m *IndexMeta) checkFreshness(repoRoot string) FreshnessResult {
	if m == nil {
		return FreshnessResult{
			Fresh:  false,
			Reason: "no index metadata found",
		}
	}

	// Try to compute current repo state
	rs, err := repostate.ComputeRepoState(repoRoot)
	if err != nil {
		// Non-git repo: fall back to time-based staleness
		return m.checkTimeFreshness()
	}

	result := FreshnessResult{
		IndexedCommit:    m.CommitHash,
		CurrentCommit:    rs.HeadCommit,
		CurrentRepoState: rs.RepoStateID,
	}

	// Check 1: RepoStateID match (covers commits + uncommitted changes)
	if m.RepoStateID == rs.RepoStateID {
		result.Fresh = true
		return result
	}

	// Check 2: Same commit but dirty working tree
	if m.CommitHash == rs.HeadCommit && rs.Dirty {
		result.Fresh = false
		result.HasUncommitted = true
		result.Reason = "uncommitted changes detected"
		return result
	}

	// Check 3: Different commits
	if m.CommitHash != rs.HeadCommit {
		behind := countCommitsBehind(repoRoot, m.CommitHash, rs.HeadCommit)
		result.Fresh = false
		result.CommitsBehind = behind

		if rs.Dirty {
			result.HasUncommitted = true
			if behind > 0 {
				result.Reason = fmt.Sprintf("%d commit(s) behind HEAD + uncommitted changes", behind)
			} else {
				result.Reason = "uncommitted changes detected"
			}
		} else if behind > 0 {
			result.Reason = fmt.Sprintf("%d commit(s) behind HEAD", behind)
		} else {
			result.Reason = "repository state changed"
		}
		return result
	}

	// Fallback: RepoStateID differs but we can't determine why
	result.Fresh = false
	result.Reason = "repository state changed"
	return result
}

// checkTimeFreshness checks freshness for non-git repos.
func (m *IndexMeta) checkTimeFreshness() FreshnessResult {
	age := time.Since(m.CreatedAt)
	if age > 24*time.Hour {
		return FreshnessResult{
			Fresh:  false,
			Reason: fmt.Sprintf("index is %s old", humanDuration(age)),
		}
	}
	return FreshnessResult{
		Fresh: true,
	}
}

// countCommitsBehind returns the number of commits between two refs.
func countCommitsBehind(repoRoot, fromCommit, toCommit string) int {
	if fromCommit == "" || toCommit == "" {
		return 0
	}

	// Use git rev-list to count commits
	cmd := exec.Command("git", "rev-list", "--count", fmt.Sprintf("%s..%s", fromCommit, toCommit)) // #nosec G204 //nolint:gosec // git command with commit hashes
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return 0
	}

	var count int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &count)
	return count
}

// Staleness provides a summary of index freshness for display.
type Staleness struct {
	IsStale       bool   `json:"isStale"`
	CommitsBehind int    `json:"commitsBehind"`
	IndexAge      string `json:"indexAge"`
	Reason        string `json:"reason,omitempty"`
}

// GetStaleness returns a staleness summary for the index.
func (m *IndexMeta) GetStaleness(repoRoot string) Staleness {
	if m == nil {
		return Staleness{
			IsStale: true,
			Reason:  "no index metadata found",
		}
	}

	freshness := m.CheckFreshness(repoRoot)
	age := time.Since(m.CreatedAt)

	return Staleness{
		IsStale:       !freshness.Fresh,
		CommitsBehind: freshness.CommitsBehind,
		IndexAge:      humanDuration(age),
		Reason:        freshness.Reason,
	}
}

// humanDuration formats a duration in human-readable form.
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", mins)
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}
