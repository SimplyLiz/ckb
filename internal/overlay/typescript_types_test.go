//go:build cgo

package overlay

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

const tsTypesLib = `export interface Base { b: number }
export interface I extends Base { q: number; m(): void; nested?: { z: number } }
export type L = { a: number; k(): void };
export const ITEMS: I[] = [];
export function first() { return ITEMS.find((i) => i.q > 0) ?? ITEMS[0]; }
export async function load(): Promise<I> { return ITEMS[0]; }
export function takeL(l: L) { return l; }
export function takeOpt(l?: L) { return l; }
export function maybe(): L | null { return { a: 1, k() {} }; }
export function make(): L { return { a: 1, k() {} }; }
export const ICONS: Record<string, { z: number }> = { x: { z: 1 } };
export class C { v: I = ITEMS[0]; get() { return this.v.q; } }
export function takeDef(l: L = { a: 0, k() {} }) { return l; }
export async function mkAsync(): Promise<L> { return { a: 1, k() {} }; }
export class K { constructor(o: L) { o.k(); } }
`

const tsTypesUse = `import { create } from "zustand";
import { I, ITEMS, first, load, takeL, takeOpt, ICONS, takeDef, K } from "./lib";
export const useS = create<I>()((set) => ({ q: 1, b: 2, m() {} }));
export async function g() {
  first().q;
  const x = await load();
  x.m();
  ITEMS.map((it) => it.q);
  ITEMS.map(({ q }) => q);
  takeL({ a: 1, k() {} });
  takeOpt({ a: 1, k() {} });
  useS((s) => s.q);
  useS.getState().m();
  ICONS.y.z;
  const { load: again } = await import("./lib");
  return again;
}
export function F({ card }: { card: I }) { return card.q + card.b; }
const using = 1;
export const n: typeof import("./lib") | number = using;
export const a = 1;
export const k = () => {};
takeL({ a, k });
takeDef({ a, k });
new K({ a: 1, k() {} });
`

