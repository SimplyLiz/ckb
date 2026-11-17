//go:build cgo

package overlay

import (
	"os"
	"path/filepath"
	"regexp"
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
		// Parameters of top-level functions have global symbols; h's f
		// is its parameter, not the imported f.
		"scip-typescript npm app 1.0.0 src/`use.ts`/g().(x)@9 15 16",
		"scip-typescript npm app 1.0.0 src/`use.ts`/h().(f)@11 31 32",
	}
	sort.Strings(want)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("references\n got  %v\n want %v", got, want)
	}
	if added != len(want) {
		t.Errorf("added %d, want %d", added, len(want))
	}
	// ext is a package: not counted.
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
  const rec: Record<string, I> = { k: x }, n: Record<string, number> = { k: 1 };
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
	// The base index has the counter-numbered members under scip-typescript's
	// numbers, not Layer 1's placeholders.
	kept := lib.Occurrences[:0]
	for _, o := range lib.Occurrences {
		if !strings.Contains(o.Symbol, "typeLiteral") {
			kept = append(kept, o)
		}
	}
	lib.Occurrences = kept
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
		"Sep().(`{ text, quiet = false }`)typeLiteral9:text.@11 15 19",
		"Sep().(`{ text, quiet = false }`)typeLiteral9:quiet.@11 24 29",
		"I#q.@11 49 50",   // <Field q>, props: I
		"Base#b.@2 16 17", // x.b: inherited from Base
		"Base#b.@4 4 5",   // u.b: both parts' b is Base's
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
	// key on Sep, h's x.q (I is h's type parameter). s.length is a
	// primitive's member, a Record's keys have no declaration.
	if unresolved != 2 {
		t.Errorf("unresolved %d, want 2", unresolved)
	}
}

// TestTSNumberLiterals: a literal whose owner has one number keeps it, any
// other gets one above the file's highest; old lines are left to Layer 0.
func TestTSNumberLiterals(t *testing.T) {
	prefix := "scip-typescript npm app 1.0.0 src/`a.ts`/"
	doc := &scip.Document{Occurrences: []*scip.Occurrence{
		{Range: []int32{1, 0, 1}, Symbol: prefix + "T#typeLiteral7:a.", SymbolRoles: scip.SymbolRoleDefinition},
		{Range: []int32{9, 0, 1}, Symbol: prefix + "U#typeLiteral12:b.", SymbolRoles: 0}, // a reference still counts for the maximum
	}}
	// The base had V's literal on a line the change rewrote: Layer 0 dropped
	// the definition, the uses elsewhere keep 3.
	old := &scip.Document{Occurrences: []*scip.Occurrence{{Range: []int32{3, 0, 1}, Symbol: prefix + "V#typeLiteral3:c.", SymbolRoles: scip.SymbolRoleDefinition}}}
	lits := []tsLit{
		{owner: "V#", group: 3, decl: tsDecl{suffix: "c.", line: 3}},
		{owner: "T#", group: 0, decl: tsDecl{suffix: "b.", line: 2}},
		{owner: "T#", group: 0, decl: tsDecl{suffix: "a.", line: 1}}, // unchanged line
		{owner: "f().(p)", group: 1, decl: tsDecl{suffix: "x.", line: 5}},
		{owner: "f().(p)", group: 1, decl: tsDecl{suffix: "m().", line: 6}},
		{owner: "g().", group: 2, decl: tsDecl{suffix: "y.", line: 7}},
	}
	var got []string
	for _, d := range tsNumberLiterals(doc, old, prefix, lits, map[int32]bool{2: true, 3: true, 5: true, 6: true, 7: true}) {
		got = append(got, d.suffix)
	}
	want := "V#typeLiteral3:c. T#typeLiteral7:b. f().(p)typeLiteral13:x. f().(p)typeLiteral13:m(). g().typeLiteral14:y."
	if strings.Join(got, " ") != want {
		t.Errorf("got  %v\nwant %s", got, want)
	}
}

