package query

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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
	// OpenSites are the untouched uses likely to break, production code first, at most
	// MaxOpenSites of them.
	OpenSites []OpenSite `json:"openSites,omitempty"`
	// OpenCount counts every untouched use: OpenSites before the cut, and RarelyCount.
	OpenCount int `json:"openCount"`
	// UpdatedCount is the uses the diff touched.
	UpdatedCount int `json:"updatedCount"`
	// RarelyCount is how many untouched uses only name a changed type in an annotation
	// or read a key whose type changed. Those rarely break, so they aren't in OpenSites:
	// Rarely counts them per symbol, and RarelySites has each one, uncut.
	RarelyCount int           `json:"rarelyCount,omitempty"`
	Rarely      []RarelyGroup `json:"rarely,omitempty"`
	RarelySites []OpenSite    `json:"rarelySites,omitempty"`
	// Truncated says OpenSites was cut at MaxOpenSites.
	Truncated bool   `json:"truncated,omitempty"`
	Note      string `json:"note,omitempty"`
}

// RarelyGroup counts the uses of one changed symbol or key that rarely break.
type RarelyGroup struct {
	Symbol string `json:"symbol"`
	Change string `json:"change"`
	Via    string `json:"via,omitempty"`
	Count  int    `json:"count"`
	Files  int    `json:"files"`
	// Uses counts them by use: annotation, read.
	Uses map[string]int `json:"uses"`
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
	// Via says why a use counts when the symbol itself isn't what changed: its signature
	// names a changed type, say, or a macro builds it from changed fields.
	Via string `json:"via,omitempty"`
	// Unverified means the index no longer matches the file, so the line may be off.
	Unverified bool `json:"unverified,omitempty"`
	// Use says how a TypeScript line uses a changed type or object key: key, index,
	// literal, switch, annotation or read. The sites are ordered by it, the first
	// being the ones a change is likely to break. Empty for any other site.
	Use string `json:"use,omitempty"`

	// literalLines is how many lines past Line a literal site's braces reach; zero
	// for any other site.
	literalLines int
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
	unreadable []string // Changed symbols whose declaration couldn't be compared, as file:name
	unindexed  []string // Changed code files the index doesn't cover, so nothing in them was checked
	modulePath []string // go.mod files whose module line changed: every import of it did too
	// dependencies are go.mod changes that put other code behind an import: a major
	// version, a dropped require, a replace.
	dependencies []string
	compared     []comparedSpan   // Type bodies read on both sides, which explain lines no symbol names
	shapes       []ContractSignal // Types read off a value whose keys or members changed
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
		if changesGoModulePath(file) {
			c.modulePath = append(c.modulePath, cmp.Or(file.NewPath, file.OldPath))
		}
		c.dependencies = append(c.dependencies, goModDoubts(file)...)
	}
	return c
}

// changesGoModulePath says whether a diff changes a go.mod's module line, or removes
// it with the file. A new go.mod starts a module: nothing imported it yet.
func changesGoModulePath(file *impact.ChangedFile) bool {
	if file.IsNew || filepath.Base(cmp.Or(file.NewPath, file.OldPath)) != "go.mod" {
		return false
	}
	isModuleLine := func(text []string, start, line int) bool {
		i := line - start
		return i >= 0 && i < len(text) && strings.HasPrefix(strings.TrimSpace(text[i]), "module ")
	}
	for i := range file.Hunks {
		hunk := &file.Hunks[i]
		for _, line := range hunk.Removed {
			if isModuleLine(hunk.OldText, hunk.OldStart, line) {
				return true
			}
		}
		for _, line := range hunk.Added {
			if isModuleLine(hunk.NewText, hunk.NewStart, line) {
				return true
			}
		}
	}
	return false
}

var (
	goModRequireRe = regexp.MustCompile(`^(?:require\s+)?(\S+)\s+(v\d\S*)\s*(//.*)?$`)
	goModReplaceRe = regexp.MustCompile(`^(?:replace\s+)?(\S+)(?:\s+v\S+)?\s+=>\s+\S+`)
	goModMajorRe   = regexp.MustCompile(`/v(\d+)$`)
)

