package scip

import (
	"reflect"
	"testing"
)

func TestVersionlessKey(t *testing.T) {
	cases := []struct {
		id, key string
		ok      bool
	}{
		{"scip-go gomod example.com/m 67d776584a29 `example.com/m/q`/NewEngine().",
			"scip-go gomod example.com/m `example.com/m/q`/NewEngine().", true},
		// A doubled space is an escaped space inside a field, not a separator.
		{"scip-java maven com.google  guava 31.0 ImmutableList#",
			"scip-java maven com.google  guava ImmutableList#", true},
		{"local 12", "", false},
		{"scip-go gomod m v1", "", false},
	}
	for _, c := range cases {
		key, ok := versionlessKey(c.id)
		if key != c.key || ok != c.ok {
			t.Errorf("versionlessKey(%q) = %q, %v; want %q, %v", c.id, key, ok, c.key, c.ok)
		}
	}
}

func TestResolveAcrossVersions(t *testing.T) {
	const (
		current  = "scip-go gomod example.com/m 4828899aaaaa `example.com/m/q`/NewEngine()."
		foreign  = "scip-go gomod example.com/m 67d776584a29 `example.com/m/q`/NewEngine()."
		dupA     = "scip-ts npm pkg 1.0.0 dup."
		dupB     = "scip-ts npm pkg 2.0.0 dup."
		otherPkg = "scip-go gomod example.com/other 67d776584a29 `example.com/m/q`/NewEngine()."
	)
	idx := &SCIPIndex{
		Symbols: map[string]*SymbolInformation{
			current: {Symbol: current},
			dupA:    {Symbol: dupA},
		},
		RefIndex: map[string][]*OccurrenceRef{dupB: nil},
	}

	if got, cands := idx.ResolveAcrossVersions(current); got != current || cands != nil {
		t.Errorf("known ID: got %q, %v", got, cands)
	}
	if got, cands := idx.ResolveAcrossVersions(foreign); got != current || cands != nil {
		t.Errorf("foreign version: got %q, %v; want %q", got, cands, current)
	}
	if got, cands := idx.ResolveAcrossVersions(otherPkg); got != "" || cands != nil {
		t.Errorf("other package must not match: got %q, %v", got, cands)
	}
	got, cands := idx.ResolveAcrossVersions("scip-ts npm pkg 3.0.0 dup.")
	if got != "" || !reflect.DeepEqual(cands, []string{dupA, dupB}) {
		t.Errorf("ambiguous: got %q, %v; want no pick and both candidates", got, cands)
	}
}
