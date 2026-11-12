package query

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
	"github.com/SimplyLiz/CodeMCP/internal/diff"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// rustFixture answers from a table instead of a SCIP index.
type rustFixture struct {
	refs map[string][]backends.Reference
	typ  string
	ctor string
	// variants are the enum's variant symbols by name; none makes the check fall back
	// to the enum's references.
	variants map[string]string
}

func (f rustFixture) FindReferences(_ context.Context, id string, _ backends.RefOptions) (*backends.ReferencesResult, error) {
	refs := f.refs[id]
	return &backends.ReferencesResult{References: refs, TotalReferences: len(refs)}, nil
}

func (f rustFixture) TypeSymbolIn(string, string) string { return f.typ }

func (f rustFixture) GeneratedMethods(string, string) []string {
	if f.ctor == "" {
		return nil
	}
	return []string{f.ctor}
}

func (f rustFixture) VariantSymbolsIn(string, string) map[string]string { return f.variants }

func rustRef(path string, line int) backends.Reference {
	return backends.Reference{Location: backends.Location{Path: path, Line: line}}
}

func rustRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		writeRepoFile(t, root, path, content)
	}
	gitInit(t, root)
	return root
}

func rustChecker(root string, changed ...string) *doneChecker {
	parsed := &impact.ParsedDiff{}
	for _, path := range changed {
		parsed.Files = append(parsed.Files, impact.ChangedFile{OldPath: path, NewPath: path})
	}
	return newDoneChecker(root, AnalyzeChangeSetOptions{}, parsed)
}

