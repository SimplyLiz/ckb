package query

import (
	"fmt"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

func TestIsDeclarationSymbol(t *testing.T) {
	for sym, want := range map[string]bool{
		"npm app 1 src/`a.tsx`/Comp().":                 true,
		"npm app 1 src/`a.tsx`/Props#":                  true,
		"npm app 1 src/`a.tsx`/count.":                  true,
		"go mod v1 pkg/Engine#Run().":                   true,
		"npm app 1 src/`a.tsx`/":                        false, // namespace
		"npm app 1 src/`a.tsx`/year0:":                  false, // meta
		"npm app 1 src/`a.tsx`/Comp().(`{ a, b }`)":     false, // parameter
		"npm app 1 src/`a.tsx`/Del#typeLiteral1:child.": false, // field
		"go mod v1 pkg/Engine#cache.":                   false, // field
	} {
		if got := isDeclarationSymbol(sym); got != want {
			t.Errorf("%s: got %v, want %v", sym, got, want)
		}
	}
}

// godModules: breadth counts Go packages once, size counts declarations, and
// tests stay out on both ends.
func TestGodModules(t *testing.T) {
	idx := &scip.SCIPIndex{DefinitionIndex: map[string]*scip.OccurrenceRef{}}
	addDoc := func(path string, defs []string, refs []string) *scip.Document {
		doc := &scip.Document{RelativePath: path}
		for _, d := range defs {
			occ := &scip.Occurrence{Symbol: d, SymbolRoles: scip.SymbolRoleDefinition}
			doc.Occurrences = append(doc.Occurrences, occ)
			doc.Symbols = append(doc.Symbols, &scip.SymbolInformation{Symbol: d})
			idx.DefinitionIndex[d] = &scip.OccurrenceRef{Doc: doc, Occ: occ}
		}
		for _, r := range refs {
			doc.Occurrences = append(doc.Occurrences, &scip.Occurrence{Symbol: r})
		}
		idx.Documents = append(idx.Documents, doc)
		return doc
	}
	var refs []string
	for i := 0; i < godMinBreadth; i++ {
		sym := fmt.Sprintf("dep%d/F().", i)
		addDoc(fmt.Sprintf("dep%d/a.go", i), []string{sym}, nil)
		refs = append(refs, sym)
	}
	// A second file in dep0: same package, no extra breadth.
	addDoc("dep0/b.go", []string{"dep0/G()."}, nil)
	refs = append(refs, "dep0/G().")
	var defs []string
	for i := 0; i < godMinDeclarations; i++ {
		defs = append(defs, fmt.Sprintf("god/F%d().", i))
	}
	addDoc("god/god.go", defs, refs)
	addDoc("god/god_test.go", defs[:1], refs) // tests never count
	skip := isTestFilePath

	got := godModules(idx, skip)
	if len(got) != 1 || got[0].File != "god/god.go" || got[0].Dependencies != godMinBreadth || got[0].Declarations != godMinDeclarations || got[0].Severity != "medium" {
		t.Fatalf("got %+v", got)
	}

	// One declaration short: no god module.
	idx.Documents[len(idx.Documents)-2].Symbols = idx.Documents[len(idx.Documents)-2].Symbols[1:]
	if got := godModules(idx, skip); len(got) != 0 {
		t.Fatalf("39 declarations: got %+v", got)
	}
}
