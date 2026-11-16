//go:build cgo

package overlay

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// The declarations and positions below are the ones scip-typescript 0.4.0
// writes for this source (checked against its index of a real repository:
// 6,138 of 6,138 comparable definitions in 539 files identical).
const tsSample = `import { x } from "./x";

export function f<K>(a: number, ...rest: string[]): void {}
const g = (b: number) => b;
export const ü = "ä"; export const after = 1;
export default class C {
  private n = 1;
  constructor(public p: number) {}
  get v(): number { return 1; }
  m(): void {}
}
interface I { q: string; r(): void }
type T = { lit: number };
enum E { A, B = 2 }
namespace N { export function h() {} }
declare module "mod" { export const z: number; }
const { d1, d2 } = { d1: 1, d2: 2 };
`

func TestParseTSDecls(t *testing.T) {
	decls, errLines, ok := parseTSDecls("a.ts", []byte(tsSample))
	if !ok || errLines != nil {
		t.Fatalf("parse: ok %v, errors on %v", ok, errLines)
	}
	got := map[string]string{}
	for _, d := range decls {
		got[d.suffix] = fmt.Sprintf("%d:%d-%d %v", d.line, d.col, d.end, d.enclosing)
	}
	want := map[string]string{
		"f().":                    "2:16-17 [2 0 59]",
		"f().[K]":                 "2:18-19 []",
		"f().(a)":                 "2:21-22 []",
		"f().(rest)":              "2:35-39 []",
		"g.":                      "3:6-7 [3 10 26]",
		"`ü`.":                    "4:13-14 []",
		"after.":                  "4:35-40 []", // UTF-16 columns: "ü" and "ä" are one unit each, two bytes
		"C#":                      "5:21-22 [5 0 10 1]",
		"C#n.":                    "6:10-11 []",
		"C#`<constructor>`().":    "7:2-13 [7 2 34]",
		"C#`<constructor>`().(p)": "7:21-22 []",
		"C#`<get>v`().":           "8:6-7 [8 2 31]",
		"C#m().":                  "9:2-3 [9 2 14]",
		"I#":                      "11:10-11 [11 0 36]",
		"I#q.":                    "11:14-15 []",
		"I#r().":                  "11:25-26 []",
		"T#":                      "12:5-6 [12 0 25]",
		"E#":                      "13:5-6 [13 0 19]",
		"E#A.":                    "13:9-10 []",
		"E#B.":                    "13:12-13 []",
		"N/":                      "14:10-11 []",
		"N/h().":                  "14:30-31 [14 14 36]",
		"`\"mod\"`/":              "15:15-20 []",
		"`\"mod\"`/z.":            "15:36-37 []",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: got %q, want %q", k, got[k], w)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected declaration %s at %s (type-literal members and destructuring are left out)", k, got[k])
		}
	}
}

func TestParseTSDecls_SyntaxError(t *testing.T) {
	_, errLines, _ := parseTSDecls("a.ts", []byte("export function f( {\n"))
	if len(errLines) == 0 {
		t.Fatal("a syntax error must be reported")
	}
}

func TestTSFileSymbols(t *testing.T) {
	head := "scip-typescript npm app 1.0.0 "
	base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{
		"web/src/(app)/page.tsx": {Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 0}, Symbol: head + "src/`(app)`/`page.tsx`/", SymbolRoles: scip.SymbolRoleDefinition}}},
	})
	if got, want := tsNewFileSymbol(base, "web/src/lib/[id]/a.ts"), head+"src/lib/`[id]`/`a.ts`/"; got != want {
		t.Errorf("new file symbol %q, want %q", got, want)
	}
	if got := tsNewFileSymbol(base, "other/a.ts"); got != "" {
		t.Errorf("a file outside every known package root got %q", got)
	}
	doc, prefix := tsNewDocument(base, "web/src/b.ts", []byte("export function b() {}\n"))
	if doc == nil || prefix != head+"src/`b.ts`/" {
		t.Fatalf("tsNewDocument: %v %q", doc != nil, prefix)
	}
	var defs []string
	for _, o := range doc.Occurrences {
		defs = append(defs, fmt.Sprintf("%s%v", strings.TrimPrefix(o.Symbol, prefix), o.EnclosingRange))
	}
	if got := strings.Join(defs, " "); got != "[0 0 1 0] b().[0 0 22]" {
		t.Errorf("definitions %s", got)
	}
}

// A changed file: a declaration on an added line comes in, one whose line
// was removed and that is gone loses its SymbolInformation, an unchanged one
// keeps its occurrence.
func TestTSLayer1(t *testing.T) {
	prefix := "scip-typescript npm app 1.0.0 `a.ts`/"
	oldSrc := "export function keep() {}\nexport function gone() {}\n"
	newSrc := "export function keep() {}\nexport function added(x: number) {}\n"
	old := &scip.Document{RelativePath: "a.ts", Language: "typescript",
		Occurrences: []*scip.Occurrence{
			{Range: []int32{0, 0, 0}, Symbol: prefix, SymbolRoles: scip.SymbolRoleDefinition, EnclosingRange: []int32{0, 0, 2, 0}},
			{Range: []int32{0, 16, 20}, Symbol: prefix + "keep().", SymbolRoles: scip.SymbolRoleDefinition, EnclosingRange: []int32{0, 0, 25}},
			{Range: []int32{1, 16, 20}, Symbol: prefix + "gone().", SymbolRoles: scip.SymbolRoleDefinition, EnclosingRange: []int32{1, 0, 25}},
		},
		Symbols: []*scip.SymbolInformation{{Symbol: prefix}, {Symbol: prefix + "keep()."}, {Symbol: prefix + "gone()."}},
	}
	base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"a.ts": old})
	lm := NewLineMap(diffOf(t, oldSrc, newSrc))
	doc, _ := RemapDocument(base.DocumentsByPath["a.ts"], lm)
	n, why := tsLayer1(doc, base, base.DocumentsByPath["a.ts"], []byte(newSrc), lm)
	if why != "" || n != 2 {
		t.Fatalf("added %d (%s), want added() and its parameter", n, why)
	}
	var occs, infos []string
	for _, o := range doc.Occurrences {
		occs = append(occs, strings.TrimPrefix(o.Symbol, prefix)+fmt.Sprint(o.Range))
	}
	for _, s := range doc.Symbols {
		infos = append(infos, strings.TrimPrefix(s.Symbol, prefix))
	}
	if got := strings.Join(occs, " "); got != "[0 0 0] keep().[0 16 20] added().[1 16 21] added().(x)[1 22 23]" {
		t.Errorf("occurrences %s", got)
	}
	if got := strings.Join(infos, ","); strings.Contains(got, "gone") || !strings.Contains(got, "added().") {
		t.Errorf("symbol information %s", got)
	}
}