const tsLitFile = `import { I } from "./lib";
export function Sep({ text, quiet = false }: { text: string; quiet?: boolean }) { return text; }
export function linkText(l: { url: string }) { return l.url; }
export function F({ q, ...rest }: I) { return rest; }
export class K { m({ z }: { z: number }) { return z; } }
`

// TestTSLayer2Literals: Layer 1 defines a new literal's members (placeholder
// number), and Layer 2 points destructured names and l.url at them.
func TestTSLayer2Literals(t *testing.T) {
	root := t.TempDir()
	for rel, text := range map[string]string{"src/lib.ts": tsShapesLib, "src/s.ts": tsLitFile} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	head := "scip-typescript npm app 1.0.0 "
	seed := &scip.Document{RelativePath: "src/seed.ts", Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 0}, Symbol: head + "src/`seed.ts`/", SymbolRoles: scip.SymbolRoleDefinition}}}
	base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"src/seed.ts": seed})
	lib, _ := tsNewDocument(base, "src/lib.ts", []byte(tsShapesLib))
	base = scip.PatchIndex(base, map[string]*scip.Document{"src/lib.ts": lib})
	doc, _ := tsNewDocument(base, "src/s.ts", []byte(tsLitFile))
	r := newTSResolver(root, func(p string) *scip.Document {
		if p == "src/s.ts" {
			return doc
		}
		return base.DocumentsByPath[p]
	})
	_, unresolved := tsLayer2(doc, "src/s.ts", []byte(tsLitFile), allLines([]byte(tsLitFile)), r)

	sPrefix, libPrefix := head+"src/`s.ts`/", head+"src/`lib.ts`/"
	digits := regexp.MustCompile(`typeLiteral\d+`)
	got := map[string]bool{}
	for _, o := range doc.Occurrences {
		sym := strings.TrimPrefix(strings.TrimPrefix(o.Symbol, sPrefix), libPrefix)
		if strings.Contains(sym, "typeLiteral") || strings.HasPrefix(sym, "I#") {
			role := "ref"
			if o.SymbolRoles&scip.SymbolRoleDefinition != 0 {
				role = "def"
			}
			got[role+" "+digits.ReplaceAllString(sym, "typeLiteralN")+"@"+fmtRange(o)] = true
		}
	}
	for _, w := range []string{
		"def Sep().(`{ text, quiet = false }`)typeLiteralN:text.@1 47 51",
		"def Sep().(`{ text, quiet = false }`)typeLiteralN:quiet.@1 61 66",
		"ref Sep().(`{ text, quiet = false }`)typeLiteralN:text.@1 22 26",
		"ref Sep().(`{ text, quiet = false }`)typeLiteralN:quiet.@1 28 33",
		"def linkText().(l)typeLiteralN:url.@2 30 33",
		"ref linkText().(l)typeLiteralN:url.@2 56 59",
		"ref I#q.@3 20 21",
		"def K#m().(`{ z }`)typeLiteralN:z.@4 28 29",
		"ref K#m().(`{ z }`)typeLiteralN:z.@4 21 22",
	} {
		if !got[w] {
			t.Errorf("missing %s", w)
		}
		delete(got, w)
	}
	for k := range got {
		if strings.HasPrefix(k, "ref") && !strings.HasPrefix(k, "ref I#@") {
			t.Errorf("unexpected %s", k)
		}
	}
	if unresolved != 0 {
		t.Errorf("unresolved %d, want 0", unresolved)
	}
}

