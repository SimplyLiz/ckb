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

const tsTypeParamFile = "export function f<T extends object, K extends keyof T>(o: T, k: K): T[K] { return o[k]; }\n" +
	"export class C<E> { items: E[] = []; m<R>(x: R): E | R { const g = <Z>(z: Z): Z => z; return x; } }\n" +
	"export interface I<V> { v: V; w: Array<V> }\n" +
	"export type M<Route extends string> = Record<Route, number> & { [P in Route]: P };\n" +
	"export const h = <Q,>(q: Q): Q => q;\n" +
	"function outer<O>() { function inner<O>(o: O): O { return o; } return inner; }\n" +
	"export type First<T> = T extends (...a: [infer T]) => any ? T : never;\n"

// TestTSLayer2TypeParams: a reference to a type parameter with a global
// symbol (a top-level function's, a class's, a method's, an interface's, a
// type alias's) gets it; one of an arrow function or a nested function is a
// local. The want list is scip-typescript's index of the file. Where infer
// T shadows T, no T gets an edge (the index has First#[T] at the first).
func TestTSLayer2TypeParams(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src/a.ts"), []byte(tsTypeParamFile), 0o644); err != nil {
		t.Fatal(err)
	}
	head := "scip-typescript npm app 1.0.0 "
	seed := &scip.Document{RelativePath: "src/seed.ts", Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 0}, Symbol: head + "src/`seed.ts`/", SymbolRoles: scip.SymbolRoleDefinition}}}
	base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"src/seed.ts": seed})
	doc, _ := tsNewDocument(base, "src/a.ts", []byte(tsTypeParamFile))
	if doc == nil {
		t.Fatal("document not synthesized")
	}
	r := newTSResolver(root, func(p string) *scip.Document {
		if p == "src/a.ts" {
			return doc
		}
		return base.DocumentsByPath[p]
	})
	tsLayer2(doc, "src/a.ts", []byte(tsTypeParamFile), allLines([]byte(tsTypeParamFile)), r)
	prefix := head + "src/`a.ts`/"
	var got []string
	for _, o := range doc.Occurrences {
		if o.Source == scip.OccurrenceSourceOverlay && strings.HasSuffix(o.Symbol, "]") {
			got = append(got, strings.TrimPrefix(o.Symbol, prefix)+"@"+fmtRange(o))
		}
	}
	sort.Strings(got)
	want := []string{
		"f().[T]@0 52 53", "f().[T]@0 58 59", "f().[T]@0 68 69", "f().[K]@0 64 65", "f().[K]@0 70 71",
		"C#[E]@1 27 28", "C#[E]@1 49 50", "C#m().[R]@1 45 46", "C#m().[R]@1 53 54",
		"I#[V]@2 27 28", "I#[V]@2 39 40",
		"M#[Route]@3 45 50", "M#[Route]@3 70 75",
	}
	sort.Strings(want)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("type parameter references\n got  %v\n want %v", got, want)
	}
}

const tsLookupFile = `export interface S { status: "a" | "b"; nested: { deep: number }; list: { item: string }[]; "/x": number }
export type L = { kind: string; run(): void };
export type A = S["status"];
export type B = S['nested']["deep"];
export type C = S["list"][number]["item"];
export type D = L["kind"] | L["run"];
export type E = (S | L)["kind"];
export type F = S["/x"];
export type G = Partial<S>["status"];
export type H = S["status" | "nested"];
`

