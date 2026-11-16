//go:build cgo

package overlay

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

const tsLib = `export function f() { return 1; }
export class C { constructor() {} static s = 1; }
export enum E { A }
export interface T { n: number }
`

const tsUse = `import { f, C, E, T } from "@/lib";
import * as ns from "./lib";
import ext from "react";
export function g(x: T): number {
  const c = new C();
  ns.f();
  E.A;
  C.s;
  ext.x;
  return f() + x.n;
}
function h(f: number) { return f; }
`

func TestTSLayer2(t *testing.T) {
	root := t.TempDir()
	write := func(rel, text string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tsconfig.json", `{
  // comments and trailing commas, as tsconfig allows
  "compilerOptions": { "paths": { "@/*": ["./src/*"], }, },
}`)
	write("src/lib.ts", tsLib)
	write("src/use.ts", tsUse)

	head := "scip-typescript npm app 1.0.0 "
	seed := &scip.Document{RelativePath: "src/seed.ts", Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 0}, Symbol: head + "src/`seed.ts`/", SymbolRoles: scip.SymbolRoleDefinition}}}
	base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"src/seed.ts": seed})
	lib, _ := tsNewDocument(base, "src/lib.ts", []byte(tsLib))
	base = scip.PatchIndex(base, map[string]*scip.Document{"src/lib.ts": lib})
	use, _ := tsNewDocument(base, "src/use.ts", []byte(tsUse))
	if lib == nil || use == nil {
		t.Fatal("documents not synthesized")
	}
	r := newTSResolver(root, func(p string) *scip.Document {
		if p == "src/use.ts" {
			return use
		}
		return base.DocumentsByPath[p]
	})
	added, unresolved := tsLayer2(use, "src/use.ts", []byte(tsUse), allLines([]byte(tsUse)), r)

	libSym := head + "src/`lib.ts`/"
	var got []string
	for _, o := range use.Occurrences {
		if o.Source != scip.OccurrenceSourceOverlay {
			continue
		}
		got = append(got, strings.TrimPrefix(o.Symbol, libSym)+"@"+fmtRange(o))
	}
	sort.Strings(got)
	want := []string{
		"@0 27 34",                     // "@/lib" -> the file
		"@1 20 27",                     // "./lib"
		"C#@0 12 13",                   // import specifier
		"C#`<constructor>`().@4 16 17", // new C()
		"C#@7 2 3",                     // C in C.s
		"C#s.@7 4 5",                   // static member
		"E#@0 15 16",
		"E#@6 2 3",
		"E#A.@6 4 5", // enum member
		"T#@0 18 19",
		"T#@3 21 22", // type annotation
		"f().@0 9 10",
		"f().@5 5 6",   // ns.f
		"f().@9 9 10",  // f()
		"T#n.@9 17 18", // x.n, x: T
	}
	sort.Strings(want)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("references\n got  %v\n want %v", got, want)
	}
	if added != len(want) {
		t.Errorf("added %d, want %d", added, len(want))
	}
	// h's f is its parameter, ext a package: neither is counted.
	if unresolved != 0 {
		t.Errorf("unresolved %d, want 0", unresolved)
	}
}

const tsShapesLib = `export interface Base { b: number }
export interface I extends Base { q: number; m(): void }
export type L = { a: number; k(): void };
export type U = Base | I;
export function Sep({ text, quiet = false }: { text: string; quiet?: boolean }) { return text; }
export function Field(props: I) { return props; }
`

const tsShapesUse = `import { I, L, U, Sep, Field } from "./lib";
export function g(x: I, y: L | undefined, u: U, s: string) {
  x.q; x.m(); x.b;
  y!.a; y?.k();
  u.b;
  s.length;
  (s as unknown as I).q;
  const o: L = { a: 1, k() {} };
  const a = 1;
  const p = { a } as L;
  return [<Sep text="t" quiet key="k" />, <Field q={1} />];
}
function h<I>(x: I) { return x.q; }
`

// TestTSLayer2TypedMembers: members of spelled types, JSX attributes of a
// component's first parameter, keys of objects with a spelled type, and a
// type literal's members under the number the document has.
func TestTSLayer2TypedMembers(t *testing.T) {
	root := t.TempDir()
	for rel, text := range map[string]string{"src/lib.ts": tsShapesLib, "src/use.tsx": tsShapesUse} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	head := "scip-typescript npm app 1.0.0 "
	libSym := head + "src/`lib.ts`/"
	seed := &scip.Document{RelativePath: "src/seed.ts", Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 0}, Symbol: head + "src/`seed.ts`/", SymbolRoles: scip.SymbolRoleDefinition}}}
	base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"src/seed.ts": seed})
	lib, _ := tsNewDocument(base, "src/lib.ts", []byte(tsShapesLib))
	// The counter-numbered members scip-typescript writes and Layer 1 leaves
	// out; the base index has them.
	for _, s := range []string{"L#typeLiteral7:a.", "L#typeLiteral7:k().", "Sep().(`{ text, quiet = false }`)typeLiteral9:quiet.", "Sep().(`{ text, quiet = false }`)typeLiteral9:text."} {
		lib.Occurrences = append(lib.Occurrences, &scip.Occurrence{Range: []int32{9, 0, 1}, Symbol: libSym + s, SymbolRoles: scip.SymbolRoleDefinition})
	}
	base = scip.PatchIndex(base, map[string]*scip.Document{"src/lib.ts": lib})
	use, _ := tsNewDocument(base, "src/use.tsx", []byte(tsShapesUse))
	r := newTSResolver(root, func(p string) *scip.Document {
		if p == "src/use.tsx" {
			return use
		}
		return base.DocumentsByPath[p]
	})
	_, unresolved := tsLayer2(use, "src/use.tsx", []byte(tsShapesUse), allLines([]byte(tsShapesUse)), r)

	got := map[string]bool{}
	for _, o := range use.Occurrences {
		if o.Source == scip.OccurrenceSourceOverlay && strings.Contains(o.Symbol, "#") || strings.Contains(o.Symbol, ")typeLiteral") {
			got[strings.TrimPrefix(o.Symbol, libSym)+"@"+fmtRange(o)] = true
		}
	}
	for _, w := range []string{
		"I#q.@2 4 5", "I#m().@2 9 10", // x: I
		"L#typeLiteral7:a.@3 5 6", "L#typeLiteral7:k().@3 11 12", // y: L | undefined, through ! and ?.
		"I#q.@6 22 23",                // (… as I).q
		"L#typeLiteral7:a.@7 17 18",   // const o: L = { a: … }
		"L#typeLiteral7:k().@7 23 24", // { k() {} }
		"L#typeLiteral7:a.@9 14 15",   // { a } as L: the property beside the local
		"Sep().(`{ text, quiet = false }`)typeLiteral9:text.@10 15 19",
		"Sep().(`{ text, quiet = false }`)typeLiteral9:quiet.@10 24 29",
		"I#q.@10 49 50", // <Field q>, props: I
	} {
		if !got[w] {
			t.Errorf("missing %s", w)
		}
		delete(got, w)
	}
	for k := range got {
		if strings.Contains(k, "#q.") || strings.Contains(k, "#b.") || strings.Contains(k, "typeLiteral") {
			t.Errorf("unexpected %s", k)
		}
	}
	// x.b (inherited from Base), u.b (a union of two types), key on Sep,
	// h's x.q (I is h's type parameter). s.length is a primitive's member.
	if unresolved != 4 {
		t.Errorf("unresolved %d, want 4", unresolved)
	}
}
