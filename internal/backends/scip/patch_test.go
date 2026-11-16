package scip

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

func occ(sym string, line int32, roles int32, enclosing ...int32) *Occurrence {
	return &Occurrence{Range: []int32{line, 5, 10}, Symbol: sym, SymbolRoles: roles, EnclosingRange: enclosing}
}

const pkgA = "scip-go gomod example.com/m v1 `example.com/m/a`/"

func patchFixture() []*Document {
	return []*Document{
		{
			RelativePath: "a/a.go", Language: "go",
			Symbols: []*SymbolInformation{{Symbol: pkgA + "Foo().", DisplayName: "Foo"}, {Symbol: pkgA + "Bar().", DisplayName: "Bar"}},
			Occurrences: []*Occurrence{
				occ(pkgA+"Foo().", 2, SymbolRoleDefinition, 2, 0, 6, 1),
				occ(pkgA+"Bar().", 3, 0),
				occ(pkgA+"Bar().", 8, SymbolRoleDefinition, 8, 0, 10, 1),
			},
		},
		{
			RelativePath: "a/b.go", Language: "go",
			Symbols: []*SymbolInformation{{Symbol: pkgA + "Baz().", DisplayName: "Baz"}},
			Occurrences: []*Occurrence{
				occ(pkgA+"Baz().", 1, SymbolRoleDefinition, 1, 0, 4, 1),
				occ(pkgA+"Foo().", 2, 0),
				occ(pkgA+"Bar().", 3, 0),
			},
		},
		{
			RelativePath: "c/c.go", Language: "go",
			Symbols: []*SymbolInformation{{Symbol: pkgA + "Qux().", DisplayName: "Qux"}},
			Occurrences: []*Occurrence{
				occ(pkgA+"Qux().", 1, SymbolRoleDefinition, 1, 0, 3, 1),
				occ(pkgA+"Foo().", 2, 0),
			},
		},
	}
}

func assembleDocs(docs []*Document) *SCIPIndex {
	results := make([]docResult, 0, len(docs))
	for _, d := range docs {
		results = append(results, indexDocument(d))
	}
	return assembleIndex(nil, results)
}

// sameIndex compares every derived structure of two indexes.
func sameIndex(t *testing.T, got, want *SCIPIndex) {
	t.Helper()
	paths := func(ds []*Document) []string {
		out := make([]string, len(ds))
		for i, d := range ds {
			out[i] = d.RelativePath
		}
		return out
	}
	if !reflect.DeepEqual(paths(got.Documents), paths(want.Documents)) {
		t.Fatalf("documents: got %v, want %v", paths(got.Documents), paths(want.Documents))
	}
	if len(got.DocumentsByPath) != len(want.DocumentsByPath) {
		t.Errorf("DocumentsByPath: %d vs %d", len(got.DocumentsByPath), len(want.DocumentsByPath))
	}
	for p, d := range want.DocumentsByPath {
		if got.DocumentsByPath[p] != d {
			t.Errorf("DocumentsByPath[%s] differs", p)
		}
	}
	if !reflect.DeepEqual(got.Symbols, want.Symbols) {
		t.Errorf("Symbols differ: %d vs %d", len(got.Symbols), len(want.Symbols))
	}
	refKey := func(rs []*OccurrenceRef) []string {
		out := make([]string, len(rs))
		for i, r := range rs {
			out[i] = fmt.Sprintf("%s@%p", r.Doc.RelativePath, r.Occ)
		}
		return out
	}
	if len(got.RefIndex) != len(want.RefIndex) {
		t.Errorf("RefIndex: %d vs %d symbols", len(got.RefIndex), len(want.RefIndex))
	}
	for sym, rs := range want.RefIndex {
		if !reflect.DeepEqual(refKey(got.RefIndex[sym]), refKey(rs)) {
			t.Errorf("RefIndex[%s]: got %v, want %v", sym, refKey(got.RefIndex[sym]), refKey(rs))
		}
	}
	if len(got.DefinitionIndex) != len(want.DefinitionIndex) {
		t.Errorf("DefinitionIndex: %d vs %d", len(got.DefinitionIndex), len(want.DefinitionIndex))
	}
	for sym, r := range want.DefinitionIndex {
		g := got.DefinitionIndex[sym]
		if g == nil || g.Occ != r.Occ || g.Doc != r.Doc {
			t.Errorf("DefinitionIndex[%s] differs", sym)
		}
	}
	if !reflect.DeepEqual(got.ContainerIndex, want.ContainerIndex) {
		t.Errorf("ContainerIndex differs: %d vs %d", len(got.ContainerIndex), len(want.ContainerIndex))
	}
	if !reflect.DeepEqual(got.ConvertedSymbols, want.ConvertedSymbols) {
		for id, w := range want.ConvertedSymbols {
			if !reflect.DeepEqual(got.ConvertedSymbols[id], w) {
				t.Errorf("ConvertedSymbols[%s]: got %+v, want %+v", id, got.ConvertedSymbols[id], w)
				break
			}
		}
		t.Errorf("ConvertedSymbols differ: %d vs %d", len(got.ConvertedSymbols), len(want.ConvertedSymbols))
	}
	if !reflect.DeepEqual(got.NameIndex, want.NameIndex) {
		t.Errorf("NameIndex differs: %d vs %d", len(got.NameIndex), len(want.NameIndex))
	}
	if !reflect.DeepEqual(buildCallerIndex(got.Documents), buildCallerIndex(want.Documents)) {
		t.Errorf("CallerIndex differs")
	}
}