// TestTSLayer2LookupTypes: T["k"] points the string, quotes included, at
// T's member k, through nested lookups and Partial; a union of types or of
// keys gets nothing. The want list is scip-typescript's index of the file
// (C's "item" is a local there, Layer 2 counts it; F's string-named member
// keeps its quotes).
func TestTSLayer2LookupTypes(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src/a.ts"), []byte(tsLookupFile), 0o644); err != nil {
		t.Fatal(err)
	}
	head := "scip-typescript npm app 1.0.0 "
	seed := &scip.Document{RelativePath: "src/seed.ts", Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 0}, Symbol: head + "src/`seed.ts`/", SymbolRoles: scip.SymbolRoleDefinition}}}
	base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"src/seed.ts": seed})
	doc, _ := tsNewDocument(base, "src/a.ts", []byte(tsLookupFile))
	if doc == nil {
		t.Fatal("document not synthesized")
	}
	r := newTSResolver(root, func(p string) *scip.Document {
		if p == "src/a.ts" {
			return doc
		}
		return base.DocumentsByPath[p]
	})
	_, unresolved := tsLayer2(doc, "src/a.ts", []byte(tsLookupFile), allLines([]byte(tsLookupFile)), r)
	prefix := head + "src/`a.ts`/"
	var got []string
	for _, o := range doc.Occurrences {
		if o.Source == scip.OccurrenceSourceOverlay && o.Range[0] >= 2 && strings.Contains(o.Symbol, "#") && !strings.HasSuffix(o.Symbol, "#") {
			got = append(got, strings.TrimPrefix(o.Symbol, prefix)+"@"+fmtRange(o))
		}
	}
	sort.Strings(got)
	want := []string{
		"S#status.@2 18 26",
		"S#nested.@3 18 26", "S#nested.typeLiteral0:deep.@3 28 34",
		"S#list.@4 18 24",
		// typeLiteral4 in the index: a new file's literals are numbered
		// by Layer 1 until the next reindex.
		"L#typeLiteral1:kind.@5 18 24", "L#typeLiteral1:run().@5 30 35",
		"S#status.@8 27 35",
		"S#`\"/x\"`.@7 18 22",
	}
	sort.Strings(want)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("lookup type members\n got  %v\n want %v", got, want)
	}
	// C's "item".
	if unresolved != 1 {
		t.Errorf("unresolved %d, want 1", unresolved)
	}
}

const tsNullCtxFile = `export interface E { t?: { a: number }; u: { b: number } | undefined }
let x: E["t"]; x = { a: 1 };
let y: { a: number } | undefined; y = { a: 1 };
let w: E["u"]; w = { b: 1 };
export function f(p?: { c: number }) { return p; }
f({ c: 1 });
const z: E["t"] = { a: 2 };
`

// TestTSStrictNullContext: a literal whose context is T | undefined has its
// own keys under strictNullChecks, T's members without it (TypeScript drops
// undefined from the union). Both want lists are scip-typescript's index of
// the file under that tsconfig (f's literal is typeLiteral7 there: a new
// file's literals are numbered by Layer 1).
func TestTSStrictNullContext(t *testing.T) {
	for _, tc := range []struct {
		config string
		want   []string
	}{
		{`{ "compilerOptions": { "strict": true } }`, nil},
		{`{ "compilerOptions": { "strict": true, "strictNullChecks": false } }`, []string{"E#t.typeLiteral0:a.@1 21 22", "f().(p)typeLiteral1:c.@5 4 5", "E#t.typeLiteral0:a.@6 20 21"}},
		{`{}`, []string{"E#t.typeLiteral0:a.@1 21 22", "f().(p)typeLiteral1:c.@5 4 5", "E#t.typeLiteral0:a.@6 20 21"}},
	} {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(tc.config), 0o644); err != nil {
			t.Fatal(err)
		}
		head := "scip-typescript npm app 1.0.0 "
		seed := &scip.Document{RelativePath: "src/seed.ts", Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 0}, Symbol: head + "src/`seed.ts`/", SymbolRoles: scip.SymbolRoleDefinition}}}
		base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"src/seed.ts": seed})
		doc, _ := tsNewDocument(base, "src/a.ts", []byte(tsNullCtxFile))
		if doc == nil {
			t.Fatal("document not synthesized")
		}
		r := newTSResolver(root, func(p string) *scip.Document {
			if p == "src/a.ts" {
				return doc
			}
			return base.DocumentsByPath[p]
		})
		tsLayer2(doc, "src/a.ts", []byte(tsNullCtxFile), allLines([]byte(tsNullCtxFile)), r)
		prefix := head + "src/`a.ts`/"
		var got []string
		for _, o := range doc.Occurrences {
			if o.Source == scip.OccurrenceSourceOverlay && strings.Contains(o.Symbol, "typeLiteral") {
				got = append(got, strings.TrimPrefix(o.Symbol, prefix)+"@"+fmtRange(o))
			}
		}
		sort.Strings(got)
		sort.Strings(tc.want)
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("%s\n got  %v\n want %v", tc.config, got, tc.want)
		}
	}
}

