package overlay

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

const goPkg = "scip-go gomod example.com/m v1 `example.com/m/lib`/"

const goOld = `package lib

func Target(a int) int {
	return a
}

func Gone() {}

type T struct {
	A int
}
`

// goOld's document as scip-go writes it.
func goOldDoc() *scip.Document {
	def := func(sym string, line, col, end int32, encl ...int32) *scip.Occurrence {
		return &scip.Occurrence{Range: []int32{line, col, end}, Symbol: sym, SymbolRoles: scip.SymbolRoleDefinition, EnclosingRange: encl}
	}
	return &scip.Document{
		RelativePath: "lib/lib.go", Language: "go",
		Symbols: []*scip.SymbolInformation{
			{Symbol: goPkg, DisplayName: "lib"},
			{Symbol: goPkg + "Target().", DisplayName: "Target"},
			{Symbol: goPkg + "Gone().", DisplayName: "Gone"},
			{Symbol: goPkg + "T#", DisplayName: "T"},
			{Symbol: goPkg + "T#A.", DisplayName: "A"},
		},
		Occurrences: []*scip.Occurrence{
			def(goPkg, 0, 8, 11),
			def(goPkg+"Target().", 2, 5, 11, 2, 0, 4, 1),
			def(goPkg+"Gone().", 6, 5, 9, 6, 0, 6, 14),
			def(goPkg+"T#", 8, 5, 6),
			def(goPkg+"T#A.", 9, 1, 2),
		},
	}
}

func TestGoLayer1(t *testing.T) {
	goNew := `package lib

// Target now takes two.
func Target(a, b int) int {
	return a + b
}

func Added() int { return Target(1, 2) }

type T struct {
	A int
	B string
}
`
	old := goOldDoc()
	base := scip.PatchIndex(&scip.SCIPIndex{DocumentsByPath: map[string]*scip.Document{}}, map[string]*scip.Document{"lib/lib.go": old})
	lm := NewLineMap(diffOf(t, goOld, goNew))
	doc, _ := RemapDocument(old, lm)
	n, ok := goLayer1(doc, base, old, "lib/lib.go", []byte(goNew), lm)
	if !ok {
		t.Fatal("did not parse")
	}
	defs := map[string]string{}
	for _, o := range doc.Occurrences {
		if o.SymbolRoles&scip.SymbolRoleDefinition != 0 {
			defs[strings.TrimPrefix(o.Symbol, goPkg)] = fmtRange(o)
		}
	}
	want := map[string]string{
		"":          "0 8 11",
		"Target().": "3 5 11 | 3 0 5 1", // signature changed: same symbol, new line
		"Added().":  "7 5 10 | 7 0 40",
		"T#":        "9 5 6",
		"T#A.":      "10 1 2",
		"T#B.":      "11 1 2",
	}
	for k, v := range want {
		if defs[k] != v {
			t.Errorf("%q: got %q, want %q", k, defs[k], v)
		}
	}
	if _, has := defs["Gone()."]; has {
		t.Error("Gone() is still defined")
	}
	if n != 3 {
		t.Errorf("declared %d, want 3 (Target, Added, B)", n)
	}
	infos := map[string]bool{}
	for _, s := range doc.Symbols {
		infos[strings.TrimPrefix(s.Symbol, goPkg)] = true
	}
	if infos["Gone()."] || !infos["Added()."] || !infos["Target()."] || !infos[""] {
		t.Errorf("symbols %v", infos)
	}
	// The base document is untouched.
	if len(old.Occurrences) != 5 || old.Occurrences[1].Range[0] != 2 {
		t.Error("base document modified")
	}
}

func TestGoLayer1_ParseErrorStaysLayer0(t *testing.T) {
	old := goOldDoc()
	broken := strings.Replace(goOld, "return a\n}", "return a\n", 1)
	base := scip.PatchIndex(&scip.SCIPIndex{DocumentsByPath: map[string]*scip.Document{}}, map[string]*scip.Document{"lib/lib.go": old})
	lm := NewLineMap(diffOf(t, goOld, broken))
	doc, _ := RemapDocument(old, lm)
	before := len(doc.Occurrences)
	if _, ok := goLayer1(doc, base, old, "lib/lib.go", []byte(broken), lm); ok {
		t.Fatal("a file with a syntax error parsed")
	}
	if len(doc.Occurrences) != before {
		t.Error("a failed parse changed the document")
	}
}

func fmtRange(o *scip.Occurrence) string {
	s := strings.Trim(fmt.Sprint(o.Range), "[]")
	if len(o.EnclosingRange) > 0 {
		s += " | " + strings.Trim(fmt.Sprint(o.EnclosingRange), "[]")
	}
	return s
}