// rustDiffChecker checks the working tree against HEAD, with the diff's hunks.
func rustDiffChecker(t *testing.T, root string) *doneChecker {
	t.Helper()
	cmd := exec.Command("git", "diff", "-U0")
	cmd.Dir = root
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := diff.ParseGitDiff(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return newDoneChecker(root, AnalyzeChangeSetOptions{}, parsed)
}

func siteLines(c *doneChecker) []string {
	var out []string
	for _, site := range c.sites {
		out = append(out, site.File+":"+strings.TrimSpace(site.Code))
	}
	return out
}

func TestRustStructFields_LiteralsAndGeneratedConstructor(t *testing.T) {
	const useSrc = `fn a() { let x = Foo { a: 1 }; }
fn b() { let x = Foo { a: 1, ..d() }; }
impl Foo {
}
fn c() { Foo::new(1); }
fn t(x: Foo) {}
`
	root := rustRepo(t, map[string]string{
		"src/foo.rs": "pub struct Foo {\n    a: u8,\n}\n",
		"src/use.rs": useSrc,
	})
	writeRepoFile(t, root, "src/foo.rs", "pub struct Foo {\n    a: u8,\n    b: u8,\n}\n")

	c := rustChecker(root, "src/foo.rs")
	adapter := rustFixture{
		typ: "m/Foo#", ctor: "m/impl#[Foo]new().",
		refs: map[string][]backends.Reference{
			"m/Foo#":             {rustRef("src/use.rs", 1), rustRef("src/use.rs", 2), rustRef("src/use.rs", 3), rustRef("src/use.rs", 5), rustRef("src/use.rs", 6)},
			"m/impl#[Foo]new().": {rustRef("src/use.rs", 5)},
		},
	}
	open := c.checkRustFiles(context.Background(), adapter)

	got := siteLines(c)
	want := []string{"src/use.rs:fn a() { let x = Foo { a: 1 }; }", "src/use.rs:fn c() { Foo::new(1); }"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("sites = %q, want %q", got, want)
	}
	if len(open) != 1 || !open[0].files["src/use.rs"] {
		t.Errorf("open = %+v", open)
	}
	if c.sites[0].Via == "" {
		t.Error("a use listed for its struct's fields has to say so")
	}
}

func TestRustStructFields_SameFieldsListNothing(t *testing.T) {
	root := rustRepo(t, map[string]string{
		"src/foo.rs": "pub struct Foo {\n    a: u8,\n}\n",
		"src/use.rs": "fn a() { let x = Foo { a: 1 }; }\n",
	})
	writeRepoFile(t, root, "src/foo.rs", "pub struct Foo {\n    // Why a is here.\n    a: u8,\n}\nfn other() {}\n")

	c := rustChecker(root, "src/foo.rs")
	adapter := rustFixture{typ: "m/Foo#", refs: map[string][]backends.Reference{"m/Foo#": {rustRef("src/use.rs", 1)}}}
	if open := c.checkRustFiles(context.Background(), adapter); len(open) != 0 || len(c.sites) != 0 {
		t.Errorf("open = %+v, sites = %v", open, siteLines(c))
	}
}

func TestRustAlias_FollowsFunctionsNamingIt(t *testing.T) {
	root := rustRepo(t, map[string]string{
		"src/place.rs": "pub type R = Result<u8, u8>;\npub fn lookup(\n    a: u8,\n) -> R {\n    let inner: R = Ok(a);\n    inner\n}\n",
		"src/use.rs":   "fn a() { lookup(1).ok(); }\n",
	})
	sym := impact.ChangedSymbol{SymbolID: "m/R#", Name: "R", File: "src/place.rs", DeclarationBefore: "pub type R = Result<u8, u8>;"}
	sig := rustRef("src/place.rs", 4)
	sig.FromSymbol, sig.FromSymbolName = "m/lookup().", "lookup"
	body := rustRef("src/place.rs", 5)
	body.FromSymbol, body.FromSymbolName = "m/caller().", "caller"
	adapter := rustFixture{refs: map[string][]backends.Reference{
		"m/R#":        {sig, body},
		"m/lookup().": {rustRef("src/use.rs", 1)},
		"m/caller().": {rustRef("src/use.rs", 1)},
	}}

	c := rustChecker(root, "src/place.rs")
	files := c.checkRustAlias(context.Background(), adapter, sym)
	if !files["src/use.rs"] || len(c.sites) != 1 || c.sites[0].Symbol != "lookup" {
		t.Errorf("files = %v, sites = %+v", files, c.sites)
	}

	// Only an alias is another type to whoever holds one.
	sym.DeclarationBefore = "pub struct R {"
	c = rustChecker(root, "src/place.rs")
	if files := c.checkRustAlias(context.Background(), adapter, sym); len(files) != 0 {
		t.Errorf("a struct was followed: %v", files)
	}
}

func TestRustReexport_RemovedNameStillImported(t *testing.T) {
	root := rustRepo(t, map[string]string{
		"src/prelude.rs": "pub use crate::core::Mode;\npub use crate::core::{Keep, Gone};\n",
		"src/named.rs":   "use crate::prelude::Mode;\nfn a(m: Mode) {}\n",
		"src/list.rs":    "use crate::prelude::{Keep, Gone};\n",
		"src/glob.rs":    "use crate::prelude::*;\nfn a(m: Mode) {}\n",
		"src/moved.rs":   "use crate::core::Mode;\nuse crate::prelude::*;\nfn a(m: Mode) {}\n",
		"src/other.rs":   "use crate::prelude::Keep;\nfn a(m: Keep) {}\n",
	})
	writeRepoFile(t, root, "src/prelude.rs", "pub use crate::core::Keep;\n")
	for _, cmd := range [][]string{{"git", "add", "-A"}} {
		run := exec.Command(cmd[0], cmd[1:]...)
		run.Dir = root
		if err := run.Run(); err != nil {
			t.Fatal(err)
		}
	}

	c := rustChecker(root, "src/prelude.rs")
	c.checkRustFiles(context.Background(), rustFixture{})
	got := strings.Join(siteLines(c), "|")
	for _, want := range []string{"src/named.rs:use crate::prelude::Mode;", "src/list.rs:use crate::prelude::{Keep, Gone};", "src/glob.rs:fn a(m: Mode) {}"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	for _, not := range []string{"src/moved.rs", "src/other.rs", "src/prelude.rs"} {
		if strings.Contains(got, not) {
			t.Errorf("%s is listed: %q", not, got)
		}
	}
}

func TestRustEnumVariants_IncompleteMatches(t *testing.T) {
	const useSrc = `fn a(e: DraftError) -> u16 {
    match e {
        DraftError::NotFound => 404,
        DraftError::Db(_) => 500,
    }
}
fn b(e: DraftError) -> u16 {
    match e {
        DraftError::NotFound => 404,
        _ => 500,
    }
}
fn c(e: &DraftError) -> bool {
    matches!(e, DraftError::NotFound)
}
`
	const before = "pub enum DraftError {\n    NotFound,\n    Db(String),\n}\n"
	const after = "pub enum DraftError {\n    NotFound,\n    /// New.\n    Unbelegt(Vec<u8>),\n    Db(String),\n}\n"
	root := rustRepo(t, map[string]string{"src/drafts.rs": before, "src/api.rs": useSrc})
	writeRepoFile(t, root, "src/drafts.rs", after)

	c := rustChecker(root, "src/drafts.rs")
	adapter := rustFixture{typ: "m/DraftError#", refs: map[string][]backends.Reference{"m/DraftError#": {
		rustRef("src/api.rs", 1), rustRef("src/api.rs", 3), rustRef("src/api.rs", 4),
		rustRef("src/api.rs", 7), rustRef("src/api.rs", 9), rustRef("src/api.rs", 13), rustRef("src/api.rs", 14),
	}}}
	open := c.checkRustFiles(context.Background(), adapter)

	got := siteLines(c)
	if want := []string{"src/api.rs:match e {"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sites = %q, want %q", got, want)
	}
	if c.sites[0].Line != 2 || !strings.Contains(c.sites[0].Via, "Unbelegt") {
		t.Errorf("site = %+v", c.sites[0])
	}
	if len(open) != 1 || !open[0].files["src/api.rs"] {
		t.Errorf("open = %+v", open)
	}
}

func TestRustEnumVariants_MatchGivenTheArm(t *testing.T) {
	const useBefore = "fn a(e: E) -> u8 {\n    match e {\n        E::A => 1,\n    }\n}\n"
	const useAfter = "fn a(e: E) -> u8 {\n    match e {\n        E::A => 1,\n        E::B => 2,\n    }\n}\n"
	root := rustRepo(t, map[string]string{"src/e.rs": "pub enum E {\n    A,\n}\n", "src/use.rs": useBefore})
	writeRepoFile(t, root, "src/e.rs", "pub enum E {\n    A,\n    B,\n}\n")
	writeRepoFile(t, root, "src/use.rs", useAfter)

	c := rustDiffChecker(t, root)
	adapter := rustFixture{typ: "m/E#", refs: map[string][]backends.Reference{"m/E#": {rustRef("src/use.rs", 1), rustRef("src/use.rs", 3)}}}
	if open := c.checkRustFiles(context.Background(), adapter); len(open) != 0 || len(c.sites) != 0 {
		t.Fatalf("a match with the new arm is done: open %+v, sites %q", open, siteLines(c))
	}
	if c.updated != 1 {
		t.Errorf("updated = %d, want the one match", c.updated)
	}
}

// Arms spelled `Self::A` or, through a glob import, `A` refer to the variant and not
// to the enum: only the variants' references find those matches.
func TestRustEnumVariants_SelfAndGlobArms(t *testing.T) {
	const before = `pub enum Kind {
    A,
    B(u8),
}

impl Kind {
    pub fn name(&self) -> &str {
        match self {
            Self::A => "a",
            Self::B(_) => "b",
        }
    }
    pub fn code(&self) -> u8 {
        match self {
            Self::A => 1,
            _ => 0,
        }
    }
}
`
	const after = `pub enum Kind {
    A,
    B(u8),
    C,
}

impl Kind {
    pub fn name(&self) -> &str {
        match self {
            Self::A => "a",
            Self::B(_) => "b",
        }
    }
    pub fn code(&self) -> u8 {
        match self {
            Self::A => 1,
            _ => 0,
        }
    }
}
`
	const useSrc = `use crate::kind::Kind::*;

fn f(k: crate::kind::Kind) -> u8 {
    match k {
        A => 1,
        B(n) => n,
    }
}
`
	root := rustRepo(t, map[string]string{"src/kind.rs": before, "src/user.rs": useSrc})
	writeRepoFile(t, root, "src/kind.rs", after)

	c := rustDiffChecker(t, root)
	adapter := rustFixture{
		typ:      "m/Kind#",
		variants: map[string]string{"A": "m/Kind#A#", "B": "m/Kind#B#"},
		refs: map[string][]backends.Reference{
			// As rust-analyzer has them: the enum only where it is spelled.
			"m/Kind#":   {rustRef("src/kind.rs", 6), rustRef("src/user.rs", 1), rustRef("src/user.rs", 3)},
			"m/Kind#A#": {rustRef("src/kind.rs", 9), rustRef("src/kind.rs", 15), rustRef("src/user.rs", 5)},
			"m/Kind#B#": {rustRef("src/kind.rs", 10), rustRef("src/user.rs", 6)},
		},
	}
	open := c.checkRustFiles(context.Background(), adapter)

	got := siteLines(c)
	want := []string{"src/kind.rs:match self {", "src/user.rs:match k {"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sites = %q, want %q", got, want)
	}
	if c.sites[0].Line != 9 || c.sites[1].Line != 4 || c.sites[0].Symbol != "Kind" {
		t.Errorf("sites = %+v", c.sites)
	}
	if len(open) != 1 || !open[0].files["src/kind.rs"] || !open[0].files["src/user.rs"] {
		t.Errorf("open = %+v", open)
	}
}

// A variant nested in another pattern leaves the match open too, unless an arm takes
// whatever is at its place; such a match is counted with the uses that rarely break.
func TestRustEnumVariants_NestedPatterns(t *testing.T) {
	const useSrc = `fn a(x: Option<E>) -> u8 {
    match x {
        Some(E::A) => 1,
        None => 0,
    }
}
fn b(x: Option<E>) -> u8 {
    match x {
        Some(E::A) => 1,
        Some(_) => 2,
        None => 0,
    }
}
`
	root := rustRepo(t, map[string]string{"src/e.rs": "pub enum E {\n    A,\n}\n", "src/use.rs": useSrc})
	writeRepoFile(t, root, "src/e.rs", "pub enum E {\n    A,\n    B,\n}\n")

	c := rustDiffChecker(t, root)
	adapter := rustFixture{
		typ:      "m/E#",
		variants: map[string]string{"A": "m/E#A#"},
		refs:     map[string][]backends.Reference{"m/E#A#": {rustRef("src/use.rs", 3), rustRef("src/use.rs", 9)}},
	}
	open := c.checkRustFiles(context.Background(), adapter)

	if got := siteLines(c); len(got) != 1 || got[0] != "src/use.rs:match x {" || c.sites[0].Line != 2 {
		t.Fatalf("sites = %+v", c.sites)
	}
	if c.sites[0].Use != diff.RustUseNested || useRank(c.sites[0].Use) != rankRarely {
		t.Errorf("use = %q, want it among the rare ones", c.sites[0].Use)
	}
	if len(open) != 1 || c.updated != 1 {
		t.Errorf("open = %+v, updated = %d: the match with Some(_) is done", open, c.updated)
	}
}

// A plain `let` over an enum's only variant stops compiling when it gets a second one;
// `if let`, `let … else` and a `let` the diff turned into one stay as they are.
func TestRustEnumVariants_IrrefutableLet(t *testing.T) {
	const useSrc = `fn a(p: P) -> u8 {
    let P::Admit { id } = p;
    id
}
fn b(w: W) -> u8 {
    let W(P::Admit { id }, _) = w;
    id
}
fn c(p: P) -> u8 {
    let P::Admit { id } = p else { return 0 };
    id
}
fn d(p: P) -> u8 {
    if let P::Admit { id } = p { id } else { 0 }
}
fn e(p: P) -> u8 {
    let P::Admit { id } = p;
    id
}
`
	fixed := strings.Replace(useSrc, "fn e(p: P) -> u8 {\n    let P::Admit { id } = p;", "fn e(p: P) -> u8 {\n    let P::Admit { id } = p else { return 0 };", 1)
	refs := map[string][]backends.Reference{"m/P#Admit#": {
		rustRef("src/use.rs", 2), rustRef("src/use.rs", 6), rustRef("src/use.rs", 10), rustRef("src/use.rs", 14), rustRef("src/use.rs", 17),
	}}
	adapter := rustFixture{typ: "m/P#", variants: map[string]string{"Admit": "m/P#Admit#"}, refs: refs}

	root := rustRepo(t, map[string]string{"src/p.rs": "pub enum P {\n    Admit { id: u8 },\n}\n", "src/use.rs": useSrc})
	writeRepoFile(t, root, "src/p.rs", "pub enum P {\n    Admit { id: u8 },\n    Forget,\n}\n")
	writeRepoFile(t, root, "src/use.rs", fixed)

	c := rustDiffChecker(t, root)
	open := c.checkRustFiles(context.Background(), adapter)
	want := []string{"src/use.rs:let P::Admit { id } = p;", "src/use.rs:let W(P::Admit { id }, _) = w;"}
	if got := siteLines(c); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sites = %q, want %q", got, want)
	}
	if c.sites[0].Line != 2 || c.sites[1].Line != 6 || c.sites[0].Use != "" {
		t.Errorf("sites = %+v, want lines 2 and 6, listed as they are", c.sites)
	}
	if len(open) != 1 || c.updated != 1 {
		t.Errorf("open = %+v, updated = %d: the let given an else is done", open, c.updated)
	}

	// With two variants before, the same lets didn't compile then; nothing to list.
	root = rustRepo(t, map[string]string{"src/p.rs": "pub enum P {\n    Admit { id: u8 },\n    Hold,\n}\n", "src/use.rs": useSrc})
	writeRepoFile(t, root, "src/p.rs", "pub enum P {\n    Admit { id: u8 },\n    Hold,\n    Forget,\n}\n")
	c = rustDiffChecker(t, root)
	adapter.variants["Hold"] = "m/P#Hold#"
	if open := c.checkRustFiles(context.Background(), adapter); len(open) != 0 || len(c.sites) != 0 {
		t.Errorf("two variants before: open = %+v, sites = %+v", open, c.sites)
	}
}