func TestPatchIndex_MatchesRebuild(t *testing.T) {
	docs := patchFixture()
	base := assembleDocs(docs)

	// a/a.go: Bar's definition gone, a new reference to Baz; a/b.go deleted;
	// c/c.go renamed to d/c.go; a new file e/e.go.
	newA := &Document{
		RelativePath: "a/a.go", Language: "go",
		Symbols: []*SymbolInformation{{Symbol: pkgA + "Foo().", DisplayName: "Foo"}},
		Occurrences: []*Occurrence{
			occ(pkgA+"Foo().", 4, SymbolRoleDefinition, 4, 0, 8, 1),
			occ(pkgA+"Baz().", 5, 0),
		},
	}
	movedC := &Document{RelativePath: "d/c.go", Language: "go", Symbols: docs[2].Symbols, Occurrences: docs[2].Occurrences}
	newE := &Document{
		RelativePath: "e/e.go", Language: "go",
		Symbols:     []*SymbolInformation{{Symbol: pkgA + "Baz().", DisplayName: "Baz"}},
		Occurrences: []*Occurrence{occ(pkgA+"Baz().", 0, SymbolRoleDefinition, 0, 0, 2, 1), occ(pkgA+"Foo().", 1, 0)},
	}
	replace := map[string]*Document{"a/a.go": newA, "a/b.go": nil, "c/c.go": nil, "d/c.go": movedC, "e/e.go": newE}

	baseSnapshot := fmt.Sprintf("%v %d %d", base.Documents, len(base.RefIndex), len(base.Symbols))
	got := PatchIndex(base, replace)
	want := assembleDocs([]*Document{newA, movedC, newE})
	sameIndex(t, got, want)

	if s := fmt.Sprintf("%v %d %d", base.Documents, len(base.RefIndex), len(base.Symbols)); s != baseSnapshot {
		t.Errorf("base index was modified")
	}
}

// TestPatchIndex_RealIndex patches this repo's own index (when it has one)
// with a few remapped documents and compares against a rebuild. It also
// reports the time a patch takes next to the rebuild.
func TestPatchIndex_RealIndex(t *testing.T) {
	path := filepath.Join("..", "..", "..", ".scip", "index.scip")
	if _, err := os.Stat(path); err != nil {
		t.Skip("no index at", path)
	}
	if testing.Short() {
		t.Skip("short")
	}
	base, err := LoadSCIPIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	// Replace every 100th document with a copy whose occurrences moved down
	// three lines, and delete one.
	replace := map[string]*Document{}
	for i, d := range base.Documents {
		switch {
		case i%100 == 0:
			cp := &Document{RelativePath: d.RelativePath, Language: d.Language, Symbols: d.Symbols}
			for _, o := range d.Occurrences {
				c := *o
				c.Range = append([]int32(nil), o.Range...)
				c.Range[0] += 3
				if len(c.Range) == 4 {
					c.Range[2] += 3
				}
				cp.Occurrences = append(cp.Occurrences, &c)
			}
			replace[d.RelativePath] = cp
		case i == 51:
			replace[d.RelativePath] = nil
		}
	}
	start := time.Now()
	got := PatchIndex(base, replace)
	patchTime := time.Since(start)

	var finalDocs []*Document
	for _, d := range base.Documents {
		if nd, ok := replace[d.RelativePath]; ok {
			if nd != nil {
				finalDocs = append(finalDocs, nd)
			}
			continue
		}
		finalDocs = append(finalDocs, d)
	}
	sort.Slice(finalDocs, func(i, j int) bool { return finalDocs[i].RelativePath < finalDocs[j].RelativePath })
	start = time.Now()
	want := assembleDocs(finalDocs)
	rebuildTime := time.Since(start)
	want.Metadata = base.Metadata
	t.Logf("%d docs, %d replaced: patch %v, rebuild %v", len(base.Documents), len(replace), patchTime, rebuildTime)
	sameIndex(t, got, want)
}