// TestTSLayer2Types: members reached through inferred and contextual types.
func TestTSLayer2Types(t *testing.T) {
	root := t.TempDir()
	for rel, text := range map[string]string{"src/lib.ts": tsTypesLib, "src/use.ts": tsTypesUse} {
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
	lib, _ := tsNewDocument(base, "src/lib.ts", []byte(tsTypesLib))
	base = scip.PatchIndex(base, map[string]*scip.Document{"src/lib.ts": lib})
	docs := map[string]*scip.Document{}
	resolver := func() *tsResolver {
		return newTSResolver(root, func(p string) *scip.Document {
			if d := docs[p]; d != nil {
				return d
			}
			return base.DocumentsByPath[p]
		})
	}
	use, _ := tsNewDocument(base, "src/use.ts", []byte(tsTypesUse))
	if use == nil {
		t.Fatal("use.ts not synthesized (parse)")
	}
	docs["src/use.ts"] = use
	r := resolver()
	_, unresolved := tsLayer2(use, "src/use.ts", []byte(tsTypesUse), allLines([]byte(tsTypesUse)), r)
	r.close()

	digits := regexp.MustCompile(`typeLiteral\d+`)
	memberRef := regexp.MustCompile(`[#.:][^@]+@`) // a member, not a type or a top-level name
	got := map[string]bool{}
	useSym := head + "src/`use.ts`/"
	for _, o := range use.Occurrences {
		switch {
		case o.Source != scip.OccurrenceSourceOverlay:
		case strings.HasPrefix(o.Symbol, libSym):
			got[digits.ReplaceAllString(strings.TrimPrefix(o.Symbol, libSym), "typeLiteralN")+"@"+fmtRange(o)] = true
		case strings.HasPrefix(o.Symbol, useSym) && o.Range[0] >= 22:
			got["use:"+strings.TrimPrefix(o.Symbol, useSym)+"@"+fmtRange(o)] = true
		}
	}
	for _, w := range []string{
		"I#q.@2 44 45",              // create<I>()(… => ({ q })): the store's state
		"Base#b.@2 50 51",           // b: inherited
		"I#m().@2 56 57",            // m() {}
		"I#q.@4 10 11",              // first().q: an inferred return through ??
		"I#m().@6 4 5",              // const x = await load()
		"I#q.@7 23 24",              // ITEMS.map((it) => it.q)
		"I#q.@8 15 16",              // ITEMS.map(({ q }) => q)
		"L#typeLiteralN:a.@9 10 11", // takeL({ a })
		"L#typeLiteralN:k().@9 16 17",
		"I#q.@11 16 17",                         // useS((s) => s.q)
		"I#m().@12 18 19",                       // useS.getState().m()
		"ICONS.Record:typeLiteralN:z.@13 10 11", // a Record's value type
		"load().@14 10 14",                      // const { load } = await import("./lib"): declared there
		"@14 39 46",                             // the module string
		"I#q.@17 55 56",                         // card.q: a destructured parameter's member
		"Base#b.@17 64 65",
		"@19 30 37",                // typeof import("./lib")
		"L#typeLiteralN:a.@22 8 9", // shorthand: the member, not the value
		"L#typeLiteralN:k().@22 11 12",
		"use:a.@23 10 11", // takeDef's parameter has a default: L | undefined, so the value
		"use:k.@23 13 14",
		"K#`<constructor>`().@24 4 5",
		"L#typeLiteralN:a.@24 8 9", // new K({ a }): the constructor's parameter
		"L#typeLiteralN:k().@24 14 15",
	} {
		if !got[w] {
			t.Errorf("missing %s", w)
		}
		delete(got, w)
	}
	for k := range got {
		if memberRef.MatchString(k) || strings.HasPrefix(k, "use:") {
			t.Errorf("unexpected %s", k)
		}
	}
	// takeOpt's literal is contextually L | undefined: its keys are its own
	// properties, neither an edge nor unresolved.
	if unresolved != 0 {
		t.Errorf("unresolved %d, want 0", unresolved)
	}

	// The lib: maybe()'s literal (L | null) has no edges, make()'s has,
	// this.v.q goes through the field's type, ICONS' nested key is the
	// Record literal's member.
	docs["src/lib.ts"] = lib
	r = resolver()
	tsLayer2(lib, "src/lib.ts", []byte(tsTypesLib), allLines([]byte(tsTypesLib)), r)
	r.close()
	got = map[string]bool{}
	for _, o := range lib.Occurrences {
		if o.Source == scip.OccurrenceSourceOverlay && strings.HasPrefix(o.Symbol, libSym) {
			got[digits.ReplaceAllString(strings.TrimPrefix(o.Symbol, libSym), "typeLiteralN")+"@"+fmtRange(o)] = true
		}
	}
	for _, w := range []string{
		"I#q.@4 53 54",                          // find((i) => i.q)
		"L#typeLiteralN:a.@9 37 38",             // make(): L { return { a } }
		"ICONS.Record:typeLiteralN:z.@10 59 60", // { x: { z: 1 } }
		"I#q.@11 56 57",                         // this.v.q
	} {
		if !got[w] {
			t.Errorf("lib: missing %s", w)
		}
	}
	for k := range got {
		// Line 12 is takeDef's default value, typed L: it has edges.
		if strings.Contains(k, "typeLiteral") && (strings.Contains(k, "@8 ") || strings.Contains(k, "@13 ")) {
			t.Errorf("lib: a literal in a nullable context got an edge: %s", k)
		}
	}
}

// TestTSParseText: the two constructs the grammar rejects parse after the
// same-length rewrite; a using declaration stays one.
func TestTSParseText(t *testing.T) {
	for src, want := range map[string]string{
		"const n = using.length;":                 "const n = usin$.length;",
		"using x = open();":                       "using x = open();",
		"await using x = open();":                 "await using x = open();",
		"for (const using of xs) {}":              "for (const usin$ of xs) {}",
		`type M = typeof import("./m");`:          `type M = typeof $mport("./m");`,
		`const s = "typeof import(x)"; using.x;`:  `const s = "typeof $mport(x)"; usin$.x;`,
		"const focusing = 1; xusing.y; using_.z;": "const focusing = 1; xusing.y; using_.z;",
	} {
		if got := string(tsParseText([]byte(src))); got != want {
			t.Errorf("%q: got %q, want %q", src, got, want)
		}
	}
	for _, src := range []string{"const using = 1;\nusing.toString();\n", `export type M = typeof import("./m");` + "\n"} {
		if _, errLines, ok := parseTSDecls("a.ts", []byte(src)); !ok || errLines != nil {
			t.Errorf("%q does not parse: %v", src, errLines)
		}
	}
}

// TestTSIntrinsicTag: a lower-case JSX tag is an intrinsic element even
// when a value of the name is imported; the value in braces is the value.
func TestTSIntrinsicTag(t *testing.T) {
	root := t.TempDir()
	lib, use := "export const table = { cls: \"t\" };\n", "import { table } from \"./lib\";\nexport const T = () => <table className={table.cls}></table>;\n"
	for rel, text := range map[string]string{"src/lib.ts": lib, "src/t.tsx": use} {
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
	ld, _ := tsNewDocument(base, "src/lib.ts", []byte(lib))
	base = scip.PatchIndex(base, map[string]*scip.Document{"src/lib.ts": ld})
	doc, _ := tsNewDocument(base, "src/t.tsx", []byte(use))
	r := newTSResolver(root, func(p string) *scip.Document {
		if p == "src/t.tsx" {
			return doc
		}
		return base.DocumentsByPath[p]
	})
	tsLayer2(doc, "src/t.tsx", []byte(use), allLines([]byte(use)), r)
	r.close()
	var got []string
	for _, o := range doc.Occurrences {
		if o.Source == scip.OccurrenceSourceOverlay && o.Range[0] == 1 {
			got = append(got, strings.TrimPrefix(o.Symbol, head+"src/`lib.ts`/")+"@"+fmtRange(o))
		}
	}
	if strings.Join(got, " ") != "table.@1 41 46" {
		t.Errorf("got %v, want only the value in braces (table.@1 41 46)", got)
	}
}

const tsNarrowLib = `export interface A { kind: "a"; x: number; shared: number }
export interface B { kind: "b"; y: number; shared: number }
export type AB = A | B;
export type Rem = { kind: "r"; id: string } | ({ kind: "s" } & A);
export interface Ok { ok: true; data: number; shared: number }
export interface Err { ok: false; error: string; shared: number }
export function isA(v: AB): v is A { return v.kind === "a"; }
`

const tsNarrowUse = `import { AB, Rem, A, Ok, Err, isA } from "./lib";
export function f(v: AB, w: Rem, s: string | A, vs: AB[]) {
  v.shared;
  if (v.kind === "a") v.x;
  v.kind === "b" && v.y;
  v.kind !== "a" ? v.y : v.x;
  switch (v.kind) { case "a": v.x; break; case "b": v.y; }
  w.kind === "s" && w.x;
  typeof s === "string" ? 0 : s.x;
  vs.map((e) => e.kind === "a" && e.x);
}
export function g(v: AB) {
  if (v.kind !== "a") return;
  v.x;
}
export function h(v: AB) {
  v = { kind: "b", y: 1, shared: 1 };
  v.shared;
}
export function k(o: { t: AB }) {
  o.t.shared;
}
export function m(v: AB) {
  if (v.kind === "a") return () => v.x;
  return null;
}
export function n(v: AB, r: Ok | Err) {
  r.ok ? r.data : r.error;
  "x" in v && v.x;
  isA(v) ? v.x : v.y;
  if (v.kind === "a") { v.x; } else { return; }
  v.x;
}
export function o(v: AB, r: Ok | Err) {
  const isOk = r.ok;
  if (isOk) r.shared;
  assertA(v);
  v.shared;
}
export function p(v: AB) {
  const { kind } = v;
  kind === "a" ? v.shared : 0;
}
declare function assertA(v: AB): asserts v is A;
`

// TestTSLayer2Narrowing: a union's member is every remaining part's, after
// the conditions that narrow an identifier that is not reassigned.
func TestTSLayer2Narrowing(t *testing.T) {
	root := t.TempDir()
	for rel, text := range map[string]string{"src/lib.ts": tsNarrowLib, "src/use.ts": tsNarrowUse} {
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
	lib, _ := tsNewDocument(base, "src/lib.ts", []byte(tsNarrowLib))
	base = scip.PatchIndex(base, map[string]*scip.Document{"src/lib.ts": lib})
	use, _ := tsNewDocument(base, "src/use.ts", []byte(tsNarrowUse))
	r := newTSResolver(root, func(p string) *scip.Document {
		if p == "src/use.ts" {
			return use
		}
		return base.DocumentsByPath[p]
	})
	_, unresolved := tsLayer2(use, "src/use.ts", []byte(tsNarrowUse), allLines([]byte(tsNarrowUse)), r)
	r.close()

	got := map[string]bool{}
	for _, o := range use.Occurrences {
		if o.Source == scip.OccurrenceSourceOverlay && strings.HasPrefix(o.Symbol, libSym) && strings.Contains(o.Symbol, "#") {
			got[strings.TrimPrefix(o.Symbol, libSym)+"@"+fmtRange(o)] = true
		}
	}
	for _, w := range []string{
		"A#shared.@2 4 10", "B#shared.@2 4 10", // v.shared: both parts' declarations
		"A#kind.@3 8 12", "B#kind.@3 8 12", // the condition itself is not narrowed
		"A#x.@3 24 25",                 // if (v.kind === "a") v.x
		"B#y.@4 22 23",                 // && narrows the right side
		"B#y.@5 21 22", "A#x.@5 27 28", // !== ?: both branches
		"A#x.@6 32 33", "B#y.@6 54 55", // switch cases
		"A#x.@7 22 23",                             // through the intersection's part
		"A#x.@8 32 33",                             // typeof drops string
		"A#x.@9 36 37",                             // a callback's parameter
		"A#x.@13 4 5",                              // after if (…) return
		"A#x.@23 37 38",                            // a closure over a parameter never reassigned (TypeScript 5.4+)
		"Ok#data.@27 11 15", "Err#error.@27 20 25", // truthiness of a discriminant
		"A#x.@28 16 17",                  // "x" in v
		"A#x.@29 13 14", "B#y.@29 19 20", // a type guard
		"A#x.@30 26 27", "A#x.@31 4 5", // the else branch returns
	} {
		if !got[w] {
			t.Errorf("missing %s", w)
		}
		delete(got, w)
	}
	for k := range got {
		if strings.Contains(k, "#x.") || strings.Contains(k, "#y.") || strings.Contains(k, "@17 ") || strings.Contains(k, "@20 ") {
			t.Errorf("unexpected %s", k)
		}
	}
	for _, w := range []string{"Ok#shared.@35 15 21", "Err#shared.@35 15 21", "A#shared.@37 4 10", "B#shared.@37 4 10",
		"A#shared.@41 19 25", "B#shared.@41 19 25"} { // a destructured discriminant (nyxcore persona-picker.tsx)
		if got[w] {
			t.Errorf("%s: a narrowing not modeled must leave the access unresolved", w)
		}
	}
	// Unresolved: w.kind (Rem's object types are local, and Layer 1 writes
	// no local definitions, so the document cannot say), h's v.shared (v is
	// reassigned) and the three keys assigned to it (a union context), k's
	// o.t.shared (a property chain), o's r.shared (an aliased condition),
	// v.shared (after an assertion), and p's { kind } (a pattern key on a
	// union) and v.shared (a destructured discriminant).
	if unresolved != 10 {
		t.Errorf("unresolved %d, want 10", unresolved)
	}
}

const tsReactLib = `export interface Doc { name: string; hyphens: H }
export interface H { manual: H; auto: boolean }
`

const tsReactUse = `import { createContext, useContext, useState, useRef, useMemo } from "react";
import * as React from "react";
import { Doc, H } from "./lib";
import { wrap } from "pkg";
const Ctx = createContext<Doc>({} as Doc);
export function F() {
  const { hyphens: doc } = useContext(Ctx);
  doc.manual;
  const [d, setD] = useState<Doc | null>(null);
  d?.name;
  const r = useRef<H>(null as unknown as H);
  r.current.auto;
  const m = useMemo(() => doc.manual, [doc]);
  m.auto;
  React.useContext(Ctx).name;
  wrap(doc).manual;
  setD(null);
}
`

// TestTSLayer2React: React's hooks pass the repository's types through; a
// package call passing a repository value is unknown, not a package's type.
func TestTSLayer2React(t *testing.T) {
	root := t.TempDir()
	for rel, text := range map[string]string{"src/lib.ts": tsReactLib, "src/use.tsx": tsReactUse} {
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
	lib, _ := tsNewDocument(base, "src/lib.ts", []byte(tsReactLib))
	base = scip.PatchIndex(base, map[string]*scip.Document{"src/lib.ts": lib})
	use, _ := tsNewDocument(base, "src/use.tsx", []byte(tsReactUse))
	r := newTSResolver(root, func(p string) *scip.Document {
		if p == "src/use.tsx" {
			return use
		}
		return base.DocumentsByPath[p]
	})
	_, unresolved := tsLayer2(use, "src/use.tsx", []byte(tsReactUse), allLines([]byte(tsReactUse)), r)
	r.close()
	got := map[string]bool{}
	for _, o := range use.Occurrences {
		s := strings.TrimPrefix(o.Symbol, libSym)
		if o.Source == scip.OccurrenceSourceOverlay && strings.HasPrefix(o.Symbol, libSym) && s != "" && !strings.HasSuffix(s, "#") {
			got[s+"@"+fmtRange(o)] = true
		}
	}
	for _, w := range []string{
		"Doc#hyphens.@6 10 17", // const { hyphens } = useContext(Ctx)
		"H#manual.@7 6 12",     // doc.manual
		"Doc#name.@9 5 9",      // [d] = useState<Doc | null>
		"H#auto.@11 12 16",     // useRef<H>: current is H
		"H#manual.@12 30 36",
		"H#auto.@13 4 8",     // useMemo: the callback's result
		"Doc#name.@14 24 28", // React.useContext
	} {
		if !got[w] {
			t.Errorf("missing %s", w)
		}
		delete(got, w)
	}
	for k := range got {
		t.Errorf("unexpected %s", k)
	}
	// wrap(doc).manual: a package's function given a repository value.
	if unresolved != 1 {
		t.Errorf("unresolved %d, want 1", unresolved)
	}
}

const tsGenLib = `export interface Ev { type: string; n: number }
export interface Opts { id: string }
`

// Every expected edge below is what scip-typescript 0.4.0 writes for this
// file (checked on a scratch project).
const tsGenUse = `import { Ev, Opts } from "./lib";
export async function* run(o: Opts): AsyncGenerator<Ev, Opts> {
  yield { type: "a", n: o.id.length };
  function wrap(e: Ev): Ev { return e; }
  yield wrap({ type: "b", n: 2 });
  return { id: "x" };
}
export function* sync(): Generator<Ev, Opts> {
  yield { type: "s", n: 0 };
  return { id: "s" };
}
export function* bare() { yield { type: "c" }; }
export async function use() {
  for await (const e of run({ id: "y" })) e.n;
  for (const f of sync()) f.type;
  const p = <p>Account & preferences {Math.max(1, 2)}</p>;
  return p;
}
class K { *gen(): Generator<Ev> { yield { type: "k", n: 1 }; } }
export const k = new K();
`

// TestTSLayer2Generators: a generator's yield operands take its declared
// result's T, its return expressions TReturn (an async generator's are
// TReturn | PromiseLike, so the literal's own), iterating its call gives T,
// and a function declared in a body types its parameters. The & in JSX text
// parses.
func TestTSLayer2Generators(t *testing.T) {
	root := t.TempDir()
	for rel, text := range map[string]string{"src/lib.ts": tsGenLib, "src/use.tsx": tsGenUse} {
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
	lib, _ := tsNewDocument(base, "src/lib.ts", []byte(tsGenLib))
	base = scip.PatchIndex(base, map[string]*scip.Document{"src/lib.ts": lib})
	use, _ := tsNewDocument(base, "src/use.tsx", []byte(tsGenUse))
	if use == nil {
		t.Fatal("use.tsx not synthesized (the & in JSX text)")
	}
	r := newTSResolver(root, func(p string) *scip.Document {
		if p == "src/use.tsx" {
			return use
		}
		return base.DocumentsByPath[p]
	})
	_, unresolved := tsLayer2(use, "src/use.tsx", []byte(tsGenUse), allLines([]byte(tsGenUse)), r)
	r.close()
	got := map[string]bool{}
	for _, o := range use.Occurrences {
		s := strings.TrimPrefix(o.Symbol, libSym)
		if o.Source == scip.OccurrenceSourceOverlay && strings.HasPrefix(o.Symbol, libSym) && s != "" && !strings.HasSuffix(s, "#") {
			got[s+"@"+fmtRange(o)] = true
		}
	}
	for _, w := range []string{
		"Ev#type.@2 10 14", // yield { type }: AsyncGenerator's T
		"Ev#n.@2 21 22",
		"Opts#id.@2 26 28",
		"Ev#type.@4 15 19", // wrap({ type }): a function declared in the body
		"Ev#n.@4 26 27",
		"Ev#type.@8 10 14", // Generator's T
		"Ev#n.@8 21 22",
		"Opts#id.@9 11 13",  // a sync generator's return: TReturn
		"Opts#id.@13 30 32", // run({ id }): a generator's parameter
		"Ev#n.@13 44 45",    // for await (const e of run(…)): what it yields
		"Ev#type.@14 28 32",
		"Ev#type.@18 42 46", // a generator method
		"Ev#n.@18 53 54",
	} {
		if !got[w] {
			t.Errorf("missing %s", w)
		}
		delete(got, w)
	}
	for k := range got {
		t.Errorf("unexpected %s", k) // the async return (line 5) and bare()'s keys are the literals' own
	}
	if unresolved != 0 {
		t.Errorf("unresolved %d, want 0", unresolved)
	}
}

const tsAsLib = `export interface Svc { run(): void }
`

const tsAsUse = `import { Svc } from "./lib";
import { Client } from "pkg";
const g = globalThis as unknown as { svc: Svc | undefined; c: Client | undefined };
export const c = g.c ?? new Client();
export function f(tenantId: string) {
  g.svc?.run();
  c.user.find({ where: { tenantId } });
}
`

// TestTSLayer2AsLiteral: the members of x as { … } are locals in
// scip-typescript's index (nyxcore's globalThis as unknown as { prisma: … }),
// but their types are followed: a repository type's members get their edge,
// a package's are nobody's. A shorthand parameter in a package's object
// literal is counted, because the index writes f().(p) there.
func TestTSLayer2AsLiteral(t *testing.T) {
	root := t.TempDir()
	for rel, text := range map[string]string{"src/lib.ts": tsAsLib, "src/use.ts": tsAsUse} {
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
	lib, _ := tsNewDocument(base, "src/lib.ts", []byte(tsAsLib))
	base = scip.PatchIndex(base, map[string]*scip.Document{"src/lib.ts": lib})
	use, _ := tsNewDocument(base, "src/use.ts", []byte(tsAsUse))
	r := newTSResolver(root, func(p string) *scip.Document {
		if p == "src/use.ts" {
			return use
		}
		return base.DocumentsByPath[p]
	})
	_, unresolved := tsLayer2(use, "src/use.ts", []byte(tsAsUse), allLines([]byte(tsAsUse)), r)
	r.close()
	got := map[string]bool{}
	for _, o := range use.Occurrences {
		s := strings.TrimPrefix(o.Symbol, libSym)
		if o.Source == scip.OccurrenceSourceOverlay && strings.HasPrefix(o.Symbol, libSym) && s != "" && !strings.HasSuffix(s, "#") {
			got[s+"@"+fmtRange(o)] = true
		}
	}
	if w := "Svc#run().@5 9 12"; !got[w] {
		t.Errorf("missing %s (got %v)", w, got)
	}
	if len(got) != 1 {
		t.Errorf("got %v, want only Svc#run()", got)
	}
	if unresolved != 1 {
		t.Errorf("unresolved %d, want 1 (the shorthand tenantId)", unresolved)
	}
}

const tsZodSchema = `import { z } from "zod";
const text = (n: number) => z.string().max(n);
export const itemSchema = z.object({ id: z.string(), level: z.number().optional() });
export const entrySchema = z.object({
  id: z.string(),
  role: text(300),
  items: z.array(itemSchema).min(1),
}).strict();
export const extSchema = entrySchema.extend({ extra: z.string() });
export type Entry = z.infer<typeof entrySchema>;
export type Ext = z.infer<typeof extSchema>;
export type Tr = z.infer<typeof trSchema>;
export const trSchema = entrySchema.transform((e) => ({ ...e, n: 1 }));
export const keepSchema = entrySchema.transform(keep);
export type Keep = z.infer<typeof keepSchema>;
function keep<P>(p: P): P { return p; }
`

const tsZodUse = `import { entrySchema, type Entry, type Ext, type Tr, type Keep } from "./schema";
export function f(e: Entry, x: Ext, t: Tr, k: Keep) {
  console.log(e.role, e.items[0].level, e.id, x.extra, x.role);
  const p = entrySchema.parse({});
  console.log(p.role, t.role);
  const lit: Entry = { id: "", role: "", items: [] };
  console.log(k.role, k.items[0].id);
  return lit;
}
`

// TestTSLayer2Zod: a member of z.infer<typeof s> is the key of the
// z.object literal that declares it, through extend, a helper that builds
// a schema, z.array and parse(); what transform returns is unknown. The
// keys' symbols are scip-typescript's (probe project, zod 4): Layer 1 does
// not write counter-numbered properties, so the test adds them.
func TestTSLayer2Zod(t *testing.T) {
	root := t.TempDir()
	for rel, text := range map[string]string{"src/schema.ts": tsZodSchema, "src/use.ts": tsZodUse} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	head := "scip-typescript npm app 1.0.0 "
	schemaSym := head + "src/`schema.ts`/"
	seed := &scip.Document{RelativePath: "src/seed.ts", Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 0}, Symbol: head + "src/`seed.ts`/", SymbolRoles: scip.SymbolRoleDefinition}}}
	base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"src/seed.ts": seed})
	schema, _ := tsNewDocument(base, "src/schema.ts", []byte(tsZodSchema))
	for _, k := range []struct {
		line, col int32
		sym       string
		def       bool
	}{
		{2, 37, "id0:", true}, {2, 53, "level0:", true},
		{4, 2, "id1:", true}, {5, 2, "role0:", true}, {6, 2, "items0:", true},
		{8, 46, "extra0:", false}, // an extend's key: a reference to a symbol nothing defines
	} {
		o := &scip.Occurrence{Range: []int32{k.line, k.col, k.col + int32(len(strings.TrimRight(k.sym, "0123456789:")))}, Symbol: schemaSym + k.sym}
		if k.def {
			o.SymbolRoles = scip.SymbolRoleDefinition
		}
		schema.Occurrences = append(schema.Occurrences, o)
	}
	base = scip.PatchIndex(base, map[string]*scip.Document{"src/schema.ts": schema})
	use, _ := tsNewDocument(base, "src/use.ts", []byte(tsZodUse))
	r := newTSResolver(root, func(p string) *scip.Document {
		if p == "src/use.ts" {
			return use
		}
		return base.DocumentsByPath[p]
	})
	_, unresolved := tsLayer2(use, "src/use.ts", []byte(tsZodUse), allLines([]byte(tsZodUse)), r)
	r.close()
	got := map[string]bool{}
	for _, o := range use.Occurrences {
		s := strings.TrimPrefix(o.Symbol, schemaSym)
		if o.Source == scip.OccurrenceSourceOverlay && strings.HasSuffix(s, ":") {
			got[s+"@"+fmtRange(o)] = true
		}
	}
	for _, w := range []string{
		"role0:@2 16 20", "items0:@2 24 29", "level0:@2 33 38", "id1:@2 42 44",
		"extra0:@2 48 53", "role0:@2 57 61", // extend: its own key, the base's
		"role0:@4 16 20",                    // parse()
		"id1:@5 23 25", "role0:@5 31 35", "items0:@5 41 46", // a literal typed Entry
		"role0:@6 16 20", "items0:@6 24 29", "id0:@6 33 35", // transform(keep), keep<P>(p: P): P; z.array(s).min(1)
	} {
		if !got[w] {
			t.Errorf("missing %s", w)
		}
		delete(got, w)
	}
	for k := range got {
		t.Errorf("unexpected %s", k)
	}
	if unresolved != 1 {
		t.Errorf("unresolved %d, want 1 (t.role: transform)", unresolved)
	}
}
