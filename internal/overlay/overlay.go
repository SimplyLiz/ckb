package overlay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/diff"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// File states in Info.
const (
	StateRemapped  = "remapped"  // Layer 0: occurrences moved, changed lines dropped
	StateDeleted   = "deleted"   // document removed
	StateRenamed   = "renamed"   // document moved to the new path and remapped
	StateUncovered = "uncovered" // changed, but the overlay has nothing for it (a new file)
)

// FileInfo is what the overlay did for one changed file.
type FileInfo struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	State   string `json:"state"`
	Layers  []int  `json:"layers,omitempty"`
	Dropped int    `json:"droppedOccurrences,omitempty"`
}

// Info describes an overlay: which changed files it covers and how.
type Info struct {
	IndexedCommit string        `json:"indexedCommit"`
	Head          string        `json:"head"`
	Key           string        `json:"-"`
	Files         []FileInfo    `json:"files"`
	BuildTime     time.Duration `json:"-"`
	BuildMs       float64       `json:"buildMs"`
}

// Covered counts the changed files the overlay serves (all but uncovered).
func (in *Info) Covered() int {
	if in == nil {
		return 0
	}
	n := 0
	for _, f := range in.Files {
		if f.State != StateUncovered {
			n++
		}
	}
	return n
}

// Layers lists the layers that contributed to at least one file.
func (in *Info) Layers() []int {
	if in == nil {
		return nil
	}
	seen := map[int]bool{}
	for _, f := range in.Files {
		for _, l := range f.Layers {
			seen[l] = true
		}
	}
	out := make([]int, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Ints(out)
	return out
}

// Changes is the working tree's difference from the indexed commit.
type Changes struct {
	Head      string
	Diff      *impact.ParsedDiff
	Untracked []string
	// Key identifies this state: HEAD, the diff text and the untracked
	// files. Two equal keys give the same overlay.
	Key string
}

// LoadChanges reads the working tree's changes since commit: tracked files
// (committed since, staged or not) as a zero-context diff with renames, and
// untracked files by name.
func LoadChanges(ctx context.Context, repoRoot, commit string) (*Changes, error) {
	if commit == "" {
		return nil, fmt.Errorf("no indexed commit")
	}
	if _, err := git(ctx, repoRoot, "cat-file", "-e", commit+"^{commit}"); err != nil {
		return nil, fmt.Errorf("indexed commit %s not in this repository", commit)
	}
	head, _ := git(ctx, repoRoot, "rev-parse", "HEAD")
	text, err := git(ctx, repoRoot, "diff", "-U0", "--no-color", "--no-ext-diff", "--no-textconv", "-M", commit, "--")
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	untracked, _ := git(ctx, repoRoot, "ls-files", "--others", "--exclude-standard", "-z")

	parsed, err := diff.ParseGitDiff(string(text))
	if err != nil {
		return nil, fmt.Errorf("parse diff: %w", err)
	}
	h := sha256.New()
	h.Write(head)
	h.Write([]byte{0})
	h.Write(text)
	h.Write([]byte{0})
	h.Write(untracked)
	c := &Changes{
		Head: strings.TrimSpace(string(head)),
		Diff: parsed,
		Key:  hex.EncodeToString(h.Sum(nil)),
	}
	for _, p := range strings.Split(string(untracked), "\x00") {
		if p != "" {
			c.Untracked = append(c.Untracked, p)
		}
	}
	return c, nil
}

func git(ctx context.Context, repoRoot string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...) // #nosec G204 //nolint:gosec // fixed git subcommands, commit verified by cat-file
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && stderr.Len() > 0 {
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, err
}

// Build patches base with the changes. It returns base itself (and an Info
// with no files) when nothing the index knows about changed. base is never
// modified.
func Build(base *scip.SCIPIndex, ch *Changes) (*scip.SCIPIndex, *Info) {
	start := time.Now()
	info := &Info{IndexedCommit: base.IndexedCommit, Head: ch.Head, Key: ch.Key}
	replace := map[string]*scip.Document{}
	indexed := indexedExtensions(base)

	for i := range ch.Diff.Files {
		f := &ch.Diff.Files[i]
		switch {
		case f.Deleted:
			if base.DocumentsByPath[f.OldPath] != nil {
				replace[f.OldPath] = nil
				info.Files = append(info.Files, FileInfo{Path: f.OldPath, State: StateDeleted, Layers: []int{0}})
			}
		case f.IsNew:
			if indexed[path.Ext(f.NewPath)] {
				info.Files = append(info.Files, FileInfo{Path: f.NewPath, State: StateUncovered})
			}
		default:
			oldPath := f.OldPath
			if oldPath == "" {
				oldPath = f.NewPath
			}
			doc := base.DocumentsByPath[oldPath]
			if doc == nil {
				// Not an indexed file (docs, config) or one the indexer
				// skipped; nothing to patch.
				continue
			}
			nd, st := RemapDocument(doc, NewLineMap(f.Hunks))
			fi := FileInfo{Path: f.NewPath, State: StateRemapped, Layers: []int{0}, Dropped: st.Dropped}
			if f.Renamed {
				replace[oldPath] = nil
				fi.OldPath = oldPath
				fi.State = StateRenamed
			}
			replace[f.NewPath] = nd
			info.Files = append(info.Files, fi)
		}
	}
	for _, p := range ch.Untracked {
		if indexed[path.Ext(p)] {
			info.Files = append(info.Files, FileInfo{Path: p, State: StateUncovered})
		}
	}
	sort.Slice(info.Files, func(i, j int) bool { return info.Files[i].Path < info.Files[j].Path })

	idx := base
	if len(replace) > 0 {
		idx = scip.PatchIndex(base, replace)
	}
	info.BuildTime = time.Since(start)
	info.BuildMs = float64(info.BuildTime.Microseconds()) / 1000
	return idx, info
}

// indexedExtensions is the set of file extensions the base index has
// documents for: a new file with one of them is one the indexer would have
// indexed, so the overlay not covering it is worth saying.
func indexedExtensions(base *scip.SCIPIndex) map[string]bool {
	out := map[string]bool{}
	for _, d := range base.Documents {
		if e := path.Ext(d.RelativePath); e != "" {
			out[e] = true
		}
	}
	return out
}