const tsLocalCtxFile = `export interface D { p: number }
export type S = { l: { d: D } | null };
export const s: S = { l: { d: { p: 1 } } };
export function g(c: D, arr: D[]) {
  const r = { ...c, p: 2 };
  return arr.map((x) => ({ ...x, p: 1 }));
}
`

// TestTSLayer2LocalMemberContext: a literal under a local member (d of a
// literal in a union) takes the member's followed type, D. The keys of a
// callback's result that spreads a repository value count: scip-typescript
// points them at the inferred result's property. Both from scip-typescript's
// index of the file; Layer 1 writes no locals, so d's is added.
func TestTSLayer2LocalMemberContext(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src/a.ts"), []byte(tsLocalCtxFile), 0o644); err != nil {
		t.Fatal(err)
	}
	head := "scip-typescript npm app 1.0.0 "
	seed := &scip.Document{RelativePath: "src/seed.ts", Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 0}, Symbol: head + "src/`seed.ts`/", SymbolRoles: scip.SymbolRoleDefinition}}}
	base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"src/seed.ts": seed})
	doc, _ := tsNewDocument(base, "src/a.ts", []byte(tsLocalCtxFile))
	if doc == nil {
		t.Fatal("document not synthesized")
	}
	doc.Occurrences = append(doc.Occurrences, &scip.Occurrence{Range: []int32{1, 23, 24}, Symbol: "local 3", SymbolRoles: scip.SymbolRoleDefinition})
	r := newTSResolver(root, func(p string) *scip.Document {
		if p == "src/a.ts" {
			return doc
		}
		return base.DocumentsByPath[p]
	})
	_, unresolved := tsLayer2(doc, "src/a.ts", []byte(tsLocalCtxFile), allLines([]byte(tsLocalCtxFile)), r)
	prefix := head + "src/`a.ts`/"
	var got []string
	for _, o := range doc.Occurrences {
		if o.Source == scip.OccurrenceSourceOverlay && strings.HasPrefix(o.Symbol, prefix+"D#p.") {
			got = append(got, fmtRange(o))
		}
	}
	if strings.Join(got, ",") != "2 32 33" {
		t.Errorf("D#p. at %v, want 2 32 33 only", got)
	}
	if unresolved != 1 { // p in the map callback
		t.Errorf("unresolved %d, want 1", unresolved)
	}
}

const tsMapCtxFile = `export interface E { a: number }
const m = new Map<string, E>();
export function f(k: string) {
  m.set(k, { a: 1 });
  let w = m.get(k);
  if (!w) { w = { a: 2 }; }
  const x = m.get(k)!;
  return x.a + w.a;
}
`

// TestTSLayer2NewMap: new Map<K, V>() is a map of V, so set's literal takes
// V's members. get is V | undefined: a member access looks through it, a
// literal assigned to the variable has its own keys under strictNullChecks.
func TestTSLayer2NewMap(t *testing.T) {
	for _, tc := range []struct {
		config string
		want   string
	}{
		{`{ "compilerOptions": { "strict": true } }`, "3 13 14,7 11 12,7 17 18"},
		{`{}`, "3 13 14,5 18 19,7 11 12,7 17 18"},
	} {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(tc.config), 0o644); err != nil {
			t.Fatal(err)
		}
		head := "scip-typescript npm app 1.0.0 "
		seed := &scip.Document{RelativePath: "src/seed.ts", Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 0}, Symbol: head + "src/`seed.ts`/", SymbolRoles: scip.SymbolRoleDefinition}}}
		base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"src/seed.ts": seed})
		doc, _ := tsNewDocument(base, "src/a.ts", []byte(tsMapCtxFile))
		if doc == nil {
			t.Fatal("document not synthesized")
		}
		r := newTSResolver(root, func(p string) *scip.Document {
			if p == "src/a.ts" {
				return doc
			}
			return base.DocumentsByPath[p]
		})
		tsLayer2(doc, "src/a.ts", []byte(tsMapCtxFile), allLines([]byte(tsMapCtxFile)), r)
		var got []string
		for _, o := range doc.Occurrences {
			if o.Source == scip.OccurrenceSourceOverlay && o.Symbol == head+"src/`a.ts`/E#a." {
				got = append(got, fmtRange(o))
			}
		}
		sort.Strings(got)
		if strings.Join(got, ",") != tc.want {
			t.Errorf("%s: E#a. at %v, want %s", tc.config, got, tc.want)
		}
	}
}

