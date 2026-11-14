package diff

import (
	"fmt"
	"strings"

	godiff "github.com/sourcegraph/go-diff/diff"

	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// GitDiffParser parses unified git diffs into structured data
type GitDiffParser struct{}

// NewGitDiffParser creates a new GitDiffParser
func NewGitDiffParser() *GitDiffParser {
	return &GitDiffParser{}
}

// Parse parses a unified diff string into a ParsedDiff
func (p *GitDiffParser) Parse(diffContent string) (*impact.ParsedDiff, error) {
	if diffContent == "" {
		return &impact.ParsedDiff{Files: []impact.ChangedFile{}}, nil
	}

	// Parse using go-diff
	fileDiffs, err := godiff.ParseMultiFileDiff([]byte(diffContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse diff: %w", err)
	}

	result := &impact.ParsedDiff{
		Files: make([]impact.ChangedFile, 0, len(fileDiffs)),
	}

	for _, fd := range fileDiffs {
		changedFile := p.parseFileDiff(fd)
		result.Files = append(result.Files, changedFile)
	}

	return result, nil
}

// parseFileDiff converts a go-diff FileDiff to our ChangedFile
func (p *GitDiffParser) parseFileDiff(fd *godiff.FileDiff) impact.ChangedFile {
	cf := impact.ChangedFile{
		OldPath: cleanPath(fd.OrigName),
		NewPath: cleanPath(fd.NewName),
		Hunks:   make([]impact.ChangedHunk, 0, len(fd.Hunks)),
	}

	// Detect file status
	if fd.OrigName == "/dev/null" || fd.OrigName == "" {
		cf.IsNew = true
		cf.OldPath = ""
	}
	if fd.NewName == "/dev/null" || fd.NewName == "" {
		cf.Deleted = true
		cf.NewPath = ""
	}
	if cf.OldPath != "" && cf.NewPath != "" && cf.OldPath != cf.NewPath {
		cf.Renamed = true
	}

	// Parse hunks
	for _, hunk := range fd.Hunks {
		ch := p.parseHunk(hunk)
		cf.Hunks = append(cf.Hunks, ch)
	}

	return cf
}

// parseHunk converts a go-diff Hunk to our ChangedHunk
func (p *GitDiffParser) parseHunk(hunk *godiff.Hunk) impact.ChangedHunk {
	ch := impact.ChangedHunk{
		OldStart: int(hunk.OrigStartLine),
		OldLines: int(hunk.OrigLines),
		NewStart: int(hunk.NewStartLine),
		NewLines: int(hunk.NewLines),
		Added:    make([]int, 0),
		Removed:  make([]int, 0),
	}

	// Parse the hunk body to find added/removed lines
	oldLine := int(hunk.OrigStartLine)
	newLine := int(hunk.NewStartLine)
	// With -U0, a side with no lines names the line before the change, not after.
	if hunk.OrigLines == 0 {
		oldLine++
	}
	if hunk.NewLines == 0 {
		newLine++
	}

	var block *impact.ChangeBlock
	flush := func() {
		if block != nil {
			ch.Blocks = append(ch.Blocks, *block)
			block = nil
		}
	}
	current := func() *impact.ChangeBlock {
		if block == nil {
			block = &impact.ChangeBlock{OldLine: oldLine, NewLine: newLine}
		}
		return block
	}

	lines := strings.Split(string(hunk.Body), "\n")
	for i, line := range lines {
		if len(line) == 0 {
			// The piece after the body's final newline isn't a line at all.
			if i == len(lines)-1 {
				break
			}
			// Empty line in diff body - treat as context line (both advance)
			flush()
			ch.OldText = append(ch.OldText, "")
			ch.NewText = append(ch.NewText, "")
			oldLine++
			newLine++
			continue
		}

		switch line[0] {
		case '+':
			b := current()
			b.Added = append(b.Added, line[1:])
			ch.Added = append(ch.Added, newLine)
			ch.NewText = append(ch.NewText, line[1:])
			newLine++
		case '-':
			b := current()
			b.Removed = append(b.Removed, line[1:])
			ch.Removed = append(ch.Removed, oldLine)
			ch.OldText = append(ch.OldText, line[1:])
			oldLine++
		case ' ':
			// Context line - both advance
			flush()
			ch.OldText = append(ch.OldText, line[1:])
			ch.NewText = append(ch.NewText, line[1:])
			oldLine++
			newLine++
		case '\\':
			// "\ No newline at end of file" - ignore
		}
	}
	flush()

	return ch
}

// cleanPath removes the a/ or b/ prefix from git diff paths
func cleanPath(path string) string {
	if path == "" || path == "/dev/null" {
		return path
	}
	// Remove a/ or b/ prefix
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		return path[2:]
	}
	return path
}

// ParseGitDiff is a convenience function to parse a git diff string
func ParseGitDiff(diffContent string) (*impact.ParsedDiff, error) {
	parser := NewGitDiffParser()
	return parser.Parse(diffContent)
}

// GetAllChangedLines returns all changed line numbers (added and modified) for a file
func GetAllChangedLines(cf *impact.ChangedFile) []int {
	lines := make([]int, 0)
	for _, hunk := range cf.Hunks {
		lines = append(lines, hunk.Added...)
	}
	return lines
}

// GetEffectivePath returns the most relevant path for a changed file
func GetEffectivePath(cf *impact.ChangedFile) string {
	if cf.Deleted {
		return cf.OldPath
	}
	return cf.NewPath
}

// IsSourceFile checks if the file is a source code file (not generated, vendor, etc.)
func IsSourceFile(path string) bool {
	// Skip common non-source paths
	skipPrefixes := []string{
		"vendor/",
		"node_modules/",
		".git/",
		"testdata/",
	}
	for _, prefix := range skipPrefixes {
		if strings.HasPrefix(path, prefix) {
			return false
		}
	}

	// Skip common generated/config files
	skipSuffixes := []string{
		".sum",
		".lock",
		".min.js",
		".min.css",
		".map",
		".pb.go",
		"_generated.go",
		"-lock.json", // package-lock.json, etc.
	}
	for _, suffix := range skipSuffixes {
		if strings.HasSuffix(path, suffix) {
			return false
		}
	}

	return true
}

// FilterSourceFiles returns only source code files from a ParsedDiff
func FilterSourceFiles(diff *impact.ParsedDiff) *impact.ParsedDiff {
	filtered := &impact.ParsedDiff{
		Files: make([]impact.ChangedFile, 0),
	}

	for _, f := range diff.Files {
		path := f.NewPath
		if path == "" {
			path = f.OldPath
		}
		if IsSourceFile(path) {
			filtered.Files = append(filtered.Files, f)
		}
	}

	return filtered
}
