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
`

const tsTypesUse = `import { create } from "zustand";
import { I, ITEMS, first, load, takeL, takeOpt, ICONS } from "./lib";
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
	for _, o := range use.Occurrences {
		if o.Source == scip.OccurrenceSourceOverlay && strings.HasPrefix(o.Symbol, libSym) {
			got[digits.ReplaceAllString(strings.TrimPrefix(o.Symbol, libSym), "typeLiteralN")+"@"+fmtRange(o)] = true
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
		"load().@14 10 14",                      // const { load } = await import("./lib")
		"@14 39 46",                             // the module string
		"I#q.@17 55 56",                         // card.q: a destructured parameter's member
		"Base#b.@17 64 65",
		"@19 30 37", // typeof import("./lib")
	} {
		if !got[w] {
			t.Errorf("missing %s", w)
		}
		delete(got, w)
	}
	for k := range got {
		if memberRef.MatchString(k) {
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
		if strings.HasSuffix(k, "@8 47 48") {
			t.Errorf("lib: maybe()'s key got an edge: %s", k)
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
