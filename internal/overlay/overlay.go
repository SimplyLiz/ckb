package overlay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/diff"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
	"github.com/SimplyLiz/CodeMCP/internal/index"
)

// File states in Info.
const (
	StateRemapped  = "remapped"  // Layer 0: occurrences moved, changed lines dropped
	StateDeleted   = "deleted"   // document removed
	StateRenamed   = "renamed"   // document moved to the new path and remapped
	StateUncovered = "uncovered" // changed, but the overlay has nothing for it (a new file)
	StateNew       = "new"       // a new file the overlay synthesized a document for (Layer 1)
)

// FileInfo is what the overlay did for one changed file.
type FileInfo struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	State   string `json:"state"`
	Layers  []int  `json:"layers,omitempty"`
	Dropped int    `json:"droppedOccurrences,omitempty"`
	// Declared counts the declarations Layer 1 added on changed lines.
	Declared int `json:"declared,omitempty"`
	// Referenced counts the references Layer 2 resolved on changed lines;
	// Unresolved the sites it could not resolve and left without an edge.
	Referenced int `json:"referenced,omitempty"`
	Unresolved int `json:"unresolved,omitempty"`
	// Fallback says why a file stayed at Layer 0 (it does not parse).
	Fallback string `json:"fallback,omitempty"`
}

// Rebuild thresholds, from the replay over CKB's history
// (docs/plans/index-overlay.md): up to 20 unresolved sites a commit kept the
// impact items' recall at 94 % or more; from 25 on it fell below 95 % in 10 of
// 14 commits. Precision stayed at 100 % throughout, so the threshold is about
// what the overlay misses.
const UnresolvedRebuildThreshold = 25

// Uncertainty is what the overlay knows it could not do. Any of it makes a
// background rebuild worthwhile (RebuildReason); a dirty working tree alone
// does not.
type Uncertainty struct {
	// UnresolvedSites sums the sites on changed lines left without an edge.
	UnresolvedSites int `json:"unresolvedSites"`
	// ChangedSignatures are declarations whose declaration line changed (a
	// new signature, a rename, a removal) and that unchanged files use: those
	// uses may no longer fit, which only the indexer can tell.
	ChangedSignatures []string `json:"changedSignatures,omitempty"`
	// Layer0Only are changed files with added lines the overlay could only
	// remap: a parse error, or a language without declaration and reference
	// layers. What is on their changed lines is missing.
	Layer0Only []string `json:"layer0Only,omitempty"`
	// Uncovered are new files the overlay has no document for.
	Uncovered []string `json:"uncovered,omitempty"`
}

// RebuildReason says why the base index should be rebuilt, or "" when the
// overlay covers the changes well enough.
func (in *Info) RebuildReason() string {
	if in == nil {
		return ""
	}
	u := in.Uncertainty
	switch {
	case len(u.ChangedSignatures) > 0:
		return fmt.Sprintf("%d changed declaration(s) used by unchanged files, e.g. %s", len(u.ChangedSignatures), shortSymbol(u.ChangedSignatures[0]))
	case len(u.Layer0Only) > 0:
		return fmt.Sprintf("%d changed file(s) the overlay can only remap, e.g. %s", len(u.Layer0Only), u.Layer0Only[0])
	case len(u.Uncovered) > 0:
		return fmt.Sprintf("%d new file(s) not in the index, e.g. %s", len(u.Uncovered), u.Uncovered[0])
	case u.UnresolvedSites >= UnresolvedRebuildThreshold:
		return fmt.Sprintf("%d unresolved sites on changed lines (threshold %d)", u.UnresolvedSites, UnresolvedRebuildThreshold)
	}
	return ""
}

// shortSymbol is a symbol's last descriptors, for messages.
func shortSymbol(sym string) string {
	if i := strings.LastIndex(sym, "`/"); i >= 0 {
		return sym[i+2:]
	}
	if i := strings.LastIndex(sym, "/"); i >= 0 && i+1 < len(sym) {
		return sym[i+1:]
	}
	return sym
}

