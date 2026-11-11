package query

import "testing"

func TestDropCoveredLiterals(t *testing.T) {
	sites := []OpenSite{
		// Single-line literal holding the removed field: the field site is enough.
		{Symbol: "Opts", File: "a.go", Line: 10, Via: "fields of Opts changed"},
		{Symbol: "Max", File: "a.go", Line: 10},
		// Multi-line literal whose field sits two lines down.
		{Symbol: "Opts", File: "a.go", Line: 20, Via: "fields of Opts changed", literalLines: 3},
		{Symbol: "Max", File: "a.go", Line: 22},
		// A literal that leaves the field out entirely stays: nothing else points here.
		{Symbol: "Opts", File: "a.go", Line: 30, Via: "fields of Opts changed", literalLines: 2},
		// Same line in another file doesn't cover it.
		{Symbol: "Opts", File: "b.go", Line: 10, Via: "fields of Opts changed"},
		// A use past the literal's braces doesn't cover it.
		{Symbol: "Opts", File: "c.go", Line: 5, Via: "fields of Opts changed", literalLines: 1},
		{Symbol: "Max", File: "c.go", Line: 7},
	}
	got := dropCoveredLiterals(sites)
	want := []struct {
		file string
		line int
		lit  bool
	}{{"a.go", 10, false}, {"a.go", 22, false}, {"a.go", 30, true}, {"b.go", 10, true}, {"c.go", 5, true}, {"c.go", 7, false}}
	if len(got) != len(want) {
		t.Fatalf("got %d sites, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.File != w.file || g.Line != w.line || isLiteralVia(g.Via) != w.lit {
			t.Errorf("site %d = %s:%d via %q, want %s:%d literal=%v", i, g.File, g.Line, g.Via, w.file, w.line, w.lit)
		}
	}
}
