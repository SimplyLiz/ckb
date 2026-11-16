package overlay

import (
	"sort"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

const otherPkg = "scip-go gomod example.com/m v1 `example.com/m/other`/"

func TestGoLayer2_ResolvesOrLeavesOut(t *testing.T) {
	src := `package lib

import "example.com/m/other"

func Use(c *other.Client) {
	other.Do()
	c.Send()
	var v Local
	v.Run()
	Helper()
	Target := 1
	_ = Target
	c.Inner().Send()
	Missing()
}
`
	base := &scip.SCIPIndex{
		Symbols: map[string]*scip.SymbolInformation{
			otherPkg:                    {Symbol: otherPkg, DisplayName: "other"},
			otherPkg + "Do().":          {},
			otherPkg + "Client#":        {},
			otherPkg + "Client#Send().": {},
			goPkg + "Local#":            {},
			goPkg + "Local#Run().":      {},
			goPkg + "Helper().":         {},
			goPkg + "Target().":         {},
		},
		RefIndex: map[string][]*scip.OccurrenceRef{},
	}
	pre := goPackagePrefixes(base)
	g := &goSymbols{base: base, prefixes: pre.byPath, names: pre.names, declared: map[string]bool{}}
	doc := &scip.Document{RelativePath: "lib/use.go"}
	added, unresolved := goLayer2(doc, []byte(src), goPkg, allLines([]byte(src)), g)

	var got []string
	for _, o := range doc.Occurrences {
		if o.Source != scip.OccurrenceSourceOverlay || o.SymbolRoles != scip.SymbolRoleReadAccess {
			t.Errorf("occurrence %v lacks the overlay source or read role", o.Range)
		}
		got = append(got, strings.NewReplacer(otherPkg, "other/", goPkg, "lib/").Replace(o.Symbol))
	}
	sort.Strings(got)
	want := []string{
		"lib/Helper().", "lib/Local#", "lib/Local#Run().",
		"other/", "other/", "other/Client#", "other/Client#Send().", "other/Do().",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("resolved\n  %v\nwant\n  %v", got, want)
	}
	if added != len(want) {
		t.Errorf("added %d, want %d", added, len(want))
	}
	// c.Inner() has no visible type for its result; .Send on it, and Missing,
	// get no edge. The local Target shadows the package's Target().
	if unresolved != 3 {
		t.Errorf("unresolved %d, want 3 (Inner on c: no Client#Inner; Send on a call; Missing)", unresolved)
	}
}
