package incremental

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Directories to skip during hash-based detection
var skipDirs = map[string]bool{
	".git":         true,
	".ckb":         true,
	"vendor":       true,
	"node_modules": true,
	"bin":          true,
	"dist":         true,
	"out":          true,
	".cache":       true,
	"testdata":     true,
}

// ChangeDetector detects file changes since last index
type ChangeDetector struct {
	repoRoot string
	store    *Store
	config   *Config
	logger   *slog.Logger

	// extensions are the source files of the language being indexed; nil
	// means Go. Set by IndexIncrementalWithLang.
	extensions []string
}

// NewChangeDetector creates a new change detector
func NewChangeDetector(repoRoot string, store *Store, config *Config, logger *slog.Logger) *ChangeDetector {
	if config == nil {
		config = DefaultConfig()
	}
	return &ChangeDetector{
		repoRoot: repoRoot,
		store:    store,
		config:   config,
		logger:   logger,
	}
}

// SetExtensions sets the source extensions of the language being indexed.
func (d *ChangeDetector) SetExtensions(exts []string) {
	d.extensions = exts
}

// DetectChanges finds files that need reindexing
func (d *ChangeDetector) DetectChanges(since string) ([]ChangedFile, error) {
	// Try git first
	if d.isGitRepo() {
		changes, err := d.detectGitChanges(since)
		if err == nil {
			return changes, nil
		}
		// Fall back to hash-based detection on git error
		d.logger.Debug("Git detection failed, falling back to hash-based", "error", err.Error())
	}

	return d.detectHashChanges()
}

// GetCurrentCommit returns current HEAD commit
func (d *ChangeDetector) GetCurrentCommit() string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = d.repoRoot
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// isGitRepo checks if we're in a git repository
func (d *ChangeDetector) isGitRepo() bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = d.repoRoot
	return cmd.Run() == nil
}

// detectGitChanges uses git diff to find changed files
// Uses -z flag for NUL-separated output to handle paths with spaces
func (d *ChangeDetector) detectGitChanges(since string) ([]ChangedFile, error) {
	if since == "" {
		// Get last indexed commit from metadata
		since = d.store.GetLastIndexedCommit()
		if since == "" {
			// No previous index, need full reindex
			return nil, fmt.Errorf("no previous index commit")
		}
	}

	head := d.GetCurrentCommit()
	if head == "" {
		return nil, fmt.Errorf("failed to get HEAD")
	}

	// If same commit, check for uncommitted changes only
	if head == since {
		return d.detectUncommittedChanges()
	}

	// Get diff between commits using -z for NUL-separated output
	cmd := exec.Command("git", "diff", "--name-status", "-z", since, head) // #nosec G204 //nolint:gosec // git command with commit hashes
	cmd.Dir = d.repoRoot
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %w", err)
	}

	changes := d.parseGitDiffNUL(output)

	// Also get uncommitted changes
	uncommitted, _ := d.detectUncommittedChanges()
	changes = append(changes, uncommitted...)

	// Deduplicate (later changes win)
	return d.deduplicateChanges(changes), nil
}

// detectUncommittedChanges finds modified files not yet committed
func (d *ChangeDetector) detectUncommittedChanges() ([]ChangedFile, error) {
	var changes []ChangedFile

	// Staged changes (using -z)
	stagedCmd := exec.Command("git", "diff", "--name-status", "-z", "--cached")
	stagedCmd.Dir = d.repoRoot
	stagedOut, _ := stagedCmd.Output()
	changes = append(changes, d.parseGitDiffNUL(stagedOut)...)

	// Unstaged changes (using -z)
	unstagedCmd := exec.Command("git", "diff", "--name-status", "-z")
	unstagedCmd.Dir = d.repoRoot
	unstagedOut, _ := unstagedCmd.Output()
	changes = append(changes, d.parseGitDiffNUL(unstagedOut)...)

	// Untracked files (using -z)
	untrackedCmd := exec.Command("git", "ls-files", "-z", "--others", "--exclude-standard")
	untrackedCmd.Dir = d.repoRoot
	untrackedOut, _ := untrackedCmd.Output()

	// Parse NUL-separated untracked files
	for _, path := range bytes.Split(untrackedOut, []byte{0}) {
		pathStr := string(path)
		if pathStr != "" && d.isSourceFile(pathStr) {
			changes = append(changes, ChangedFile{
				Path:       pathStr,
				ChangeType: ChangeAdded,
			})
		}
	}

	return d.deduplicateChanges(changes), nil
}