// goModDoubts names what a go.mod change does to the code behind an import, where the
// check can't follow: a require whose major version changed, with v0 to v1 and the /vN
// of the path counted, a require that is gone, and a replace that came, went or points
// elsewhere. A minor or patch bump promises compatibility, an indirect require isn't
// imported here, and a new require has no uses yet: none of those is a doubt.
func goModDoubts(file *impact.ChangedFile) []string {
	path := cmp.Or(file.NewPath, file.OldPath)
	if file.IsNew || file.Deleted || filepath.Base(path) != "go.mod" {
		return nil
	}
	type require struct{ major, version string }
	before, after := make(map[string]require), make(map[string]require)
	replaced := make(map[string]bool)
	read := func(text []string, start int, lines []int, into map[string]require) {
		for _, line := range lines {
			i := line - start
			if i < 0 || i >= len(text) {
				continue
			}
			entry := strings.TrimSpace(text[i])
			if m := goModReplaceRe.FindStringSubmatch(entry); m != nil {
				replaced[m[1]] = true
				continue
			}
			m := goModRequireRe.FindStringSubmatch(entry)
			if m == nil || strings.Contains(m[3], "indirect") {
				continue
			}
			module, major := m[1], "0"
			if v := goModMajorRe.FindStringSubmatch(module); v != nil {
				module, major = strings.TrimSuffix(module, v[0]), v[1]
			} else if strings.HasPrefix(m[2], "v1.") {
				major = "1"
			}
			into[module] = require{major: major, version: m[2]}
		}
	}
	for i := range file.Hunks {
		hunk := &file.Hunks[i]
		read(hunk.OldText, hunk.OldStart, hunk.Removed, before)
		read(hunk.NewText, hunk.NewStart, hunk.Added, after)
	}

	var doubts []string
	for _, module := range slices.Sorted(maps.Keys(before)) {
		was := before[module]
		switch now, kept := after[module]; {
		case !kept:
			doubts = append(doubts, fmt.Sprintf("%s no longer requires %s", path, module))
		case now.major != was.major:
			doubts = append(doubts, fmt.Sprintf("%s changes the major version of %s (%s to %s)", path, module, was.version, now.version))
		}
	}
	for _, module := range slices.Sorted(maps.Keys(replaced)) {
		doubts = append(doubts, fmt.Sprintf("%s changes the replace of %s", path, module))
	}
	return doubts
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
	openFiles := make(map[string]bool)
	name := leafName(sym.Name)
	if class := scip.ConstructorType(sym.SymbolID); class != "" {
		name = class // `new Foo(x)` is the use of Foo's constructor
	}
	ok := c.checkUses(ctx, adapter, sym, useQuery{name: name, change: change}, openFiles)
	return openFiles, ok
}

// useQuery says how to read the uses of a symbol. name is what a use spells out.
type useQuery struct {
	name    string
	change  impact.DeclarationChange
	via     string
	literal bool   // Only literals count: Rust's without `..`, Go's and TypeScript's that build the type
	lang    string // The reading of a literal; empty is Rust
	// goBroken, for a Go struct, holds the fields a keyed literal can't set as before.
	// Non-nil means only literals the compiler rejects count: positional ones, and keyed
	// ones setting one of these.
	goBroken map[string]bool
	// derived is set for a TypeScript type read off a changed value with typeof. A
	// Record keyed by it is a literal of it too, and with key, a literal that sets the
	// key is listed there.
	derived bool
	keyVia  map[string]string // Key to the reason a literal setting it counts
	// recordOnly leaves the Record keyed by the type as the only literal of it: a
	// union that gained a member breaks nothing else that is built.
	recordOnly bool
	// missing, with keys, narrows a literal to the ones that set none of them: the
	// keys the type now requires. path leads from the literal to the one that has to
	// have them, through the keys that hold it.
	missing *missingKeys
	// readsOnly skips a key where an object literal sets it: what was written still
	// fits a type that got wider.
	readsOnly bool
}

// missingKeys is what a literal has to set since the change, and where in it.
type missingKeys struct {
	path, keys []string
}

// checkUses classifies a symbol's references into openFiles. False means they couldn't
// be looked up.
func (c *doneChecker) checkUses(ctx context.Context, adapter referenceFinder, sym impact.ChangedSymbol, query useQuery, openFiles map[string]bool) bool {
	if adapter == nil || query.name == "" {
		c.failed++
		return false
	}
	refs, err := adapter.FindReferences(ctx, sym.SymbolID, backends.RefOptions{
		MaxResults:   maxImpactReferences,
		IncludeTests: true,
	})
	if err != nil || refs == nil {
		c.failed++
		return false
	}
	if refs.TotalReferences > len(refs.References) {
		c.cut = true
	}
	for i := range refs.References {
		c.checkReference(&refs.References[i], sym, query, openFiles)
	}
	return true
}

// referenceFinder is the part of the SCIP adapter the check uses.
type referenceFinder interface {
	FindReferences(ctx context.Context, symbolID string, opts backends.RefOptions) (*backends.ReferencesResult, error)
}