// TestTSConfigPaths: the root tsconfig.json's project and the projects it
// references govern, as when ckb index runs scip-typescript at the root;
// extends is followed (paths relative to the config that sets them, baseUrl
// to its own), and a package's tsconfig nothing references is not read.
func TestTSConfigPaths(t *testing.T) {
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
	write("configs/base.json", `{ "compilerOptions": { "paths": { "@lib/*": ["../lib/*"] } } }`)
	write("tsconfig.json", `{ "extends": "./configs/base.json", "references": [{ "path": "./packages/api" }, { "path": "./apps/vite" }] }`)
	write("lib/a.ts", "export const a = 1;\n")
	write("packages/api/tsconfig.json", `{ "extends": "../../configs/base", "compilerOptions": { "baseUrl": "src", "paths": { "~/*": ["*"] } } }`)
	write("packages/api/src/b.ts", "export const b = 1;\n")
	write("packages/web/tsconfig.json", `{ "compilerOptions": { "paths": { "#/*": ["./src/*"] } } }`) // not referenced
	write("packages/web/src/w.ts", "export const w = 1;\n")
	write("apps/vite/tsconfig.json", `{ "files": [], "references": [{ "path": "./tsconfig.app.json" }, { "path": "./tsconfig.node.json" }] }`)
	write("apps/vite/tsconfig.app.json", `{ "compilerOptions": { "paths": { "@/*": ["./src/*"] } } }`)
	write("apps/vite/tsconfig.node.json", `{ "include": ["vite.config.ts"] }`)
	write("apps/vite/src/c.ts", "export const c = 1;\n")
	indexed := map[string]bool{"lib/a.ts": true, "packages/api/src/b.ts": true, "packages/web/src/w.ts": true, "apps/vite/src/c.ts": true}
	r := newTSResolver(root, func(p string) *scip.Document {
		if indexed[p] {
			return &scip.Document{RelativePath: p}
		}
		return nil
	})
	for _, c := range []struct{ from, spec, want string }{
		{"src/x.ts", "@lib/a", "lib/a.ts"},                        // the root's paths, from the extended file
		{"packages/api/src/x.ts", "~/b", "packages/api/src/b.ts"}, // a referenced project: own paths, own baseUrl
		{"packages/api/src/x.ts", "@lib/a", ""},                   // its paths replace the parent's
		{"packages/web/src/x.ts", "#/w", ""},                      // not referenced: the root's paths
		{"packages/web/src/x.ts", "@lib/a", "lib/a.ts"},
		{"apps/vite/src/x.ts", "@/c", "apps/vite/src/c.ts"}, // a solution config's references
		{"apps/vite/src/x.ts", "@lib/a", ""},
	} {
		if got := r.resolveModule(c.from, c.spec); got != c.want {
			t.Errorf("%s from %s: got %q, want %q", c.spec, c.from, got, c.want)
		}
	}
}

// TestTSWorkspaces: a workspace package's specifier resolves to its file
// through exports, types or main, as the package manager's symlink lets
// TypeScript see it.
func TestTSWorkspaces(t *testing.T) {
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
	write("package.json", `{ "workspaces": ["packages/*"] }`)
	write("pnpm-workspace.yaml", "packages:\n  - 'apps/*' # the apps\n  - '!apps/old'\n")
	write("packages/spur/package.json", `{ "name": "@m/spur", "exports": "./src/index.ts" }`)
	write("packages/spur/src/index.ts", "export const a = 1;\n")
	write("packages/kern/package.json", `{ "name": "@m/kern", "exports": { ".": { "types": "./src/main.ts", "default": "./dist/main.js" }, "./util/*": "./src/util/*.ts" } }`)
	write("packages/kern/src/main.ts", "export const b = 1;\n")
	write("packages/kern/src/util/x.ts", "export const x = 1;\n")
	write("apps/web/package.json", `{ "name": "web", "types": "lib/entry.ts" }`)
	write("apps/web/lib/entry.ts", "export const c = 1;\n")
	write("apps/web/lib/other.ts", "export const d = 1;\n")
	indexed := map[string]bool{"packages/spur/src/index.ts": true, "packages/kern/src/main.ts": true, "packages/kern/src/util/x.ts": true, "apps/web/lib/entry.ts": true, "apps/web/lib/other.ts": true}
	r := newTSResolver(root, func(p string) *scip.Document {
		if indexed[p] {
			return &scip.Document{RelativePath: p}
		}
		return nil
	})
	for spec, want := range map[string]string{
		"@m/spur":        "packages/spur/src/index.ts",
		"@m/kern":        "packages/kern/src/main.ts",
		"@m/kern/util/x": "packages/kern/src/util/x.ts",
		"@m/kern/other":  "", // not in exports
		"web":            "apps/web/lib/entry.ts",
		"web/lib/other":  "apps/web/lib/other.ts",
		"react":          "",
	} {
		if got := r.resolveModule("apps/web/src/a.ts", spec); got != want {
			t.Errorf("%s: got %q, want %q", spec, got, want)
		}
	}
}

