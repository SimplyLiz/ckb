package query

import (
	"cmp"
	"fmt"
	"math"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/diff"
)

// compactTestFiles and compactOpenSites bound the lists a compact assessChange
// shows when the caller sets no limit; TestsTotal and OpenCount still count all.
const (
	compactTestFiles = 20
	compactOpenSites = 50
)

// Focus values for a compact assessChange: which part of the answer the caller
// is after. "" returns every section.
const (
	AssessFocusDone  = "done"  // open sites and co-change partners: is the change finished?
	AssessFocusTests = "tests" // affected tests and the command that runs them
	AssessFocusRisk  = "risk"  // score, what drives it, contract changes, consumers
	// AssessFocusRarely lists the open sites that rarely break, which the other views
	// only count per symbol.
	AssessFocusRarely = "rarely"
)

// CompactAssessOptions selects what a compact assessChange returns.
type CompactAssessOptions struct {
	Focus string
	// Offset and Limit page the focused list: open sites for done, the ones that
	// rarely break for rarely, test files for tests. Limit 0 means the default bound.
	// Without a focus Offset must be 0.
	Offset, Limit int
	// RunCommand runs every affected test; shown with focus tests.
	RunCommand string
}

// CompactAssessChange is the token-budget view of an assessChange result
// (format "compact"): what an agent acts on before calling a change finished,
// with file:line positions and short names instead of symbol ids.
type CompactAssessChange struct {
	Summary string `json:"summary"`
	// Focus echoes the requested focus; sections outside it are left out.
	Focus     string  `json:"focus,omitempty"`
	Risk      string  `json:"risk"`
	RiskScore float64 `json:"riskScore"`
	// RiskFactors say what raised the score (focus risk only).
	RiskFactors []string          `json:"riskFactors,omitempty"`
	DoneCheck   *CompactDoneCheck `json:"doneCheck,omitempty"`
	// CoChangeOpen are files that usually change with the touched ones and didn't.
	CoChangeOpen []string `json:"coChangeOpen,omitempty"`
	// CoChangeLeftOut says how many usual partners sit in generated or data paths
	// and weren't listed.
	CoChangeLeftOut string            `json:"coChangeLeftOut,omitempty"`
	Contracts       []CompactContract `json:"contracts,omitempty"`
	// Tests are the test files that use a changed symbol themselves, or with focus
	// tests one page of all of them. TestsTotal counts all, TestsIndirect those that
	// reach the change through other code and aren't listed.
	Tests         []CompactTest `json:"tests,omitempty"`
	TestsTotal    int           `json:"testsTotal"`
	TestsIndirect int           `json:"testsIndirect,omitempty"`
	// RunTests is a command that runs all affected tests (focus tests only).
	RunTests string `json:"runTests,omitempty"`
	// Consumers are the files referencing a changed symbol themselves.
	Consumers      []string `json:"consumers,omitempty"`
	ConsumersTotal int      `json:"consumersTotal"`
	// Stale says the index doesn't match the code, so positions may be off.
	Stale string `json:"stale,omitempty"`
	// More says how to fetch the rest of a list that was cut.
	More string `json:"more,omitempty"`
}

