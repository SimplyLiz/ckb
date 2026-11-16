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
		"f().@5 5 6",  // ns.f
		"f().@9 9 10", // f()
	}
	sort.Strings(want)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("references\n got  %v\n want %v", got, want)
	}
	if added != len(want) {
		t.Errorf("added %d, want %d", added, len(want))
	}
	// x.n: a member of a parameter whose type is spelled but whose members
	// Layer 2 does not follow. h's f is its parameter, ext a package.
	if unresolved != 1 {
		t.Errorf("unresolved %d, want 1 (x.n)", unresolved)
	}
}
