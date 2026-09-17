package query

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/git"
	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
	"github.com/SimplyLiz/CodeMCP/internal/coupling"
)

const maxCouplingAge = 180 * 24 * time.Hour

// batchFileLastModified returns the last git modification time for each file
// from a single git-log invocation, avoiding O(n) subprocess spawns.
//
// git log walks history newest-first and, with --name-only restricted to the
// requested paths, lists which of them each commit touched — so the first
// commit a path shows up under is the one `git log -1 -- <path>` would report.
// The walk stops as soon as every path has been seen.
//
// The paths come from git history, which on a PR branch is whatever the PR
// author committed, so they go to git as argv — never through a shell — and
// as literal pathspecs, so a name like ":(glob)**" matches only itself.
func (e *Engine) batchFileLastModified(ctx context.Context, files []string) map[string]time.Time {
	result := make(map[string]time.Time, len(files))
	if len(files) == 0 {
		return result
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	args := []string{
		"--literal-pathspecs", "log",
		"-z", "--no-renames", "--name-only",
		"--format=" + lastModifiedHeader + "%aI",
		"--",
	}
	args = append(args, files...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = e.repoRoot
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return result
	}
	if err := cmd.Start(); err != nil {
		return result
	}
	parseLastModified(stdout, files, result)
	// Either git has finished, or every path is resolved and the rest of the
	// history is not needed: cancel kills it, and its exit status is moot.
	cancel()
	_ = cmd.Wait()
	return result
}

// lastModifiedHeader prefixes the commit-date record in batchFileLastModified's
// git log output, so it can be told apart from the path records around it.
const lastModifiedHeader = "ckb-date:"

// parseLastModified reads `git log -z --name-only --format=<header>%aI` output:
// NUL-separated records, each either a commit header or a path the commit
// touched. git puts a newline in front of the first path after a header and
// nowhere else; a commit that touched none of the paths is a header followed
// directly by the next header. The first date seen for each requested path is
// recorded, and reading stops once all of them have one.
func parseLastModified(r io.Reader, files []string, result map[string]time.Time) {
	wanted := make(map[string]bool, len(files))
	for _, f := range files {
		wanted[f] = true
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	sc.Split(splitNUL)

	var current time.Time
	afterHeader := false
	for sc.Scan() {
		rec := sc.Text()
		if afterHeader && strings.HasPrefix(rec, "\n") {
			rec = rec[1:]
		} else if !wanted[rec] {
			if date, ok := strings.CutPrefix(rec, lastModifiedHeader); ok {
				if t, err := time.Parse(time.RFC3339, date); err == nil {
					current = t
					afterHeader = true
					continue
				}
			}
		}
		afterHeader = false
		if !wanted[rec] || current.IsZero() {
			continue
		}
		if _, seen := result[rec]; !seen {
			result[rec] = current
			if len(result) == len(wanted) {
				return
			}
		}
	}
}

// splitNUL is a bufio.SplitFunc for NUL-terminated records.
func splitNUL(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// CouplingGap represents a missing co-changed file.
type CouplingGap struct {
	ChangedFile  string  `json:"changedFile"`
	MissingFile  string  `json:"missingFile"`
	CoChangeRate float64 `json:"coChangeRate"`
	LastCoChange string  `json:"lastCoChange,omitempty"`
}

// checkCouplingGaps checks if commonly co-changed files are missing from the changeset.
func (e *Engine) checkCouplingGaps(ctx context.Context, changedFiles []string, diffStats []git.DiffStats) (ReviewCheck, []ReviewFinding) {
	start := time.Now()

	changedSet := make(map[string]bool)
	for _, f := range changedFiles {
		changedSet[f] = true
	}

	// Build diff stats lookup for smart filtering
	diffStatsMap := make(map[string]git.DiffStats, len(diffStats))
	for _, ds := range diffStats {
		diffStatsMap[ds.FilePath] = ds
	}

	analyzer := coupling.NewAnalyzer(e.repoRoot, e.logger)
	minCorrelation := 0.7

	var gaps []CouplingGap

	// For each changed file, check if its highly-coupled partners are also in the changeset.
	// Skip config/CI paths — they always co-change and produce noise, not signal.
	// Limit to first 20 source files to avoid excessive git log calls.
	var filesToCheck []string
	for _, f := range changedFiles {
		if isCouplingNoiseFile(f) {
			continue
		}
		// Skip new files — they have no meaningful co-change history
		if ds, ok := diffStatsMap[f]; ok && ds.IsNew {
			continue
		}
		filesToCheck = append(filesToCheck, f)
		if len(filesToCheck) >= 20 {
			break
		}
	}

	// First pass: collect candidate gaps (before date filtering).
	type candidateGap struct {
		changedFile  string
		missingFile  string
		coChangeRate float64
	}
	var candidates []candidateGap
	missingFiles := make(map[string]bool)

	for _, file := range filesToCheck {
		if ctx.Err() != nil {
			break
		}
		result, err := analyzer.Analyze(ctx, coupling.AnalyzeOptions{
			Target:         file,
			MinCorrelation: minCorrelation,
			WindowDays:     365,
			Limit:          5,
		})
		if err != nil {
			continue
		}

		for _, corr := range result.Correlations {
			missing := corr.FilePath
			if missing == "" {
				missing = corr.File
			}
			if corr.Correlation >= minCorrelation && !changedSet[missing] && !isCouplingNoiseFile(missing) {
				candidates = append(candidates, candidateGap{
					changedFile:  file,
					missingFile:  missing,
					coChangeRate: corr.Correlation,
				})
				missingFiles[missing] = true
			}
		}
	}

	// Batch-lookup last modification dates in a single git invocation.
	filesToLookup := make([]string, 0, len(missingFiles))
	for f := range missingFiles {
		filesToLookup = append(filesToLookup, f)
	}
	lastModDates := e.batchFileLastModified(ctx, filesToLookup)

	// Second pass: filter stale couplings.
	for _, c := range candidates {
		lastMod := lastModDates[c.missingFile]
		if !lastMod.IsZero() && time.Since(lastMod) > maxCouplingAge {
			continue
		}
		var lastCoChange string
		if !lastMod.IsZero() {
			lastCoChange = lastMod.Format(time.RFC3339)
		}
		gaps = append(gaps, CouplingGap{
			ChangedFile:  c.changedFile,
			MissingFile:  c.missingFile,
			CoChangeRate: c.coChangeRate,
			LastCoChange: lastCoChange,
		})
	}

	var findings []ReviewFinding
	for _, gap := range gaps {
		severity := "warning"
		// Downgrade to info for append-only changes (low risk of breaking coupled files)
		if ds, ok := diffStatsMap[gap.ChangedFile]; ok {
			if ds.Deletions == 0 && ds.Additions > 0 {
				severity = "info"
			} else if ds.Additions > 0 && ds.Deletions < ds.Additions/10 {
				severity = "info"
			}
		}
		findings = append(findings, ReviewFinding{
			Check:      "coupling",
			Severity:   severity,
			File:       gap.ChangedFile,
			Message:    fmt.Sprintf("Missing co-change: %s (%.0f%% co-change rate)", gap.MissingFile, gap.CoChangeRate*100),
			Suggestion: fmt.Sprintf("Consider also changing %s — it historically changes together with %s", gap.MissingFile, gap.ChangedFile),
			Category:   "coupling",
			RuleID:     "ckb/coupling/missing-cochange",
		})
	}

	// Augment with Cartographer hidden coupling: co-change pairs with NO import edge.
	// These represent implicit dependencies invisible in the static dependency graph.
	if cartographer.Available() {
		hidden, err := cartographer.HiddenCoupling(e.repoRoot, 0, 3)
		if err == nil {
			existing := make(map[string]bool, len(gaps)*2)
			for _, g := range gaps {
				existing[g.ChangedFile+"\x00"+g.MissingFile] = true
				existing[g.MissingFile+"\x00"+g.ChangedFile] = true
			}
			// Cap hidden-coupling findings at N per changed file. The regular
			// co-change pass uses Limit:5 — match that intent here. Without a
			// per-file cap, large/vendor-sync PRs produce hundreds of weak
			// hits that drown out actionable findings.
			const maxHiddenPerFile = 3
			perFile := make(map[string]int)
			for _, pair := range hidden {
				srcChanged := changedSet[pair.FileA]
				tgtChanged := changedSet[pair.FileB]
				if !srcChanged && !tgtChanged {
					continue
				}
				changedFile, missingFile := pair.FileA, pair.FileB
				if tgtChanged && !srcChanged {
					changedFile, missingFile = pair.FileB, pair.FileA
				}
				// Filter noise on BOTH sides. The regular co-change pass skips
				// noise-changed files at filesToCheck assembly; this pass walks
				// hidden pairs directly and must filter the changed side too,
				// otherwise files like .gitignore (which co-change with everything
				// in any vendor/release PR) produce a flood of meaningless hits.
				if isCouplingNoiseFile(changedFile) {
					continue
				}
				if changedSet[missingFile] || isCouplingNoiseFile(missingFile) {
					continue
				}
				key := changedFile + "\x00" + missingFile
				if existing[key] {
					continue
				}
				if perFile[changedFile] >= maxHiddenPerFile {
					continue
				}
				existing[key] = true
				perFile[changedFile]++
				gaps = append(gaps, CouplingGap{
					ChangedFile:  changedFile,
					MissingFile:  missingFile,
					CoChangeRate: pair.CouplingScore,
				})
				findings = append(findings, ReviewFinding{
					Check:      "coupling",
					Severity:   "warning",
					File:       changedFile,
					Message:    fmt.Sprintf("Hidden coupling: %s co-changes with %s (%.0f%% score, no import link)", changedFile, missingFile, pair.CouplingScore*100),
					Suggestion: fmt.Sprintf("Consider also changing %s — it co-changes with %s but has no direct import relationship", missingFile, changedFile),
					Category:   "coupling",
					RuleID:     "ckb/coupling/hidden",
				})
			}
		}
	}

	status := "pass"
	summary := "No missing co-change files"
	if len(gaps) > 0 {
		status = "warn"
		summary = fmt.Sprintf("%d commonly co-changed file(s) missing from changeset", len(gaps))
	}

	return ReviewCheck{
		Name:     "coupling",
		Status:   status,
		Severity: "warning",
		Summary:  summary,
		Details:  gaps,
		Duration: time.Since(start).Milliseconds(),
	}, findings
}

// isCouplingNoiseFile returns true for paths where co-change analysis produces
// noise rather than signal (CI workflows, config dirs, generated files, tests,
// dependency manifests, and documentation).
func isCouplingNoiseFile(path string) bool {
	noisePrefixes := []string{
		".github/",
		".gitlab-ci",
		"ci/",
		".circleci/",
		".buildkite/",
		"dist/",
		"build/",
		"out/",
		"target/",
		".next/",
		"vendor/",
		"third_party/",
		"node_modules/",
		"testdata/",
		"fixtures/",
		"__tests__/",
		"l10n/",          // Flutter/i18n localization generated files
		"generated/",     // Common generated code directory
		"__generated__/", // GraphQL/Relay generated
		".dart_tool/",    // Dart tooling
		"__pycache__/",   // Python bytecode cache
	}
	for _, prefix := range noisePrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}

	noiseSuffixes := []string{
		// Config/metadata
		".yml", ".yaml", ".lock", ".sum",
		// Go generated
		".generated.go", ".gen.go", "_string.go", "_enumer.go",
		"wire_gen.go", "_mock.go",
		// Protobuf/gRPC generated
		".pb.go", ".pb.h", ".pb.cc", ".pb.ts", ".pb.js",
		"_grpc.pb.go", "_pb2.py", "_pb2_grpc.py",
		// Dart/Flutter generated
		".g.dart", ".freezed.dart", ".mocks.dart", ".arb",
		// JS/TS generated/bundled
		".min.js", ".min.css", ".bundle.js", ".chunk.js",
		// Other generated
		".d.ts",
	}
	for _, suffix := range noiseSuffixes {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}

	// Test files — co-change with source is expected, not actionable
	if strings.HasSuffix(path, "_test.go") ||
		strings.HasSuffix(path, ".test.ts") ||
		strings.HasSuffix(path, ".test.js") ||
		strings.HasSuffix(path, ".test.tsx") ||
		strings.HasSuffix(path, ".spec.ts") ||
		strings.HasSuffix(path, ".spec.js") ||
		strings.HasSuffix(path, "_test.py") ||
		strings.HasSuffix(path, "_test.rs") ||
		strings.Contains(path, "/test/") ||
		strings.Contains(path, "/tests/") {
		return true
	}

	// Exact-match noise files that change with everything
	noiseExact := map[string]bool{
		"go.mod":            true,
		"go.sum":            true,
		"package.json":      true,
		"package-lock.json": true,
		"yarn.lock":         true,
		"pnpm-lock.yaml":    true,
		"Cargo.lock":        true,
		"Cargo.toml":        true,
		"requirements.txt":  true,
		"pyproject.toml":    true,
		"pom.xml":           true,
		"build.gradle":      true,
		"README.md":         true,
		"CHANGELOG.md":      true,
		"HISTORY.md":        true,
		".gitignore":        true,
		".editorconfig":     true,
		"Dockerfile":        true,
		"Makefile":          true,
	}

	// Check basename for exact matches
	base := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		base = path[idx+1:]
	}
	return noiseExact[base]
}