const tsExpLib = `export const other = 1;
export type T2 = number;
export const both = 1;
export type both = string;
`

const tsExpUse = `import { other as o3, T2, both as b2 } from "./lib";
import * as ns from "./lib";
function foo() { return 1; }
type U = number;
export { foo as baz, U, o3, ns, b2, T2 };
export { other as o2, both as b3, both } from "./lib";
`

// TestTSLayer2ExportClauses: export { … } with and without a source, and
// import aliases. The want list is scip-typescript's index of these two
// files, except the names of aliased value-and-type names, which only count.
func TestTSLayer2ExportClauses(t *testing.T) {
	root := t.TempDir()
	for rel, text := range map[string]string{"src/lib.ts": tsExpLib, "src/use.ts": tsExpUse} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	head := "scip-typescript npm app 1.0.0 "
	seed := &scip.Document{RelativePath: "src/seed.ts", Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 0}, Symbol: head + "src/`seed.ts`/", SymbolRoles: scip.SymbolRoleDefinition}}}
	base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"src/seed.ts": seed})
	lib, _ := tsNewDocument(base, "src/lib.ts", []byte(tsExpLib))
	base = scip.PatchIndex(base, map[string]*scip.Document{"src/lib.ts": lib})
	use, _ := tsNewDocument(base, "src/use.ts", []byte(tsExpUse))
	if lib == nil || use == nil {
		t.Fatal("documents not synthesized")
	}
	r := newTSResolver(root, func(p string) *scip.Document {
		if p == "src/use.ts" {
			return use
		}
		return base.DocumentsByPath[p]
	})
	_, unresolved := tsLayer2(use, "src/use.ts", []byte(tsExpUse), allLines([]byte(tsExpUse)), r)

	libSym, useSym := head+"src/`lib.ts`/", head+"src/`use.ts`/"
	got := map[string]bool{}
	for _, o := range use.Occurrences {
		if o.Source == scip.OccurrenceSourceOverlay {
			got[strings.NewReplacer(libSym, "lib:", useSym, "use:").Replace(o.Symbol)+"@"+fmtRange(o)] = true
		}
	}
	want := []string{
		"lib:other.@0 9 14", "lib:other.@0 18 20", "lib:T2#@0 22 24",
		"lib:both.@0 34 36", "lib:both#@0 34 36", "lib:@0 44 51",
		"use:foo().@4 9 12", "use:foo().@4 16 19", "use:U#@4 21 22", "lib:other.@4 24 26", "lib:@4 28 30",
		"lib:both.@4 32 34", "lib:both#@4 32 34", "lib:T2#@4 36 38",
		"lib:other.@5 9 14", "lib:other.@5 18 20", "lib:both.@5 30 32", "lib:both#@5 30 32",
		"lib:both.@5 34 38", "lib:both#@5 34 38", "lib:@5 46 53",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing %s", w)
		}
		delete(got, w)
	}
	for g := range got {
		if !strings.HasPrefix(g, "lib:@1 ") { // import * as ns: covered elsewhere
			t.Errorf("unexpected %s", g)
		}
	}
	if unresolved != 2 { // both in "both as b2" and "both as b3"
		t.Errorf("unresolved %d, want 2", unresolved)
	}
}
