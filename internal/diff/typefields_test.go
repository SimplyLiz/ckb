package diff

import (
	"reflect"
	"strings"
	"testing"
)

func TestGoStructFields(t *testing.T) {
	lines := strings.Split(`package cv

// CV is a résumé.
type CV struct {
	Title string `+"`json:\"title\"`"+` // the headline
	Skills []string
	Meta struct {
		Created int
	}
}

type (
	Grouped struct {
		A, B int
	}
	Unit struct{}
)
`, "\n")
	fields, ok := GoStructFields(lines, "CV")
	if !ok || !reflect.DeepEqual(fields, []string{"Metastruct{Createdint}", "Skills[]string", "Titlestring"}) {
		t.Errorf("CV = %v, %v", fields, ok)
	}
	if fields, ok := GoStructFields(lines, "Grouped"); !ok || !reflect.DeepEqual(fields, []string{"A,Bint"}) {
		t.Errorf("Grouped = %v, %v", fields, ok)
	}
	if fields, ok := GoStructFields(lines, "Unit"); !ok || len(fields) != 0 {
		t.Errorf("Unit = %v, %v", fields, ok)
	}
	if _, ok := GoStructFields(lines, "Missing"); ok {
		t.Error("Missing found")
	}

	tagged := strings.Split("type T struct {\n\tA int `json:\"a\"`\n}\n", "\n")
	retagged := strings.Split("type T struct {\n\tA int `json:\"b\"`\n}\n", "\n")
	a, _ := GoStructFields(tagged, "T")
	b, _ := GoStructFields(retagged, "T")
	if !reflect.DeepEqual(a, b) {
		t.Errorf("a tag change counts as a field change: %v vs %v", a, b)
	}
	if got := DeclaredTypes(lines, "go"); !reflect.DeepEqual(got, []string{"CV", "Meta", "Grouped", "Unit"}) {
		t.Errorf("declared = %v", got)
	}
}

func TestTSTypeFields(t *testing.T) {
	lines := strings.Split(`import { Base } from './base';

export interface CV extends Base {
  title: string; // headline
  skills?: string[],
  render(): string
}

export type Draft<T> = {
  title: string
  body: T
}

export class Store {
  private items: CV[] = [];
  readonly name = 'store';
  add(cv: CV): void {
    this.items.push(cv);
  }
}
`, "\n")
	if fields, ok := TSTypeFields(lines, "CV"); !ok || !reflect.DeepEqual(fields, []string{"render():string", "skills?:string[]", "title:string"}) {
		t.Errorf("CV = %v, %v", fields, ok)
	}
	if fields, ok := TSTypeFields(lines, "Draft"); !ok || !reflect.DeepEqual(fields, []string{"body:T", "title:string"}) {
		t.Errorf("Draft = %v, %v", fields, ok)
	}
	if fields, ok := TSTypeFields(lines, "Store"); !ok || !reflect.DeepEqual(fields, []string{"privateitems:CV[]=[]", "readonlyname='store'"}) {
		t.Errorf("Store = %v, %v (methods must not count)", fields, ok)
	}
	if got := DeclaredTypes(lines, "typescript"); !reflect.DeepEqual(got, []string{"CV", "Draft", "Store"}) {
		t.Errorf("declared = %v", got)
	}
}

func TestGoLiteral(t *testing.T) {
	lines := strings.Split(`package cv

func Import(title string) CV {
	return CV{
		Title: title,
	}
}

var empty = CV{}
var all = []CV{{Title: "a"}}
var byName = map[string]CV{"a": {}}
var ptr = &CV{Title: "p"}
var gen = Box[CV]{V: 1}
type CV struct {
	Title string
}
func (c CV) String() string { return c.Title }
var passed = []CV{existing}
var multi = []CV{
	{Title: "m"},
}
`, "\n")
	cases := map[int]struct {
		ok  bool
		end int
	}{
		3:  {false, 0}, // return type, a space before the brace
		4:  {true, 6},  // return CV{ ... }
		9:  {false, 0}, // CV{} builds nothing by its fields
		10: {true, 10}, // []CV{{...}}
		11: {true, 11}, // map[string]CV{...}
		12: {true, 12}, // &CV{...}
		14: {false, 0}, // the declaration
		17: {false, 0}, // receiver
		18: {false, 0}, // []CV{existing} builds nothing here
		19: {true, 21}, // []CV{ {..} } does
	}
	for line, want := range cases {
		end, ok := goLiteral(lines, line, 0, "CV")
		if ok != want.ok || (ok && end != want.end) {
			t.Errorf("line %d %q: ok=%v end=%d, want ok=%v end=%d", line, strings.TrimSpace(lines[line-1]), ok, end, want.ok, want.end)
		}
	}
	if end, ok := goLiteral(lines, 13, 0, "Box"); !ok || end != 13 {
		t.Errorf("Box[CV]{...}: %v %d", ok, end)
	}
	qualified := []string{"return []impact.CV{entry}, nil", "x := impact.CV{Title: t}", "y := &impact.CV{}"}
	if _, ok := goLiteral(qualified, 1, 0, "CV"); ok {
		t.Error("[]impact.CV{entry} builds nothing by its fields")
	}
	if _, ok := goLiteral(qualified, 2, 0, "CV"); !ok {
		t.Error("impact.CV{Title: t} is a literal")
	}
	if _, ok := goLiteral(qualified, 3, 0, "CV"); ok {
		t.Error("&impact.CV{} builds nothing by its fields")
	}
}

