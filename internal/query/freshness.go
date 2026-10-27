package query

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
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
	return cachedIndexFreshness(e.repoRoot, info.IndexedCommit, files)
}

func cachedIndexFreshness(repoRoot, indexedCommit string, resultFiles []string) *IndexFreshness {
	key := repoRoot + "\x00" + indexedCommit
	freshnessCacheMu.Lock()
	ent, ok := freshnessCache[key]
	freshnessCacheMu.Unlock()
	if !ok || time.Since(ent.at) > freshnessCacheTTL {
		ent = freshnessCacheEntry{at: time.Now(), set: loadChangedSet(repoRoot, indexedCommit)}
		freshnessCacheMu.Lock()
		freshnessCache[key] = ent
		freshnessCacheMu.Unlock()
	}
	return buildIndexFreshness(repoRoot, indexedCommit, ent.set, resultFiles)
}

// computeIndexFreshness is the uncached pure computation (runs git).
func computeIndexFreshness(repoRoot, indexedCommit string, resultFiles []string) *IndexFreshness {
	return buildIndexFreshness(repoRoot, indexedCommit, loadChangedSet(repoRoot, indexedCommit), resultFiles)
}

func gitOut(repoRoot string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...) // #nosec G204 //nolint:gosec // fixed git subcommands, commit validated by cat-file
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	return cmd.Output()
}

func loadChangedSet(repoRoot, indexedCommit string) *changedSet {
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
