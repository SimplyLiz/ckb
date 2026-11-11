package query

import (
	"fmt"
	"strings"
)

// compactTestFiles bounds the affected test files a compact assessChange lists;
// TestsTotal still counts all of them.
const compactTestFiles = 20

// CompactAssessChange is the token-budget view of an assessChange result
// (format "compact"): what an agent acts on before calling a change finished,
// with file:line positions and short names instead of symbol ids.
type CompactAssessChange struct {
	Summary   string            `json:"summary"`
	Risk      string            `json:"risk"`
	RiskScore float64           `json:"riskScore"`
	DoneCheck *CompactDoneCheck `json:"doneCheck,omitempty"`
	// CoChangeOpen are files that usually change with the touched ones and didn't.
	CoChangeOpen []string          `json:"coChangeOpen,omitempty"`
	Contracts    []CompactContract `json:"contracts,omitempty"`
	// Tests are the affected test files, most certain first; TestsTotal counts all.
	Tests      []CompactTest `json:"tests,omitempty"`
	TestsTotal int           `json:"testsTotal"`
	// Consumers are the files referencing a changed symbol themselves.
	Consumers      []string `json:"consumers,omitempty"`
	ConsumersTotal int      `json:"consumersTotal"`
	// Stale says the index doesn't match the code, so positions may be off.
	Stale string `json:"stale,omitempty"`
}

// CompactDoneCheck is DoneCheck without the per-site symbol ids.
type CompactDoneCheck struct {
	Status    string            `json:"status"`
	Diff      string            `json:"diff,omitempty"`
	OpenCount int               `json:"openCount"`
	OpenSites []CompactOpenSite `json:"openSites,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
	Note      string            `json:"note,omitempty"`
}

// CompactOpenSite is one untouched use: At is "file:line".
type CompactOpenSite struct {
	At       string `json:"at"`
	Symbol   string `json:"symbol,omitempty"`
	Code     string `json:"code,omitempty"`
	Consumer string `json:"consumer,omitempty"`
	Via      string `json:"via,omitempty"`
	Test     bool   `json:"test,omitempty"`
}

// CompactContract is a possible contract change: its kind and the symbol.
type CompactContract struct {
	Kind   string `json:"kind"`
	Symbol string `json:"symbol"`
}

// CompactTest is an affected test file and the test functions seen in it.
type CompactTest struct {
	File  string   `json:"file"`
	Names []string `json:"names,omitempty"`
}

// CompactAssessChangeFrom builds the compact view of an AnalyzeChangeSet response.
func CompactAssessChangeFrom(r *AnalyzeChangeSetResponse) *CompactAssessChange {
	c := &CompactAssessChange{Risk: "unknown"}
	if r == nil {
		c.Summary = "Nothing assessed."
		return c
	}
	if r.RiskScore != nil {
		c.Risk = r.RiskScore.Level
		c.RiskScore = r.RiskScore.Score
	} else if r.Summary != nil && r.Summary.EstimatedRisk != "" {
		c.Risk = r.Summary.EstimatedRisk
	}

	if d := r.DoneCheck; d != nil {
		cd := &CompactDoneCheck{Status: d.Status, Diff: d.Diff, OpenCount: d.OpenCount, Truncated: d.Truncated, Note: d.Note}
		for _, s := range d.OpenSites {
			cd.OpenSites = append(cd.OpenSites, CompactOpenSite{
				At:       fmt.Sprintf("%s:%d", s.File, s.Line),
				Symbol:   s.Symbol,
				Code:     s.Code,
				Consumer: s.Consumer,
				Via:      s.Via,
				Test:     s.IsTest,
			})
		}
		c.DoneCheck = cd
	}

	if cc := r.CoChangeCheck; cc != nil {
		for _, f := range cc.OpenFiles {
			c.CoChangeOpen = append(c.CoChangeOpen, f.File)
		}
	}

	for _, k := range r.Contracts {
		c.Contracts = append(c.Contracts, CompactContract{Kind: k.Kind, Symbol: k.Symbol})
	}

	c.TestsTotal = len(r.AffectedTests)
	for i, t := range r.AffectedTests {
		if i >= compactTestFiles {
			break
		}
		c.Tests = append(c.Tests, CompactTest{File: t.FilePath, Names: t.TestNames})
	}

	if cf := r.ConsumerFiles; cf != nil {
		c.Consumers = cf.Direct
		c.ConsumersTotal = len(cf.Direct)
	}

	if s := r.IndexStaleness; s != nil && s.IsStale {
		c.Stale = s.StalenessMessage
		if c.Stale == "" {
			c.Stale = "The index is older than the code."
		}
	}

	c.Summary = compactAssessSummary(r, c)
	return c
}

func compactAssessSummary(r *AnalyzeChangeSetResponse, c *CompactAssessChange) string {
	var parts []string
	if s := r.Summary; s != nil {
		parts = append(parts, fmt.Sprintf("%s, %s changed; %d direct and %d transitive consumers; risk %s.",
			plural(s.FilesChanged, "file"), plural(s.SymbolsChanged, "symbol"), s.DirectlyAffected, s.TransitivelyAffected, c.Risk))
	}
	if c.DoneCheck != nil {
		switch c.DoneCheck.Status {
		case "open":
			parts = append(parts, plural(c.DoneCheck.OpenCount, "use")+" of a changed declaration left open.")
		default:
			parts = append(parts, "doneCheck "+c.DoneCheck.Status+".")
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
