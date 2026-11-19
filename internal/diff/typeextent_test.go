package diff

import (
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// goLib is goDoc's file, lib/lib.go.
var goLib = strings.Split("package lib\n\nimport \"fmt\"\n\nconst Limit = 10\n\nfunc Target(x int) int {\n\ty := x + 1\n\tfmt.Println(y)\n\treturn y\n}\n\nvar Table = map[string]int{\n\t\"a\": 1,\n}\n\ntype Point struct {\n\tX int\n}\n\nfunc After() {}", "\n")

// readerFor returns a FileReader with the old side and the new side of one file.
func readerFor(old, new []string) FileReader {
	return func(_ string, isOld bool) []string {
		if isOld {
			return old
		}
		return new
	}
}

func insertLines(lines []string, at int, add ...string) []string {
	out := append([]string{}, lines[:at]...)
	out = append(out, add...)
	return append(out, lines[at:]...)
}

// scip-go gives a struct only the line of its name. A field inserted into its body, in
// a hunk that starts below that line, is the struct's change, not a file-level one.
func TestMapToSymbols_GoFieldInsertedIntoStruct(t *testing.T) {
	diffText := fileDiff("lib/lib.go", "@@ -18,2 +18,3 @@\n \tX int\n+\tY int\n }\n")
	t.Run("with the file", func(t *testing.T) {
		got := mapDocWith(t, goDoc(), diffText, readerFor(goLib, insertLines(goLib, 18, "\tY int")))
		if len(got) != 1 || got[0].SymbolID != goPkg+"Point#" {
			t.Fatalf("got %+v, want Point", got)
		}
		if got[0].Confidence != confidenceExactBody {
			t.Errorf("confidence = %v, want %v", got[0].Confidence, confidenceExactBody)
		}
		if len(got[0].Lines) != 1 || got[0].Lines[0] != 19 {
			t.Errorf("lines = %v, want [19]", got[0].Lines)
		}
	})
	t.Run("hunk with the declaration line", func(t *testing.T) {
		got := mapDoc(t, goDoc(), fileDiff("lib/lib.go", "@@ -17,3 +17,4 @@\n type Point struct {\n \tX int\n+\tY int\n }\n"))
		if len(got) != 1 || got[0].SymbolID != goPkg+"Point#" {
			t.Fatalf("got %+v, want Point", got)
		}
	})
	t.Run("without the declaration line nothing says whose it is", func(t *testing.T) {
		got := mapDoc(t, goDoc(), diffText)
		if len(got) != 1 || got[0].SymbolID != "file:lib/lib.go" {
			t.Fatalf("got %+v, want the file-level entry", got)
		}
	})
	t.Run("a changed field line is still the field's", func(t *testing.T) {
		got := mapDocWith(t, goDoc(), fileDiff("lib/lib.go", "@@ -18,2 +18,2 @@\n-\tX int\n+\tX int64\n }\n"),
			readerFor(goLib, append(append(append([]string{}, goLib[:17]...), "\tX int64"), goLib[18:]...)))
		if names(got) != "X" {
			t.Fatalf("changed symbols = %q, want X", names(got))
		}
	})
	t.Run("code inserted after the closing brace is not the struct's", func(t *testing.T) {
		got := mapDocWith(t, goDoc(), fileDiff("lib/lib.go", "@@ -19,2 +19,3 @@\n }\n+var Extra = 1\n \n"),
			readerFor(goLib, insertLines(goLib, 19, "var Extra = 1")))
		if len(got) != 1 || got[0].SymbolID != "file:lib/lib.go" {
			t.Fatalf("got %+v, want the file-level entry", got)
		}
	})
}

// A struct deleted whole is still reported deleted when the diff gives its closing
// brace to the code before it: the body read from the text doesn't have to be all
// removed, the name's line is enough, as before.
func TestMapToSymbols_GoStructDeletedWithShiftedBrace(t *testing.T) {
	// Table's `}` (15) through Point's field (18) removed; Point's `}` (19) kept.
	diffText := fileDiff("lib/lib.go", "@@ -14,6 +14,2 @@\n \t\"a\": 1,\n-}\n-\n-type Point struct {\n-\tX int\n }\n \n")
	newLines := append(append([]string{}, goLib[:14]...), goLib[18:]...)
	got := mapDocWith(t, goDoc(), diffText, readerFor(goLib, newLines))
	var point *impact.ChangedSymbol
	for i := range got {
		if got[i].SymbolID == goPkg+"Point#" {
			point = &got[i]
		}
	}
	if point == nil || point.ChangeType != impact.ChangeDeleted {
		t.Fatalf("got %+v, want Point deleted", got)
	}
}

// goStore is store/store.go:
//
//	1 package store
//	2
//	3 type Store interface {
//	4 	Get(key string) string
//	5 	Put(key, value string)
//	6 }
var goStore = strings.Split("package store\n\ntype Store interface {\n\tGet(key string) string\n\tPut(key, value string)\n}", "\n")

func goStoreDoc() *scip.Document {
	pkg := "scip-go gomod example.com/demo abc `example.com/demo/store`/"
	return &scip.Document{
		RelativePath: "store/store.go",
		Occurrences: []*scip.Occurrence{
			def(pkg, 0),
			def(pkg+"Store#", 2),
			def(pkg+"Store#Get.", 3),
			def(pkg+"Store#Put.", 4),
		},
		Symbols: []*scip.SymbolInformation{
			{Symbol: pkg + "Store#", DisplayName: "Store", Kind: 21},
			{Symbol: pkg + "Store#Get.", DisplayName: "Get", Kind: 26},
			{Symbol: pkg + "Store#Put.", DisplayName: "Put", Kind: 26},
		},
	}
}

func TestMapToSymbols_GoMethodInsertedIntoInterface(t *testing.T) {
	diffText := fileDiff("store/store.go", "@@ -4,2 +4,3 @@\n \tGet(key string) string\n+\tDelete(key string)\n \tPut(key, value string)\n")
	got := mapDocWith(t, goStoreDoc(), diffText, readerFor(goStore, insertLines(goStore, 4, "\tDelete(key string)")))
	if names(got) != "Store" {
		t.Fatalf("changed symbols = %q, want Store", names(got))
	}
}

// rustErr is src/err.rs, from an index that gives the enum no EnclosingRange:
//
//	1 #[derive(Debug)]
//	2 pub enum DraftError {
//	3     #[error("nicht gefunden")]
//	4     NotFound,
//	5     #[error("{0}")]
//	6     Invalid(String),
//	7     #[error(transparent)]
//	8     Db(#[from] sqlx::Error),
//	9 }
var rustErr = strings.Split("#[derive(Debug)]\npub enum DraftError {\n    #[error(\"nicht gefunden\")]\n    NotFound,\n    #[error(\"{0}\")]\n    Invalid(String),\n    #[error(transparent)]\n    Db(#[from] sqlx::Error),\n}", "\n")

func rustEnumDoc() *scip.Document {
	return &scip.Document{
		RelativePath: "src/err.rs",
		Occurrences: []*scip.Occurrence{
			def(rsPkg+"DraftError#", 1),
			def(rsPkg+"DraftError#NotFound#", 3),
			def(rsPkg+"DraftError#Invalid#", 5),
			def(rsPkg+"DraftError#Db#", 7),
		},
		Symbols: []*scip.SymbolInformation{
			{Symbol: rsPkg + "DraftError#", DisplayName: "DraftError", Kind: 11},
		},
	}
}

// The MailBuddy case: a variant with a doc comment and attribute, in a hunk starting
// inside the enum.
func TestMapToSymbols_RustVariantInsertedIntoEnum(t *testing.T) {
	added := []string{"    /// KI-Entwurf mit unbelegten Stellen.", "    #[error(\"unbelegte Angaben im Entwurf\")]", "    Unbelegt(Vec<Finding>),"}
	diffText := fileDiff("src/err.rs", "@@ -5,4 +5,7 @@\n     #[error(\"{0}\")]\n     Invalid(String),\n+"+strings.Join(added, "\n+")+"\n     #[error(transparent)]\n     Db(#[from] sqlx::Error),\n")
	got := mapDocWith(t, rustEnumDoc(), diffText, readerFor(rustErr, insertLines(rustErr, 6, added...)))
	if names(got) != "DraftError" || got[0].SymbolID != rsPkg+"DraftError#" {
		t.Fatalf("got %+v, want DraftError", got)
	}
}

// tsOpts is src/opts.ts, from an index that gives the interface no EnclosingRange:
//
//	1 export interface Options {
//	2   name: string;
//	3   verbose: boolean;
//	4 }
var tsOpts = strings.Split("export interface Options {\n  name: string;\n  verbose: boolean;\n}", "\n")

func tsPointDoc() *scip.Document {
	pkg := "scip-typescript npm demo 1.0.0 src/`opts.ts`/"
	return &scip.Document{
		RelativePath: "src/opts.ts",
		Occurrences: []*scip.Occurrence{
			def(pkg+"Options#", 0),
			def(pkg+"Options#name.", 1),
			def(pkg+"Options#verbose.", 2),
		},
		Symbols: []*scip.SymbolInformation{{Symbol: pkg + "Options#"}, {Symbol: pkg + "Options#name."}, {Symbol: pkg + "Options#verbose."}},
	}
}

func TestMapToSymbols_TSMemberInsertedIntoInterface(t *testing.T) {
	diffText := fileDiff("src/opts.ts", "@@ -2,3 +2,4 @@\n   name: string;\n+  timeout: number;\n   verbose: boolean;\n }\n")
	got := mapDocWith(t, tsPointDoc(), diffText, readerFor(tsOpts, insertLines(tsOpts, 2, "  timeout: number;")))
	if names(got) != "Options" {
		t.Fatalf("changed symbols = %q, want Options", names(got))
	}
}

func TestTypeBodyEnd(t *testing.T) {
	toMap := func(lines ...string) map[int]string {
		m := make(map[int]string, len(lines))
		for i, l := range lines {
			m[i+1] = l
		}
		return m
	}
	tests := []struct {
		name, lang, typ string
		lines           map[int]string
		want            int
	}{
		{"go struct", "go", "P", toMap("type P struct {", "\tX int // }", "\tY string `json:\"}\"`", "}"), 4},
		{"go struct in a type group", "go", "P", toMap("type (", "\tP struct {", "\t\tX int", "\t}", ")"), 0}, // line 1 isn't P's
		{"go generic interface", "go", "S", toMap("type S[T any] interface {", "\tGet() T", "}"), 3},
		{"go named type has no body", "go", "N", toMap("type N int", "", "var x = struct{}{}"), 0},
		{"go other name", "go", "Q", toMap("type P struct {", "}"), 0},
		{"go body past what is shown", "go", "P", map[int]string{1: "type P struct {", 2: "\tX int", 4: "}"}, 0},
		{"ts interface", "ts", "O", toMap("export interface O extends B {", "  a: { b: string };", "}"), 3},
		{"ts const enum", "ts", "M", toMap("export const enum M {", "  A,", "  B,", "}"), 4},
		{"ts object type", "ts", "T", toMap("type T<K> = {", "  k: K;", "};"), 3},
		{"ts type alias without braces", "ts", "U", toMap("type U = string | number;", "function f() {", "}"), 0},
		{"rust enum", "rust", "E", toMap("pub(crate) enum E {", "    A { x: u8 },", "    B(String),", "}"), 4},
		{"rust tuple struct", "rust", "T", toMap("pub struct T(u8);", "impl T {", "}"), 0},
		{"rust string with a brace", "rust", "S", toMap("struct S {", "    #[doc = \"}\"]", "    x: u8,", "}"), 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := typeBodyEnd(tt.lines, 1, tt.lang, tt.typ); got != tt.want {
				t.Errorf("typeBodyEnd = %d, want %d", got, tt.want)
			}
		})
	}
	// Inside a group the name's own line is read.
	if got := typeBodyEnd(toMap("type (", "\tP struct {", "\t\tX int", "\t}", ")"), 2, "go", "P"); got != 4 {
		t.Errorf("type group: typeBodyEnd = %d, want 4", got)
	}
}