func TestTSConstruction(t *testing.T) {
	lines := strings.Split(`import { CV } from './types';
import type { Draft } from './types';

export function importCv(raw: any): CV {
  return {
    title: raw.title,
  };
}

export const empty: CV = {
  title: '',
};
export const many: CV[] = [
  { title: 'a' },
];
const make = (raw: any): CV => ({
  title: raw.title,
});
const cast = { title: 'x' } as CV;
const sat = { title: 'x' } satisfies CV;
const old = <CV>{ title: 'y' };
function use(cv: CV): void {}
class Impl implements CV {
  title = '';
}
interface Rich extends CV {
  extra: string;
}
export interface CV {
  title: string;
}
const loaded: CV = await load();
const p: Promise<CV> = fetchIt();
`, "\n")
	cases := map[int]struct {
		ok  bool
		end int
	}{
		1:  {false, 0}, // import
		4:  {true, 8},  // ): CV { ... }
		10: {true, 12}, // : CV = { ... }
		13: {true, 15}, // : CV[] = [ ... ]
		16: {true, 18}, // ): CV => ({ ... })
		19: {true, 19}, // as CV
		20: {true, 20}, // satisfies CV
		21: {true, 21}, // <CV>{...}
		22: {false, 0}, // parameter
		23: {false, 0}, // implements
		26: {false, 0}, // extends
		29: {false, 0}, // the declaration
		32: {false, 0}, // = await load()
		33: {false, 0}, // Promise<CV>
	}
	for line, want := range cases {
		end, ok := tsConstruction(lines, line, 0, "CV")
		if ok != want.ok || (ok && end != want.end) {
			t.Errorf("line %d %q: ok=%v end=%d, want ok=%v end=%d", line, strings.TrimSpace(lines[line-1]), ok, end, want.ok, want.end)
		}
	}
}

func TestTypeSpan(t *testing.T) {
	lines := strings.Split("package cv\n\ntype CV struct {\n\tTitle string\n}\n\nfunc f() {}\n", "\n")
	if first, last, ok := TypeSpan(lines, "go", "CV"); !ok || first != 3 || last != 5 {
		t.Errorf("CV span = %d..%d %v, want 3..5", first, last, ok)
	}
	if _, _, ok := TypeSpan(lines, "go", "f"); ok {
		t.Error("a function is not a type")
	}
	ts := strings.Split("export interface CV {\n  title: string;\n}\n", "\n")
	if first, last, ok := TypeSpan(ts, "typescript", "CV"); !ok || first != 1 || last != 3 {
		t.Errorf("TS span = %d..%d %v, want 1..3", first, last, ok)
	}
}

func TestTypeLanguage(t *testing.T) {
	for path, want := range map[string]string{"a/b.go": "go", "x.ts": "typescript", "x.tsx": "typescript", "x.rs": "", "x.js": ""} {
		if got := TypeLanguage(path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}

func TestBraceEnd_SkipsStringsAndComments(t *testing.T) {
	lines := []string{
		`x := CV{`,
		`	Title: "}", // }`,
		`	Note: '}',`,
		`}`,
	}
	if end := braceEnd(lines, 1, 7); end != 4 {
		t.Errorf("end = %d, want 4", end)
	}
}

func TestGoLiteralKeys(t *testing.T) {
	tests := []struct {
		name       string
		lines      []string
		keys       string
		positional bool
	}{
		{"keyed one line", []string{"x := T{A: 1, B: f(a, b)}"}, "A B", false},
		{"keyed multi-line", []string{"x := &T{", "\tA: 1, // why", "\tB: \"a, b\",", "}"}, "A B", false},
		{"positional", []string{"x := T{1, 2}"}, "", true},
		{"nested value", []string{"x := T{A: map[string]int{\"k\": 1}, B: []int{1}}"}, "A B", false},
		{"slice elements", []string{"xs := []T{", "\t{A: 1},", "\t{B: 2},", "}"}, "A B", false},
		{"slice positional element", []string{"xs := []T{{A: 1}, {1, 2}}"}, "A", true},
		{"map values", []string{"m := map[string]T{", "\t\"a\": {A: 1},", "}"}, "A", false},
		{"comments around keys", []string{"x := T{", "\t/* first */ A: 1,", "\tB: 2, // last", "\t// trailing", "}"}, "A B", false},
		{"comparison in a value", []string{"x := T{A: a < b, B: c > d}"}, "A B", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys, positional, ok := GoLiteralKeys(tt.lines, 1, 0, "T")
			if !ok || strings.Join(keys, " ") != tt.keys || positional != tt.positional {
				t.Errorf("got %v positional=%v ok=%v, want %q positional=%v", keys, positional, ok, tt.keys, tt.positional)
			}
		})
	}
}

func TestGoStructFieldTypes(t *testing.T) {
	lines := []string{"type T struct {", "\tA, B int `json:\"a\"`", "\t*pkg.Embedded", "\tC func(a, b int) error // c", "}"}
	got, ok := GoStructFieldTypes(lines, "T")
	want := map[string]string{"A": "int", "B": "int", "Embedded": "*pkg.Embedded", "C": "func(a,bint)error"}
	if !ok || len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
}
