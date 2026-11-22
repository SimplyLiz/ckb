package query

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	indexmeta "github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/overlay"
)

const (
	// maxChangedSinceIndex caps the changedSinceIndex list so meta stays small.
	maxChangedSinceIndex = 50
	// freshnessCacheTTL is how long a git-derived changed-file set is reused.
	freshnessCacheTTL = 5 * time.Second
)

// IndexFreshness describes how the loaded SCIP index relates to the working
// tree, and whether the difference touches the files in one specific result.
type IndexFreshness struct {
	IndexedCommit              string   `json:"indexedCommit"`
	CommitsBehind              int      `json:"commitsBehind"`
	ChangedSinceIndex          []string `json:"changedSinceIndex,omitempty"`
	ChangedSinceIndexTruncated bool     `json:"changedSinceIndexTruncated,omitempty"`
	AffectsResult              bool     `json:"affectsResult"`
	Hint                       string   `json:"hint,omitempty"`
	// Overlay says how much of the change since indexing the index overlay
	// serves. Absent without an overlay.
	Overlay *OverlayFreshness `json:"overlay,omitempty"`
}

type changedSet struct {
	known         bool
	commitsBehind int
	files         []string // sorted, repo-relative, slash-separated
}

type freshnessCacheEntry struct {
	at  time.Time
	set *changedSet
}

var (
	freshnessCacheMu sync.Mutex
	freshnessCache   = map[string]freshnessCacheEntry{}
)

// IndexFreshnessFor reports index freshness for a result touching the given
// repo-relative files. Returns nil when SCIP did not contribute (no adapter or
// index not available).
func (e *Engine) IndexFreshnessFor(ctx context.Context, files []string) *IndexFreshness {
	if e == nil || e.scipAdapter == nil || !e.scipAdapter.IsAvailable() {
		return nil
	}
	info := e.scipAdapter.GetIndexInfo()
	if info == nil || !info.Available {
		return nil
	}
	f := cachedIndexFreshness(e.repoRoot, info.IndexedCommit, indexSnapshot(e.repoRoot, info.IndexedCommit), files)
	// Only when the overlay served this result: a diff-driven analysis pins
	// the base (SCIPAdapter.PinBase).
	if ov := e.OverlayFreshness(); ov != nil && ov.Files > 0 && e.scipAdapter.ServingTag() != "" {
		f.Overlay = ov
		applyOverlayHint(e.repoRoot, f, e.coveredByOverlay(), files)
	}
	return f
}

// applyOverlayHint rewrites the hint when the overlay serves the result's
// changed files: their positions are current, and what is missing depends on
// the layers each file got. With the declaration and reference layers (Go,
// TypeScript) only the sites they could not resolve are; a file left at the
// line remap misses everything on its changed lines.
func applyOverlayHint(repoRoot string, f *IndexFreshness, covered map[string]overlay.FileInfo, resultFiles []string) {
	if !f.AffectsResult || len(covered) == 0 {
		if !f.AffectsResult && f.Overlay.Covered > 0 && f.Hint != "" {
			f.Hint += fmt.Sprintf("; %d of them served from the overlay", f.Overlay.Covered)
		}
		return
	}
	served, other, unresolved := 0, 0, 0
	var remapOnly []string
	changed := map[string]bool{}
	for _, p := range f.ChangedSinceIndex {
		changed[p] = true
	}
	counted := map[string]bool{}
	for _, p := range resultFiles {
		p = normalizeResultPath(repoRoot, p)
		if !changed[p] || counted[p] {
			continue
		}
		counted[p] = true
		fi, ok := covered[p]
		if !ok {
			other++
			continue
		}
		served++
		switch {
		case fi.State == overlay.StateDeleted:
		case slices.Contains(fi.Layers, 2):
			unresolved += fi.Unresolved
		default:
			remapOnly = append(remapOnly, p)
		}
	}
	if served == 0 {
		return
	}
	var missing string
	switch {
	case len(remapOnly) > 0:
		missing = fmt.Sprintf("in %d of them (e.g. %s) declarations and uses on the changed lines come in with a reindex", len(remapOnly), remapOnly[0])
	case unresolved > 0:
		missing = fmt.Sprintf("declarations and uses on the changed lines are read from the source, except %d use(s) it could not resolve, which come in with a reindex", unresolved)
	default:
		missing = "declarations and uses on the changed lines are read from the source"
	}
	if other == 0 {
		f.Hint = fmt.Sprintf("%d result file(s) changed since indexing, served from the overlay: positions are current; %s", served, missing)
		return
	}
	f.Hint = fmt.Sprintf("%d result file(s) changed since indexing: %d served from the overlay (positions current; %s), "+
		"%d not covered — call reindex for exact results", served+other, served, missing, other)
}