// goOpts is opts/opts.go, the shape of backends.SearchOptions:
//
//	1 package opts
//	2
//	3 type Options struct {
//	4 	// Max limits the results
//	5 	Max int
//	6
//	7 	// Tests includes test files
//	8 	Tests bool
//	9 }
var goOpts = strings.Split("package opts\n\ntype Options struct {\n\t// Max limits the results\n\tMax int\n\n\t// Tests includes test files\n\tTests bool\n}", "\n")

func goOptsDoc() *scip.Document {
	pkg := "scip-go gomod example.com/demo abc `example.com/demo/opts`/"
	return &scip.Document{
		RelativePath: "opts/opts.go",
		Occurrences: []*scip.Occurrence{
			def(pkg, 0),
			def(pkg+"Options#", 2),
			def(pkg+"Options#Max.", 4),
			def(pkg+"Options#Tests.", 7),
		},
		Symbols: []*scip.SymbolInformation{
			{Symbol: pkg + "Options#", DisplayName: "Options", Kind: 49},
			{Symbol: pkg + "Options#Max.", DisplayName: "Max", Kind: 15},
			{Symbol: pkg + "Options#Tests.", DisplayName: "Tests", Kind: 15},
		},
	}
}

func replaceLines(lines []string, at int, with ...string) []string {
	out := append([]string{}, lines[:at]...)
	out = append(out, with...)
	return append(out, lines[at+len(with):]...)
}

