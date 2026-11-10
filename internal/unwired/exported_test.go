package unwired

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// rulesFor writes the files into a temp repo and indexes one definition per
// symbol at the given 0-based line.
func rulesFor(t *testing.T, files map[string]string, defs map[string]struct {
	path string
	line int32
}) *exportRules {
	t.Helper()
	root := t.TempDir()
	for path, text := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	index := &scip.SCIPIndex{DefinitionIndex: make(map[string]*scip.OccurrenceRef)}
	docs := make(map[string]*scip.Document)
	for id, d := range defs {
		doc := docs[d.path]
		if doc == nil {
			doc = &scip.Document{RelativePath: d.path}
			docs[d.path] = doc
		}
		index.DefinitionIndex[id] = &scip.OccurrenceRef{
			Doc: doc,
			Occ: &scip.Occurrence{Symbol: id, Range: []int32{d.line, 0, 4}, SymbolRoles: scip.SymbolRoleDefinition},
		}
	}
	return newExportRules(root, index)
}

type def = struct {
	path string
	line int32
}

func TestExportedTypeScript(t *testing.T) {
	const p = "scip-typescript npm unw 1.0.0 src/`lib.ts`/"
	lib := `export function wired(): number { return helper(); }
function helper(): number { return 1; }
export const arrow = () => 3;
function listed() {}
function renamed() {}
export { listed, renamed as other };
export { reexported } from './elsewhere';
function reexported() {}
export class Box {
  open(): void {}
  private shut(): void {}
  protected static peek(): void {}
}
class Hidden {
  open(): void {}
}
export default function main() {}
`
	r := rulesFor(t, map[string]string{"src/lib.ts": lib}, map[string]def{
		p + "wired().":       {"src/lib.ts", 0},
		p + "helper().":      {"src/lib.ts", 1},
		p + "arrow.":         {"src/lib.ts", 2},
		p + "listed().":      {"src/lib.ts", 3},
		p + "renamed().":     {"src/lib.ts", 4},
		p + "reexported().":  {"src/lib.ts", 7},
		p + "Box#":           {"src/lib.ts", 8},
		p + "Box#open().":    {"src/lib.ts", 9},
		p + "Box#shut().":    {"src/lib.ts", 10},
		p + "Box#peek().":    {"src/lib.ts", 11},
		p + "Hidden#":        {"src/lib.ts", 13},
		p + "Hidden#open().": {"src/lib.ts", 14},
		p + "main().":        {"src/lib.ts", 16},
		p + "notIndexed().":  {"src/gone.ts", 0},
	})
	cases := map[string]bool{
		"wired().":       true,
		"helper().":      false,
		"arrow.":         true,
		"listed().":      true,
		"renamed().":     true,
		"reexported().":  false, // the list exports another file's reexported
		"Box#":           true,
		"Box#open().":    true,
		"Box#shut().":    false,
		"Box#peek().":    false,
		"Hidden#open().": false,
		"main().":        true,
		"notIndexed().":  false,
	}
	for suffix, want := range cases {
		id := p + suffix
		if got := r.exported(id, extractName(id)); got != want {
			t.Errorf("%s: exported = %v, want %v", suffix, got, want)
		}
	}
}

func TestExportedCommonJS(t *testing.T) {
	const p = "scip-typescript npm unw 1.0.0 `lib.js`/"
	lib := `function a() {}
function b() {}
function c() {}
function d() {}
exports.a = a;
module.exports = { b, alias: c };
`
	r := rulesFor(t, map[string]string{"lib.js": lib}, map[string]def{
		p + "a().": {"lib.js", 0}, p + "b().": {"lib.js", 1}, p + "c().": {"lib.js", 2}, p + "d().": {"lib.js", 3},
	})
	for suffix, want := range map[string]bool{"a().": true, "b().": true, "c().": true, "d().": false} {
		id := p + suffix
		if got := r.exported(id, extractName(id)); got != want {
			t.Errorf("%s: exported = %v, want %v", suffix, got, want)
		}
	}
}

func TestExportedRust(t *testing.T) {
	const p = "rust-analyzer cargo unw 0.1.0 "
	lib := `pub fn wired() -> i32 { 1 }
fn helper() -> i32 { 1 }
pub(crate) fn crate_only() {}
#[inline] pub fn attributed() {}
    pub fn open(&self) {}
`
	r := rulesFor(t, map[string]string{"src/lib.rs": lib}, map[string]def{
		p + "wired().":           {"src/lib.rs", 0},
		p + "helper().":          {"src/lib.rs", 1},
		p + "crate_only().":      {"src/lib.rs", 2},
		p + "attributed().":      {"src/lib.rs", 3},
		p + "impl#[Boxy]open().": {"src/lib.rs", 4},
	})
	for suffix, want := range map[string]bool{
		"wired().": true, "helper().": false, "crate_only().": false, "attributed().": true, "impl#[Boxy]open().": true,
	} {
		if got := r.exported(p+suffix, "x"); got != want {
			t.Errorf("%s: exported = %v, want %v", suffix, got, want)
		}
	}
	// rust-analyzer IDs carry no path; the definition supplies it.
	if path, line := r.site(p + "helper()."); path != "src/lib.rs" || line != 2 {
		t.Errorf("site = %q:%d, want src/lib.rs:2", path, line)
	}
}

func TestExportedPython(t *testing.T) {
	const p = "scip-python python unw 0.1.0 `pkg._impl`/"
	cases := map[string]struct {
		name string
		want bool
	}{
		"run().":             {"run", true},
		"_helper().":         {"_helper", false},
		"Engine#start().":    {"start", true},
		"_Engine#start().":   {"start", false},
		"Engine#__init__().": {"__init__", false},
	}
	r := newExportRules(t.TempDir(), nil)
	for suffix, c := range cases {
		if got := r.exported(p+suffix, c.name); got != c.want {
			t.Errorf("%s: exported = %v, want %v", suffix, got, c.want)
		}
	}
}

func TestExportedGoUnchanged(t *testing.T) {
	r := newExportRules(t.TempDir(), nil)
	const p = "scip-go gomod github.com/x/y v1 `github.com/x/y/pkg`/"
	if !r.exported(p+"Run().", "Run") || r.exported(p+"run().", "run") {
		t.Error("Go keeps the capital-letter rule")
	}
}