// Info describes an overlay: which changed files it covers and how.
type Info struct {
	// Uncertainty is what the overlay could not do (see RebuildReason).
	Uncertainty   Uncertainty   `json:"uncertainty"`
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

// UnresolvedSites sums the sites Layer 2 left without an edge.
func (in *Info) UnresolvedSites() int {
	if in == nil {
		return 0
	}
	n := 0
	for _, f := range in.Files {
		n += f.Unresolved
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
	// Root is the repository the files are read from (Layer 1 parses them).
	Root      string
	Head      string
	Diff      *impact.ParsedDiff
	Untracked []string
	// Key identifies this state: HEAD, the diff text and the untracked
	// files. Two equal keys give the same overlay.
	Key string
}

// LoadChanges reads the working tree's changes since the index was built:
// since snapshotTree when the index was built from a dirty working tree
// (index.SnapshotWorktree; untracked source files are part of the diff then),
// else since commit: tracked files (committed since, staged or not) as a
// zero-context diff with renames, and untracked files by name.
func LoadChanges(ctx context.Context, repoRoot, commit, snapshotTree string) (*Changes, error) {
	if snapshotTree != "" {
		return loadChangesSinceSnapshot(ctx, repoRoot, snapshotTree)
	}
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
		Root: repoRoot,
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

func loadChangesSinceSnapshot(ctx context.Context, repoRoot, tree string) (*Changes, error) {
	head, _ := git(ctx, repoRoot, "rev-parse", "HEAD")
	text, err := index.DiffSinceSnapshot(ctx, repoRoot, tree)
	if err != nil {
		return nil, err
	}
	parsed, err := diff.ParseGitDiff(string(text))
	if err != nil {
		return nil, fmt.Errorf("parse diff: %w", err)
	}
	h := sha256.New()
	h.Write([]byte("snapshot\x00" + tree + "\x00"))
	h.Write(head)
	h.Write([]byte{0})
	h.Write(text)
	return &Changes{Root: repoRoot, Head: strings.TrimSpace(string(head)), Diff: parsed, Key: hex.EncodeToString(h.Sum(nil))}, nil
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
	var jobs []*goJob
	var droppedDefs []string

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
				if fi, job := newFile(f.NewPath, base, ch.Root, replace); fi.Path != "" {
					info.Files = append(info.Files, fi)
					jobs = appendJob(jobs, job)
				}
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
			if excludedGoFile(ch.Root, f.NewPath) {
				// A build constraint now leaves it out: scip-go would not
				// index it, so its symbols leave the index like a deleted
				// file's, and uses elsewhere count as changed declarations.
				replace[oldPath] = nil
				info.Files = append(info.Files, FileInfo{Path: oldPath, State: StateDeleted, Layers: []int{0}})
				droppedDefs = append(droppedDefs, definedSymbols(doc)...)
				continue
			}
			lm := NewLineMap(f.Hunks)
			nd, st := RemapDocument(doc, lm)
			pinTSFileSymbol(nd, doc)
			fi := FileInfo{Path: f.NewPath, State: StateRemapped, Layers: []int{0}, Dropped: st.Dropped}
			jobs = appendJob(jobs, layer1(&fi, nd, base, doc, oldPath, ch.Root, lm))
			droppedDefs = append(droppedDefs, st.DroppedDefs...)
			if len(fi.Layers) == 1 && (lm.HasAdditions() || f.Renamed && isTSPath(f.NewPath)) {
				info.Uncertainty.Layer0Only = append(info.Uncertainty.Layer0Only, f.NewPath)
			}
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
			if fi, job := newFile(p, base, ch.Root, replace); fi.Path != "" {
				info.Files = append(info.Files, fi)
				jobs = appendJob(jobs, job)
			}
		}
	}
	layer2(info, jobs, base, ch.Root, replace)
	sort.Slice(info.Files, func(i, j int) bool { return info.Files[i].Path < info.Files[j].Path })
	info.Uncertainty.UnresolvedSites = info.UnresolvedSites()
	for _, fi := range info.Files {
		if fi.State == StateUncovered {
			info.Uncertainty.Uncovered = append(info.Uncertainty.Uncovered, fi.Path)
		}
	}
	info.Uncertainty.ChangedSignatures = usedElsewhere(base, droppedDefs, info.Files)

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

// layer1 adds the declarations on changed lines to a remapped document, for
// the languages that have a Layer 1 (Go). A file that cannot be read or
// parsed stays at Layer 0, and fi says why.
func layer1(fi *FileInfo, doc *scip.Document, base *scip.SCIPIndex, oldDoc *scip.Document, oldPath, root string, lm *LineMap) *goJob {
	if disabledLayer(1) || root == "" {
		return nil
	}
	if isTSPath(fi.Path) && strings.HasPrefix(firstSymbol(oldDoc), "scip-typescript ") {
		if fi.OldPath != "" || oldPath != fi.Path {
			// scip-typescript writes the path into every ID: a rename
			// changes them all, here and wherever they are used.
			fi.Fallback = "renamed: scip-typescript IDs carry the file path"
			return nil
		}
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(fi.Path)))
		if err != nil {
			fi.Fallback = "unreadable: " + err.Error()
			return nil
		}
		n, why := tsLayer1(doc, base, oldDoc, src, lm)
		if why != "" {
			fi.Fallback = why
			return nil
		}
		fi.Layers = append(fi.Layers, 1)
		fi.Declared = n
		return &goJob{path: fi.Path, doc: doc, src: src, lines: addedLines(lm), ts: true}
	}
	if !strings.HasSuffix(fi.Path, ".go") || !strings.HasPrefix(firstSymbol(oldDoc), "scip-go ") {
		return nil
	}
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(fi.Path)))
	if err != nil {
		fi.Fallback = "unreadable: " + err.Error()
		return nil
	}
	n, prefix, ok := goLayer1Prefix(doc, base, oldDoc, oldPath, src, lm)
	if !ok {
		fi.Fallback = "does not parse"
		return nil
	}
	fi.Layers = append(fi.Layers, 1)
	fi.Declared = n
	return &goJob{path: fi.Path, doc: doc, src: src, prefix: prefix, lines: addedLines(lm)}
}