// Renaming a field with its doc comment is the field's change: the comment line sits
// only in the struct's body read from the text, and naming the struct too would add
// the struct's consumers to a rename that leaves them alone (MaxResults -> Limit).
func TestMapToSymbols_GoFieldRenamedWithItsComment(t *testing.T) {
	newLines := replaceLines(goOpts, 3, "\t// Limit limits the results", "\tLimit int")
	diffText := fileDiff("opts/opts.go", "@@ -3,4 +3,4 @@\n type Options struct {\n-\t// Max limits the results\n-\tMax int\n+\t// Limit limits the results\n+\tLimit int\n \n")
	got := mapDocWith(t, goOptsDoc(), diffText, readerFor(goOpts, newLines))
	if names(got) != "Max" {
		t.Fatalf("changed symbols = %q, want Max alone", names(got))
	}

	t.Run("a changed comment alone is still the struct's", func(t *testing.T) {
		newLines := replaceLines(goOpts, 3, "\t// Max caps the results")
		diffText := fileDiff("opts/opts.go", "@@ -3,3 +3,3 @@\n type Options struct {\n-\t// Max limits the results\n+\t// Max caps the results\n \tMax int\n")
		got := mapDocWith(t, goOptsDoc(), diffText, readerFor(goOpts, newLines))
		if names(got) != "Options" || !got[0].Trivial {
			t.Fatalf("got %+v, want Options, trivial", got)
		}
	})
	t.Run("a field inserted with a comment is the struct's", func(t *testing.T) {
		newLines := insertLines(goOpts, 5, "\t// Extra is new", "\tExtra int")
		diffText := fileDiff("opts/opts.go", "@@ -5,2 +5,4 @@\n \tMax int\n+\t// Extra is new\n+\tExtra int\n \n")
		got := mapDocWith(t, goOptsDoc(), diffText, readerFor(goOpts, newLines))
		if names(got) != "Options" {
			t.Fatalf("changed symbols = %q, want Options", names(got))
		}
	})
	t.Run("the declaration line stays the struct's", func(t *testing.T) {
		newLines := replaceLines(goOpts, 2, "type Opts struct {", "\t// Limit limits the results", "\tLimit int")
		diffText := fileDiff("opts/opts.go", "@@ -2,4 +2,4 @@\n \n-type Options struct {\n-\t// Max limits the results\n-\tMax int\n+type Opts struct {\n+\t// Limit limits the results\n+\tLimit int\n \n")
		got := mapDocWith(t, goOptsDoc(), diffText, readerFor(goOpts, newLines))
		if names(got) != "Max Options" {
			t.Fatalf("changed symbols = %q, want Max Options", names(got))
		}
	})
}

