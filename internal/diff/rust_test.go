package diff

import (
	"slices"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

func rustLines(text string) []string { return strings.Split(text, "\n") }

func TestRustStructFields(t *testing.T) {
	before := rustLines(`#[salsa::interned(debug)]
pub struct CallableType<'db> {
    #[returns(ref)]
    pub(crate) signatures: CallableSignature<'db>,
    /// Doc, with a comma, here.
    is_function_like: bool, // trailing
}
struct Unit;
struct Tuple(u32, u32);`)
	got, ok := RustStructFields(before, "CallableType")
	want := []string{"is_function_like:bool", "signatures:CallableSignature<db>"}
	if !ok || !slices.Equal(got, want) {
		t.Errorf("fields = %v, %v, want %v", got, ok, want)
	}
	for _, name := range []string{"Unit", "Tuple", "Missing"} {
		if _, ok := RustStructFields(before, name); ok {
			t.Errorf("%s has no braced fields", name)
		}
	}
	// A comment or reformat isn't a change; a generic in a field type doesn't split it.
	a, _ := RustStructFields(rustLines("struct S {\n  a: Map<K, V>,\n  b: u8\n}"), "S")
	b, _ := RustStructFields(rustLines("struct S { b: u8, /* x */\n // y\n a: Map<K,V> }"), "S")
	if len(a) != 2 || !slices.Equal(a, b) {
		t.Errorf("same fields read differently: %v vs %v", a, b)
	}
}

func TestIsRustLiteralUse(t *testing.T) {
	cases := []struct {
		name, text string
		want       bool
	}{
		{"literal", "let a = Foo { x: 1 };", true},
		{"nested field", "outer: Foo { x: 1 },", true},
		{"in a call", "Some(Foo { x: 1 })", true},
		{"path", "let a = m::n::Foo { x: 1 };", true},
		{"turbofish", "let a = Foo::<u8> { x: 1 };", true},
		{"pattern", "let Foo { x } = a;", true},
		{"pattern, rest", "let Foo { x, .. } = a;", false},
		{"update syntax", "let a = Foo { x: 1, ..Default::default() };", false},
		{"nested rest isn't this one's", "let a = Foo { x: Bar { .. } };", true},
		{"inherent impl", "impl Foo {", false},
		{"generic impl", "impl<T> Foo<T> {", false},
		{"trait impl", "impl Trait for Foo {", false},
		{"return type", "fn f() -> Foo {", false},
		{"return path", "fn f() -> m::Foo {", false},
		{"return reference", "fn f(&self) -> &Foo {", false},
		{"return mutable reference", "pub fn f(&mut self) -> &mut Foo {", false},
		{"return reference, own line", ") -> &mut Foo {", false},
		{"return reference with a lifetime", "fn f<'a>(a: &'a B) -> &'a mut Foo {", false},
		{"return reference to a path", "fn f(&self) -> &m::Foo {", false},
		{"return raw pointer", "fn f() -> *mut Foo {", false},
		{"literal behind a reference", "g(&Foo { x: 1 })", true},
		{"literal behind a mutable reference", "let a = &mut Foo { x: 1 };", true},
		{"call", "Foo::new(1)", false},
		{"type", "let a: Foo = b;", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A body follows, as in a real file: without its closing brace, a signature
			// reads as an unterminated literal and every case below it passes for free.
			lines := []string{tc.text, "    body", "}"}
			if got := IsRustLiteralUse(lines, 1, 0, "Foo"); got != tc.want {
				t.Errorf("%q: %v, want %v", tc.text, got, tc.want)
			}
		})
	}
	multi := rustLines("let a = Foo {\n  x: 1,\n  ..b\n};")
	if IsRustLiteralUse(multi, 1, 0, "Foo") {
		t.Error("`..` on a later line covers the rest")
	}
}

func TestRustReexports(t *testing.T) {
	got := RustReexports(rustLines(`use crate::private::Hidden;
pub use crate::core::CompileMode;
pub(crate) use a::{B, c::{D, E as F}, self};
pub use g::*;
pub use h::{
    I,
    J as K,
};
fn pub_use() {}`))
	var names []string
	for name := range got {
		names = append(names, name)
	}
	slices.Sort(names)
	want := []string{"B", "CompileMode", "D", "F", "I", "K"}
	if !slices.Equal(names, want) {
		t.Errorf("re-exports = %v, want %v", names, want)
	}
}

func TestIsRustTypeAlias(t *testing.T) {
	for text, want := range map[string]bool{
		"pub(crate) type R<'db> = Result<A, B>;": true,
		"type R = u8":                            true,
		"pub struct R {":                         false,
		"fn type_of() -> u8":                     false,
	} {
		if IsRustTypeAlias(text) != want {
			t.Errorf("%q: want %v", text, want)
		}
	}
}