// goJob is a Go file that passed Layer 1, waiting for Layer 2: references
// resolve against every file's Layer 1 declarations, so Layer 2 runs after
// all of them.
type goJob struct {
	path   string
	doc    *scip.Document
	src    []byte
	prefix string
	lines  map[int32]bool // the added lines; nil means every line (a new file)
	ts     bool           // a TypeScript file: Layer 2 for TypeScript
}

func appendJob(jobs []*goJob, j *goJob) []*goJob {
	if j == nil {
		return jobs
	}
	return append(jobs, j)
}

// layer2 resolves the references on the changed lines of every file that
// passed Layer 1.
func layer2(info *Info, jobs []*goJob, base *scip.SCIPIndex, root string, replace map[string]*scip.Document) {
	if disabledLayer(2) || len(jobs) == 0 {
		return
	}
	byPath := map[string]*FileInfo{}
	for i := range info.Files {
		byPath[info.Files[i].Path] = &info.Files[i]
	}
	var goJobs []*goJob
	var ts *tsResolver
	for _, j := range jobs {
		if !j.ts {
			goJobs = append(goJobs, j)
			continue
		}
		if ts == nil {
			ts = newTSResolver(root, func(p string) *scip.Document {
				if d, ok := replace[p]; ok {
					return d
				}
				return base.DocumentsByPath[p]
			})
		}
		lines := j.lines
		if lines == nil {
			lines = allLines(j.src)
		}
		added, unresolved := tsLayer2(j.doc, j.path, j.src, lines, ts)
		if fi := byPath[j.path]; fi != nil {
			fi.Layers = append(fi.Layers, 2)
			fi.Referenced = added
			fi.Unresolved = unresolved
		}
		sortOccurrences(j.doc.Occurrences)
	}
	jobs = goJobs
	if len(jobs) == 0 {
		return
	}
	pre := goPackagePrefixes(base)
	g := &goSymbols{base: base, prefixes: pre.byPath, names: pre.names, declared: map[string]bool{}}
	for _, j := range jobs {
		for _, o := range j.doc.Occurrences {
			if o.SymbolRoles&scip.SymbolRoleDefinition != 0 {
				g.declared[o.Symbol] = true
			}
		}
	}
	types := newGoTypes(g, root)
	for _, j := range jobs {
		types.noteDeclared(j.doc)
	}
	for _, j := range jobs {
		lines := j.lines
		if lines == nil {
			lines = allLines(j.src)
		}
		added, unresolved := goLayer2Typed(j.doc, j.src, j.prefix, lines, g, types)
		if fi := byPath[j.path]; fi != nil {
			fi.Layers = append(fi.Layers, 2)
			fi.Referenced = added
			fi.Unresolved = unresolved
		}
	}
}

func allLines(src []byte) map[int32]bool {
	n := int32(strings.Count(string(src), "\n") + 1) // #nosec G115 -- line count fits
	out := make(map[int32]bool, n)
	for i := int32(0); i < n; i++ {
		out[i] = true
	}
	return out
}

// firstSymbol is a document's first non-local symbol, to tell its indexer.
func firstSymbol(d *scip.Document) string {
	if d == nil {
		return ""
	}
	for _, o := range d.Occurrences {
		if o.Symbol != "" && !strings.HasPrefix(o.Symbol, "local ") {
			return o.Symbol
		}
	}
	return ""
}

// pinTSFileSymbol keeps the definition scip-typescript writes for the file
// itself at [0,0,0]: it is not on a line, so Layer 0 must neither move it
// when lines are inserted above the first one nor drop it when the first
// line changes. Its EnclosingRange (the whole file) Layer 1 sets.
func pinTSFileSymbol(nd, old *scip.Document) {
	sym := tsFileSymbolOf(old)
	if sym == "" {
		return
	}
	for _, o := range nd.Occurrences {
		if o.Symbol == sym && o.SymbolRoles&scip.SymbolRoleDefinition != 0 {
			o.Range = []int32{0, 0, 0}
			sortOccurrences(nd.Occurrences)
			return
		}
	}
	for _, o := range old.Occurrences {
		if o.Symbol == sym && o.SymbolRoles&scip.SymbolRoleDefinition != 0 {
			cp := *o
			nd.Occurrences = append(nd.Occurrences, &cp)
			sortOccurrences(nd.Occurrences)
			return
		}
	}
}

