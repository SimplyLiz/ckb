package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/diff"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
	"github.com/SimplyLiz/CodeMCP/internal/index"
)

// Done check statuses. "clear" is deliberately not "safe": it covers one question.
const (
	DoneCheckOpen        = "open"         // A use of a changed declaration wasn't touched
	DoneCheckClear       = "clear"        // None found, and the lookup could have found them
	DoneCheckNotAssessed = "not_assessed" // None found, but the lookup can't vouch for that
)

// MaxOpenSites caps the sites listed; DoneCheck.OpenCount stays the real count.
const MaxOpenSites = 100

// maxDoneCheckFiles bounds the files read per check, on either side of the diff.
const maxDoneCheckFiles = 500

// DoneCheck answers what an agent has to know before calling a change finished: which
// uses of a declaration it changed did it leave as they were? It works per use, not
// per file, so a file the diff touched elsewhere doesn't count as handled.
//
// It compares text and compiles nothing. A touched use may still be wrong, and an
// untouched one may be fine, as with a change the language accepts at the call site.
type DoneCheck struct {
	Status string `json:"status"`
	// Diff identifies the exact diff this describes. Any edit changes it, so a result
	// from before the last edit can be told from a current one.
	Diff string `json:"diff"`
	// OpenSites are the untouched uses, production code first.
	OpenSites []OpenSite `json:"openSites,omitempty"`
	OpenCount int        `json:"openCount"`
	// UpdatedCount is the uses the diff touched.
	UpdatedCount int    `json:"updatedCount"`
	Truncated    bool   `json:"truncated,omitempty"`
	Note         string `json:"note,omitempty"`
}

// OpenSite is one use of a changed declaration that the diff left as it was.
type OpenSite struct {
	Symbol   string `json:"symbol"`
	Change   string `json:"change"` // changed, removed
	File     string `json:"file"`
	Line     int    `json:"line"`
	Code     string `json:"code,omitempty"`
	Consumer string `json:"consumer,omitempty"`
	IsTest   bool   `json:"isTest,omitempty"`
	// Unverified means the index no longer matches the file, so the line may be off.
	Unverified bool `json:"unverified,omitempty"`
}

// doneChecker collects a change's use sites, one changed symbol at a time.
type doneChecker struct {
	repoRoot string
	staged   bool
	base     string

	files      map[string]*impact.ChangedFile
	oldLines   map[string][]string
	newLines   map[string][]string
	seen       map[string]bool
	sites      []OpenSite
	updated    int
	filesRead  int
	cut        bool     // A reference list or the file limit stopped the check short
	failed     int      // Changed declarations whose uses couldn't be looked up
	unreadable int      // Changed symbols whose declaration couldn't be compared
	unindexed  []string // Changed code files the index doesn't cover, so nothing in them was checked
}

func newDoneChecker(repoRoot string, opts AnalyzeChangeSetOptions, parsed *impact.ParsedDiff) *doneChecker {
	c := &doneChecker{
		repoRoot: repoRoot,
		staged:   opts.Staged,
		base:     opts.BaseBranch,
		files:    make(map[string]*impact.ChangedFile, len(parsed.Files)),
		oldLines: make(map[string][]string),
		newLines: make(map[string][]string),
		seen:     make(map[string]bool),
	}
	for i := range parsed.Files {
		file := &parsed.Files[i]
		if file.OldPath != "" {
			c.files[file.OldPath] = file
		}
		if file.NewPath != "" {
			c.files[file.NewPath] = file
		}
	}
	return c
}

var lastIdentifier = regexp.MustCompile(`[\p{L}_$][\p{L}\p{N}_$]*`)

