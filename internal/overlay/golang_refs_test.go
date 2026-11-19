package overlay

import (
	"os"
	"path/filepath"
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

func TestGoLayer2_TypesFromTheDeclaringFile(t *testing.T) {
	root := t.TempDir()
	decl := `package lib

import "example.com/m/other"

type Base struct{}

type Engine struct {
	Base
	client *other.Client
}

func NewEngine() (*Engine, error) { return nil, nil }
`
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lib", "engine.go"), []byte(decl), 0o644); err != nil {
		t.Fatal(err)
	}
	def := func(sym string, line int32) *scip.Occurrence {
		return &scip.Occurrence{Range: []int32{line, 5, 9}, Symbol: sym, SymbolRoles: scip.SymbolRoleDefinition}
	}
	engineDoc := &scip.Document{RelativePath: "lib/engine.go", Occurrences: []*scip.Occurrence{
		def(goPkg+"Base#", 4), def(goPkg+"Engine#", 6), def(goPkg+"Engine#client.", 8), def(goPkg+"NewEngine().", 11),
		def(goPkg+"Base#Reset().", 4),
	}, Symbols: []*scip.SymbolInformation{{Symbol: goPkg + "Base#"}, {Symbol: goPkg + "Engine#"}, {Symbol: goPkg + "Engine#client."},
		{Symbol: goPkg + "NewEngine()."}, {Symbol: goPkg + "Base#Reset()."}}}
	otherDoc := &scip.Document{RelativePath: "other/client.go", Occurrences: []*scip.Occurrence{
		def(otherPkg, 0), def(otherPkg+"Client#", 2), def(otherPkg+"Client#Send().", 4),
	}, Symbols: []*scip.SymbolInformation{{Symbol: otherPkg, DisplayName: "other"}, {Symbol: otherPkg + "Client#"}, {Symbol: otherPkg + "Client#Send()."}}}
	base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"lib/engine.go": engineDoc, "other/client.go": otherDoc})

	src := `package lib

func (e *Engine) Run() {
	e.client.Send()
	e.Reset()
	n, err := NewEngine()
	_ = err
	n.client.Send()
	e.client.Missing()
}
`
	pre := goPackagePrefixes(base)
	g := &goSymbols{base: base, prefixes: pre.byPath, names: pre.names, declared: map[string]bool{}}
	doc := &scip.Document{RelativePath: "lib/run.go"}
	_, unresolved := goLayer2Typed(doc, []byte(src), goPkg, allLines([]byte(src)), g, newGoTypes(g, root))
	at := map[int32][]string{}
	for _, o := range doc.Occurrences {
		at[o.Range[0]] = append(at[o.Range[0]], strings.NewReplacer(otherPkg, "other/", goPkg, "lib/").Replace(o.Symbol))
	}
	want := map[int32]string{
		3: "lib/Engine#client. other/Client#Send().", // the field, then the method on its type
		4: "lib/Base#Reset().",                       // promoted from the embedded Base
		5: "lib/NewEngine().",
		7: "lib/Engine#client. other/Client#Send().", // n's type is NewEngine's first result
		8: "lib/Engine#client.",
	}
	for line, w := range want {
		got := at[line]
		sort.Strings(got)
		if strings.Join(got, " ") != w {
			t.Errorf("line %d: %v, want %s", line, got, w)
		}
	}
	if unresolved != 1 {
		t.Errorf("unresolved %d, want 1 (Missing)", unresolved)
	}
}