// tsFileSymbolOf is the scip-typescript file symbol a document defines at
// [0,0,0], language-independent so Layer 0 can use it without cgo.
func tsFileSymbolOf(d *scip.Document) string {
	if d == nil {
		return ""
	}
	for _, o := range d.Occurrences {
		if o.SymbolRoles&scip.SymbolRoleDefinition != 0 && len(o.Range) == 3 && o.Range[0] == 0 && o.Range[1] == 0 && o.Range[2] == 0 &&
			strings.HasPrefix(o.Symbol, "scip-typescript ") {
			return o.Symbol
		}
	}
	return ""
}

// isTSPath reports whether p is a file scip-typescript indexes with the
// TypeScript layers (TypeScript and TSX; JavaScript stays at Layer 0).
func isTSPath(p string) bool {
	return strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".tsx") || strings.HasSuffix(p, ".mts") || strings.HasSuffix(p, ".cts")
}

// disabledLayer reports whether CKB_OVERLAY_LAYERS leaves layer n out
// ("0" = Layer 0 only, "0,1" = without Layer 2). For the replay
// measurement; unset means all layers.
func disabledLayer(n int) bool {
	v := os.Getenv("CKB_OVERLAY_LAYERS")
	if v == "" {
		return false
	}
	for _, f := range strings.Split(v, ",") {
		if strings.TrimSpace(f) == fmt.Sprint(n) {
			return false
		}
	}
	return true
}

// newFile handles a file the base index has no document for: Layer 1
// synthesizes one when it can (a Go file in a package the index has), else
// the file is uncovered.
func newFile(p string, base *scip.SCIPIndex, root string, replace map[string]*scip.Document) (FileInfo, *goJob) {
	fi := FileInfo{Path: p, State: StateUncovered}
	if disabledLayer(1) || root == "" || base.DocumentsByPath[p] != nil {
		return fi, nil
	}
	if isTSPath(p) {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p))) // #nosec G304 -- a file in the repository
		if err != nil {
			return fi, nil
		}
		doc, _ := tsNewDocument(base, p, src)
		if doc == nil {
			fi.Fallback = "does not parse, or no indexed file of its package"
			return fi, nil
		}
		replace[p] = doc
		fi.State = StateNew
		fi.Layers = []int{1}
		fi.Declared = len(doc.Symbols) - 1 // without the file symbol
		return fi, &goJob{path: p, doc: doc, src: src, ts: true}
	}
	if !strings.HasSuffix(p, ".go") {
		return fi, nil
	}
	abs := filepath.Join(root, filepath.FromSlash(p))
	if !goBuilds(abs) {
		return FileInfo{}, nil // excluded by a build constraint: not indexed, nothing to cover
	}
	src, err := os.ReadFile(abs)
	if err != nil {
		return fi, nil
	}
	doc, prefix := goNewDocument(base, root, p, src)
	if doc == nil {
		fi.Fallback = "does not parse, or a package the index does not have"
		return fi, nil
	}
	replace[p] = doc
	fi.State = StateNew
	fi.Layers = []int{1}
	fi.Declared = len(doc.Symbols)
	return fi, &goJob{path: p, doc: doc, src: src, prefix: prefix}
}

// excludedGoFile reports whether the Go file at p (repo-relative) exists and
// a build constraint excludes it under the default context.
func excludedGoFile(root, p string) bool {
	if root == "" || !strings.HasSuffix(p, ".go") {
		return false
	}
	abs := filepath.Join(root, filepath.FromSlash(p))
	if _, err := os.Stat(abs); err != nil {
		return false
	}
	return !goBuilds(abs)
}

// definedSymbols lists the non-local symbols a document defines.
func definedSymbols(doc *scip.Document) []string {
	var out []string
	for _, o := range doc.Occurrences {
		if o.SymbolRoles&scip.SymbolRoleDefinition != 0 && !scip.IsLocalSymbol(o.Symbol) {
			out = append(out, o.Symbol)
		}
	}
	return out
}

// usedElsewhere keeps the symbols the base index has uses of outside the
// changed files.
func usedElsewhere(base *scip.SCIPIndex, syms []string, files []FileInfo) []string {
	changed := make(map[string]bool, len(files)*2)
	for _, f := range files {
		changed[f.Path] = true
		if f.OldPath != "" {
			changed[f.OldPath] = true
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, sym := range syms {
		if seen[sym] || !scip.IsChangeableDefinition(sym) {
			continue
		}
		seen[sym] = true
		for _, r := range base.RefIndex[sym] {
			if r.Occ.SymbolRoles&scip.SymbolRoleDefinition == 0 && !changed[r.Doc.RelativePath] {
				out = append(out, sym)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
