package secrets

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// GitScanner scans git history for secrets.
type GitScanner struct {
	repoRoot string
	patterns []Pattern
	timeout  time.Duration
}

// NewGitScanner creates a new git history scanner.
func NewGitScanner(repoRoot string) *GitScanner {
	return &GitScanner{
		repoRoot: repoRoot,
		patterns: BuiltinPatterns,
		timeout:  10 * time.Minute,
	}
}

// ScanHistory scans git commit history for secrets.
func (g *GitScanner) ScanHistory(ctx context.Context, opts ScanOptions) ([]SecretFinding, error) {
	findings, _, err := g.scanHistory(ctx, opts)
	return findings, err
}

// scanHistory is ScanHistory that also counts the distinct files whose
// added lines it read.
func (g *GitScanner) scanHistory(ctx context.Context, opts ScanOptions) ([]SecretFinding, int, error) {
	// Build git log command
	args := []string{
		"log",
		"-p",    // Show patch
		"--all", // All branches
		"--full-history",
		"--format=COMMIT:%H|%an|%aI", // Custom format for parsing
	}

	if opts.SinceCommit != "" {
		args = append(args, opts.SinceCommit+"..HEAD")
	}
	if opts.UntilCommit != "" {
		args = append(args, "--until", opts.UntilCommit)
	}
	if opts.MaxCommits > 0 {
		args = append(args, "-n", strconv.Itoa(opts.MaxCommits))
	}

	// Add path filters
	if len(opts.Paths) > 0 {
		args = append(args, "--")
		args = append(args, opts.Paths...)
	}

	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.repoRoot

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, 0, fmt.Errorf("failed to start git: %w", err)
	}

	var findings []SecretFinding
	files := make(map[string]bool)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024) // 10MB buffer for large diffs

	var currentCommit, currentAuthor, currentDate string
	var currentFile string
	lineInFile := 0
	inDiff := false

	for scanner.Scan() {
		line := scanner.Text()

		// Parse commit header
		if strings.HasPrefix(line, "COMMIT:") {
			parts := strings.SplitN(strings.TrimPrefix(line, "COMMIT:"), "|", 3)
			if len(parts) >= 3 {
				currentCommit = parts[0]
				currentAuthor = parts[1]
				currentDate = parts[2]
			}
			inDiff = false
			continue
		}

		// Parse diff header
		if strings.HasPrefix(line, "diff --git") {
			inDiff = true
			lineInFile = 0
			// Extract file name
			parts := strings.Split(line, " b/")
			if len(parts) >= 2 {
				currentFile = parts[1]
				files[currentFile] = true
			}
			continue
		}

		// Parse hunk header
		if strings.HasPrefix(line, "@@") && inDiff {
			// Extract starting line number
			// Format: @@ -old,count +new,count @@
			parts := strings.Split(line, "+")
			if len(parts) >= 2 {
				numPart := strings.Split(parts[1], ",")[0]
				lineInFile, _ = strconv.Atoi(numPart)
			}
			continue
		}

		// Only scan added lines (starting with +)
		if inDiff && strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			lineInFile++
			content := strings.TrimPrefix(line, "+")

			// Scan for secrets
			for _, pattern := range g.patterns {
				matches := pattern.Regex.FindAllStringSubmatchIndex(content, -1)
				if matches == nil {
					continue
				}

				for _, match := range matches {
					var secret string
					if len(match) >= 4 {
						secret = content[match[2]:match[3]]
					} else {
						secret = content[match[0]:match[1]]
					}

					// Check entropy if required
					if pattern.MinEntropy > 0 {
						if ShannonEntropy(secret) < pattern.MinEntropy {
							continue
						}
					}

					// Skip likely false positives
					if isLikelyFalsePositive(content, secret) {
						continue
					}

					findings = append(findings, SecretFinding{
						File:       currentFile,
						Line:       lineInFile,
						Type:       pattern.Type,
						Severity:   pattern.Severity,
						Match:      redactSecret(secret, 4),
						RawMatch:   secret,
						Context:    redactLine(content, match[0], match[1]),
						Rule:       pattern.Name,
						Confidence: calculateConfidence(secret, pattern),
						Source:     "builtin",
						Commit:     currentCommit,
						Author:     currentAuthor,
						CommitDate: currentDate,
					})
				}
			}
		} else if inDiff && !strings.HasPrefix(line, "-") {
			// Count lines for context (but don't scan removed lines)
			lineInFile++
		}
	}

	if err := scanner.Err(); err != nil {
		return findings, len(files), fmt.Errorf("error reading git output: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		// Git may exit with error if there are no commits matching criteria
		// This is not necessarily an error for us
		if ctx.Err() == context.DeadlineExceeded {
			return findings, len(files), fmt.Errorf("git scan timed out")
		}
	}

	return findings, len(files), nil
}

// ScanStaged scans only staged (git add'd) changes for secrets.
func (g *GitScanner) ScanStaged(ctx context.Context, opts ScanOptions) ([]SecretFinding, error) {
	findings, _, err := g.scanStaged(ctx)
	return findings, err
}

// scanStaged is ScanStaged that also counts the staged files it read.
func (g *GitScanner) scanStaged(ctx context.Context) ([]SecretFinding, int, error) {
	// Get staged diff
	args := []string{"diff", "--cached", "--unified=0"}

	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.repoRoot

	output, err := cmd.Output()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get staged diff: %w", err)
	}

	findings, err := g.scanDiff(string(output), "staged")
	return findings, countDiffFiles(string(output)), err
}

// countDiffFiles counts the files a unified diff touches.
func countDiffFiles(diff string) int {
	n := 0
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git") {
			n++
		}
	}
	return n
}

// scanDiff scans a unified diff for secrets.
func (g *GitScanner) scanDiff(diff, source string) ([]SecretFinding, error) {
	var findings []SecretFinding
	var currentFile string
	lineInFile := 0

	lines := strings.Split(diff, "\n")
	for _, line := range lines {
		// Parse diff header
		if strings.HasPrefix(line, "diff --git") {
			parts := strings.Split(line, " b/")
			if len(parts) >= 2 {
				currentFile = parts[1]
			}
			continue
		}

		// Parse hunk header
		if strings.HasPrefix(line, "@@") {
			parts := strings.Split(line, "+")
			if len(parts) >= 2 {
				numPart := strings.Split(parts[1], ",")[0]
				lineInFile, _ = strconv.Atoi(numPart)
			}
			continue
		}

		// Only scan added lines
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			content := strings.TrimPrefix(line, "+")

			for _, pattern := range g.patterns {
				matches := pattern.Regex.FindAllStringSubmatchIndex(content, -1)
				if matches == nil {
					continue
				}

				for _, match := range matches {
					var secret string
					if len(match) >= 4 {
						secret = content[match[2]:match[3]]
					} else {
						secret = content[match[0]:match[1]]
					}

					if pattern.MinEntropy > 0 && ShannonEntropy(secret) < pattern.MinEntropy {
						continue
					}

					if isLikelyFalsePositive(content, secret) {
						continue
					}

					findings = append(findings, SecretFinding{
						File:       currentFile,
						Line:       lineInFile,
						Type:       pattern.Type,
						Severity:   pattern.Severity,
						Match:      redactSecret(secret, 4),
						RawMatch:   secret,
						Context:    redactLine(content, match[0], match[1]),
						Rule:       pattern.Name,
						Confidence: calculateConfidence(secret, pattern),
						Source:     source,
					})
				}
			}
			lineInFile++
		} else if !strings.HasPrefix(line, "-") {
			lineInFile++
		}
	}

	return findings, nil
}