const tsObjExprFile = `import { ext } from "pkg";
export function theme() {
  return { warn: 1, error: (s: string) => s };
}
export async function inner() { return { path: "", bytes: 1 }; }
export async function outer() { return inner(); }
export async function g() {
  const { warn, error } = theme();
  const w = await outer();
  const s = { ...ext, warn: 2 };
  return [warn, error(""), w.path, theme().warn, s.warn];
}
`

// TestTSLayer2ObjectExpression: an object literal is a type whose members
// are the symbols the declaring document defines at its keys
// (scip-typescript's warn0:; Layer 1 writes none, so the test adds them),
// through a function's inferred result and a returned promise, which async
// does not wrap again. Beside a spread of a package's value the key is
// counted: the index has nothing there.
func TestTSLayer2ObjectExpression(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src/a.ts"), []byte(tsObjExprFile), 0o644); err != nil {
		t.Fatal(err)
	}
	head := "scip-typescript npm app 1.0.0 "
	prefix := head + "src/`a.ts`/"
	seed := &scip.Document{RelativePath: "src/seed.ts", Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 0}, Symbol: head + "src/`seed.ts`/", SymbolRoles: scip.SymbolRoleDefinition}}}
	base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"src/seed.ts": seed})
	doc, _ := tsNewDocument(base, "src/a.ts", []byte(tsObjExprFile))
	if doc == nil {
		t.Fatal("document not synthesized")
	}
	for _, d := range []struct {
		line, col int32
		sym       string
	}{{2, 11, "warn0:"}, {2, 20, "error0:"}, {4, 41, "path0:"}, {4, 51, "bytes0:"}, {9, 22, "warn1:"}} {
		n := int32(len(strings.TrimRight(d.sym, "0123456789:")))
		doc.Occurrences = append(doc.Occurrences, &scip.Occurrence{Range: []int32{d.line, d.col, d.col + n}, Symbol: prefix + d.sym, SymbolRoles: scip.SymbolRoleDefinition})
	}
	r := newTSResolver(root, func(p string) *scip.Document {
		if p == "src/a.ts" {
			return doc
		}
		return base.DocumentsByPath[p]
	})
	_, unresolved := tsLayer2(doc, "src/a.ts", []byte(tsObjExprFile), allLines([]byte(tsObjExprFile)), r)
	var got []string
	for _, o := range doc.Occurrences {
		if o.Source == scip.OccurrenceSourceOverlay && strings.HasSuffix(o.Symbol, ":") {
			got = append(got, strings.TrimPrefix(o.Symbol, prefix)+"@"+fmtRange(o))
		}
	}
	sort.Strings(got)
	want := "error0:@7 16 21 path0:@10 29 33 warn0:@10 43 47 warn0:@7 10 14"
	if strings.Join(got, " ") != want {
		t.Errorf("got  %v\nwant %s", got, want)
	}
	if unresolved != 1 { // s.warn beside ...ext
		t.Errorf("unresolved %d, want 1", unresolved)
	}
}