// A field added on its own line touches a multi-line literal without touching its name.
func TestClassifyUseLiteralSpansItsBraces(t *testing.T) {
	oldLines := rustLines("fn f() {\n    let a = Foo {\n        x: 1,\n    };\n}")
	newLines := rustLines("fn f() {\n    let a = Foo {\n        x: 1,\n        y: 2,\n    };\n}")
	file := changed(impact.ChangeBlock{OldLine: 4, NewLine: 4, Added: []string{"        y: 2,"}})
	site := UseSite{Line: 2, Name: "Foo", Literal: true}
	if got := ClassifyUse(file, oldLines, newLines, site); got.State != UseUpdated {
		t.Errorf("state = %v, want updated", got.State)
	}
	site.Literal = false
	if got := ClassifyUse(file, oldLines, newLines, site); got.State != UseOpen {
		t.Errorf("without Literal: state = %v, want open", got.State)
	}
}

// A declaration right after the imports is code, not the tail of the import list.
func TestIsImportLine_DeclarationAfterImports(t *testing.T) {
	lines := rustLines(`import { A } from './a';
export interface Foo {
  ledger: BudgetLedger
  other,
}
import {
  B,
  C as D,
} from './b';
use crate::{x, y};
pub struct Bar {
    lookup: Lookup,
}
use crate::{
    z,
};
pub struct Baz {
    z,
}`)
	for line, want := range map[int]bool{2: false, 3: false, 4: false, 7: true, 8: true, 11: false, 12: false, 15: true, 17: false, 18: false} {
		if got := isImportLine(lines, line); got != want {
			t.Errorf("line %d (%q): %v, want %v", line, lines[line-1], got, want)
		}
	}
}

func TestIsImportLine_RustUseTree(t *testing.T) {
	lines := rustLines(`use crate::{
    place::{Place, lookup},
    types::{
        a, lookup as b,
    },
};
fn f() {
    let v = T {
        lookup,
    };
    g(
        lookup,
    );
}
use x::y;
S {
    lookup,
}`)
	for line, want := range map[int]bool{2: true, 4: true, 9: false, 12: false, 17: false} {
		if got := isImportLine(lines, line); got != want {
			t.Errorf("line %d (%q): %v, want %v", line, lines[line-1], got, want)
		}
	}
}

// A removed entry of a private import list took nothing from another file; one of a
// re-export did.
func TestWithoutPrivateImports(t *testing.T) {
	oldAll := map[int]string{}
	for i, line := range strings.Split(`use core::{
    a, b,
};
pub use core::{
    c,
};
fn f() {
    g(a, b);
}`, "\n") {
		oldAll[i+1] = line
	}
	for _, tc := range []struct {
		oldLine int
		removed string
		kept    bool
	}{
		{2, "    a, b,", false},
		{5, "    c,", true},
		{8, "    g(a, b);", true},
	} {
		got := withoutPrivateImports("lib.rs", oldAll, impact.ChangeBlock{OldLine: tc.oldLine, Removed: []string{tc.removed}})
		if (len(got) == 1) != tc.kept {
			t.Errorf("line %d %q: kept %v, want %v", tc.oldLine, tc.removed, got, tc.kept)
		}
	}
	ts := map[int]string{1: "import {", 2: "  A,", 3: "} from './a';", 4: "export {", 5: "  B,", 6: "} from './b';"}
	if got := withoutPrivateImports("x.ts", ts, impact.ChangeBlock{OldLine: 2, Removed: []string{"  A,"}}); len(got) != 0 {
		t.Errorf("ts import entry kept: %v", got)
	}
	if got := withoutPrivateImports("x.ts", ts, impact.ChangeBlock{OldLine: 5, Removed: []string{"  B,"}}); len(got) != 1 {
		t.Errorf("ts re-export entry dropped")
	}
}

func TestArrayLengthChange(t *testing.T) {
	if got, ok := ArrayLengthChange("pub const KNOWN: [&str; 10]", "pub const KNOWN: [&str; 12]"); !ok || len(got) != 1 || got[0] != "10" {
		t.Errorf("length only: %v %v", got, ok)
	}
	for _, pair := range [][2]string{
		{"pub const KNOWN: [&str; 10]", "pub const KNOWN: [String; 12]"},
		{"pub const KNOWN: [&str; 10]", "pub const KNOWN: [&str; 10]"},
		{"pub fn f(x: u8)", "pub fn f(x: u16)"},
	} {
		if _, ok := ArrayLengthChange(pair[0], pair[1]); ok {
			t.Errorf("%q -> %q taken for a length change", pair[0], pair[1])
		}
	}
	if !SpellsArrayLength("fn f(x: &[&str; 10]) {", []string{"10"}) || SpellsArrayLength("KNOWN.contains(&op)", []string{"10"}) || SpellsArrayLength("let a: [u8; 4] = x;", []string{"10"}) {
		t.Error("SpellsArrayLength")
	}
}
