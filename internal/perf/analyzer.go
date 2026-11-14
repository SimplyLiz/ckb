package perf

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// Analyzer finds structural performance issues (AnalyzeStructural).
type Analyzer struct {
	repoRoot string
	logger   *slog.Logger
}

// NewAnalyzer creates an Analyzer for the repository at repoRoot.
func NewAnalyzer(repoRoot string, logger *slog.Logger) *Analyzer {
	return &Analyzer{repoRoot: repoRoot, logger: logger}
}

// filePair is an ordered (a <= b) pair used as a map key.
type filePair struct{ a, b string }

// defaultIgnorePrefixes are path prefixes that generate noise in hidden-coupling
// analysis. They change in sweeps (fixture updates, vendoring) that have nothing
// to do with behavioral coupling.
var defaultIgnorePrefixes = []string{
	"testdata/",
	"vendor/",
	"node_modules/",
	".ckb/",
}

func shouldIgnore(file string) bool {
	for _, prefix := range defaultIgnorePrefixes {
		if strings.HasPrefix(file, prefix) {
			return true
		}
	}
	return false
}

// buildCoChangePairs runs a single git log pass and builds co-change counts
// for all file pairs. Returns pairCounts and per-file commit totals.
//
// maxCommitFiles: skip commits touching more than this many files (0 = unlimited).
// minCoChanges: prune pairs below this count after all commits are parsed —
// those entries would be filtered in Scan anyway, so dropping them early
// reduces memory and speeds up the O(N²/2) correlation iteration.
func (a *Analyzer) buildCoChangePairs(ctx context.Context, since time.Time, scope []string, maxCommitFiles, minCoChanges int) (map[filePair]int, map[string]int, error) {
	sinceStr := since.Format("2006-01-02")

	args := []string{
		"log",
		"--format=COMMIT %H",
		"--name-only",
		"--since=" + sinceStr,
		"--diff-filter=d", // exclude deleted files
		"--no-merges",
	}
	if len(scope) > 0 {
		args = append(args, "--")
		args = append(args, scope...)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = a.repoRoot

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("git log pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("git log: %w", err)
	}

	pairCounts := make(map[filePair]int)
	fileTotals := make(map[string]int)

	// Reusable buffers — allocated once, cleared between commits.
	seen := make(map[string]bool, 32)
	var currentFiles []string

	// Stream git output line by line — avoids loading the full log into memory
	// and copying it to a string before splitting.
	commitPrefix := []byte("COMMIT ")
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		b := bytes.TrimRight(scanner.Bytes(), "\r")
		if bytes.HasPrefix(b, commitPrefix) {
			// Fix 2: skip commits that touch more files than the threshold.
			// Mass renames / formatting sweeps inflate pairCounts O(files²)
			// without contributing useful coupling signal.
			if maxCommitFiles > 0 && len(currentFiles) > maxCommitFiles {
				a.logger.Debug("skipping large commit", "files", len(currentFiles))
				currentFiles = currentFiles[:0]
				continue
			}
			a.recordCommit(currentFiles, pairCounts, fileTotals, seen)
			currentFiles = currentFiles[:0]
			continue
		}
		if len(b) == 0 {
			continue
		}
		currentFiles = append(currentFiles, string(b))
	}
	if scanErr := scanner.Err(); scanErr != nil {
		_ = cmd.Wait()
		return nil, nil, fmt.Errorf("reading git log: %w", scanErr)
	}
	// Flush last commit (apply the same size guard).
	if maxCommitFiles == 0 || len(currentFiles) <= maxCommitFiles {
		a.recordCommit(currentFiles, pairCounts, fileTotals, seen)
	}

	if err := cmd.Wait(); err != nil {
		if s := stderrBuf.String(); s != "" {
			return nil, nil, fmt.Errorf("git log: %s", s)
		}
		return nil, nil, fmt.Errorf("git log: %w", err)
	}

	// Fix 3: early prune — drop pairs that can never reach MinCoChanges.
	// The correlation filter in Scan() would remove these anyway; pruning here
	// shrinks the map before the O(N²/2) correlation iteration, which matters
	// for monorepos with thousands of hot files.
	if minCoChanges > 1 {
		for pair, count := range pairCounts {
			if count < minCoChanges {
				delete(pairCounts, pair)
			}
		}
	}

	return pairCounts, fileTotals, nil
}

func (a *Analyzer) recordCommit(files []string, pairCounts map[filePair]int, fileTotals map[string]int, seen map[string]bool) {
	if len(files) == 0 {
		return
	}
	// Clear the reusable seen map from the previous commit.
	for k := range seen {
		delete(seen, k)
	}
	// Deduplicate and filter in a single pass, writing unique entries
	// back into the files slice (safe: we only write index j where j <= i).
	unique := files[:0]
	for _, f := range files {
		if shouldIgnore(f) {
			continue
		}
		if !seen[f] {
			seen[f] = true
			unique = append(unique, f)
			fileTotals[f]++
		}
	}
	// Build all pairs (order-independent key).
	for i := 0; i < len(unique); i++ {
		for j := i + 1; j < len(unique); j++ {
			fa, fb := unique[i], unique[j]
			if fa > fb {
				fa, fb = fb, fa
			}
			pairCounts[filePair{fa, fb}]++
		}
	}
}