func (c *doneChecker) checkReference(ref *backends.Reference, sym impact.ChangedSymbol, query useQuery, openFiles map[string]bool) {
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

	literalLines := 0
	if query.literal {
		lines := newLines
		if oldLines != nil {
			lines = oldLines
		}
		switch {
		case query.recordOnly:
			end, ok := diff.TSRecordLiteral(lines, ref.Location.Line, ref.Location.Column, query.name)
			if !ok {
				return
			}
			literalLines = end - ref.Location.Line
		case diff.IsLiteralUse(query.lang, lines, ref.Location.Line, ref.Location.Column, query.name):
			literalLines = diff.LiteralEnd(query.lang, lines, ref.Location.Line, ref.Location.Column, query.name) - ref.Location.Line
		case query.derived:
			end, ok := diff.TSRecordLiteral(lines, ref.Location.Line, ref.Location.Column, query.name)
			if !ok {
				return
			}
			literalLines = end - ref.Location.Line
		default:
			return
		}
		if query.goBroken != nil && !breaksGoLiteral(lines, ref, query) {
			return
		}
	}

	result := diff.ClassifyUse(file, oldLines, newLines, diff.UseSite{
		Line:    ref.Location.Line,
		Column:  ref.Location.Column,
		Name:    query.name,
		Literal: query.literal,
		Lang:    query.lang,
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
	if (result.Import || ref.Kind == "import") && query.change != impact.DeclRemoved {
		return
	}

	if query.readsOnly && diff.TSIsPropertyWrite(newLines, result.Line, query.name) {
		return
	}
	// Only the literals that lack what the type now requires are open, each at its
	// own line. One that can't be read stays listed as it is.
	var missingAt []int
	if query.missing != nil {
		at, read := diff.TSLiteralsMissing(newLines, result.Line, 0, query.name, query.missing.path, query.missing.keys)
		if read && len(at) == 0 {
			return
		}
		missingAt = at
	}

	openFiles[path] = true
	if file != nil && file.NewPath != "" {
		path = file.NewPath // A use in a renamed file is reported where it is now
	}
	// Classified where the reference is: a site listed at a key further down is
	// still the literal found here.
	use := tsUse(sym, query, cmp.Or(ref.Location.Path, path), newLines, result.Line, len(missingAt) > 0)
	add := func(line int, code, via string, literalLines int) {
		key := fmt.Sprintf("%s:%s:%d", sym.SymbolID, path, line)
		if c.seen[key] {
			return
		}
		c.seen[key] = true
		c.sites = append(c.sites, OpenSite{
			Use:        use,
			Symbol:     sym.Name,
			Change:     string(query.change),
			File:       path,
			Line:       line,
			Code:       code,
			Consumer:   ref.FromSymbolName,
			IsTest:     isTestFilePath(path),
			Unverified: result.Unverified,
			Via:        via,

			literalLines: literalLines,
		})
	}
	if len(missingAt) > 0 {
		for _, at := range missingAt {
			add(at, strings.TrimSpace(newLines[at-1]), query.via, 0)
		}
		return
	}
	// A literal that holds the changed type under a key is listed at that key: that
	// is the place to look at.
	atKey := false
	for _, name := range sortedKeys(query.keyVia) {
		if at := diff.TSLiteralKeyLine(newLines, result.Line, result.Line+literalLines, name); at > 0 {
			add(at, strings.TrimSpace(newLines[at-1]), query.keyVia[name], 0)
			atKey = true
		}
	}
	if !atKey {
		add(result.Line, result.Code, query.via, literalLines)
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// groupRarely counts the sites that rarely break per symbol, change and reason, in the
// order they first appear.
func groupRarely(sites []OpenSite) []RarelyGroup {
	var groups []RarelyGroup
	at := make(map[[3]string]int)
	files := make(map[[3]string]map[string]bool)
	for _, s := range sites {
		key := [3]string{s.Symbol, s.Change, s.Via}
		i, ok := at[key]
		if !ok {
			i = len(groups)
			at[key] = i
			files[key] = make(map[string]bool)
			groups = append(groups, RarelyGroup{Symbol: s.Symbol, Change: s.Change, Via: s.Via, Uses: make(map[string]int)})
		}
		groups[i].Count++
		groups[i].Uses[s.Use]++
		files[key][s.File] = true
		groups[i].Files = len(files[key])
	}
	return groups
}

// rankRarely is the rank of a use that rarely breaks.
const rankRarely = 4

// useRank orders the sites: what a change is likely to break first. A site without a
// use, which is every site outside TypeScript types and keys, is as likely as any.
func useRank(use string) int {
	switch use {
	case diff.TSUseLiteral:
		return 2
	case diff.TSUseSwitch:
		return 3
	case diff.TSUseAnnotation, diff.TSUseRead:
		return rankRarely
	}
	return 1
}

// tsUse classifies an open site by how it uses a changed TypeScript type or object
// key. Empty for anything else: a call of a changed function, a use of something that
// is gone, a literal known to lack a key, a site in another language.
func tsUse(sym impact.ChangedSymbol, query useQuery, path string, lines []string, line int, exact bool) string {
	if diff.TypeLanguage(path) != "typescript" || query.change == impact.DeclRemoved || exact {
		return ""
	}
	isKey := strings.HasSuffix(sym.SymbolID, ":")
	if !isKey && !strings.HasSuffix(sym.SymbolID, "#") {
		return ""
	}
	return diff.TSUseKind(lines, line, query.name, isKey)
}

// breaksGoLiteral says whether a Go composite literal stops compiling: it is
// positional, or sets a field in goBroken. A literal that can't be read counts.
func breaksGoLiteral(lines []string, ref *backends.Reference, query useQuery) bool {
	keys, positional, ok := diff.GoLiteralKeys(lines, ref.Location.Line, ref.Location.Column, query.name)
	if !ok || positional {
		return true
	}
	for _, key := range keys {
		if query.goBroken[key] {
			return true
		}
	}
	return false
}

// dropCoveredLiterals removes a literal site whose braces already hold an open use
// of a changed field: the field's site says what to fix there, and the literal's
// would count the same edit twice.
func dropCoveredLiterals(sites []OpenSite) []OpenSite {
	kept := sites[:0]
	for i := range sites {
		if sites[i].literalLines > 0 || isLiteralVia(sites[i].Via) {
			if coversUse(sites, i) {
				continue
			}
		}
		kept = append(kept, sites[i])
	}
	return kept
}

func isLiteralVia(via string) bool {
	return strings.HasPrefix(via, "fields of ")
}

func coversUse(sites []OpenSite, lit int) bool {
	l := &sites[lit]
	for j := range sites {
		s := &sites[j]
		// A use that rarely breaks is only counted, not listed, so it can't stand in
		// for the literal: `Record<Changed, Other> = {` names Other on the literal's line.
		if j == lit || s.File != l.File || isLiteralVia(s.Via) || useRank(s.Use) == rankRarely {
			continue
		}
		if s.Line >= l.Line && s.Line <= l.Line+l.literalLines {
			return true
		}
	}
	return false
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

// unreadableName names a changed symbol in a note: file:name, and for a file-level
// entry, whose name is its path, the path once.
func unreadableName(sym impact.ChangedSymbol) string {
	if strings.HasPrefix(sym.SymbolID, "file:") || sym.Name == sym.File {
		return sym.File
	}
	return sym.File + ":" + sym.Name
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
	c.sites = dropCoveredLiterals(c.sites)
	check := &DoneCheck{
		Diff:         hex.EncodeToString(sum[:])[:12],
		OpenCount:    len(c.sites),
		UpdatedCount: c.updated,
	}

	sort.SliceStable(c.sites, func(i, j int) bool {
		a, b := &c.sites[i], &c.sites[j]
		if ra, rb := useRank(a.Use), useRank(b.Use); ra != rb {
			return ra < rb
		}
		if a.IsTest != b.IsTest {
			return !a.IsTest
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	// The sort puts the rare ones last: everything from the first of them on.
	likely := len(c.sites)
	for likely > 0 && useRank(c.sites[likely-1].Use) == rankRarely {
		likely--
	}
	check.OpenSites = c.sites[:likely]
	if rarely := c.sites[likely:]; len(rarely) > 0 {
		check.RarelySites = rarely
		check.RarelyCount = len(rarely)
		check.Rarely = groupRarely(rarely)
	}
	if len(check.OpenSites) > MaxOpenSites {
		check.OpenSites = check.OpenSites[:MaxOpenSites]
		check.Truncated = true
	}

	var doubts []string
	if indexDoubt != "" {
		doubts = append(doubts, indexDoubt)
	}
	if len(c.modulePath) > 0 {
		doubts = append(doubts, fmt.Sprintf("the module path in %s changed, and the imports of it aren't checked", namedFiles(c.modulePath)))
	}
	if len(c.dependencies) > 0 {
		doubts = append(doubts, strings.Join(c.dependencies, ", ")+", and what uses it isn't checked")
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
	if len(c.unreadable) > 0 {
		doubts = append(doubts, fmt.Sprintf("%d changed symbol(s) have a declaration that can't be compared here (%s)", len(c.unreadable), namedFiles(c.unreadable)))
	}
	doubt := strings.Join(doubts, "; ")

	switch {
	case nothingChanged:
		check.Status = DoneCheckClear
		check.Note = NothingToAssess
	case len(c.sites) > 0:
		check.Status = DoneCheckOpen
		check.Note = "Uses of a changed declaration the diff didn't touch. Text comparison, nothing compiled: some may need no edit."
		if check.RarelyCount > 0 {
			check.Note += fmt.Sprintf(" %d of them only annotate with a changed type or read a changed key, which rarely breaks: they are counted per symbol, not listed.", check.RarelyCount)
		}
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
