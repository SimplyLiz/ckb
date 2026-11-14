package coupling

import (
	"bufio"
	"context"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/pathclass"
)

// History is the co-change record of a repository's recent commits, for
// questions about pairs of files in both directions: hidden coupling, hotspot
// partners, implicit co-owners. Analyze answers the one-sided question "what
// changes when this file changes" over a time window; History scores a pair
// against the less-changed file, so a file that always follows a busy one is
// coupled to it even though the busy file mostly changes alone.
//
// It reads the last DefaultHistoryCommits commits once, as Cartographer's
// git_cochange did, which this replaces: bot authors and formatting-only
// commits are left out, commits touching more than maxFilesPerCommit files
// carry no signal and are dropped, and lockfiles, binaries and assets are not
// files of a pair.
type History struct {
	commits [][]string
	churn   map[string]int
	byFile  map[string][]int // file -> indexes into commits
}

// DefaultHistoryCommits is how many recent commits LoadHistory reads.
const DefaultHistoryCommits = 500

// HistoryPrior is the number of pseudo-commits in which a pair is assumed not
// to have changed together (see Score).
const HistoryPrior = 2.0

// Pair is two files that changed together in Count commits.
type Pair struct {
	FileA string  `json:"fileA"`
	FileB string  `json:"fileB"`
	Count int     `json:"count"`
	Score float64 `json:"score"`
}

// Score is the coupling of two files that changed together in count commits,
// out of churnA and churnB commits each: Tornhill's ratio count/min(churn)
// shrunk by HistoryPrior commits without a co-change. 2 of 2 scores 0.5, 5 of
// 5 0.71, 14 of 16 0.78 — a file changed twice, both times with another, no
// longer outranks a partner that followed in 14 of 16 commits. Same formula as
// Cartographer's coupling_score, so the floors calibrated on it still hold.
func Score(count, churnA, churnB int) float64 {
	minChurn := churnA
	if churnB < minChurn {
		minChurn = churnB
	}
	if count > minChurn {
		minChurn = count
	}
	return float64(count) / (float64(minChurn) + HistoryPrior)
}

// LoadHistory reads the last maxCommits commits of the repository at repoRoot
// (DefaultHistoryCommits when maxCommits <= 0). classes may be nil; generated
// and data paths it names are left out of the pairs like noise files.
func LoadHistory(ctx context.Context, repoRoot string, maxCommits int, classes *pathclass.Classifier) (*History, error) {
	if maxCommits <= 0 {
		maxCommits = DefaultHistoryCommits
	}
	cmd := exec.CommandContext(ctx, "git", "log", "-n", strconv.Itoa(maxCommits), "--name-only", "--format=%x1f%an%x1f%s") // #nosec G204 -- fixed binary and arguments
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && strings.Contains(string(exitErr.Stderr), "does not have any commits") {
			return newHistory(nil), nil
		}
		return nil, err
	}
	return newHistory(parseHistoryLog(string(out), classes)), nil
}

// parseHistoryLog splits `git log --name-only --format=%x1f%an%x1f%s` output
// into per-commit file lists, dropping skipped commits, noise files and
// commits too large to mean anything.
func parseHistoryLog(out string, classes *pathclass.Classifier) [][]string {
	var commits [][]string
	var current []string
	skip := false
	flush := func() {
		if !skip && len(current) > 0 && len(current) <= maxFilesPerCommit {
			commits = append(commits, current)
		}
		current = nil
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "\x1f") {
			flush()
			parts := strings.SplitN(line, "\x1f", 3)
			author, subject := "", ""
			if len(parts) > 1 {
				author = parts[1]
			}
			if len(parts) > 2 {
				subject = parts[2]
			}
			skip = isBotAuthor(author) || isFormattingSubject(subject)
			continue
		}
		if line == "" || isHistoryNoise(line) {
			continue
		}
		if classes != nil && classes.Excluded(line) {
			continue
		}
		current = append(current, line)
	}
	flush()
	return commits
}

// HistoryFromCommits builds a History from per-commit file lists, newest
// first, already filtered. For tests and callers holding their own log.
func HistoryFromCommits(commits [][]string) *History { return newHistory(commits) }