// parseGitDiffNUL parses git diff --name-status -z output
// Format: STATUS\0PATH\0 (or STATUS\0OLDPATH\0NEWPATH\0 for renames/copies)
//
// CRITICAL: For renames, we must read BOTH paths before deciding to skip.
// The old path might not be .go but the new path might be (or vice versa).
func (d *ChangeDetector) parseGitDiffNUL(output []byte) []ChangedFile {
	var changes []ChangedFile

	parts := bytes.Split(output, []byte{0})

	for i := 0; i < len(parts); {
		if len(parts[i]) == 0 {
			i++
			continue
		}

		status := string(parts[i])
		if i+1 >= len(parts) {
			break
		}

		// For renames/copies: STATUS\0OLDPATH\0NEWPATH
		// For others: STATUS\0PATH
		isRenameOrCopy := strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C")

		var oldPath, newPath string
		if isRenameOrCopy {
			// Read both paths first
			oldPath = string(parts[i+1])
			i += 2
			if i < len(parts) {
				newPath = string(parts[i])
				i++
			} else {
				continue // Malformed, skip
			}
		} else {
			// Single path
			newPath = string(parts[i+1])
			oldPath = newPath
			i += 2
		}

		// Now decide what to do based on status and paths
		switch {
		case status == "A":
			if d.isSourceFile(newPath) {
				changes = append(changes, ChangedFile{
					Path:       newPath,
					ChangeType: ChangeAdded,
				})
			}

		case status == "M":
			if d.isSourceFile(newPath) {
				changes = append(changes, ChangedFile{
					Path:       newPath,
					ChangeType: ChangeModified,
				})
			}

		case status == "D":
			if d.isSourceFile(oldPath) {
				changes = append(changes, ChangedFile{
					Path:       oldPath,
					ChangeType: ChangeDeleted,
				})
			}

		case strings.HasPrefix(status, "R"):
			// Rename: decide based on new path (what we'll index going forward)
			// But we need OldPath to delete the old entry
			oldIsGo := d.isSourceFile(oldPath)
			newIsGo := d.isSourceFile(newPath)

			if oldIsGo && newIsGo {
				// .go -> .go rename: track as rename
				changes = append(changes, ChangedFile{
					Path:       newPath,
					OldPath:    oldPath,
					ChangeType: ChangeRenamed,
				})
			} else if oldIsGo && !newIsGo {
				// .go -> non-.go: treat as delete of old
				changes = append(changes, ChangedFile{
					Path:       oldPath,
					ChangeType: ChangeDeleted,
				})
			} else if !oldIsGo && newIsGo {
				// non-.go -> .go: treat as add of new
				changes = append(changes, ChangedFile{
					Path:       newPath,
					ChangeType: ChangeAdded,
				})
			}
			// else: neither is .go, skip entirely

		case strings.HasPrefix(status, "C"):
			// Copy: new file appears, old file unchanged
			// Only care about new path
			if d.isSourceFile(newPath) {
				changes = append(changes, ChangedFile{
					Path:       newPath,
					ChangeType: ChangeAdded,
				})
			}

		default:
			// Unknown status, treat as modified if it's a Go file
			if d.isSourceFile(newPath) {
				changes = append(changes, ChangedFile{
					Path:       newPath,
					ChangeType: ChangeModified,
				})
			}
		}
	}

	return changes
}