// CompactDoneCheck is DoneCheck without the per-site symbol ids.
type CompactDoneCheck struct {
	Status    string            `json:"status"`
	Diff      string            `json:"diff,omitempty"`
	OpenCount int               `json:"openCount"`
	OpenSites []CompactOpenSite `json:"openSites,omitempty"`
	// Rarely says, one line per symbol, how many open uses rarely break and aren't in
	// OpenSites; RarelyCount counts them all. With focus rarely OpenSites lists them.
	Rarely      []string `json:"rarely,omitempty"`
	RarelyCount int      `json:"rarelyCount,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
	Note        string   `json:"note,omitempty"`
}

// CompactOpenSite is one untouched use: At is "file:line".
type CompactOpenSite struct {
	At       string `json:"at"`
	Symbol   string `json:"symbol,omitempty"`
	Code     string `json:"code,omitempty"`
	Consumer string `json:"consumer,omitempty"`
	Via      string `json:"via,omitempty"`
	Test     bool   `json:"test,omitempty"`
	Change   string `json:"change,omitempty"` // changed, removed
}

// CompactContract is a possible contract change: its kind and the symbol.
type CompactContract struct {
	Kind   string `json:"kind"`
	Symbol string `json:"symbol"`
	File   string `json:"file,omitempty"`
	// Before and After are the declarations compared, where both could be read.
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	// Detail says what changed where the declaration didn't.
	Detail string `json:"detail,omitempty"`
}

// CompactTest is an affected test file and the test functions seen in it.
type CompactTest struct {
	File  string   `json:"file"`
	Names []string `json:"names,omitempty"`
}

// CompactAssessChangeFrom builds the compact view of an AnalyzeChangeSet response
// with every section and the default bounds.
func CompactAssessChangeFrom(r *AnalyzeChangeSetResponse) *CompactAssessChange {
	c, _ := CompactAssessChangeFor(r, CompactAssessOptions{})
	return c
}

// CompactAssessChangeFor builds the compact view for opts: with a focus only the
// sections that answer it, the focused list paged by Offset and Limit.
func CompactAssessChangeFor(r *AnalyzeChangeSetResponse, opts CompactAssessOptions) (*CompactAssessChange, error) {
	switch opts.Focus {
	case "", AssessFocusDone, AssessFocusRarely, AssessFocusTests, AssessFocusRisk:
	default:
		return nil, fmt.Errorf("focus must be %q, %q, %q or %q", AssessFocusDone, AssessFocusRarely, AssessFocusTests, AssessFocusRisk)
	}
	if opts.Offset < 0 || opts.Limit < 0 {
		return nil, fmt.Errorf("offset and limit must not be negative")
	}
	if opts.Offset > 0 && opts.Focus != AssessFocusDone && opts.Focus != AssessFocusRarely && opts.Focus != AssessFocusTests {
		return nil, fmt.Errorf("offset pages the focused list, so it needs focus %q, %q or %q", AssessFocusDone, AssessFocusRarely, AssessFocusTests)
	}

	c := &CompactAssessChange{Risk: "unknown", Focus: opts.Focus}
	if r == nil {
		c.Summary = "Nothing assessed."
		return c, nil
	}
	want := func(f string) bool { return opts.Focus == "" || opts.Focus == f }

	if r.RiskScore != nil {
		c.Risk = r.RiskScore.Level
		c.RiskScore = math.Round(r.RiskScore.Score*100) / 100 // 0.66, not 0.6599999999999999
	} else if r.Summary != nil && r.Summary.EstimatedRisk != "" {
		c.Risk = r.Summary.EstimatedRisk
	}
	if opts.Focus == AssessFocusRisk && r.RiskScore != nil {
		for _, f := range r.RiskScore.Factors {
			if f.Evidence != "" {
				c.RiskFactors = append(c.RiskFactors, f.Name+": "+f.Evidence)
			} else {
				c.RiskFactors = append(c.RiskFactors, f.Name)
			}
		}
	}

	var more []string
	if d := r.DoneCheck; d != nil && (want(AssessFocusDone) || opts.Focus == AssessFocusRarely) {
		cd := &CompactDoneCheck{Status: d.Status, Diff: d.Diff, OpenCount: d.OpenCount, RarelyCount: d.RarelyCount, Truncated: d.Truncated, Note: d.Note}
		// The rare ones are a list of their own: with focus rarely it is the page, and
		// otherwise a count per symbol, never cut.
		sites, focus := d.OpenSites, AssessFocusDone
		if opts.Focus == AssessFocusRarely {
			sites, focus = d.RarelySites, AssessFocusRarely
		}
		limit, offset := opts.Limit, 0
		if opts.Focus == focus {
			offset = opts.Offset
		}
		if limit == 0 || opts.Focus != focus {
			limit = compactOpenSites
		}
		page, rest := pageRange(len(sites), offset, limit)
		for _, s := range sites[page[0]:page[1]] {
			cd.OpenSites = append(cd.OpenSites, CompactOpenSite{
				At:       fmt.Sprintf("%s:%d", s.File, s.Line),
				Symbol:   cmp.Or(s.QualifiedName, s.Symbol),
				Code:     s.Code,
				Consumer: s.Consumer,
				Via:      s.Via,
				Test:     s.IsTest,
				Change:   s.Change,
			})
		}
		if rest > 0 {
			noun := "open sites"
			if focus == AssessFocusRarely {
				noun = "sites that rarely break"
			}
			more = append(more, fmt.Sprintf("%s %d-%d of %d shown; the next with focus %q, offset %d",
				noun, page[0]+1, page[1], len(sites), focus, page[1]))
		}
		if focus == AssessFocusDone {
			for _, g := range d.Rarely {
				cd.Rarely = append(cd.Rarely, rarelyLine(g))
			}
			if d.Truncated {
				more = append(more, fmt.Sprintf("the check keeps the first %d of %d open sites likely to break; fix those and call again for the rest",
					len(d.OpenSites), d.OpenCount-d.RarelyCount))
			}
		}
		c.DoneCheck = cd
	}

	if cc := r.CoChangeCheck; cc != nil && want(AssessFocusDone) {
		for _, f := range cc.OpenFiles {
			c.CoChangeOpen = append(c.CoChangeOpen, f.File)
		}
		if cc.LeftOut != nil {
			c.CoChangeLeftOut = cc.LeftOut.Note
		}
	}

	if want(AssessFocusRisk) {
		qualified := make(map[[2]string]string, len(r.ChangedSymbols))
		for _, s := range r.ChangedSymbols {
			if s.QualifiedName != "" {
				qualified[[2]string{s.Name, s.File}] = s.QualifiedName
			}
		}
		for _, k := range r.Contracts {
			symbol := cmp.Or(qualified[[2]string{k.Symbol, k.File}], k.Symbol)
			c.Contracts = append(c.Contracts, CompactContract{Kind: k.Kind, Symbol: symbol, File: k.File, Before: k.Before, After: k.After, Detail: k.Detail})
		}
		if cf := r.ConsumerFiles; cf != nil {
			c.Consumers = cf.Direct
			c.ConsumersTotal = len(cf.Direct)
		}
	}

	c.TestsTotal = len(r.AffectedTests)
	if want(AssessFocusTests) {
		limit := opts.Limit
		if limit == 0 || opts.Focus != AssessFocusTests {
			limit = compactTestFiles
		}
		offset := 0
		if opts.Focus == AssessFocusTests {
			offset = opts.Offset
			c.RunTests = opts.RunCommand
		}
		// Without the focus only the files that use a changed symbol themselves are
		// named: those are the ones to run first, and the rest is a number.
		tests := r.AffectedTests
		if opts.Focus != AssessFocusTests {
			tests = nil
			for _, t := range r.AffectedTests {
				if t.Reason == "direct" {
					tests = append(tests, t)
				}
			}
			if len(tests) == 0 {
				tests = r.AffectedTests // A lookup that gives no reasons: list what there is
			}
			c.TestsIndirect = len(r.AffectedTests) - len(tests)
		}
		page, rest := pageRange(len(tests), offset, limit)
		for _, t := range tests[page[0]:page[1]] {
			c.Tests = append(c.Tests, CompactTest{File: t.FilePath, Names: t.TestNames})
		}
		if rest > 0 {
			more = append(more, fmt.Sprintf("test files %d-%d of %d shown; the next with focus %q, offset %d",
				page[0]+1, page[1], len(r.AffectedTests), AssessFocusTests, page[1]))
		} else if c.TestsIndirect > 0 {
			more = append(more, fmt.Sprintf("every test file with focus %q", AssessFocusTests))
		}
	}

	if s := r.IndexStaleness; s != nil && s.IsStale {
		c.Stale = s.StalenessMessage
		if c.Stale == "" {
			c.Stale = "The index is older than the code."
		}
	}

	c.More = strings.Join(more, "; ")
	c.Summary = compactAssessSummary(r, c)
	return c, nil
}

// rarelyLine says in one line how many uses of a symbol rarely break, where, and how
// to see them: `sectionSchema.key (changed; …): 128 in 30 files, 120 read, 8 annotation`.
func rarelyLine(g RarelyGroup) string {
	uses := make([]string, 0, len(g.Uses))
	for _, use := range []string{diff.TSUseRead, diff.TSUseAnnotation, useNameMatch, useLeafKey} {
		if n := g.Uses[use]; n > 0 {
			uses = append(uses, fmt.Sprintf("%d %s", n, use))
		}
	}
	line := fmt.Sprintf("%s: %d in %s", siteHead(cmp.Or(g.QualifiedName, g.Symbol), g.Change, g.Via), g.Count, plural(g.Files, "file"))
	if len(uses) > 0 {
		line += ", " + strings.Join(uses, ", ")
	}
	return line
}

// siteHead names a symbol with its change and why its uses count.
func siteHead(symbol, change, via string) string {
	switch {
	case change != "" && via != "":
		return symbol + " (" + change + "; " + via + ")"
	case change != "":
		return symbol + " (" + change + ")"
	case via != "":
		return symbol + " (" + via + ")"
	}
	return symbol
}

// pageRange returns the [start, end) slice bounds of one page of a list of n,
// and how many items follow it.
func pageRange(n, offset, limit int) ([2]int, int) {
	start := offset
	if start > n {
		start = n
	}
	end := start + limit
	if end > n {
		end = n
	}
	return [2]int{start, end}, n - end
}

func compactAssessSummary(r *AnalyzeChangeSetResponse, c *CompactAssessChange) string {
	var parts []string
	if s := r.Summary; s != nil {
		parts = append(parts, fmt.Sprintf("%s, %s changed; %d direct and %d transitive consumers; risk %s.",
			plural(s.FilesChanged, "file"), plural(s.SymbolsChanged, "symbol"), s.DirectlyAffected, s.TransitivelyAffected, c.Risk))
	}
	// From the response, not c: the summary covers every section, whatever the focus.
	if d := r.DoneCheck; d != nil {
		switch d.Status {
		case "open":
			if d.RarelyCount > 0 {
				parts = append(parts, fmt.Sprintf("%s of a changed declaration left open, %d of them rarely break.", plural(d.OpenCount, "use"), d.RarelyCount))
			} else {
				parts = append(parts, plural(d.OpenCount, "use")+" of a changed declaration left open.")
			}
		default:
			parts = append(parts, "doneCheck "+d.Status+".")
		}
	}
	if c.TestsTotal > 0 {
		parts = append(parts, plural(c.TestsTotal, "test file")+" affected.")
	}
	if len(parts) == 0 {
		return "Nothing assessed."
	}
	return strings.Join(parts, " ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// TestRunCommand is a command that runs tests in this repo, as GetAffectedTests
// reports it; "" when there are none or the language has no known runner.
func (e *Engine) TestRunCommand(tests []AffectedTest) string {
	return generateTestRunCommand(e.repoRoot, tests)
}

// maxCompactCode bounds a line of code in the text: past it the line is cut, and the
// position still says where to read the rest.
const maxCompactCode = 240

// Text renders the compact view as the text an agent reads: no keys, no quoting, each
// file named once per section. Open sites come under their symbol, then their file,
// one `line: code` each, in the order the check gave them.
func (c *CompactAssessChange) Text() string {
	var b strings.Builder
	b.WriteString(c.Summary)
	b.WriteByte('\n')
	for _, f := range c.RiskFactors {
		b.WriteString("  risk: " + f + "\n")
	}

	if d := c.DoneCheck; d != nil {
		b.WriteString("\ndoneCheck: " + d.Status)
		if d.OpenCount > 0 {
			b.WriteString(", " + plural(d.OpenCount, "use") + " left as they were")
		}
		if d.Diff != "" {
			b.WriteString(" (diff " + d.Diff + ")")
		}
		b.WriteByte('\n')
		if d.Note != "" {
			b.WriteString(d.Note + "\n")
		}
		writeOpenSites(&b, d.OpenSites)
		if len(d.Rarely) > 0 {
			fmt.Fprintf(&b, "rarely break, %d counted and not listed (focus %q lists them):\n", d.RarelyCount, AssessFocusRarely)
			for _, line := range d.Rarely {
				b.WriteString("  " + line + "\n")
			}
		}
	}

	if len(c.CoChangeOpen) > 0 {
		b.WriteString("\nusually changed together with these files, and left alone:\n")
		for _, f := range c.CoChangeOpen {
			b.WriteString("  " + f + "\n")
		}
	}

	if len(c.Contracts) > 0 {
		b.WriteString("\ncontracts:\n")
		// Two files can declare the same name (a simulate() in each): then the
		// line says whose.
		named := make(map[string]int)
		for _, k := range c.Contracts {
			named[k.Symbol]++
		}
		for _, k := range c.Contracts {
			b.WriteString("  " + k.Kind + " " + k.Symbol)
			if named[k.Symbol] > 1 && k.File != "" {
				b.WriteString(" (" + k.File + ")")
			}
			switch {
			case k.Before != "" && k.After != "":
				b.WriteString(": " + oneLine(k.Before) + " -> " + oneLine(k.After))
			case k.Before != "":
				b.WriteString(": " + oneLine(k.Before))
			}
			if k.Detail != "" {
				if k.Before != "" {
					b.WriteString(";")
				} else {
					b.WriteString(":")
				}
				b.WriteString(" " + k.Detail)
			}
			b.WriteByte('\n')
		}
	}

	if len(c.Tests) > 0 || c.TestsTotal > 0 {
		b.WriteString("\ntests:\n")
		for _, t := range c.Tests {
			b.WriteString("  " + t.File)
			if len(t.Names) > 0 {
				b.WriteString(": " + strings.Join(t.Names, ", "))
			}
			b.WriteByte('\n')
		}
		if c.TestsIndirect > 0 {
			b.WriteString("  " + plural(c.TestsIndirect, "more test file") + " reach the change through other code\n")
		}
		if c.RunTests != "" {
			b.WriteString("  run: " + c.RunTests + "\n")
		}
	}

	if len(c.Consumers) > 0 {
		fmt.Fprintf(&b, "\nconsumers (%s using a changed symbol):\n", plural(c.ConsumersTotal, "file"))
		for _, f := range c.Consumers {
			b.WriteString("  " + f + "\n")
		}
	}

	if c.Stale != "" {
		b.WriteString("\nstale: " + c.Stale + "\n")
	}
	if c.More != "" {
		b.WriteString("\nmore: " + c.More + "\n")
	}
	return b.String()
}

// writeOpenSites writes the sites grouped by symbol and reason, then by file.
func writeOpenSites(b *strings.Builder, sites []CompactOpenSite) {
	type group struct {
		head  string
		files []string
		lines map[string][]string
	}
	var groups []*group
	byHead := make(map[string]*group)
	for _, s := range sites {
		head := siteHead(s.Symbol, s.Change, s.Via)
		g := byHead[head]
		if g == nil {
			g = &group{head: head, lines: make(map[string][]string)}
			byHead[head] = g
			groups = append(groups, g)
		}
		file, line := s.At, ""
		if at := strings.LastIndexByte(s.At, ':'); at >= 0 {
			file, line = s.At[:at], s.At[at+1:]
		}
		if s.Test {
			file += " (test)"
		}
		if _, ok := g.lines[file]; !ok {
			g.files = append(g.files, file)
		}
		code := oneLine(s.Code)
		if len(code) > maxCompactCode {
			code = strings.ToValidUTF8(code[:maxCompactCode], "") + " …"
		}
		g.lines[file] = append(g.lines[file], line+": "+code)
	}
	for _, g := range groups {
		b.WriteString(g.head + "\n")
		for _, file := range g.files {
			b.WriteString("  " + file + "\n")
			for _, line := range g.lines[file] {
				b.WriteString("    " + line + "\n")
			}
		}
	}
}

// oneLine folds a declaration or a line of code that spans lines into one.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
