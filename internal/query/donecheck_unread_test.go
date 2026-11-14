package query

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
)

// A reader that can't read what it found doesn't make the check clear: each of these
// skipped the symbol silently before, which reads as done.

func longStruct(name string, fields int, extra string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "pub(crate) struct %s {\n", name)
	for i := 0; i < fields; i++ {
		fmt.Fprintf(&b, "    f%d: u8,\n", i)
	}
	b.WriteString(extra)
	return b.String() // No closing brace: the body never ends
}

func TestDoneCheck_UnreadStructBodyIsADoubt(t *testing.T) {
	root := rustRepo(t, map[string]string{"src/a.rs": "fn x() {}\n" + longStruct("S", 3, "")})
	writeRepoFile(t, root, "src/a.rs", "fn x() {}\n"+longStruct("S", 3, "    added: u8,\n"))
	c := rustChecker(root, "src/a.rs")
	c.checkRustFiles(context.Background(), rustFixture{typ: "m/S#"})
	if strings.Join(c.unreadable, ",") != "src/a.rs:S" {
		t.Fatalf("unreadable = %q, want the struct", c.unreadable)
	}

	// The same unread body on both sides, the change elsewhere in the file: no doubt.
	root = rustRepo(t, map[string]string{"src/a.rs": "fn x() {}\n" + longStruct("S", 3, "")})
	writeRepoFile(t, root, "src/a.rs", "fn y() {}\n"+longStruct("S", 3, ""))
	c = rustChecker(root, "src/a.rs")
	c.checkRustFiles(context.Background(), rustFixture{typ: "m/S#"})
	if len(c.unreadable) != 0 {
		t.Errorf("an unchanged body is no doubt: %q", c.unreadable)
	}
}

func TestDoneCheck_ChangedTypeWithoutSymbolIsADoubt(t *testing.T) {
	root := rustRepo(t, map[string]string{"src/a.rs": "pub(crate) struct S {\n    a: u8,\n}\n"})
	writeRepoFile(t, root, "src/a.rs", "pub(crate) struct S {\n    a: u8,\n    b: u8,\n}\n")
	c := rustChecker(root, "src/a.rs")
	c.checkRustFiles(context.Background(), rustFixture{}) // TypeSymbolIn finds nothing
	if c.failed != 1 {
		t.Errorf("failed = %d, want the struct whose uses can't be looked up", c.failed)
	}
}

func TestDoneCheck_VariantWithoutSymbolFallsBackAndDoubts(t *testing.T) {
	const use = "fn a(e: E) -> u8 {\n    match e {\n        E::A => 1,\n        E::B => 2,\n    }\n}\n"
	root := rustRepo(t, map[string]string{"src/e.rs": "pub enum E {\n    A,\n    B,\n}\n", "src/use.rs": use})
	writeRepoFile(t, root, "src/e.rs", "pub enum E {\n    A,\n    B,\n    C,\n}\n")
	c := rustChecker(root, "src/e.rs")
	// The index knows A, not B, and the enum's references reach the match.
	adapter := rustFixture{typ: "m/E#", variants: map[string]string{"A": "m/E#A#"}, refs: map[string][]backends.Reference{
		"m/E#":   {rustRef("src/use.rs", 1), rustRef("src/use.rs", 3), rustRef("src/use.rs", 4)},
		"m/E#A#": {rustRef("src/use.rs", 3)},
	}}
	c.checkRustFiles(context.Background(), adapter)
	if got := siteLines(c); strings.Join(got, "|") != "src/use.rs:match e {" {
		t.Errorf("sites = %q, want the match once", got)
	}
	if c.failed != 1 {
		t.Errorf("failed = %d, want a doubt for B's `Self::`/glob arms", c.failed)
	}
}

func TestDoneCheck_UnreadMatchIsADoubt(t *testing.T) {
	var b strings.Builder
	b.WriteString("fn a(e: E) {\n    match e {\n        E::A => {\n")
	for i := 0; i < 320; i++ {
		b.WriteString("            step();\n")
	}
	b.WriteString("        }\n    }\n}\n")
	root := rustRepo(t, map[string]string{"src/e.rs": "pub enum E {\n    A,\n}\n", "src/use.rs": b.String()})
	writeRepoFile(t, root, "src/e.rs", "pub enum E {\n    A,\n    B,\n}\n")
	c := rustChecker(root, "src/e.rs")
	adapter := rustFixture{typ: "m/E#", variants: map[string]string{"A": "m/E#A#"}, refs: map[string][]backends.Reference{
		"m/E#A#": {rustRef("src/use.rs", 3)},
	}}
	c.checkRustFiles(context.Background(), adapter)
	if len(c.sites) != 0 || !c.cut {
		t.Errorf("sites %q, cut %v: a match too long to read is a doubt, not complete", siteLines(c), c.cut)
	}
}

func TestDoneCheck_UnreadLiteralIsADoubt(t *testing.T) {
	var b strings.Builder
	b.WriteString("fn f() -> S {\n    S {\n        a: vec![\n")
	for i := 0; i < 1100; i++ {
		b.WriteString("            1,\n")
	}
	b.WriteString("        ],\n    }\n}\n")
	root := rustRepo(t, map[string]string{"src/s.rs": "pub struct S {\n    a: Vec<u8>,\n}\n", "src/use.rs": b.String()})
	writeRepoFile(t, root, "src/s.rs", "pub struct S {\n    a: Vec<u8>,\n    b: u8,\n}\n")
	c := rustChecker(root, "src/s.rs")
	c.checkRustFiles(context.Background(), rustFixture{typ: "m/S#", refs: map[string][]backends.Reference{"m/S#": {rustRef("src/use.rs", 2)}}})
	if len(c.sites) != 0 || !c.cut {
		t.Errorf("sites %q, cut %v: a literal too long to read is a doubt", siteLines(c), c.cut)
	}
}

func TestDoneCheck_UnreadGoStructIsADoubt(t *testing.T) {
	body := func(extra string) string {
		var b strings.Builder
		b.WriteString("package p\n\ntype Big struct {\n")
		for i := 0; i < 600; i++ {
			fmt.Fprintf(&b, "\tF%d int\n", i)
		}
		b.WriteString(extra + "}\n")
		return b.String()
	}
	root := rustRepo(t, map[string]string{"p/big.go": body("")})
	writeRepoFile(t, root, "p/big.go", body("\tAdded int\n"))
	c := rustChecker(root, "p/big.go")
	c.checkTypeFiles(context.Background(), rustFixture{typ: "p/Big#"})
	if strings.Join(c.unreadable, ",") != "p/big.go:Big" {
		t.Errorf("unreadable = %q, want Big", c.unreadable)
	}
}
