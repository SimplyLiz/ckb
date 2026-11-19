package query

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/cycles"
)

func TestClassifyTSImport(t *testing.T) {
	cases := []struct {
		name string
		src  string // the specifier starts at the first quote of the last line
		want importKind
	}{
		{"named", `import { a } from "./x";`, importStatic},
		{"default", `import a from './x';`, importStatic},
		{"namespace", `import * as x from "./x";`, importStatic},
		{"side effect", `import "./polyfill";`, importStatic},
		{"re-export", `export { a } from "./x";`, importStatic},
		{"export star", `export * from "./x";`, importStatic},
		{"import type", `import type { A } from "./x";`, importType},
		{"export type", `export type { A } from "./x";`, importType},
		{"all names type", `import { type A, type B } from "./x";`, importType},
		{"one value name", `import { type A, b } from "./x";`, importStatic},
		{"default beside type", `import a, { type B } from "./x";`, importStatic},
		{"multi-line type", "import type {\n  A,\n  B,\n} from \"./x\";", importType},
		{"multi-line names", "import {\n  type A,\n  b,\n} from \"./x\";", importStatic},
		{"multi-line all type", "import {\n  type A,\n  type B,\n} from \"./x\";", importType},
		{"dynamic", `const m = await import("./x");`, importLazy},
		{"dynamic inline", `return (await import("@/server/debug/tier")).simulate(s);`, importLazy},
		{"require", `const x = require("./x");`, importStatic},
		{"import equals", `import x = require("./x");`, importStatic},
		{"jest.mock", `jest.mock("./x");`, 0},
		{"declare module", `declare module "./x" {`, 0},
	}
	for _, c := range cases {
		lines := strings.Split(c.src, "\n")
		last := len(lines) - 1
		q := strings.IndexAny(lines[last], "\"'")
		if got := classifyTSImport(lines, last, lines[last][:q]); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestTSSpecifierAt(t *testing.T) {
	line := `import { a } from "./x"; // ü`
	if !tsSpecifierAt(line, []int32{0, 18, 23}) {
		t.Error("range over the quoted specifier should match")
	}
	if tsSpecifierAt(line, []int32{0, 9, 10}) {
		t.Error("range over a name is no specifier: the file changed after indexing")
	}
	if tsSpecifierAt(line, []int32{0, 18, 99}) {
		t.Error("range past the line end")
	}
}

func TestPythonImportStatements(t *testing.T) {
	src := strings.Join([]string{
		"from __future__ import annotations", // 0 static
		"import os",                          // 1 static
		"from typing import TYPE_CHECKING",   // 2 static
		"if TYPE_CHECKING:",                  // 3
		"    from pkg.models import Doc",     // 4 type
		"try:",                               // 5
		"    import fast",                    // 6 static
		"except ImportError:",                // 7
		"    fast = None",                    // 8
		"from pkg.util import (",             // 9 static, spans 9-11
		"    a,",                             // 10
		")",                                  // 11
		"class C:",                           // 12
		"    from pkg import attr",           // 13 static (class body runs on import)
		"    def m(self):",                   // 14
		"        from pkg.late import x",     // 15 lazy
		"def f():",                           // 16
		"    import pkg.lazy",                // 17 lazy
		"async def g():",                     // 18
		"    from pkg import h",              // 19 lazy
		"from pkg.after import z",            // 20 static again
	}, "\n")
	got := pythonImportStatements(strings.Split(src, "\n"))
	want := map[int]importKind{0: importStatic, 1: importStatic, 2: importStatic, 4: importType, 6: importStatic, 9: importStatic, 13: importStatic, 15: importLazy, 17: importLazy, 19: importLazy, 20: importStatic}
	if len(got) != len(want) {
		t.Fatalf("got %d statements %+v, want %d", len(got), got, len(want))
	}
	for _, st := range got {
		if w, ok := want[st.first]; !ok || w != st.kind {
			t.Errorf("line %d: kind %d, want %d", st.first, st.kind, w)
		}
		if st.first == 9 && st.last != 11 {
			t.Errorf("parenthesized import should span to line 11, got %d", st.last)
		}
	}
}

// tsFixture writes files and returns an index whose module symbols and
// specifier occurrences mirror scip-typescript's.
type tsFixture struct {
	t    *testing.T
	root string
	idx  *scip.SCIPIndex
}

func newTSFixture(t *testing.T) *tsFixture {
	return &tsFixture{t: t, root: t.TempDir(), idx: &scip.SCIPIndex{
		DocumentsByPath: map[string]*scip.Document{},
		DefinitionIndex: map[string]*scip.OccurrenceRef{},
	}}
}

func tsModuleSymbol(path string) string {
	dir, base := filepath.Split(path)
	return "scip-typescript npm app 1.0.0 " + dir + "`" + base + "`/"
}

func (f *tsFixture) file(path, src string) {
	if err := os.MkdirAll(filepath.Join(f.root, filepath.Dir(path)), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, path), []byte(src), 0o644); err != nil {
		f.t.Fatal(err)
	}
	def := &scip.Occurrence{Range: []int32{0, 0, 0}, Symbol: tsModuleSymbol(path), SymbolRoles: scip.SymbolRoleDefinition}
	doc := &scip.Document{RelativePath: path, Occurrences: []*scip.Occurrence{def}}
	f.idx.Documents = append(f.idx.Documents, doc)
	f.idx.DocumentsByPath[path] = doc
	f.idx.DefinitionIndex[def.Symbol] = &scip.OccurrenceRef{Doc: doc, Occ: def}
}

// link adds the specifier occurrences of every `"./name"` in path's source.
func (f *tsFixture) link(path string) {
	data, _ := os.ReadFile(filepath.Join(f.root, path))
	doc := f.idx.DocumentsByPath[path]
	for l, line := range strings.Split(string(data), "\n") {
		for _, target := range f.idx.Documents {
			spec := `"./` + strings.TrimSuffix(filepath.Base(target.RelativePath), filepath.Ext(target.RelativePath)) + `"`
			if i := strings.Index(line, spec); i >= 0 {
				doc.Occurrences = append(doc.Occurrences, &scip.Occurrence{Range: []int32{int32(l), int32(i), int32(i + len(spec))}, Symbol: tsModuleSymbol(target.RelativePath)})
			}
		}
	}
}

func TestImportGraphRuntimeCycles(t *testing.T) {
	f := newTSFixture(t)
	// a ⇄ b statically: a cycle. b → c and c → b with c importing b only as a
	// type: no cycle. a → d lazily and d → a: no cycle.
	f.file("src/a.ts", "import { b } from \"./b\";\nexport const lazy = () => import(\"./d\");\n")
	f.file("src/b.ts", "import { a } from \"./a\";\nimport { c } from \"./c\";\n")
	f.file("src/c.ts", "import type { B } from \"./b\";\nexport const c = 1;\n")
	f.file("src/d.ts", "import { a } from \"./a\";\n")
	f.file("main.go", "package main\n") // not a language the graph reads
	for _, p := range []string{"src/a.ts", "src/b.ts", "src/c.ts", "src/d.ts"} {
		f.link(p)
	}
	g := buildImportGraph(f.idx, f.root)
	if k := g.edges["src/c.ts"]["src/b.ts"]; k != importType {
		t.Errorf("c → b kind = %d, want type", k)
	}
	if k := g.edges["src/a.ts"]["src/d.ts"]; k != importLazy {
		t.Errorf("a → d kind = %d, want lazy", k)
	}
	cyc := g.runtimeCycles()
	if len(cyc) != 1 || strings.Join(cyc[0].Nodes, ",") != "src/a.ts,src/b.ts" {
		t.Fatalf("cycles = %+v, want only a ⇄ b", cyc)
	}
	if langs := g.checkedLanguages(); len(langs) != 1 || langs[0] != "typescript" {
		t.Errorf("languages = %v", langs)
	}

	// After an edit the index points at the wrong text: the kind is unknown,
	// so the edge counts as static rather than hiding a cycle.
	if err := os.WriteFile(filepath.Join(f.root, "src/c.ts"), []byte("// moved\n// down\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g = buildImportGraph(f.idx, f.root)
	if k := g.edges["src/c.ts"]["src/b.ts"]; k != importStatic {
		t.Errorf("stale c → b kind = %d, want static", k)
	}
	if cyc := g.runtimeCycles(); len(cyc) != 1 || cyc[0].Size != 3 {
		t.Errorf("stale index: cycles %+v, want a, b and c in one", cyc)
	}

	if res := runtimeCyclesUnder(g, "src/c.ts", 0); res.TotalCycles != 1 {
		t.Errorf("runtimeCyclesUnder(src/c.ts) = %d, want 1", res.TotalCycles)
	}
}

func TestPythonImportGraph(t *testing.T) {
	root := t.TempDir()
	write := func(p, src string) {
		_ = os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755)
		_ = os.WriteFile(filepath.Join(root, p), []byte(src), 0o644)
	}
	write("pkg/a.py", "from pkg.b import B\n")
	write("pkg/b.py", "def f():\n    from pkg.a import A\n    return A\n")
	idx := &scip.SCIPIndex{DocumentsByPath: map[string]*scip.Document{}, DefinitionIndex: map[string]*scip.OccurrenceRef{}}
	add := func(p, sym string, refs ...*scip.Occurrence) {
		def := &scip.Occurrence{Range: []int32{0, 0, 1}, Symbol: sym, SymbolRoles: scip.SymbolRoleDefinition}
		doc := &scip.Document{RelativePath: p, Occurrences: append([]*scip.Occurrence{def}, refs...)}
		idx.Documents = append(idx.Documents, doc)
		idx.DocumentsByPath[p] = doc
		idx.DefinitionIndex[sym] = &scip.OccurrenceRef{Doc: doc, Occ: def}
	}
	add("pkg/a.py", "scip-python python app 1 pkg.a/A#", &scip.Occurrence{Range: []int32{0, 18, 19}, Symbol: "scip-python python app 1 pkg.b/B#"})
	add("pkg/b.py", "scip-python python app 1 pkg.b/B#",
		&scip.Occurrence{Range: []int32{1, 22, 23}, Symbol: "scip-python python app 1 pkg.a/A#"},
		&scip.Occurrence{Range: []int32{2, 11, 12}, Symbol: "scip-python python app 1 pkg.a/A#"}) // a use, not an import
	g := buildImportGraph(idx, root)
	if k := g.edges["pkg/a.py"]["pkg/b.py"]; k != importStatic {
		t.Errorf("a → b = %d, want static", k)
	}
	if k := g.edges["pkg/b.py"]["pkg/a.py"]; k != importLazy {
		t.Errorf("b → a = %d, want lazy only (the use on line 3 is no import)", k)
	}
	if n := len(g.runtimeCycles()); n != 0 {
		t.Errorf("a function-local import closes no runtime cycle, got %d", n)
	}
}

// scip-python in a src layout defines a module by its path ("`src.pkg.b`")
// and references it by its import name ("`pkg.b`"); the edge still counts.
func TestPythonSrcLayoutModuleImports(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "src/pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "src/pkg/a.py"), []byte("import pkg.b\nfrom pkg import c\n"), 0o644)
	const p = "scip-python python pkg 0.1 "
	idx := &scip.SCIPIndex{DocumentsByPath: map[string]*scip.Document{}, DefinitionIndex: map[string]*scip.OccurrenceRef{}}
	add := func(path, sym string, refs ...*scip.Occurrence) {
		def := &scip.Occurrence{Range: []int32{0, 0, 0}, Symbol: sym, SymbolRoles: scip.SymbolRoleDefinition}
		doc := &scip.Document{RelativePath: path, Occurrences: append([]*scip.Occurrence{def}, refs...)}
		idx.Documents = append(idx.Documents, doc)
		idx.DocumentsByPath[path] = doc
		idx.DefinitionIndex[sym] = &scip.OccurrenceRef{Doc: doc, Occ: def}
	}
	add("src/pkg/a.py", p+"`src.pkg.a`/__init__:",
		&scip.Occurrence{Range: []int32{0, 7, 12}, Symbol: p + "`pkg.b`/__init__:"},
		&scip.Occurrence{Range: []int32{1, 5, 8}, Symbol: p + "pkg/__init__:"},
		&scip.Occurrence{Range: []int32{1, 16, 17}, Symbol: p + "`pkg.c`/__init__:"},
		&scip.Occurrence{Range: []int32{1, 16, 17}, Symbol: "scip-python python other 1.0 `pkg.c`/__init__:"}) // another package's pkg.c
	add("src/pkg/__init__.py", p+"`src.pkg`/__init__:")
	add("src/pkg/b.py", p+"`src.pkg.b`/__init__:")
	add("src/pkg/c.py", p+"`src.pkg.c`/__init__:")
	add("lib/pkg/c.py", p+"`lib.pkg.c`/__init__:") // makes "pkg.c" ambiguous
	g := buildImportGraph(idx, root)
	for _, to := range []string{"src/pkg/b.py", "src/pkg/__init__.py"} {
		if g.edges["src/pkg/a.py"][to] != importStatic {
			t.Errorf("a → %s missing: %v", to, g.edges["src/pkg/a.py"])
		}
	}
	if _, ok := g.edges["src/pkg/a.py"]["src/pkg/c.py"]; ok {
		t.Error("pkg.c is defined twice (src/, lib/): no guess")
	}
	if !filesLinked(idx, "src/pkg/b.py", "src/pkg/a.py") {
		t.Error("hidden coupling must see the module import as a link")
	}
}

func TestCycleFindingsOnlyForChangedFiles(t *testing.T) {
	all := []cycles.Cycle{
		{Nodes: []string{"a.ts", "b.ts"}, Size: 2, Edges: []cycles.CycleEdge{{From: "b.ts", To: "a.ts", Recommended: true}}},
		{Nodes: []string{"x.ts", "y.ts", "z.ts"}, Size: 3},
	}
	got, findings := cycleFindings(all, []string{"b.ts", "other.ts"})
	if len(got) != 1 || len(findings) != 1 {
		t.Fatalf("got %d cycles, %d findings; want the one through b.ts", len(got), len(findings))
	}
	fd := findings[0]
	if fd.File != "b.ts" || !strings.Contains(fd.Message, "a.ts, b.ts") || !strings.Contains(fd.Suggestion, "b.ts → a.ts") {
		t.Errorf("finding = %+v", fd)
	}
	if _, none := cycleFindings(all, []string{"other.ts"}); len(none) != 0 {
		t.Error("a cycle the change does not touch is no finding")
	}
}
