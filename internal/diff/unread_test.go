package diff

import (
	"fmt"
	"strings"
	"testing"
)

// A reader that finds what it looks for but not its end says so: "unread" is a doubt
// for the caller, "none" a fact. Each bound below is one of those ends.

func TestTypeFields_UnreadIsNotMissing(t *testing.T) {
	var b strings.Builder
	b.WriteString("type Big struct {\n")
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&b, "\tF%d int\n", i)
	}
	b.WriteString("}\ntype Small struct {\n\tA int\n}\n")
	src := lines(b.String())
	if _, state := TypeFields(src, "go", "Big"); state != BodyUnread {
		t.Errorf("Big: %v, want unread", state)
	}
	if fields, state := TypeFields(src, "go", "Small"); state != BodyRead || len(fields) != 1 {
		t.Errorf("Small: %v %v", fields, state)
	}
	if _, state := TypeFields(src, "go", "Missing"); state != BodyNone {
		t.Errorf("Missing: %v, want none", state)
	}
	if at := DeclarationAt(src, "go", "Small"); at != 603 {
		t.Errorf("Small declared at %d, want 603", at)
	}
}

func TestRustMatchAt_Unread(t *testing.T) {
	var b strings.Builder
	b.WriteString("fn f(e: E) {\n    match e {\n        E::A => {\n")
	for i := 0; i < 320; i++ {
		b.WriteString("            step();\n")
	}
	b.WriteString("        }\n        E::B => {}\n    }\n}\n")
	src := lines(b.String())
	// The first arm: its match is in reach but runs past the bound.
	if m, ok := RustMatchAt(src, 3, 0, "E"); ok || !m.Unread {
		t.Errorf("first arm: %+v %v, want unread", m, ok)
	}
	// The last: the keyword is past the bound above it; the arm's `=>` says it is one.
	if m, ok := RustMatchAt(src, 325, 0, "E"); ok || !m.Unread {
		t.Errorf("last arm: %+v %v, want unread", m, ok)
	}
	// A use outside any match is no doubt.
	plain := lines("fn g() -> E {\n    E::A\n}\n")
	if m, ok := RustMatchAt(plain, 2, 0, "E"); ok || m.Unread {
		t.Errorf("plain use: %+v %v", m, ok)
	}
	// `=>` in a string after the name isn't an arm.
	str := lines("fn g() {\n    log(E::A, \"=>\");\n}\n")
	if m, _ := RustMatchAt(str, 2, 0, "E"); m.Unread {
		t.Error("`=>` in a string read as an arm")
	}
}

func TestIsLiteralUnread(t *testing.T) {
	var b strings.Builder
	b.WriteString("fn f() -> S {\n    S {\n        a: vec![\n")
	for i := 0; i < 1100; i++ {
		b.WriteString("            1,\n")
	}
	b.WriteString("        ],\n    }\n}\n")
	src := lines(b.String())
	if IsRustLiteralUse(src, 2, 0, "S") || !IsLiteralUnread("rust", src, 2, 0, "S") {
		t.Error("a literal past the bound is unread, not a literal without `..`")
	}
	short := lines("fn f() -> S {\n    S { a: 1 }\n}\n")
	if !IsRustLiteralUse(short, 2, 0, "S") || IsLiteralUnread("rust", short, 2, 0, "S") {
		t.Error("a short literal is read")
	}
	if IsLiteralUnread("go", src, 2, 0, "S") {
		t.Error("only Rust's literal reader has a bound")
	}
}