// leafName is the part of a symbol name a use spells out: bar for Foo.bar().
func leafName(name string) string {
	matches := lastIdentifier.FindAllString(name, -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

// checkSymbol classifies every use of a symbol whose declaration changed or went away.
// It returns the files with an open use, and false if the uses couldn't be looked up.
func (c *doneChecker) checkSymbol(ctx context.Context, adapter referenceFinder, sym impact.ChangedSymbol, change impact.DeclarationChange) (map[string]bool, bool) {
	name := leafName(sym.Name)
	if adapter == nil || name == "" {
		c.failed++
		return nil, false
	}
	refs, err := adapter.FindReferences(ctx, sym.SymbolID, backends.RefOptions{
		MaxResults:   maxImpactReferences,
		IncludeTests: true,
	})
	if err != nil || refs == nil {
		c.failed++
		return nil, false
	}
	if refs.TotalReferences > len(refs.References) {
		c.cut = true
	}

	openFiles := make(map[string]bool)
	for i := range refs.References {
		ref := &refs.References[i]
		c.checkReference(ref, sym, name, change, openFiles)
	}
	return openFiles, true
}

// referenceFinder is the part of the SCIP adapter the check uses.
type referenceFinder interface {
	FindReferences(ctx context.Context, symbolID string, opts backends.RefOptions) (*backends.ReferencesResult, error)
}

func (c *doneChecker) checkReference(ref *backends.Reference, sym impact.ChangedSymbol, name string, change impact.DeclarationChange, openFiles map[string]bool) {
	path := ref.Location.Path
	if path == "" {
		return
	}
	file := c.files[path]
	newLines, ok := c.readNew(file, path)
	if !ok {
		c.cut = true // Past the file limit, or it can't be read: nothing to say about this use
		return
	}
	var oldLines []string
	if file != nil {
		oldLines = c.readOld(file, path)
	}

	result := diff.ClassifyUse(file, oldLines, newLines, diff.UseSite{
		Line:   ref.Location.Line,
		Column: ref.Location.Column,
		Name:   name,
	})
	switch result.State {
	case diff.UseGone:
		return
	case diff.UseUpdated:
		c.updated++
		return
	}
	// An import of a name whose signature changed is still valid. Of a name that went
	// away, it is exactly what has to go too.
	if (result.Import || ref.Kind == "import") && change != impact.DeclRemoved {
		return
	}

	openFiles[path] = true
	if file != nil && file.NewPath != "" {
		path = file.NewPath // A use in a renamed file is reported where it is now
	}
	key := fmt.Sprintf("%s:%s:%d", sym.SymbolID, path, result.Line)
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	c.sites = append(c.sites, OpenSite{
		Symbol:     sym.Name,
		Change:     string(change),
		File:       path,
		Line:       result.Line,
		Code:       result.Code,
		Consumer:   ref.FromSymbolName,
		IsTest:     isTestFilePath(path),
		Unverified: result.Unverified,
	})
}

// readNew returns a file as the diff's new side has it. A file outside the diff is the
// same on both sides.
func (c *doneChecker) readNew(file *impact.ChangedFile, path string) ([]string, bool) {
	if file != nil && file.Deleted {
		return nil, true
	}
	if file != nil && file.NewPath != "" {
		path = file.NewPath
	}
	if lines, ok := c.newLines[path]; ok {
		return lines, lines != nil
	}
	if c.filesRead >= maxDoneCheckFiles {
		return nil, false
	}
	c.filesRead++

	var lines []string
	if c.staged {
		lines = c.gitShow(":" + path)
	} else if content, err := os.ReadFile(filepath.Join(c.repoRoot, path)); err == nil {
		lines = splitFileLines(string(content))
	}
	c.newLines[path] = lines
	return lines, lines != nil
}

// readOld returns a file from before the diff. Without it a use is classified from the
// new side alone, which at worst leaves it open.
func (c *doneChecker) readOld(file *impact.ChangedFile, path string) []string {
	if file.OldPath != "" {
		path = file.OldPath
	}
	if lines, ok := c.oldLines[path]; ok {
		return lines
	}
	if c.filesRead >= maxDoneCheckFiles {
		c.cut = true
		return nil
	}
	c.filesRead++
	base := c.base
	if base == "" {
		base = "HEAD"
	}
	lines := c.gitShow(base + ":" + path)
	c.oldLines[path] = lines
	return lines
}

func (c *doneChecker) gitShow(object string) []string {
	cmd := exec.Command("git", "show", object)
	cmd.Dir = c.repoRoot
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return splitFileLines(string(out))
}

func splitFileLines(content string) []string {
	return strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
}

// namedFiles lists the first few files, so a note can be acted on.
func namedFiles(files []string) string {
	sorted := append([]string{}, files...)
	sort.Strings(sorted)
	if len(sorted) > 3 {
		return fmt.Sprintf("%s and %d more", strings.Join(sorted[:3], ", "), len(sorted)-3)
	}
	return strings.Join(sorted, ", ")
}

// indexDoubt says why the index can't vouch for the uses at HEAD, if it can't. Only
// commits count: uncommitted edits are what the check is there to look at, and the
// index not having them is how it tells them apart. HEAD is read here rather than taken
// from the engine's repo state, which is cached for seconds: long enough for an agent
// to commit and ask again, and that is the answer an agent stops on.
func indexDoubt(repoRoot, indexedCommit string) string {
	if indexedCommit == "" {
		return "the commit the index was built from is unknown, so newer uses may be missing"
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "HEAD can't be read, so the index can't be compared with it"
	}
	head := strings.TrimSpace(string(out))
	if head == indexedCommit {
		return ""
	}
	if behind, err := scip.CountCommitsBetween(repoRoot, indexedCommit, head); err == nil && behind > 0 {
		return fmt.Sprintf("the index is %d commit(s) behind HEAD, so newer uses are missing", behind)
	}
	return "the index was built from a commit other than HEAD, so newer uses may be missing"
}

// result builds the response. The conditions under which "nothing open" can't be
// trusted are all reasons to say not_assessed: a clean result is the one an agent will
// stop on, so it has to be earned.
func (c *doneChecker) result(diffContent string, hasGraph, nothingChanged bool, indexDoubt string) *DoneCheck {
	sum := sha256.Sum256([]byte(diffContent))
	check := &DoneCheck{
		Diff:         hex.EncodeToString(sum[:])[:12],
		OpenCount:    len(c.sites),
		UpdatedCount: c.updated,
	}

	sort.SliceStable(c.sites, func(i, j int) bool {
		a, b := &c.sites[i], &c.sites[j]
		if a.IsTest != b.IsTest {
			return !a.IsTest
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	check.OpenSites = c.sites
	if len(check.OpenSites) > MaxOpenSites {
		check.OpenSites = check.OpenSites[:MaxOpenSites]
		check.Truncated = true
	}

	var doubts []string
	if indexDoubt != "" {
		doubts = append(doubts, indexDoubt)
	}
	if len(c.unindexed) > 0 {
		doubts = append(doubts, fmt.Sprintf("%d changed file(s) aren't in the index (%s)", len(c.unindexed), namedFiles(c.unindexed)))
	}
	if c.cut {
		doubts = append(doubts, "not every use could be looked at")
	}
	if c.failed > 0 {
		doubts = append(doubts, fmt.Sprintf("the uses of %d changed declaration(s) could not be looked up", c.failed))
	}
	if c.unreadable > 0 {
		doubts = append(doubts, fmt.Sprintf("%d changed symbol(s) have a declaration that can't be compared here", c.unreadable))
	}
	doubt := strings.Join(doubts, "; ")

	switch {
	case nothingChanged:
		check.Status = DoneCheckClear
		check.Note = NothingToAssess
	case len(c.sites) > 0:
		check.Status = DoneCheckOpen
		check.Note = "Uses of a changed declaration the diff didn't touch. Text comparison, nothing compiled: some may need no edit."
		if doubt != "" {
			check.Note += " There may be more: " + doubt + "."
		}
	case !hasGraph:
		check.Status = DoneCheckNotAssessed
		check.Note = "No SCIP index, so the uses of what changed are unknown. Run 'ckb index'."
		if index.BuildRunning(filepath.Join(c.repoRoot, ".ckb")) {
			check.Note = "No SCIP index yet, so the uses of what changed are unknown. One is being built: ask again when it is done."
		}
	case doubt != "":
		check.Status = DoneCheckNotAssessed
		check.Note = "No open use found, but " + doubt + "."
	default:
		check.Status = DoneCheckClear
		check.Note = "No use of a changed declaration was left untouched. This is not a proof: nothing was compiled, and behaviour and other repos weren't checked."
	}
	return check
}