// indexSnapshot is the working-tree snapshot the index at commit was built
// from (index-meta.json worktreeTree), or "" for an index built from a clean
// tree or without metadata.
func indexSnapshot(repoRoot, commit string) string {
	meta, err := indexmeta.LoadMeta(filepath.Join(repoRoot, ".ckb"))
	if err != nil || meta == nil || commit == "" || meta.CommitHash != commit {
		return ""
	}
	return meta.WorktreeTree
}

func cachedIndexFreshness(repoRoot, indexedCommit, snapshot string, resultFiles []string) *IndexFreshness {
	key := repoRoot + "\x00" + indexedCommit + "\x00" + snapshot
	freshnessCacheMu.Lock()
	ent, ok := freshnessCache[key]
	freshnessCacheMu.Unlock()
	if !ok || time.Since(ent.at) > freshnessCacheTTL {
		ent = freshnessCacheEntry{at: time.Now(), set: loadChangedSet(repoRoot, indexedCommit, snapshot)}
		freshnessCacheMu.Lock()
		freshnessCache[key] = ent
		freshnessCacheMu.Unlock()
	}
	return buildIndexFreshness(repoRoot, indexedCommit, ent.set, resultFiles)
}

// computeIndexFreshness is the uncached pure computation (runs git).
func computeIndexFreshness(repoRoot, indexedCommit string, resultFiles []string) *IndexFreshness {
	return buildIndexFreshness(repoRoot, indexedCommit, loadChangedSet(repoRoot, indexedCommit, ""), resultFiles)
}

func gitOut(repoRoot string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...) // #nosec G204 //nolint:gosec // fixed git subcommands, commit validated by cat-file
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	return cmd.Output()
}

// loadChangedSet lists the files that differ from what the index was built
// from: the indexed commit, or the working-tree snapshot when the index was
// built from a dirty tree (then the uncommitted changes it already contains
// are not listed again).
func loadChangedSet(repoRoot, indexedCommit, snapshot string) *changedSet {
	if indexedCommit == "" {
		return &changedSet{}
	}
	if _, err := gitOut(repoRoot, "cat-file", "-e", indexedCommit+"^{commit}"); err != nil {
		return &changedSet{}
	}
	set := &changedSet{known: true}
	if n, err := scip.CountCommitsBetween(repoRoot, indexedCommit, "HEAD"); err == nil {
		set.commitsBehind = n
	}
	if snapshot != "" {
		if files, err := indexmeta.ChangedSinceSnapshot(context.Background(), repoRoot, snapshot); err == nil {
			sort.Strings(files)
			set.files = files
			return set
		}
		// The snapshot tree is gone (gc): fall back to the commit.
	}
	seen := map[string]bool{}
	add := func(out []byte) {
		for _, p := range strings.Split(string(out), "\x00") {
			if p != "" && !seen[p] {
				seen[p] = true
				set.files = append(set.files, filepath.ToSlash(p))
			}
		}
	}
	if out, err := gitOut(repoRoot, "diff", "--name-only", "-z", indexedCommit); err == nil {
		add(out)
	}
	if out, err := gitOut(repoRoot, "ls-files", "--others", "--exclude-standard", "-z"); err == nil {
		add(out)
	}
	sort.Strings(set.files)
	return set
}

func normalizeResultPath(repoRoot, p string) string {
	p = filepath.ToSlash(p)
	if filepath.IsAbs(p) {
		if rel, err := filepath.Rel(repoRoot, p); err == nil {
			p = filepath.ToSlash(rel)
		}
	}
	return strings.TrimPrefix(p, "./")
}

func buildIndexFreshness(repoRoot, indexedCommit string, set *changedSet, resultFiles []string) *IndexFreshness {
	if !set.known {
		return &IndexFreshness{
			IndexedCommit: "",
			Hint:          "indexed commit unknown — freshness cannot be determined",
		}
	}
	f := &IndexFreshness{IndexedCommit: indexedCommit, CommitsBehind: set.commitsBehind}

	inResult := map[string]bool{}
	for _, p := range resultFiles {
		if p != "" {
			inResult[normalizeResultPath(repoRoot, p)] = true
		}
	}
	var affecting, others []string
	for _, p := range set.files {
		if inResult[p] {
			affecting = append(affecting, p)
		} else {
			others = append(others, p)
		}
	}
	f.AffectsResult = len(affecting) > 0

	all := append(append([]string{}, affecting...), others...)
	if len(all) > maxChangedSinceIndex {
		all = all[:maxChangedSinceIndex]
		f.ChangedSinceIndexTruncated = true
	}
	f.ChangedSinceIndex = all

	switch {
	case f.AffectsResult:
		f.Hint = fmt.Sprintf("%d result file(s) changed since indexing — call reindex for exact results", len(affecting))
	case len(set.files) > 0:
		f.Hint = fmt.Sprintf("%d file(s) changed since indexing, none in this result", len(set.files))
	case f.CommitsBehind > 0:
		f.Hint = fmt.Sprintf("index is %d commit(s) behind HEAD, but no file content differs", f.CommitsBehind)
	}
	return f
}