func newHistory(commits [][]string) *History {
	h := &History{commits: commits, churn: map[string]int{}, byFile: map[string][]int{}}
	for i, files := range commits {
		for _, f := range files {
			h.churn[f]++
			h.byFile[f] = append(h.byFile[f], i)
		}
	}
	return h
}

// Churn is the number of counted commits that touched file.
func (h *History) Churn(file string) int {
	if h == nil {
		return 0
	}
	return h.churn[file]
}

// Partners returns every file that changed together with file, FileA always
// being file, strongest first (score, then count, then path).
func (h *History) Partners(file string) []Pair {
	if h == nil {
		return nil
	}
	counts := map[string]int{}
	for _, i := range h.byFile[file] {
		for _, other := range h.commits[i] {
			if other != file {
				counts[other]++
			}
		}
	}
	out := make([]Pair, 0, len(counts))
	ca := h.churn[file]
	for other, n := range counts {
		out = append(out, Pair{FileA: file, FileB: other, Count: n, Score: Score(n, ca, h.churn[other])})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].FileB < out[j].FileB
	})
	return out
}

// StrongPartners returns the partners of file with at least minCount shared
// commits and a score of at least minScore, strongest first, at most limit
// (all when limit <= 0).
func (h *History) StrongPartners(file string, minCount int, minScore float64, limit int) []Pair {
	var out []Pair
	for _, p := range h.Partners(file) {
		if p.Count < minCount || p.Score < minScore {
			continue
		}
		out = append(out, p)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func isBotAuthor(author string) bool {
	lower := strings.ToLower(author)
	for _, p := range []string{"[bot]", "dependabot", "renovate", "github-actions", "snyk-bot", "greenkeeper", "semantic-release", "auto-merge", "release-bot", "ci-bot"} {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func isFormattingSubject(subject string) bool {
	lower := strings.ToLower(subject)
	for _, p := range []string{
		"apply prettier", "run prettier", "prettier format", "format code", "fix formatting",
		"auto format", "lint fix", "eslint fix", "fix lint", "apply lint", "rustfmt", "cargo fmt",
		"gofmt", "black format", "isort", "trailing whitespace", "fix whitespace", "whitespace fix",
		"normalize line endings", "editorconfig",
	} {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

var historyNoiseBase = map[string]bool{
	"package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true, "bun.lockb": true,
	"Cargo.lock": true, "go.sum": true, "poetry.lock": true, "Pipfile.lock": true, "uv.lock": true,
	"composer.lock": true, "Gemfile.lock": true, "pubspec.lock": true, "packages.lock.json": true, ".DS_Store": true,
}

var historyNoiseExt = map[string]bool{
	"png": true, "jpg": true, "jpeg": true, "gif": true, "ico": true, "webp": true, "bmp": true,
	"pdf": true, "lock": true, "zip": true, "gz": true, "tar": true, "tgz": true, "svg": true,
	"woff": true, "woff2": true, "ttf": true, "eot": true, "otf": true, "mp3": true, "mp4": true,
	"mov": true, "webm": true, "wasm": true, "class": true, "o": true, "a": true, "so": true,
	"dylib": true, "dll": true, "exe": true, "log": true, "map": true,
}

// historyIgnoredDirs are directories whose files are never a pair: vendored
// and bundled third-party source, build output, tool state. Cartographer's
// scanner list, which its co-change used.
var historyIgnoredDirs = map[string]bool{
	"node_modules": true, ".git": true, "target": true, "dist": true, "vendor": true,
	"thirdparty": true, "third_party": true, "third-party": true, "3rdparty": true,
	"external": true, "externals": true, ".next": true, "build": true, ".build": true,
	"out": true, ".env": true, "__pycache__": true, ".venv": true, "venv": true,
	".idea": true, ".vscode": true, "coverage": true, ".nuxt": true,
}

// isHistoryNoise reports paths that only add noise to co-change: lockfiles,
// binaries and assets, logs, source maps, minified bundles, vendored code.
func isHistoryNoise(p string) bool {
	base := path.Base(p)
	if historyNoiseBase[base] {
		return true
	}
	if strings.HasSuffix(p, ".min.js") || strings.HasSuffix(p, ".min.css") {
		return true
	}
	for _, part := range strings.Split(p, "/") {
		if historyIgnoredDirs[part] || historyNoiseBase[part] {
			return true
		}
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(p), "."))
	return historyNoiseExt[ext]
}