// TestTSLayer2TypeofImport: typeof import("m").x points at m's export x,
// followed through a barrel's export *, as scip-typescript's index does.
// A name re-exported by name (export { n as m } from) is a local there, and a
// package's member has no edge: neither gets one nor counts.
func TestTSLayer2TypeofImport(t *testing.T) {
	files := map[string]string{
		"src/lib.ts":    "export function load() { return 1; }\nexport const n = 1;\n",
		"src/barrel.ts": "export * from \"./lib\";\nexport { n as m } from \"./lib\";\n",
		"src/use.ts": "export type A = typeof import(\"./lib\").load;\n" +
			"export type B = ReturnType<typeof import(\"./barrel\").load>;\n" +
			"export type C = typeof import(\"./barrel\").m;\n" +
			"export type D = typeof import(\"react\").useState;\n" +
			"export type E = typeof import(\"./lib\").missing;\n",
	}
	root := t.TempDir()
	for rel, text := range files {
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
	for _, rel := range []string{"src/lib.ts", "src/barrel.ts"} {
		d, _ := tsNewDocument(base, rel, []byte(files[rel]))
		if d == nil {
			t.Fatalf("%s not synthesized", rel)
		}
		base = scip.PatchIndex(base, map[string]*scip.Document{rel: d})
	}
	src := []byte(files["src/use.ts"])
	use, _ := tsNewDocument(base, "src/use.ts", src)
	if use == nil {
		t.Fatal("use.ts not synthesized")
	}
	r := newTSResolver(root, func(p string) *scip.Document {
		if p == "src/use.ts" {
			return use
		}
		return base.DocumentsByPath[p]
	})
	_, unresolved := tsLayer2(use, "src/use.ts", src, allLines(src), r)
	r.close()

	libSym := head + "src/`lib.ts`/"
	got := map[string]bool{}
	for _, o := range use.Occurrences {
		if o.Source == scip.OccurrenceSourceOverlay && strings.HasPrefix(o.Symbol, libSym) && strings.TrimPrefix(o.Symbol, libSym) != "" {
			got[strings.TrimPrefix(o.Symbol, libSym)+"@"+fmtRange(o)] = true
		}
	}
	for _, w := range []string{"load().@0 39 43", "load().@1 53 57"} {
		if !got[w] {
			t.Errorf("missing %s", w)
		}
		delete(got, w)
	}
	for g := range got {
		t.Errorf("unexpected %s", g)
	}
	if unresolved != 1 { // missing
		t.Errorf("unresolved %d, want 1", unresolved)
	}
}

// TestTSLayer2ParamProperties: a constructor parameter with a modifier is a
// member, and scip-typescript's symbol for it is the parameter's; a plain
// parameter is not a member.
func TestTSLayer2ParamProperties(t *testing.T) {
	files := map[string]string{
		"src/lib.ts": "export class E extends Error {\n" +
			"  constructor(readonly reason: string, public code?: R, z = 1) { super(); }\n" +
			"  is() { return this.reason === \"x\"; }\n" +
			"}\n" +
			"export interface R { n: number }\n" +
			"export class F extends E {}\n",
		"src/use.ts": "import { E, F } from \"./lib\";\n" +
			"export const a = (e: E) => e.reason === \"y\" || e.code?.n;\n" +
			"export const b = (f: F) => f.reason + f.z;\n",
	}
	root := t.TempDir()
	for rel, text := range files {
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
	lib, _ := tsNewDocument(base, "src/lib.ts", []byte(files["src/lib.ts"]))
	if lib == nil {
		t.Fatal("lib.ts not synthesized")
	}
	base = scip.PatchIndex(base, map[string]*scip.Document{"src/lib.ts": lib})
	docs := map[string]*scip.Document{}
	r := newTSResolver(root, func(p string) *scip.Document {
		if d := docs[p]; d != nil {
			return d
		}
		return base.DocumentsByPath[p]
	})
	got := map[string]bool{}
	libSym := head + "src/`lib.ts`/"
	for _, rel := range []string{"src/lib.ts", "src/use.ts"} {
		src := []byte(files[rel])
		d, _ := tsNewDocument(base, rel, src)
		if d == nil {
			t.Fatalf("%s not synthesized", rel)
		}
		docs[rel] = d
		tsLayer2(d, rel, src, allLines(src), r)
		for _, o := range d.Occurrences {
			if o.Source == scip.OccurrenceSourceOverlay && strings.HasPrefix(o.Symbol, libSym+"E#") || o.Source == scip.OccurrenceSourceOverlay && strings.HasPrefix(o.Symbol, libSym+"R#") {
				got[rel[4:]+":"+strings.TrimPrefix(o.Symbol, libSym)+"@"+fmtRange(o)] = true
			}
		}
	}
	r.close()
	ctor := "E#`<constructor>`()."
	for _, w := range []string{
		"lib.ts:" + ctor + "(reason)@2 21 27", // this.reason
		"use.ts:" + ctor + "(reason)@1 29 35",
		"use.ts:" + ctor + "(code)@1 49 53",
		"use.ts:R#n.@1 55 56",                 // the parameter's type followed
		"use.ts:" + ctor + "(reason)@2 29 35", // inherited by F
	} {
		if !got[w] {
			t.Errorf("missing %s", w)
		}
		delete(got, w)
	}
	for g := range got {
		if !strings.HasPrefix(g, "use.ts:E#@") && !strings.HasPrefix(g, "lib.ts:R#@") && !strings.HasPrefix(g, "lib.ts:E#@") {
			t.Errorf("unexpected %s", g)
		}
	}
}

const tsRoutesFile = `type R = "/a/[id]" | "/b"
interface ParamMap {
  "/a/[id]": { "id": string; }
  "/b": {}
}
export type { ParamMap }
declare global {
  interface RouteContext<X extends R> {
    params: Promise<ParamMap[X]>
  }
}
`

const tsRouteFile = `export async function GET(ctx: RouteContext<"/a/[id]">) {
  return (await ctx.params).id;
}
`

// TestTSLayer2DeclareGlobal: Next.js's generated routes.d.ts. Layer 1 names
// declare global's declarations under global/ and string-named members with
// their quotes; Layer 2 resolves a free name to them and binds the type
// parameter to the literal its use passes, so ParamMap[X] is the member
// "/a/[id]" and .id its literal's "id" (the symbols scip-typescript writes
// for Lebenslauf's routes). When a second document has the same file symbol
// (.next/types and .next/dev/types) and numbers the literal apart, .id is
// counted.
func TestTSLayer2DeclareGlobal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		renumber bool
		wantID   bool
	}{{"one numbering", false, true}, {"two numberings", true, false}} {
		root := t.TempDir()
		for _, f := range []struct{ path, src string }{{"types/routes.d.ts", tsRoutesFile}, {"dev/types/routes.d.ts", tsRoutesFile}, {"src/route.ts", tsRouteFile}} {
			if err := os.MkdirAll(filepath.Join(root, filepath.Dir(f.path)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, f.path), []byte(f.src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		head := "scip-typescript npm app 1.0.0 "
		seed := &scip.Document{RelativePath: "src/seed.ts", Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 0}, Symbol: head + "src/`seed.ts`/", SymbolRoles: scip.SymbolRoleDefinition}}}
		base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"src/seed.ts": seed})
		routes, _ := tsNewDocument(base, "types/routes.d.ts", []byte(tsRoutesFile))
		doc, _ := tsNewDocument(base, "src/route.ts", []byte(tsRouteFile))
		if routes == nil || doc == nil {
			t.Fatal("document not synthesized")
		}
		lit := ""
		for _, o := range routes.Occurrences {
			if strings.Contains(o.Symbol, "typeLiteral") {
				lit = o.Symbol
			}
		}
		if lit == "" {
			t.Fatal("no literal member in routes.d.ts")
		}
		dev := &scip.Document{RelativePath: "dev/types/routes.d.ts", Symbols: routes.Symbols}
		for _, o := range routes.Occurrences {
			c := *o
			if tc.renumber && c.Symbol == lit {
				c.Symbol = strings.Replace(c.Symbol, "typeLiteral", "typeLiteral9", 1)
			}
			dev.Occurrences = append(dev.Occurrences, &c)
		}
		docs := map[string]*scip.Document{"types/routes.d.ts": routes, "dev/types/routes.d.ts": dev, "src/route.ts": doc}
		r := newTSResolver(root, func(p string) *scip.Document {
			if d, ok := docs[p]; ok {
				return d
			}
			return base.DocumentsByPath[p]
		})
		r.paths = func() []string { return []string{"src/route.ts", "types/routes.d.ts", "dev/types/routes.d.ts"} }
		_, unresolved := tsLayer2(doc, "src/route.ts", []byte(tsRouteFile), allLines([]byte(tsRouteFile)), r)
		prefix := head + "types/`routes.d.ts`/"
		var got []string
		for _, o := range doc.Occurrences {
			if o.Source == scip.OccurrenceSourceOverlay && strings.HasPrefix(o.Symbol, prefix) {
				got = append(got, strings.TrimPrefix(o.Symbol, prefix)+"@"+fmtRange(o))
			}
		}
		sort.Strings(got)
		want := []string{"global/RouteContext#@0 31 43", "global/RouteContext#params.@1 20 26"}
		wantUnres := 1
		if tc.wantID {
			want = append(want, strings.TrimPrefix(lit, prefix)+"@1 28 30")
			wantUnres = 0
		}
		sort.Strings(want)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s\n got  %v\n want %v", tc.name, got, want)
		}
		if unresolved != wantUnres {
			t.Errorf("%s: unresolved %d, want %d", tc.name, unresolved, wantUnres)
		}
		if !strings.Contains(lit, "ParamMap#`\"/a/[id]\"`.typeLiteral") || !strings.HasSuffix(lit, ":`\"id\"`.") {
			t.Errorf("literal member %s", lit)
		}
	}
}