// isSourceFile checks if a path is a source file of the language being
// indexed (Go unless SetExtensions said otherwise).
func (d *ChangeDetector) isSourceFile(path string) bool {
	exts := d.extensions
	if len(exts) == 0 {
		exts = []string{".go"}
	}
	matched := false
	for _, ext := range exts {
		if strings.HasSuffix(path, ext) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	// Exclude test files unless configured to include them
	if !d.config.IndexTests && strings.HasSuffix(path, "_test.go") {
		return false
	}
	// Check exclude patterns
	if d.isExcluded(path) {
		return false
	}
	return true
}

// detectHashChanges falls back to comparing file hashes
// Used when git is unavailable or fails
func (d *ChangeDetector) detectHashChanges() ([]ChangedFile, error) {
	var changes []ChangedFile

	// Get all indexed files
	indexed, err := d.store.GetAllFileStates()
	if err != nil {
		return nil, fmt.Errorf("failed to get indexed files: %w", err)
	}

	indexedMap := make(map[string]IndexedFile)
	for _, f := range indexed {
		indexedMap[f.Path] = f
	}

	// Walk current files
	seen := make(map[string]bool)
	err = filepath.Walk(d.repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // Skip inaccessible files, continue walking
		}

		// Skip directories
		if info.IsDir() {
			base := filepath.Base(path)
			if skipDirs[base] || d.isExcluded(path) {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(d.repoRoot, path)

		// Skip non-Go files
		if !d.isSourceFile(relPath) {
			return nil
		}

		if d.isExcluded(relPath) {
			return nil
		}

		seen[relPath] = true

		// Compute hash
		hash, hashErr := d.hashFile(path)
		if hashErr != nil {
			return nil //nolint:nilerr // Skip unreadable files, continue walking
		}

		if prev, exists := indexedMap[relPath]; !exists {
			changes = append(changes, ChangedFile{
				Path:       relPath,
				ChangeType: ChangeAdded,
				Hash:       hash,
			})
		} else if prev.Hash != hash {
			changes = append(changes, ChangedFile{
				Path:       relPath,
				ChangeType: ChangeModified,
				Hash:       hash,
			})
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk repository: %w", err)
	}

	// Find deleted files
	for path := range indexedMap {
		if !seen[path] {
			changes = append(changes, ChangedFile{
				Path:       path,
				ChangeType: ChangeDeleted,
			})
		}
	}

	return changes, nil
}

// isExcluded checks if a path matches CKB config excludes
// Paths are normalized to forward slashes for consistent matching across OS
func (d *ChangeDetector) isExcluded(path string) bool {
	// Normalize to forward slashes for consistent matching
	normalizedPath := filepath.ToSlash(path)

	for _, pattern := range d.config.Excludes {
		normalizedPattern := filepath.ToSlash(pattern)

		// Try glob match
		if matched, _ := filepath.Match(normalizedPattern, normalizedPath); matched {
			return true
		}

		// Directory exclude: pattern "vendor" should match "vendor/foo/bar.go"
		// Treat pattern as directory prefix
		dirPattern := strings.TrimSuffix(normalizedPattern, "/") + "/"
		if strings.HasPrefix(normalizedPath, dirPattern) {
			return true
		}

		// Exact match for the directory itself
		if normalizedPath == strings.TrimSuffix(normalizedPattern, "/") {
			return true
		}
	}
	return false
}

// hashFile computes SHA256 of a file
func (d *ChangeDetector) hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck // Best effort cleanup

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// deduplicateChanges removes duplicate entries, keeping the last occurrence
func (d *ChangeDetector) deduplicateChanges(changes []ChangedFile) []ChangedFile {
	seen := make(map[string]int) // path -> index in result
	var result []ChangedFile

	for _, c := range changes {
		if idx, exists := seen[c.Path]; exists {
			// Replace earlier entry with this one
			result[idx] = c
		} else {
			seen[c.Path] = len(result)
			result = append(result, c)
		}
	}

	return result
}

// HasDirtyWorkingTree checks if there are uncommitted changes
func (d *ChangeDetector) HasDirtyWorkingTree() bool {
	uncommitted, err := d.detectUncommittedChanges()
	if err != nil {
		return false
	}
	return len(uncommitted) > 0
}