// changedSymbols named three methods "Priority" and a Python constructor "__init__":
// the display name now says whose.
func TestQualifiedSymbolName(t *testing.T) {
	cases := []struct{ id, name, want string }{
		{"scip-go gomod github.com/x/y abc `github.com/x/y/internal/backends`/Backend#Priority.", "Priority", "Backend.Priority"},
		{"scip-go gomod github.com/x/y abc `github.com/x/y/internal/backends/lsp`/LspAdapter#Priority().", "Priority", "LspAdapter.Priority"},
		{"scip-go gomod github.com/x/y abc `github.com/x/y/internal/backends`/SearchOptions#MaxResults.", "MaxResults", "SearchOptions.MaxResults"},
		{"scip-python python . 1bb27f5 `experiments.living-context.lc1_account`/Win#__init__().", "__init__", "Win.__init__"},
		{tsPkg + "Store#get().", "get", "Store.get"},
		{"scip-go gomod github.com/x/y abc `github.com/x/y/internal/query`/NewEngine().", "NewEngine", ""},
		{"scip-go gomod github.com/x/y abc `github.com/x/y/internal/backends`/SearchOptions#", "SearchOptions", ""},
		{"scip-typescript npm demo 1.0.0 src/`lib.ts`/sectionSchema.key.", "sectionSchema.key", ""},
		{"file:lib/lib.go", "lib/lib.go", ""},
		{"local 3", "x", ""},
	}
	for _, c := range cases {
		if got := QualifiedSymbolName(c.id, c.name); got != c.want {
			t.Errorf("QualifiedSymbolName(%q, %q) = %q, want %q", c.id, c.name, got, c.want)
		}
	}
	got := mapDocWith(t, goDoc(), fileDiff("lib/lib.go", "@@ -18,2 +18,2 @@\n-\tX int\n+\tX int64\n }\n"),
		readerFor(goLib, append(append(append([]string{}, goLib[:17]...), "\tX int64"), goLib[18:]...)))
	if len(got) != 1 || got[0].Name != "X" || got[0].QualifiedName != "Point.X" {
		t.Fatalf("got %+v, want X with qualified name Point.X", got)
	}
}
